package tools

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

// This file holds the guarded-write machinery every manage_* tool
// shares. Per CLAUDE.md "Guarded writes": read the target first, then
// refuse anything the write would destroy that the caller cannot see.
//
// The one thing a resource supplies is a []fieldSpec — the LLM-facing
// names of the fields an update can touch, each with a way to project
// its stored value to a string. That single table then drives both the
// `changed` report and the `overwrite` guard, so the two can never
// disagree about what a write would do.
//
// Strings are the projection because they make int64, *int64, float64,
// *int and string fields comparable and emptiness-testable through one
// code path. Reflection over struct tags would be the other way to do
// this, and would be worse: these names are the LLM-facing contract,
// deliberately a parallel shadow of the upstream struct rather than a
// mirror of it.

// fieldSpec describes one updatable field of a record.
type fieldSpec[T any] struct {
	Name string
	Get  func(*T) string
}

// changedFields names the fields that differ between two records, in
// table order so the report is stable.
func changedFields[T any](spec []fieldSpec[T], before, after *T) []string {
	var changed []string
	for _, f := range spec {
		if f.Get(before) != f.Get(after) {
			changed = append(changed, f.Name)
		}
	}
	return changed
}

// populatedFields narrows names to the fields already holding a value
// on r. These are what a write would clobber rather than fill, which is
// exactly the set the overwrite guard refuses over — filling an empty
// field destroys nothing and needs no permission.
func populatedFields[T any](spec []fieldSpec[T], r *T, names []string) []string {
	var out []string
	for _, f := range spec {
		// A linear scan, not a set: names is what one write touches, so
		// it is a handful of entries and building a map to search them
		// costs more than the search.
		if slices.Contains(names, f.Name) && f.Get(r) != "" {
			out = append(out, f.Name)
		}
	}
	return out
}

// requireOverwrite refuses a write that would replace fields already
// holding a value, unless the caller NAMES each one. The refusal names
// every such field, because a caller told only that "something" would
// be clobbered cannot decide whether they meant it.
//
// overwrite used to be a bool, and a blanket one: `overwrite: true`
// permitted replacing every populated field the write happened to
// touch. So a caller who meant "change the title" and sent a write that
// also landed on value got both replaced on one flag, having been told
// about both but having agreed to nothing in particular.
//
// Naming them makes the permit per-field. A caller that names `title`
// and not `value` is still refused over value, which is the case the
// bool could not express.
//
// It does NOT stop a model routing around the guard, and nothing in
// band can: whatever the refusal says, a model can echo back. Three
// eval runs at v0.6.0 had one re-send with `overwrite: true` every
// time, and rewriting the field description did not change that. What
// this removes is the blanket — the model now has to be wrong about
// each field separately, and the call records which ones it claimed.
func requireOverwrite[T any](spec []fieldSpec[T], resource string, before *T, changed, overwrite []string, all bool) error {
	if all {
		return nil
	}
	clobbered := populatedFields(spec, before, changed)
	if len(clobbered) == 0 {
		return nil
	}

	named := make(map[string]bool, len(overwrite))
	for _, f := range overwrite {
		named[f] = true
	}
	var unnamed []string
	for _, f := range clobbered {
		if !named[f] {
			unnamed = append(unnamed, f)
		}
	}
	if len(unnamed) == 0 {
		return nil
	}
	return refuse(
		fmt.Sprintf("%s already has %s set, and this write would replace what is there",
			resource, strings.Join(unnamed, ", ")),
		fmt.Sprintf("overwrite: [%s]", quotedList(unnamed)),
	)
}

// quotedList renders field names as a JSON array body, so the refusal
// shows the argument the caller can paste back rather than describing
// it in prose.
func quotedList(fields []string) string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = strconv.Quote(f)
	}
	return strings.Join(out, ", ")
}

// The projections below are the value-to-string helpers a fieldSpec
// table is built from. A plain string field needs none of them and its
// table entry returns the field — an identity projection would buy a
// uniform column and spend a reader's attention on the entries where
// the projection is the load-bearing part.
//
// Each renders "not set" as the empty string, so
// the overwrite guard treats absent and zero identically — Pipedrive
// uses null and 0 interchangeably across v1 and v2 responses, and a
// guard that told them apart would refuse writes over fields that hold
// nothing.

func projectID(v int64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatInt(v, 10)
}

func projectOptID(p *int64) string {
	if p == nil {
		return ""
	}
	return projectID(*p)
}

func projectMoney(v float64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// projectOptFloat renders an optional field Pipedrive declares as
// `number`. Probability is the one: it reads as a whole percentage in
// the UI and arrives as a JSON number, which is not the same thing —
// this used to be an int projection over an int field, and a deal with
// a fractional probability failed the whole page's decode.
func projectOptFloat(p *float64) string {
	if p == nil || *p == 0 {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

// projectCollection renders a collection as every element's full
// contribution, joined in stored order.
//
// THE RULE, and the reason this helper exists rather than two
// hand-written projections: `render` must cover EVERY field of E that a
// write can set. Anything it leaves out is a field the caller can change
// while the diff sees nothing — the write is then skipped as a no-op and
// the caller is told the record already looks that way.
//
// That failure has now happened three times in this package: participants
// missing from the table entirely, contact points projected to the
// primary value alone (truncation invisible), and then contact points
// projected to their values alone (re-flagging the primary and
// relabeling invisible). Each fix chased the symptom. Stating the rule
// in one place, with both collections written against it, is the thing
// that stops a fourth.
func projectCollection[E any](es []E, render func(E) string) string {
	if len(es) == 0 {
		return ""
	}
	parts := make([]string, 0, len(es))
	for _, e := range es {
		parts = append(parts, render(e))
	}
	return strings.Join(parts, ",")
}

// projectContactPoints covers all three fields of ContactPoint, because
// manage_person's schema advertises all three as writable: {value,
// primary, label}. Dropping the primary flag made "make her work address
// the primary" read as a no-op.
//
// Value and Label are quoted rather than concatenated with a separator.
// Both are free text the caller supplies, so an unquoted join lets two
// different collections render identically — a label containing the
// separator can absorb the next entry. That only ever fails closed (the
// write is skipped rather than waved through), but "two different
// collections that look the same to the diff" is the exact shape of the
// three bugs this guard has already had, and quoting removes the
// category rather than arguing about its reachability.
func projectContactPoints(cps []pipedrive.ContactPoint) string {
	return projectCollection(cps, func(cp pipedrive.ContactPoint) string {
		out := strconv.Quote(cp.Value) + strconv.Quote(cp.Label)
		if cp.Primary {
			out += "*"
		}
		return out
	})
}

// projectParticipants covers both fields of ActivityParticipant.
// Promoting a different participant is a real change the caller should
// see reported, so the primary flag is part of the projection.
//
// No quoting needed here, unlike projectContactPoints: an id renders as
// digits and the flag as a fixed marker, so neither can contain the
// separator and no two distinct lists can collide.
func projectParticipants(ps []pipedrive.ActivityParticipant) string {
	return projectCollection(ps, func(p pipedrive.ActivityParticipant) string {
		out := strconv.FormatInt(p.PersonID, 10)
		if p.Primary {
			out += "|*"
		}
		return out
	})
}

// projectAddress and projectLocation render the structured records
// Pipedrive parses out of a single line back to that line, which is the
// same shape a write sends.
func projectAddress(a *pipedrive.Address) string {
	if a == nil {
		return ""
	}
	return a.Value
}

func projectLocation(l *pipedrive.ActivityLocation) string {
	if l == nil {
		return ""
	}
	return l.Value
}

// projectBool renders false as empty, which makes a false flag
// structurally un-refusable: the overwrite guard can never see `done` or
// `busy` as populated. That is deliberate — flipping a flag that is
// already false destroys nothing, so demanding permission for it would
// be friction. It is stated here because it is a policy, not an
// accident of the zero value.
func projectBool(b bool) string {
	if !b {
		return ""
	}
	return "true"
}

// withCustomFields returns a resource's field table extended by the
// custom fields this write names, so the diff and the overwrite guard
// reach them exactly as they reach the typed fields above.
//
// The extension is built per call rather than written out once: which
// custom fields exist is the workspace's business, not ours, and a
// static list would be a second place to add one. That is the same
// invariant "one list of field names per resource" protects — the list
// is simply derived here instead of literal.
//
// One function rather than a build-then-append pair, because the pair
// left a loaded gun on the table: appending to a package-level table
// writes through to it whenever it has spare capacity, and every later
// call on that resource would then inherit one call's custom fields.
// Building a fresh slice here means no caller is holding the append.
// The empty fast path below returns base itself, which is safe because
// it is only ever read — but it is why this says "no caller appends"
// rather than "appending is impossible".
//
// Fields go in sorted key order so `changed` reads the same twice, and
// each is reported under the workspace's name for it. The fallback to
// the key matters: a name that went missing would otherwise render as a
// blank entry in `changed`, which reads as a field with no name rather
// than as the bug it is.
func withCustomFields[T any](base []fieldSpec[T], w pipedrive.CustomFieldWrite, get func(*T) map[string]any) []fieldSpec[T] {
	if w.Empty() {
		return base
	}
	out := make([]fieldSpec[T], 0, len(base)+len(w.Values))
	out = append(out, base...)
	for _, key := range slices.Sorted(maps.Keys(w.Values)) {
		name := w.Names[key]
		if name == "" {
			name = key
		}
		out = append(out, fieldSpec[T]{
			Name: name,
			Get:  func(r *T) string { return projectCustomValue(get(r)[key]) },
		})
	}
	return out
}

// projectCustomValue renders a stored custom-field value for the diff.
// Absent and empty are the same "" every other projection uses, so the
// overwrite guard treats an unset custom field as free to fill.
//
// A set's elements are sorted before joining. A set has no order, so two
// orderings of the same choices are the same value — and reporting a
// reordering as a change would make the guard refuse a write that alters
// nothing.
func projectCustomValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case []any:
		// Through projectCollection rather than joined here, so the
		// quote-and-join rule has one definition. An option id usually
		// could not contain the separator anyway — but "usually" is what
		// the three bugs that rule exists for had in common.
		keys := make([]string, 0, len(t))
		for _, e := range t {
			keys = append(keys, pipedrive.OptionKey(e))
		}
		slices.Sort(keys)
		return projectCollection(keys, strconv.Quote)
	default:
		// Quoted like the list branch, and for its reason: an unquoted
		// scalar whose text happened to read as the quoted-join form
		// would render identically to the list it encodes, and two
		// values that render alike are a change the diff cannot see.
		// Empty stays empty — that is what the guard reads as "nothing
		// stored, free to fill".
		key := pipedrive.OptionKey(v)
		if key == "" {
			return ""
		}
		return strconv.Quote(key)
	}
}

// mergeCustomFields overlays a write's custom fields on the stored ones,
// for the predicted record the diff runs against. It copies rather than
// writing into the record it was handed: the prediction must not reach
// back into the upstream value the guard still needs to compare with.
func mergeCustomFields(stored, enc map[string]any) map[string]any {
	if len(enc) == 0 {
		return stored
	}
	out := make(map[string]any, len(stored)+len(enc))
	maps.Copy(out, stored)
	maps.Copy(out, enc)
	return out
}

// deref unwraps an optional tool input. Creates need plain values,
// because a create has nothing to clear — only an update needs to tell
// "leave it alone" from "set it to zero".
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// ptr is for the literal values a transition writes: status "won", the
// blank that reopen puts over a lost reason, done true and false.
func ptr[T any](v T) *T { return &v }

// clone returns a pointer to a fresh copy, so an LLM-facing summary or a
// predicted record never aliases the upstream struct's pointer. Keeps
// the parallel-shadow boundary intact even if a later caller mutates the
// upstream value.
func clone[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// setIf assigns *src to *dst when src is non-nil. A manage_* overlay is
// one decision — "if the request set this field, take it" — repeated
// per field, and writing it as a branch each time makes a function
// whose measured complexity is that repetition rather than any real
// intricacy.
func setIf[T any](dst, src *T) {
	if src != nil {
		*dst = *src
	}
}

// guardedWrite is the read-guard-write sequence every manage_* mutation
// runs. It exists because that sequence is the security-critical part of
// this package and it was written out once per resource, which is how
// the postures drifted apart: deals and activities gated the overwrite
// check behind the action while persons and organizations did not, and
// nothing made the five agree. Ordering lives here now, once.
//
// The caller supplies what actually differs — the field table, the
// record label, how to fetch, how to predict, how to write — and gets
// back the record to summarize plus the fields that moved.
type guardedWrite[T any] struct {
	// Spec is the resource's BASE field table. Custom fields are not in
	// it and must not be added to it — see Custom below.
	Spec     []fieldSpec[T]
	Resource string // "deal 9", for the refusal text

	// Custom is this write's custom fields, and CustomOf reaches the
	// record's custom-field map.
	//
	// They live here, together, because reaching a custom field needs
	// two things done and they used to be two edits in two functions:
	// extend the field table so the diff and the overwrite guard see it,
	// and overlay it onto the predicted record so the diff has something
	// to see. A resource that did one and not the other dry-ran as
	// "nothing changes" and then changed the field unreported. Setting
	// these two adjacent fields is now the whole of it, and run does
	// both or neither.
	Custom   pipedrive.CustomFieldWrite
	CustomOf func(*T) *map[string]any

	// ExpectVersion is the caller's copy of the record version; empty
	// means they did not ask for the check. Version reads it off the
	// stored record.
	ExpectVersion string
	Version       func(*T) string

	// Overwrite names the populated fields the caller agrees to
	// replace. A transition passes
	// true because it names both the change and the field it lands on,
	// so the caller already sees the whole blast radius.
	Overwrite []string
	// OverwriteAll is the transition case: the caller named the
	// transition, and the field it lands on is the field they named, so
	// there is nothing for them to acknowledge separately. Never set
	// from caller input.
	OverwriteAll bool
	DryRun       bool

	Get     func(context.Context) (*T, error)
	Predict func(*T) T
	Put     func(context.Context) (*T, error)
}

// run returns the record to report, the fields that changed, and a
// non-nil result when the caller should stop and return it.
//
// Three outcomes reach the caller with res == nil: a no-op (changed is
// empty, nothing was sent), a dry run (changed is what would move,
// nothing was sent), and a real write (changed is what the upstream
// actually altered, diffed against the echo rather than the prediction
// because Pipedrive normalizes some of what it stores).
func (w guardedWrite[T]) run(ctx context.Context) (rec *T, changed []string, stop *mcp.CallToolResult) {
	before, err := w.Get(ctx)
	if err != nil {
		return nil, nil, errorResult(err)
	}
	if err = checkExpectVersion(w.ExpectVersion, w.Version(before), w.Resource); err != nil {
		return nil, nil, errorResult(err)
	}

	spec := w.spec()
	predicted := w.predict(before)
	changed = changedFields(spec, before, &predicted)
	if len(changed) == 0 {
		// Everything asked for is already stored. Saying so beats a
		// round trip that would report the same thing.
		return before, nil, nil
	}
	if err = requireOverwrite(spec, w.Resource, before, changed, w.Overwrite, w.OverwriteAll); err != nil {
		return nil, nil, errorResult(err)
	}
	if w.DryRun {
		return before, changed, nil
	}

	after, err := w.Put(ctx)
	if err != nil {
		return nil, nil, errorResult(err)
	}
	return after, changedFields(spec, before, after), nil
}

// spec is the base table plus this write's custom fields.
func (w guardedWrite[T]) spec() []fieldSpec[T] {
	if w.CustomOf == nil {
		return w.Spec
	}
	return withCustomFields(w.Spec, w.Custom, func(r *T) map[string]any { return *w.CustomOf(r) })
}

// predict is the caller's overlay plus this write's custom fields, which
// the caller's overlay no longer has to remember. The merge copies, so
// the prediction never writes through to the record the diff still has
// to compare against.
func (w guardedWrite[T]) predict(before *T) T {
	predicted := w.Predict(before)
	if w.CustomOf != nil {
		*w.CustomOf(&predicted) = mergeCustomFields(*w.CustomOf(before), w.Custom.Values)
	}
	return predicted
}

// SelfAuthorizingActions names every manage_* action that grants its own
// overwrite — the transitions, which need no `overwrite` because the
// field they change is the field the caller named.
//
// Exported for one reason: the server's MCP instructions state this list
// in prose, and prose drifts. v0.5.0 shipped with `archive` and
// `unarchive` missing from that sentence after they were added here, so
// the instructions understated what a caller could do without asking.
// internal/server holds the sentence against this.
func SelfAuthorizingActions() []string {
	var out []string
	for name, a := range dealActions {
		if a.transition {
			out = append(out, name)
		}
	}
	for name, a := range activityActions {
		if a.transition && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

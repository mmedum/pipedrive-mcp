package tools

import (
	"context"
	"fmt"
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
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []string
	for _, f := range spec {
		if want[f.Name] && f.Get(r) != "" {
			out = append(out, f.Name)
		}
	}
	return out
}

// requireOverwrite refuses a write that would replace fields already
// holding a value, unless overwrite says to go ahead. The refusal names
// every such field, because a caller told only that "something" would
// be clobbered cannot decide whether they meant it.
func requireOverwrite[T any](spec []fieldSpec[T], resource string, before *T, changed []string, overwrite bool) error {
	if overwrite {
		return nil
	}
	clobbered := populatedFields(spec, before, changed)
	if len(clobbered) == 0 {
		return nil
	}
	return refuse(
		fmt.Sprintf("%s already has %s set, and this write would replace what is there",
			resource, strings.Join(clobbered, ", ")),
		"overwrite: true",
	)
}

// The projections below are the value-to-string helpers a fieldSpec
// table is built from. Each renders "not set" as the empty string, so
// the overwrite guard treats absent and zero identically — Pipedrive
// uses null and 0 interchangeably across v1 and v2 responses, and a
// guard that told them apart would refuse writes over fields that hold
// nothing.

func projectString(s string) string { return s }

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

func projectOptInt(p *int) string {
	if p == nil || *p == 0 {
		return ""
	}
	return strconv.Itoa(*p)
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
// relabelling invisible). Each fix chased the symptom. Stating the rule
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
	Spec     []fieldSpec[T]
	Resource string // "deal 9", for the refusal text

	// ExpectVersion is the caller's copy of the record version; empty
	// means they did not ask for the check. Version reads it off the
	// stored record.
	ExpectVersion string
	Version       func(*T) string

	// Overwrite permits replacing populated fields. A transition passes
	// true because it names both the change and the field it lands on,
	// so the caller already sees the whole blast radius.
	Overwrite bool
	DryRun    bool

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
// because Pipedrive normalises some of what it stores).
func (w guardedWrite[T]) run(ctx context.Context) (rec *T, changed []string, stop *mcp.CallToolResult) {
	before, err := w.Get(ctx)
	if err != nil {
		return nil, nil, errorResult(err)
	}
	if err = checkExpectVersion(w.ExpectVersion, w.Version(before), w.Resource); err != nil {
		return nil, nil, errorResult(err)
	}

	predicted := w.Predict(before)
	changed = changedFields(w.Spec, before, &predicted)
	if len(changed) == 0 {
		// Everything asked for is already stored. Saying so beats a
		// round trip that would report the same thing.
		return before, nil, nil
	}
	if err = requireOverwrite(w.Spec, w.Resource, before, changed, w.Overwrite); err != nil {
		return nil, nil, errorResult(err)
	}
	if w.DryRun {
		return before, changed, nil
	}

	after, err := w.Put(ctx)
	if err != nil {
		return nil, nil, errorResult(err)
	}
	return after, changedFields(w.Spec, before, after), nil
}

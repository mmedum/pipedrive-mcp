package pipedrive

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// FieldCache lazy-loads Pipedrive field metadata for one resource type
// and resolves a stored custom-field entry back into the words the
// workspace uses for it: the 40-char hash key to its field name, and an
// enum or set field's option id to its label. Loaded behind a per-entry
// sync.Once: concurrent first-callers see one fetch, all subsequent
// callers observe the same error if the fetch failed. Reload() swaps the
// entry so the next access refetches, while in-flight callers safely
// complete on their own entry — never on a Reload-corrupted Once.
type FieldCache struct {
	fetch func(context.Context) ([]Field, error)

	mu  sync.Mutex
	cur *cacheEntry
}

// cacheEntry holds one generation of cached field metadata. Each
// entry has its own sync.Once so Reload can replace the active entry
// without corrupting the mutex state of an in-flight Load. byKey,
// options and err are written exactly once from inside once.Do; later
// readers observe stable values via the once's happens-before
// guarantee.
//
// loaded is set to true inside once.Do *after* byKey/err are written.
// It lets Count() report the cache size without itself triggering
// once.Do — an earlier no-op once.Do here would silently seal the
// once and skip the real fetch on a concurrent first Load.
type cacheEntry struct {
	once  sync.Once
	err   error
	byKey map[string]cachedField
	// byName indexes the same fields by their lowercased workspace name,
	// for the write direction. A name is not unique the way a key is —
	// two fields may share one, and a custom field may share one with a
	// built-in — so this holds every key that answers to a name and lets
	// the lookup decide. Keys rather than copies of the fields: byKey
	// already holds those, and the ambiguity refusal wants this list.
	byName map[string][]string
	loaded atomic.Bool
}

// cachedField is one field flattened into what Resolve needs of it: the
// name to surface it under, and the table to look its stored value up
// in. Both hang off the same lookup, so resolving a custom field is one
// map probe rather than two indexes over the same set of keys.
type cachedField struct {
	key      string
	name     string
	custom   bool
	writable bool
	// labels maps an option id, rendered by OptionKey, to the label the
	// workspace shows for it. Nil for a field that has no options, which
	// is what tells label there is nothing to look up.
	labels map[string]string
	// ids is the same table inverted for the write direction: a
	// lowercased label to the option id as Pipedrive stores it, kept in
	// its decoded form so it goes back out as the number or string the
	// workspace uses.
	ids map[string]any
}

// NewFieldCache wraps fetch; fetch is invoked at most once per Reload
// cycle and must return a stable snapshot of the resource's fields.
func NewFieldCache(fetch func(context.Context) ([]Field, error)) *FieldCache {
	return &FieldCache{fetch: fetch}
}

// currentEntry returns the active entry, creating one if needed.
// Holding fc.mu only across the cur read/write keeps the fetch (which
// may block on HTTP) outside the lock so a concurrent Reload can swap
// fc.cur without waiting for the in-flight fetch.
func (fc *FieldCache) currentEntry() *cacheEntry {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.cur == nil {
		fc.cur = &cacheEntry{}
	}
	return fc.cur
}

func (e *cacheEntry) load(ctx context.Context, fetch func(context.Context) ([]Field, error)) {
	e.once.Do(func() {
		defer e.loaded.Store(true)
		fields, err := fetch(ctx)
		if err != nil {
			e.err = err
			return
		}
		e.byKey = make(map[string]cachedField, len(fields))
		e.byName = make(map[string][]string, len(fields))
		for _, f := range fields {
			labels, ids := optionTables(f.Options)
			cf := cachedField{
				key:      f.Key,
				name:     f.Name,
				custom:   f.IsCustom,
				writable: f.IsWritable,
				labels:   labels,
				ids:      ids,
			}
			e.byKey[f.Key] = cf
			lower := strings.ToLower(f.Name)
			e.byName[lower] = append(e.byName[lower], f.Key)
		}
	})
}

// Load triggers the underlying fetch on first call. Every subsequent
// caller sees the same error so the LLM-facing tool can decide
// whether to fall back to raw output.
func (fc *FieldCache) Load(ctx context.Context) error {
	e := fc.currentEntry()
	e.load(ctx, fc.fetch)
	return e.err
}

// Resolve returns a copy of raw with hash keys replaced by names and
// option ids replaced by their labels. Unknown keys and unknown option
// ids pass through verbatim so the LLM never silently loses data when
// the cache lags behind a freshly-created field or option; on cache
// load failure, raw is returned untouched.
//
// Reads work off the entry snapshot rather than fc.cur so a Reload
// mid-call doesn't risk a torn read. Each entry's byKey is set under
// once.Do, which provides the happens-before for safe concurrent reads.
func (fc *FieldCache) Resolve(ctx context.Context, raw map[string]any) map[string]any {
	if len(raw) == 0 {
		return raw
	}
	e := fc.currentEntry()
	e.load(ctx, fc.fetch)
	if e.err != nil {
		return raw
	}
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		if f, ok := e.byKey[k]; ok {
			out[f.name] = f.label(v)
		} else {
			out[k] = v
		}
	}
	return out
}

// optionTables flattens a field's options into the two lookups the two
// directions need: id → label for a read, lowercased label → id for a
// write. One pass, and one nil guard, because "this field has options"
// is a single fact — label tests labels and encode tests ids, and two
// separately-written guards could come to disagree about it.
//
// Lowercased on the write side only: the caller is an LLM relaying what
// a person said, and "enterprise" is the same choice as "Enterprise".
// The id keeps its decoded type so it goes back on the wire as the
// number or string Pipedrive stores.
func optionTables(opts []FieldOption) (labels map[string]string, ids map[string]any) {
	if len(opts) == 0 {
		return nil, nil
	}
	labels = make(map[string]string, len(opts))
	ids = make(map[string]any, len(opts))
	for _, o := range opts {
		labels[OptionKey(o.ID)] = o.Label
		ids[strings.ToLower(o.Label)] = o.ID
	}
	return labels, ids
}

// label replaces the option ids in one stored value with the labels the
// workspace shows for them.
//
// Whether to resolve at all is the option table's decision: a field
// with no options has nothing to look a value up in. The value's own
// shape then decides only how to walk it — an array is a set's several
// ids, anything else a single one — which is what keeps this
// independent of Pipedrive's field-type taxonomy.
//
// An id with no matching option comes back as itself, the same soft
// failure the unknown-key path takes: a cache lagging behind a
// freshly-added option must not turn into data the caller never sees.
//
// A nil value is nothing stored, so it has no label to look up. Saying
// so here rather than letting it miss the table is worth a line twice
// over: an empty custom field is the common case in a CRM, and it is
// the one value whose rendering reaches OptionKey's reflection branch.
func (f cachedField) label(v any) any {
	if v == nil || len(f.labels) == 0 {
		return v
	}
	ids, ok := v.([]any)
	if !ok {
		return f.labelOf(v)
	}
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = f.labelOf(id)
	}
	return out
}

func (f cachedField) labelOf(id any) any {
	if label, ok := f.labels[OptionKey(id)]; ok {
		return label
	}
	return id
}

// OptionKey renders an option id — or a stored value that might be one
// — to the string the two are compared on.
//
// Comparing the decoded values directly would be cheaper and would be
// wrong. Pipedrive does not spell an option id one way: custom fields
// carry numeric ids and built-ins like `status` carry strings, and a
// JSON number arrives in an `any` as a float64 whatever it looked like
// on the wire. Rendering both sides is the tolerant form, and tolerance
// is the point — an id that fails to match its own option is left in
// front of the caller as a bare number.
//
// FormatFloat with 'f' rather than %v, so a large id renders as its
// digits instead of 1e+07.
//
// Exported for internal/integration, which reads the workspace's option
// table straight from the API and has to render ids the way this
// package does or its check quietly stops matching.
func OptionKey(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

// Encode turns an LLM-facing custom-field map — keyed by the names the
// workspace shows, with a dropdown's value as its label — into the shape
// Pipedrive stores it in: hash keys, option ids. It is Resolve run
// backwards, and it is what makes a custom field writable.
//
// Every failure here refuses the whole map rather than encoding what it
// can. A write that silently dropped the one field the caller cared
// about would report success over a record that never changed it, and
// Pipedrive has no undo to fall back on.
//
// Unlike Resolve, a cache that will not load is fatal. Reading past a
// failed load costs the caller a hash key in place of a name; writing
// past one would invent a field.
func (fc *FieldCache) Encode(ctx context.Context, in map[string]any) (CustomFieldWrite, error) {
	if len(in) == 0 {
		return CustomFieldWrite{}, nil
	}
	e := fc.currentEntry()
	e.load(ctx, fc.fetch)
	if e.err != nil {
		// Not wrapped in ErrValidation: nothing the caller wrote is
		// wrong, the workspace could not be read. Classing it as
		// validation would tell the LLM to fix a field name over a 401,
		// and the upstream error carries the class that is actually
		// worth acting on.
		return CustomFieldWrite{}, fmt.Errorf("the workspace's custom-field definitions could not be read, so a field name cannot be checked against them: %w", e.err)
	}
	out := CustomFieldWrite{
		Values: make(map[string]any, len(in)),
		Names:  make(map[string]string, len(in)),
	}
	var refused []string
	for name, v := range in {
		f, err := e.writableField(name)
		if err != nil {
			refused = append(refused, err.Error())
			continue
		}
		// One field, named two ways — its name and its key, say — would
		// otherwise resolve to one entry whose value depends on which
		// way the map happened to range first.
		if _, dup := out.Values[f.key]; dup {
			refused = append(refused, fmt.Sprintf(
				"the custom field %q is named twice in this write, so which value to store is ambiguous; name it once",
				f.name))
			continue
		}
		enc, err := f.encode(v)
		if err != nil {
			refused = append(refused, err.Error())
			continue
		}
		out.Values[f.key] = enc
		out.Names[f.key] = f.name
	}
	if len(refused) > 0 {
		// Every bad field, not the first. This is the rule the overwrite
		// guard already states: name them all, because a caller made to
		// discover their mistakes one round trip at a time is being told
		// less than we know. Sorted, because ranging a map is not.
		slices.Sort(refused)
		return CustomFieldWrite{}, fmt.Errorf("%w: %s", ErrValidation, strings.Join(refused, "; "))
	}
	return out, nil
}

// CustomFieldWrite is one write's custom fields: the body to send, and
// what to call each of those fields when reporting on it.
//
// The names matter because they are the only thing the caller can act
// on. A `changed` report or an overwrite refusal that named a 40-char
// hash would be telling them about a field they have no way to
// recognize — and the read side has always answered in names, so the
// write side saying anything else would read as a different field.
type CustomFieldWrite struct {
	Values map[string]any    // hash key → the value Pipedrive stores
	Names  map[string]string // hash key → the workspace's name for it
}

// Empty reports whether the write names no custom field at all.
func (w CustomFieldWrite) Empty() bool { return len(w.Values) == 0 }

// writableField resolves what the caller called a field into the one
// field a write may set, or says why it cannot.
//
// A 40-char key is accepted alongside a name: it is what a caller falls
// back to when two fields share a name, and Resolve hands it out already
// whenever the cache does not recognize a key.
func (e *cacheEntry) writableField(name string) (cachedField, error) {
	// The key branch checks `custom` for the same reason the name branch
	// does. byKey holds the built-ins too, and a built-in's key is its
	// plain identifier — so without this, "Title" is refused while
	// "title" goes through, and the guard then measures the wrong thing:
	// a built-in's value does not live in the record's custom_fields
	// map, so it always reads as empty and the overwrite refusal never
	// fires over it. Pipedrive rejects such a body ("Unknown key
	// 'title'"), so the damage was bounded upstream — which is not where
	// this server's guard is supposed to hold.
	keys := e.byName[strings.ToLower(name)]
	if _, ok := e.byKey[name]; ok {
		// An exact key beats a name match, and seeding the candidate
		// list with it rather than returning here keeps one spelling of
		// each refusal below instead of two that can drift apart.
		keys = []string{name}
	}
	custom := make([]string, 0, len(keys))
	for _, k := range keys {
		if e.byKey[k].custom {
			custom = append(custom, k)
		}
	}

	switch {
	case len(custom) == 1:
		f := e.byKey[custom[0]]
		return f, f.checkWritable()
	case len(custom) > 1:
		slices.Sort(custom)
		return cachedField{}, fmt.Errorf("this workspace has %d custom fields named %q, so the name does not say which one to write; pass one of these field keys instead: %s",
			len(custom), name, strings.Join(custom, ", "))
	case len(keys) > 0:
		// Named something real, but a built-in. Worth saying so: the
		// caller is one input away from what they wanted.
		return cachedField{}, errBuiltinField(name)
	default:
		return cachedField{}, fmt.Errorf("this workspace has no custom field named %q; call refresh_field_cache if it was added or renamed since this server started", name)
	}
}

func errBuiltinField(name string) error {
	return fmt.Errorf("%q is a built-in Pipedrive field, not a custom field, so it cannot be written through custom_fields; use the tool's own input for it", name)
}

// Each refusal below is a bare message: Encode wraps the joined set in
// ErrValidation once, so the class is stated in one place rather than
// per field.
func (f cachedField) checkWritable() error {
	if f.writable {
		return nil
	}
	return fmt.Errorf("the custom field %q is read-only — Pipedrive reports it as not writable, so no write can set it", f.name)
}

// encode turns one LLM-facing value into what Pipedrive stores. A field
// without options passes its value through — a number stays a number and
// a string a string, which is what every scalar custom-field type wants.
// A dropdown takes a label, or the id itself for a caller echoing back
// what a read gave them.
func (f cachedField) encode(v any) (any, error) {
	if v == nil {
		return nil, fmt.Errorf("a custom field cannot be cleared, so %q cannot be set to null; omit it to leave it as it is", f.name)
	}
	if len(f.ids) == 0 {
		return v, nil
	}
	if vs, ok := v.([]any); ok {
		out := make([]any, 0, len(vs))
		for _, one := range vs {
			id, err := f.optionID(one)
			if err != nil {
				return nil, err
			}
			out = append(out, id)
		}
		return out, nil
	}
	return f.optionID(v)
}

// optionID takes one dropdown value the way a caller would say it. A
// label is the expected form; an id is accepted because a read that
// could not resolve an option hands one back, and refusing to take it
// again would make that output unusable.
func (f cachedField) optionID(v any) (any, error) {
	if s, ok := v.(string); ok {
		if id, ok := f.ids[strings.ToLower(s)]; ok {
			return id, nil
		}
	}
	if _, ok := f.labels[OptionKey(v)]; ok {
		return v, nil
	}
	return nil, fmt.Errorf("%q is not one of the choices on the custom field %q; it takes one of: %s",
		v, f.name, strings.Join(f.labelList(), ", "))
}

// labelList names a dropdown's choices for a refusal, in a stable order.
// A refusal the caller cannot act on is a bug, and "that is not a valid
// choice" without the choices is exactly that.
func (f cachedField) labelList() []string {
	return slices.Sorted(maps.Values(f.labels))
}

// Reload clears the cache so the next access refetches. Concurrent
// callers in flight at the moment of Reload finish their load on the
// outgoing entry (its once.Do is unaffected by this swap), and their
// already-acquired entry pointer continues to surface the old data.
// The next batch of callers sees a fresh entry and a fresh fetch.
func (fc *FieldCache) Reload() {
	fc.mu.Lock()
	fc.cur = nil
	fc.mu.Unlock()
}

// Count returns the number of fields currently cached. Returns 0 if
// the cache hasn't been loaded or the load failed. Used by the
// refresh_field_cache tool to surface a per-resource sanity check
// the LLM (and operator) can read after triggering a reload.
//
// Count never triggers once.Do — calling once.Do(no-op) here would
// race with a concurrent first Load and silently seal the once,
// causing the real fetch to be skipped. Instead it reads the loaded
// flag (set inside once.Do *after* byKey is written), which provides
// the happens-before edge to byKey for free.
func (fc *FieldCache) Count() int {
	fc.mu.Lock()
	e := fc.cur
	fc.mu.Unlock()
	if e == nil || !e.loaded.Load() {
		return 0
	}
	return len(e.byKey)
}

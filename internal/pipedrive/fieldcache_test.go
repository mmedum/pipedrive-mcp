package pipedrive

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestFieldCache_LazyAndOnce(t *testing.T) {
	var calls atomic.Int64
	fields := []Field{
		{Key: "abc123", Name: "Account Manager"},
		{Key: "title", Name: "Title"},
	}
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		calls.Add(1)
		return fields, nil
	})

	// Concurrent first use must trigger only one fetch.
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = fc.Resolve(context.Background(), map[string]any{"abc123": "Alice"})
		}()
	}
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Errorf("fetch invoked %d times; want 1", got)
	}
}

func TestFieldCache_FetchErrorDoesNotPanic(t *testing.T) {
	wantErr := errors.New("upstream down")
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		return nil, wantErr
	})
	if err := fc.Load(context.Background()); !errors.Is(err, wantErr) {
		t.Errorf("Load err = %v; want %v", err, wantErr)
	}
	// Resolve must pass through raw map untouched on a failed cache.
	raw := map[string]any{"abc123": 42}
	got := fc.Resolve(context.Background(), raw)
	if v, ok := got["abc123"]; !ok || v != 42 {
		t.Errorf("Resolve fallthrough lost data: got %v", got)
	}
}

func TestFieldCache_Resolve(t *testing.T) {
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		return []Field{
			{Key: "abc", Name: "Account Manager"},
			{Key: "def", Name: "Renewal Date"},
		}, nil
	})

	in := map[string]any{
		"abc":     "Alice",
		"def":     "2026-12-01",
		"unknown": "passthrough",
	}
	got := fc.Resolve(context.Background(), in)

	if got["Account Manager"] != "Alice" {
		t.Errorf("missing Account Manager: %v", got)
	}
	if got["Renewal Date"] != "2026-12-01" {
		t.Errorf("missing Renewal Date: %v", got)
	}
	if got["unknown"] != "passthrough" {
		t.Errorf("unknown-key passthrough lost: %v", got)
	}
	// Original input must not be mutated.
	if _, ok := in["Account Manager"]; ok {
		t.Error("Resolve mutated the input map")
	}
}

func TestFieldCache_ResolveEmpty(t *testing.T) {
	// Empty input doesn't trigger a fetch.
	var calls atomic.Int64
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		calls.Add(1)
		return nil, nil
	})
	got := fc.Resolve(context.Background(), nil)
	if got != nil {
		t.Errorf("Resolve(nil) = %v; want nil", got)
	}
	got = fc.Resolve(context.Background(), map[string]any{})
	if len(got) != 0 {
		t.Errorf("Resolve(empty) = %v; want empty", got)
	}
	if calls.Load() != 0 {
		t.Errorf("empty Resolve triggered %d fetches; want 0", calls.Load())
	}
}

// optionFixture is the field metadata and the stored record the option
// tests share, both as the JSON Pipedrive really sends.
//
// Both sides are decoded rather than hand-built on purpose. Every JSON
// number lands in an `any` as a float64, so an option id written as an
// untyped constant in a struct literal would compare equal down a path
// the real one never takes — the test would pass on a Resolve that is
// broken in production.
const (
	optionFieldsJSON = `[
	  {"field_code":"seg","field_name":"Segment","field_type":"enum",
	   "options":[{"id":107,"label":"Enterprise"},{"id":108,"label":"Mid-market"}]},
	  {"field_code":"reg","field_name":"Regions","field_type":"set",
	   "options":[{"id":68,"label":"EMEA"},{"id":69,"label":"APAC"}]},
	  {"field_code":"vis","field_name":"Visible to","field_type":"visible_to",
	   "options":[{"id":"open","label":"Everyone"}]},
	  {"field_code":"note","field_name":"Renewal note","field_type":"varchar"}
	]`

	optionRecordJSON = `{"seg":107,"reg":[68,69],"vis":"open","note":"107","gone":404}`
)

// fieldCacheFrom builds a cache over one of the fixtures below. Both
// suites use it, so the read side and the write side cannot drift into
// decoding their metadata differently.
func fieldCacheFrom(t *testing.T, fieldsJSON string) *FieldCache {
	t.Helper()
	var fields []Field
	if err := json.Unmarshal([]byte(fieldsJSON), &fields); err != nil {
		t.Fatalf("decoding field metadata: %v", err)
	}
	return NewFieldCache(func(_ context.Context) ([]Field, error) { return fields, nil })
}

func optionCache(t *testing.T) *FieldCache {
	t.Helper()
	return fieldCacheFrom(t, optionFieldsJSON)
}

func resolveJSON(t *testing.T, fc *FieldCache, record string) map[string]any {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(record), &raw); err != nil {
		t.Fatalf("decoding record: %v", err)
	}
	return fc.Resolve(context.Background(), raw)
}

func TestFieldCache_ResolveOptionLabels(t *testing.T) {
	got := resolveJSON(t, optionCache(t), optionRecordJSON)

	if got["Segment"] != "Enterprise" {
		t.Errorf("enum option not labelled: %v", got["Segment"])
	}
	if want := []any{"EMEA", "APAC"}; !reflect.DeepEqual(got["Regions"], want) {
		t.Errorf("set options = %v; want %v", got["Regions"], want)
	}
	// A string-id option, which is how Pipedrive spells the built-ins.
	if got["Visible to"] != "Everyone" {
		t.Errorf("string-id option not labelled: %v", got["Visible to"])
	}
	// "107" is the Renewal note's actual text. A field with no options
	// must not be searched for one, or every varchar that happens to
	// read like an id becomes somebody else's label.
	if got["Renewal note"] != "107" {
		t.Errorf("optionless field was relabelled: %v", got["Renewal note"])
	}
	if got["gone"] != float64(404) {
		t.Errorf("unknown-key passthrough lost: %v", got["gone"])
	}
}

// An id the cache has never heard of passes through as itself, the same
// soft failure an unknown key takes: a workspace can add an option at
// any moment, and the caller losing the value entirely is worse than
// the caller seeing a number.
func TestFieldCache_ResolveUnknownOption(t *testing.T) {
	fc := optionCache(t)

	got := resolveJSON(t, fc, `{"seg":999,"reg":[68,999]}`)
	if got["Segment"] != float64(999) {
		t.Errorf("unknown enum id = %v; want it passed through", got["Segment"])
	}
	if want := []any{"EMEA", float64(999)}; !reflect.DeepEqual(got["Regions"], want) {
		t.Errorf("partly-unknown set = %v; want %v", got["Regions"], want)
	}
}

func TestFieldCache_ResolveEmptyAndNullOptionValues(t *testing.T) {
	got := resolveJSON(t, optionCache(t), `{"seg":null,"reg":[]}`)

	if got["Segment"] != nil {
		t.Errorf("null option value = %v; want nil", got["Segment"])
	}
	if want := []any{}; !reflect.DeepEqual(got["Regions"], want) {
		t.Errorf("empty set = %v; want %v", got["Regions"], want)
	}
}

func TestOptionKey(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"float from a decode", float64(107), "107"},
		// %v would render this 1e+07 and stop matching its own option.
		{"large float", float64(10000000), "10000000"},
		{"string id", "open", "open"},
		{"untyped int from a literal", 107, "107"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := OptionKey(tc.in); got != tc.want {
				t.Errorf("OptionKey(%v) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

// encodeFieldsJSON is the write-direction fixture: a plain text field, a
// dropdown, a multi-select, a read-only custom field, a built-in, and
// two custom fields that share a name.
const encodeFieldsJSON = `[
  {"field_code":"txt","field_name":"Renewal note","field_type":"varchar",
   "is_custom_field":true,"is_writable":true},
  {"field_code":"seg","field_name":"Segment","field_type":"enum",
   "is_custom_field":true,"is_writable":true,
   "options":[{"id":107,"label":"Enterprise"},{"id":108,"label":"Mid-market"}]},
  {"field_code":"reg","field_name":"Regions","field_type":"set",
   "is_custom_field":true,"is_writable":true,
   "options":[{"id":68,"label":"EMEA"},{"id":69,"label":"APAC"}]},
  {"field_code":"calc","field_name":"Computed score","field_type":"double",
   "is_custom_field":true,"is_writable":false},
  {"field_code":"title","field_name":"Title","field_type":"varchar",
   "is_custom_field":false,"is_writable":true},
  {"field_code":"dup1","field_name":"Owner note","field_type":"varchar",
   "is_custom_field":true,"is_writable":true},
  {"field_code":"dup2","field_name":"Owner note","field_type":"varchar",
   "is_custom_field":true,"is_writable":true}
]`

func encodeCache(t *testing.T) *FieldCache {
	t.Helper()
	return fieldCacheFrom(t, encodeFieldsJSON)
}

func TestFieldCache_Encode(t *testing.T) {
	fc := encodeCache(t)

	cases := []struct {
		name string
		in   map[string]any
		want map[string]any
	}{
		{"name to key, value through", map[string]any{"Renewal note": "call in May"},
			map[string]any{"txt": "call in May"}},
		{"label to option id", map[string]any{"Segment": "Enterprise"},
			map[string]any{"seg": float64(107)}},
		{"label match ignores case", map[string]any{"Segment": "enterprise"},
			map[string]any{"seg": float64(107)}},
		{"field name match ignores case", map[string]any{"segment": "Enterprise"},
			map[string]any{"seg": float64(107)}},
		{"a set takes a list of labels", map[string]any{"Regions": []any{"EMEA", "APAC"}},
			map[string]any{"reg": []any{float64(68), float64(69)}}},
		// A read hands back a bare id whenever the cache could not name
		// the option; refusing to take it again would make that output
		// unusable.
		{"an option id is accepted back", map[string]any{"Segment": float64(108)},
			map[string]any{"seg": float64(108)}},
		{"a 40-char key is accepted in place of a name", map[string]any{"txt": "by key"},
			map[string]any{"txt": "by key"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := fc.Encode(context.Background(), tc.in)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if !reflect.DeepEqual(got.Values, tc.want) {
				t.Errorf("Encode = %#v; want %#v", got.Values, tc.want)
			}
			// Every key sent must carry a name to report it by, or the
			// changed report and the overwrite refusal go anonymous.
			for k := range got.Values {
				if got.Names[k] == "" {
					t.Errorf("encoded key %q has no reporting name", k)
				}
			}
		})
	}
}

// Every refusal has to name what the caller should do instead, so each
// case asserts on the text as well as the class. A refusal they cannot
// act on is the bug this table exists to prevent.
func TestFieldCache_EncodeRefusals(t *testing.T) {
	fc := encodeCache(t)

	cases := []struct {
		name    string
		in      map[string]any
		wantHas []string
	}{
		{"unknown field", map[string]any{"Nope": "x"},
			[]string{"no custom field named", "refresh_field_cache"}},
		{"built-in field by name", map[string]any{"Title": "x"},
			[]string{"built-in", "the tool's own input"}},
		// By KEY as well as by name. byKey holds the built-ins too, and
		// a built-in's key is its plain identifier, so checking only the
		// name refused "Title" and let "title" through — where the guard
		// would then read it as an empty custom field and never demand
		// overwrite for it.
		{"built-in field by key", map[string]any{"title": "x"},
			[]string{"built-in", "the tool's own input"}},
		{"ambiguous name", map[string]any{"Owner note": "x"},
			[]string{"2 custom fields", "dup1, dup2"}},
		{"read-only field", map[string]any{"Computed score": 1},
			[]string{"read-only"}},
		{"clearing", map[string]any{"Renewal note": nil},
			[]string{"cannot be cleared", "omit it"}},
		{"label off the list", map[string]any{"Segment": "Enterprisey"},
			[]string{"not one of the choices", "Enterprise, Mid-market"}},
		{"bad label inside a set", map[string]any{"Regions": []any{"EMEA", "Atlantis"}},
			[]string{"not one of the choices", "APAC, EMEA"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := fc.Encode(context.Background(), tc.in)
			if err == nil {
				t.Fatalf("Encode(%v) = %v; want a refusal", tc.in, got)
			}
			if !errors.Is(err, ErrValidation) {
				t.Errorf("error is not ErrValidation: %v", err)
			}
			if !got.Empty() {
				t.Errorf("a refused Encode returned %v; want nothing, so a caller cannot half-write", got.Values)
			}
			for _, want := range tc.wantHas {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal does not say %q: %v", want, err)
				}
			}
		})
	}
}

// A refusal names every field that was wrong, not the first one the map
// happened to yield. Ranging a map is unordered, so reporting one would
// make the caller's next attempt a coin flip.
func TestFieldCache_EncodeNamesEveryBadField(t *testing.T) {
	fc := encodeCache(t)

	_, err := fc.Encode(context.Background(), map[string]any{
		"Nope":           "x",
		"Computed score": 1,
		"Segment":        "Enterprisey",
	})
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, want := range []string{"no custom field named", "read-only", "not one of the choices"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
}

// One bad field refuses the whole map. Encoding what it could would
// report a success over a record that never got the field the caller
// cared about, and Pipedrive has no undo.
func TestFieldCache_EncodeRefusesTheWholeMap(t *testing.T) {
	fc := encodeCache(t)
	got, err := fc.Encode(context.Background(), map[string]any{
		"Renewal note": "this one is fine",
		"Nope":         "this one is not",
	})
	if err == nil {
		t.Fatalf("Encode = %v; want a refusal", got.Values)
	}
	if !got.Empty() {
		t.Errorf("Encode returned %v alongside its error; want nothing", got.Values)
	}
}

// Reading past a failed cache load costs a hash key in place of a name.
// Writing past one would invent a field, so Encode fails where Resolve
// falls through.
//
// The underlying error is carried, NOT re-classed as validation: nothing
// the caller wrote is wrong, and the tool layer reads the wrapped error
// to pick the [class] the LLM sees. Calling a 401 a validation problem
// would send it away to fix a field name.
func TestFieldCache_EncodeFailsWhenTheCacheDoes(t *testing.T) {
	wantErr := errors.New("upstream down")
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) { return nil, wantErr })

	got, err := fc.Encode(context.Background(), map[string]any{"Segment": "Enterprise"})
	if err == nil {
		t.Fatalf("Encode = %v; want a refusal", got.Values)
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("the upstream error was not carried: %v", err)
	}
	if errors.Is(err, ErrValidation) {
		t.Error("a workspace that could not be read was reported as the caller's validation error")
	}
}

// One field named two ways in one write is ambiguous, and ranging a map
// would pick a winner at random. Refused instead.
func TestFieldCache_EncodeRefusesAFieldNamedTwice(t *testing.T) {
	fc := encodeCache(t)

	got, err := fc.Encode(context.Background(), map[string]any{
		"Renewal note": "by name",
		"txt":          "by key",
	})
	if err == nil {
		t.Fatalf("Encode = %v; want a refusal", got.Values)
	}
	if !strings.Contains(err.Error(), "named twice") {
		t.Errorf("refusal does not say the field was named twice: %v", err)
	}
}

func TestFieldCache_EncodeEmpty(t *testing.T) {
	var calls atomic.Int64
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		calls.Add(1)
		return nil, nil
	})
	got, err := fc.Encode(context.Background(), nil)
	if err != nil || !got.Empty() {
		t.Errorf("Encode(nil) = %v, %v; want nothing, nil", got.Values, err)
	}
	if calls.Load() != 0 {
		t.Errorf("an empty Encode triggered %d fetches; want 0", calls.Load())
	}
}

func TestFieldCache_Count(t *testing.T) {
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		return []Field{
			{Key: "a", Name: "A"},
			{Key: "b", Name: "B"},
			{Key: "c", Name: "C"},
		}, nil
	})

	// Pre-load: 0.
	if got := fc.Count(); got != 0 {
		t.Errorf("Count before load = %d; want 0", got)
	}
	if err := fc.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := fc.Count(); got != 3 {
		t.Errorf("Count after load = %d; want 3", got)
	}
	// Post-Reload (before next Load): cache is empty again.
	fc.Reload()
	if got := fc.Count(); got != 0 {
		t.Errorf("Count after Reload (no Load) = %d; want 0", got)
	}
}

func TestFieldCache_CountAfterFetchError(t *testing.T) {
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		return nil, errors.New("upstream down")
	})
	_ = fc.Load(context.Background())
	if got := fc.Count(); got != 0 {
		t.Errorf("Count after failed load = %d; want 0", got)
	}
}

// TestFieldCache_CountDoesNotPoisonOnce regresses the bug where
// Count() armed the once with a no-op closure: a Count call racing
// with a concurrent first Load could seal the once and skip the
// real fetch, leaving byKey nil and silently breaking Resolve.
//
// The fetcher blocks on a channel until released, letting us hold a
// Load in flight while we call Count many times in parallel. After
// release the Load must observe the populated byKey, not the
// poisoned no-op.
func TestFieldCache_CountDoesNotPoisonOnce(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int64
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		calls.Add(1)
		<-release
		return []Field{{Key: "k1", Name: "N1"}, {Key: "k2", Name: "N2"}}, nil
	})

	// Kick off a Load that will block in the fetcher.
	loadDone := make(chan error, 1)
	go func() { loadDone <- fc.Load(context.Background()) }()

	// Race a flurry of Count() calls against the in-flight Load.
	// Pre-fix these would seal the once and the Load would return
	// nil with byKey still nil. With the fix, Count returns 0
	// (loaded flag is false) and the Load proceeds untouched.
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := fc.Count(); got != 0 {
				t.Errorf("Count during in-flight load = %d; want 0", got)
			}
		}()
	}
	wg.Wait()

	// Release the fetcher; the Load now completes for real.
	close(release)
	if err := <-loadDone; err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := calls.Load(); got != 1 {
		t.Errorf("fetcher invocations = %d; want 1", got)
	}
	if got := fc.Count(); got != 2 {
		t.Errorf("Count after Load completed = %d; want 2", got)
	}
	resolved := fc.Resolve(context.Background(), map[string]any{"k1": "v1"})
	if resolved["N1"] != "v1" {
		t.Errorf("Resolve did not surface populated byKey: %#v", resolved)
	}
}

func TestFieldCache_ReloadDuringInFlightLoad(t *testing.T) {
	// Regression: Reload used to overwrite the FieldCache's sync.Once
	// while a Load was mid-flight, corrupting the once's internal mutex
	// state and panicking with "unlock of unlocked mutex" on the
	// in-flight goroutine. The fix swaps the active entry instead so
	// in-flight callers complete safely on their own once.
	started := make(chan struct{})
	gate := make(chan struct{})
	released := make(chan struct{})
	var startedOnce sync.Once
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		// Only the first fetch (the in-flight one) signals + waits
		// for the gate; the post-reload sanity Load below runs the
		// fetch a second time and must not block.
		startedOnce.Do(func() {
			close(started)
			<-gate
		})
		return []Field{{Key: "abc", Name: "AccountManager"}}, nil
	})

	go func() {
		_ = fc.Load(context.Background())
		close(released)
	}()
	// Wait for the load goroutine to enter fetch — no polling, no
	// timing assumptions. The signal makes the test deterministic
	// even on heavily-loaded CI boxes.
	<-started
	// Reload while the in-flight load is parked in fetch.
	fc.Reload()
	// Release the in-flight load — it must finish without panicking.
	close(gate)
	<-released
	// Sanity: a fresh Load after the dance still works (post-Reload
	// entry loads cleanly).
	if err := fc.Load(context.Background()); err != nil {
		t.Errorf("post-reload Load failed: %v", err)
	}
}

func TestFieldCache_Reload(t *testing.T) {
	calls := 0
	fields := []Field{{Key: "v1", Name: "First"}}
	fc := NewFieldCache(func(_ context.Context) ([]Field, error) {
		calls++
		return fields, nil
	})

	if got := fc.Resolve(context.Background(), map[string]any{"v1": "x"}); got["First"] != "x" {
		t.Fatalf("first load: name resolution lost: %v", got)
	}

	// Mutate the source then reload.
	fields = []Field{{Key: "v2", Name: "Second"}}
	fc.Reload()

	if got := fc.Resolve(context.Background(), map[string]any{"v2": "y"}); got["Second"] != "y" {
		t.Errorf("post-reload: name resolution lost: %v", got)
	}
	if got := fc.Resolve(context.Background(), map[string]any{"v1": "stale"}); got["First"] == "stale" {
		t.Error("post-reload: stale v1 still resolves")
	}
	if calls != 2 {
		t.Errorf("fetch invocation count = %d; want 2 (one before Reload, one after)", calls)
	}
}

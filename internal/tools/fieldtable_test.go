package tools

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

// Every field an Update*Request can send must have a fieldSpec entry.
//
// The table is what changedFields and requireOverwrite walk, so a field
// that is writable but untabled gets written WITHOUT being guarded and
// WITHOUT appearing in the `changed` report — while the tool description
// promises to refuse over any populated field and name each one.
//
// This is an internal test on purpose: the tables stay unexported, and
// exporting five accessors just to assert an invariant would be a worse
// trade than putting the assertion in the same package.
//
// It exists because the /security-review of 2026-09-16 found exactly
// this bug twice — activity participants, and (in a subtler form, via a
// lossy projection) person emails and phones. Both were silently
// destructive: a wholesale-replace collection dropped its other entries
// with nothing refusing and nothing reporting it.
func TestEveryWritableFieldHasAFieldSpec(t *testing.T) {
	// Creates are checked too: createXAction reports `changed` by
	// diffing the created record against a zero one, so a create field
	// with no table entry is a field the create silently fails to
	// report having set.
	cases := []struct {
		resource string
		req      any
		tabled   []string
	}{
		{"deal/update", pipedrive.UpdateDealRequest{}, names(dealBaseFields)},
		{"person/update", pipedrive.UpdatePersonRequest{}, names(personBaseFields)},
		{"organization/update", pipedrive.UpdateOrganizationRequest{}, names(organizationBaseFields)},
		{"activity/update", pipedrive.UpdateActivityRequest{}, names(activityFields)},
		{"note/update", pipedrive.UpdateNoteRequest{}, names(noteFields)},
		{"deal/create", pipedrive.CreateDealRequest{}, names(dealBaseFields)},
		{"person/create", pipedrive.CreatePersonRequest{}, names(personBaseFields)},
		{"organization/create", pipedrive.CreateOrganizationRequest{}, names(organizationBaseFields)},
		{"activity/create", pipedrive.CreateActivityRequest{}, names(activityFields)},
		{"note/create", pipedrive.CreateNoteRequest{}, names(noteFields)},
	}
	for _, tc := range cases {
		t.Run(tc.resource, func(t *testing.T) {
			tabled := make(map[string]bool, len(tc.tabled))
			for _, f := range tc.tabled {
				tabled[f] = true
			}
			rt := reflect.TypeOf(tc.req)
			for i := range rt.NumField() {
				name, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
				if name == "" || name == "-" {
					continue
				}
				// custom_fields IS tabled, just not here: which custom
				// fields a workspace has is its own business, so the
				// specs are derived per call by withCustomFields from
				// the fields the write actually names.
				// TestCustomFieldsReachTheDiffAndTheGuard is the half of
				// this invariant that covers them.
				if name == "custom_fields" {
					continue
				}
				if !tabled[name] {
					t.Errorf("%s: %q is writable but has no fieldSpec entry — it would be written without being guarded or reported",
						tc.resource, name)
				}
			}
		})
	}
}

// The dynamic half of the invariant above: a custom field a write names
// must reach the diff and the overwrite guard the same way a typed field
// does. Nothing static can assert this, because the fields do not exist
// until a caller names one.
func TestCustomFieldsReachTheDiffAndTheGuard(t *testing.T) {
	w := pipedrive.CustomFieldWrite{
		Values: map[string]any{"h1": "after"},
		Names:  map[string]string{"h1": "Renewal note"},
	}
	spec := withCustomFields(nil, w, func(d *pipedrive.Deal) map[string]any { return d.CustomFields })

	filled := &pipedrive.Deal{CustomFields: map[string]any{"h1": "before"}}
	after := &pipedrive.Deal{CustomFields: map[string]any{"h1": "after"}}

	changed := changedFields(spec, filled, after)
	if !slices.Equal(changed, []string{"Renewal note"}) {
		t.Errorf("changed = %v; want the field reported under its workspace name", changed)
	}

	// Replacing a value that is there needs permission...
	if got := populatedFields(spec, filled, changed); !slices.Equal(got, []string{"Renewal note"}) {
		t.Errorf("populated = %v; want the overwrite guard to see it", got)
	}
	// ...filling one that is not does not.
	empty := &pipedrive.Deal{}
	if got := populatedFields(spec, empty, changedFields(spec, empty, after)); len(got) != 0 {
		t.Errorf("populated on an empty field = %v; want none, filling destroys nothing", got)
	}
}

// A set has no order, so the same choices in a different order are the
// same value. Reporting that as a change would make the guard refuse a
// write that alters nothing, and Pipedrive does not promise to echo a
// multi-select back in the order it was sent.
func TestCustomFieldSetOrderIsNotAChange(t *testing.T) {
	w := pipedrive.CustomFieldWrite{
		Values: map[string]any{"h1": []any{69.0, 68.0}},
		Names:  map[string]string{"h1": "Regions"},
	}
	spec := withCustomFields(nil, w, func(d *pipedrive.Deal) map[string]any { return d.CustomFields })

	before := &pipedrive.Deal{CustomFields: map[string]any{"h1": []any{68.0, 69.0}}}
	after := &pipedrive.Deal{CustomFields: map[string]any{"h1": []any{69.0, 68.0}}}
	if changed := changedFields(spec, before, after); len(changed) != 0 {
		t.Errorf("changed = %v; want none, the set holds the same choices", changed)
	}

	// A genuinely different set still registers.
	other := &pipedrive.Deal{CustomFields: map[string]any{"h1": []any{68.0}}}
	if changed := changedFields(spec, before, other); len(changed) != 1 {
		t.Errorf("changed = %v; want the dropped choice reported", changed)
	}
}

// withCustomFields must not write through to the package-level table.
// Appending into its spare capacity would leak one call's custom fields
// into every later call on that resource — a guard reporting fields the
// caller never named, and refusing over them.
func TestWithCustomFieldsDoesNotMutateTheBaseTable(t *testing.T) {
	before := len(dealBaseFields)
	w := pipedrive.CustomFieldWrite{
		Values: map[string]any{"h1": "x"},
		Names:  map[string]string{"h1": "Renewal note"},
	}
	get := func(d *pipedrive.Deal) map[string]any { return d.CustomFields }

	got := withCustomFields(dealBaseFields, w, get)
	if len(got) != before+1 {
		t.Errorf("extended table has %d entries; want %d", len(got), before+1)
	}
	if len(dealBaseFields) != before {
		t.Errorf("the base table grew to %d; it must not be written through", len(dealBaseFields))
	}

	// Twice over, with different fields: the second call must not see
	// the first call's, which is what a shared backing array would give.
	other := pipedrive.CustomFieldWrite{
		Values: map[string]any{"h2": "y"},
		Names:  map[string]string{"h2": "Segment"},
	}
	if n := names(withCustomFields(dealBaseFields, other, get)); slices.Contains(n, "Renewal note") {
		t.Errorf("second call inherited the first call's custom field: %v", n)
	}
}

// Reaching a custom field means the diff sees it and the prediction
// carries it. guardedWrite does both from one pair of adjacent fields —
// Custom and CustomOf — so a resource cannot do one and not the other.
// It used to be two edits in two functions, and a resource that did the
// first and forgot the second dry-ran as "nothing changes" and then
// changed the field unreported.
func TestGuardedWriteCarriesCustomFieldsIntoTheDiff(t *testing.T) {
	stored := map[string]any{"h1": "stored"}
	before := &pipedrive.Deal{CustomFields: stored}

	w := guardedWrite[pipedrive.Deal]{
		Spec:     dealBaseFields,
		Resource: "deal 1",
		Custom: pipedrive.CustomFieldWrite{
			Values: map[string]any{"h1": "written"},
			Names:  map[string]string{"h1": "Renewal note"},
		},
		CustomOf:  func(d *pipedrive.Deal) *map[string]any { return &d.CustomFields },
		Version:   func(*pipedrive.Deal) string { return "" },
		Overwrite: true,
		DryRun:    true,
		Get:       func(context.Context) (*pipedrive.Deal, error) { return before, nil },
		// An overlay that says nothing about custom fields, which is the
		// point: every xAfterUpdate looks like this now.
		Predict: func(d *pipedrive.Deal) pipedrive.Deal { return *d },
	}

	_, changed, stop := w.run(context.Background())
	if stop != nil {
		t.Fatalf("unexpected refusal: %s", contentTextOf(stop))
	}
	if !slices.Equal(changed, []string{"Renewal note"}) {
		t.Errorf("changed = %v; want the custom field reported by name", changed)
	}
	// The prediction must not write through to the record the guard
	// still has to compare against.
	if stored["h1"] != "stored" {
		t.Errorf("the prediction mutated the stored record: %v", stored["h1"])
	}
}

// And the overwrite guard reaches it: replacing a populated custom field
// without permission is refused, naming the field.
func TestGuardedWriteRefusesAPopulatedCustomField(t *testing.T) {
	before := &pipedrive.Deal{CustomFields: map[string]any{"h1": "stored"}}
	w := guardedWrite[pipedrive.Deal]{
		Spec:     dealBaseFields,
		Resource: "deal 1",
		Custom: pipedrive.CustomFieldWrite{
			Values: map[string]any{"h1": "written"},
			Names:  map[string]string{"h1": "Renewal note"},
		},
		CustomOf: func(d *pipedrive.Deal) *map[string]any { return &d.CustomFields },
		Version:  func(*pipedrive.Deal) string { return "" },
		Get:      func(context.Context) (*pipedrive.Deal, error) { return before, nil },
		Predict:  func(d *pipedrive.Deal) pipedrive.Deal { return *d },
	}

	_, _, stop := w.run(context.Background())
	if stop == nil {
		t.Fatal("replacing a populated custom field was allowed without overwrite")
	}
	if text := contentTextOf(stop); !strings.Contains(text, "Renewal note") {
		t.Errorf("the refusal does not name the field: %s", text)
	}
}

// A write that names no custom field leaves CustomOf nil, and must
// behave exactly as it did before any of this existed.
func TestGuardedWriteWithoutCustomFields(t *testing.T) {
	before := &pipedrive.Deal{Title: "Acme renewal"}
	w := guardedWrite[pipedrive.Deal]{
		Spec:      dealBaseFields,
		Resource:  "deal 1",
		Version:   func(*pipedrive.Deal) string { return "" },
		Overwrite: true,
		DryRun:    true,
		Get:       func(context.Context) (*pipedrive.Deal, error) { return before, nil },
		Predict: func(d *pipedrive.Deal) pipedrive.Deal {
			after := *d
			after.Title = "Renamed"
			return after
		},
	}
	_, changed, stop := w.run(context.Background())
	if stop != nil {
		t.Fatalf("unexpected refusal: %s", contentTextOf(stop))
	}
	if !slices.Equal(changed, []string{"title"}) {
		t.Errorf("changed = %v; want title", changed)
	}
}

// contentTextOf reads a refusal's text. The tools_test package has its
// own; this file is the internal one, which cannot see it.
func contentTextOf(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			return t.Text
		}
	}
	return ""
}

// A field whose name went missing is reported under its key rather than// A field whose name went missing is reported under its key rather than
// as a blank entry, which would read as a field with no name instead of
// as the bug it is.
func TestCustomFieldWithoutANameFallsBackToItsKey(t *testing.T) {
	w := pipedrive.CustomFieldWrite{Values: map[string]any{"h1": "x"}}
	spec := withCustomFields(nil, w, func(d *pipedrive.Deal) map[string]any { return d.CustomFields })
	if len(spec) != 1 || spec[0].Name != "h1" {
		t.Errorf("spec = %v; want the key as the fallback name", names(spec))
	}
}

// Every field of a collection element must be rendered by its
// projection. This is projectCollection's stated rule, asserted rather
// than trusted.
//
// The per-field walk matters: TestEverySettableFieldReachesTheDiff sets
// one request field against a ZERO record, so any non-empty collection
// registers as a change there whatever the projection drops. The bug
// that shipped needed same values and a different primary flag — only a
// per-sub-field comparison sees it.
func TestCollectionProjectionsRenderEverySubField(t *testing.T) {
	t.Run("ContactPoint", func(t *testing.T) {
		assertRendersEverySubField(t,
			pipedrive.ContactPoint{Value: "a@example.com", Primary: true, Label: "work"},
			func(cp pipedrive.ContactPoint) string {
				return projectContactPoints([]pipedrive.ContactPoint{cp})
			})
	})
	t.Run("ActivityParticipant", func(t *testing.T) {
		assertRendersEverySubField(t,
			pipedrive.ActivityParticipant{PersonID: 1, Primary: true},
			func(p pipedrive.ActivityParticipant) string {
				return projectParticipants([]pipedrive.ActivityParticipant{p})
			})
	})
}

func assertRendersEverySubField[E any](t *testing.T, base E, project func(E) string) {
	t.Helper()
	rv := reflect.ValueOf(base)
	rt := rv.Type()
	for i := range rt.NumField() {
		name := rt.Field(i).Name
		mutated := reflect.New(rt).Elem()
		mutated.Set(rv)
		if !flipField(mutated.Field(i)) {
			t.Skipf("no flip for %s.%s (%s)", rt.Name(), name, rt.Field(i).Type)
			continue
		}
		if project(base) == project(mutated.Interface().(E)) {
			t.Errorf("%s.%s is not rendered: a write changing only that field is invisible to the guard, so it is skipped as a no-op and reported as already applied",
				rt.Name(), name)
		}
	}
}

// flipField changes a value to something distinguishable from itself.
func flipField(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		v.SetString(v.String() + "-flipped")
		return true
	case reflect.Bool:
		v.SetBool(!v.Bool())
		return true
	case reflect.Int, reflect.Int64:
		v.SetInt(v.Int() + 1)
		return true
	case reflect.Float64:
		v.SetFloat(v.Float() + 1)
		return true
	}
	return false
}

// Truncation must stay visible too: the collection shrinking while its
// first entry survives is what the original contact-point guard missed.
func TestCollectionProjectionsSeeATruncation(t *testing.T) {
	full := []pipedrive.ContactPoint{
		{Value: "a@example.com", Primary: true}, {Value: "b@example.com"},
	}
	if projectContactPoints(full) == projectContactPoints(full[:1]) {
		t.Error("dropping a secondary contact point is invisible to the guard")
	}
	if projectContactPoints(nil) != "" {
		t.Error("an empty collection must project empty, or the guard would refuse over nothing")
	}

	people := []pipedrive.ActivityParticipant{{PersonID: 1, Primary: true}, {PersonID: 2}}
	if projectParticipants(people) == projectParticipants(people[:1]) {
		t.Error("dropping a participant is invisible to the guard")
	}
	if projectParticipants(nil) != "" {
		t.Error("an empty participant list must project empty")
	}
}

func names[T any](spec []fieldSpec[T]) []string {
	out := make([]string, 0, len(spec))
	for _, f := range spec {
		out = append(out, f.Name)
	}
	return out
}

// Setting any single field on an update request must reach `changed`.
//
// This is the assertion that would have caught all three of the bugs
// this package has had in its guard. It pins the whole chain at once:
// the request field must exist in the overlay (or `predicted` never
// moves), in the fieldSpec table (or changedFields never looks), and in
// a projection that renders it (or before and after compare equal).
//
// The overlay is the link nothing else covers — TestEveryWritableFieldHasAFieldSpec
// ties the request to the table, but a field present in both and missing
// from xAfterUpdate makes the dry run predict nothing while the real
// write changes it anyway: unguarded and unreported, the exact shape of
// the participants bug one layer up.
func TestEverySettableFieldReachesTheDiff(t *testing.T) {
	assertFieldsDiff(t, "deal", dealBaseFields, dealAfterUpdate)
	assertFieldsDiff(t, "person", personBaseFields, personAfterUpdate)
	assertFieldsDiff(t, "organization", organizationBaseFields, organizationAfterUpdate)
	assertFieldsDiff(t, "activity", activityFields, activityAfterUpdate)
	assertFieldsDiff(t, "note", noteFields, noteAfterUpdate)
}

func assertFieldsDiff[Rec, Req any](t *testing.T, resource string, spec []fieldSpec[Rec], overlay func(Rec, Req) Rec) {
	t.Helper()
	var zeroReq Req
	rt := reflect.TypeOf(zeroReq)
	for i := range rt.NumField() {
		field := rt.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		t.Run(resource+"/"+name, func(t *testing.T) {
			req := reflect.New(rt).Elem()
			if !setProbeValue(req.Field(i)) {
				t.Skipf("no probe value for %s", field.Type)
			}
			var before Rec
			after := overlay(before, req.Interface().(Req))
			changed := changedFields(spec, &before, &after)
			if !slices.Contains(changed, name) {
				t.Errorf("setting %q on Update%sRequest does not reach `changed` (got %v) — the overlay, the table entry or the projection is missing it, so the write would be unguarded and unreported",
					name, strings.ToUpper(resource[:1])+resource[1:], changed)
			}
		})
	}
}

// setProbeValue writes a distinctive non-zero value, recursing through
// pointers and into the first scalar of a slice element so a collection
// projects non-empty. Reports false for a shape it cannot populate, so a
// new field type surfaces as a skip rather than a silent pass.
func setProbeValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		if !setProbeValue(p.Elem()) {
			return false
		}
		v.Set(p)
		return true
	case reflect.String:
		v.SetString("probe")
		return true
	case reflect.Int, reflect.Int64:
		v.SetInt(42)
		return true
	case reflect.Float64:
		v.SetFloat(1.5)
		return true
	case reflect.Bool:
		v.SetBool(true)
		return true
	case reflect.Slice:
		elem := reflect.New(v.Type().Elem()).Elem()
		if elem.Kind() != reflect.Struct {
			return false
		}
		filled := false
		for i := range elem.NumField() {
			if setProbeValue(elem.Field(i)) {
				filled = true
				break
			}
		}
		if !filled {
			return false
		}
		v.Set(reflect.Append(reflect.MakeSlice(v.Type(), 0, 1), elem))
		return true
	}
	return false
}

// Two distinct collections must never project identically. The values a
// caller supplies are free text, so an unquoted join lets a label
// containing the separator absorb the next entry and make a real change
// look like no change at all.
func TestCollectionProjectionsCannotCollide(t *testing.T) {
	cases := []struct {
		name string
		a, b []pipedrive.ContactPoint
	}{
		{
			// Collides under a plain `value|label` join separated by
			// commas: the first label swallows the second entry whole.
			"label absorbs the next entry",
			[]pipedrive.ContactPoint{{Value: "a", Label: "b"}, {Value: "c", Label: "d"}},
			[]pipedrive.ContactPoint{{Value: "a", Label: "b,c|d"}},
		},
		{
			"separator inside a value",
			[]pipedrive.ContactPoint{{Value: "a,b"}},
			[]pipedrive.ContactPoint{{Value: "a"}, {Value: "b"}},
		},
		{
			"primary marker forged in a label",
			[]pipedrive.ContactPoint{{Value: "a@example.com", Primary: true}},
			[]pipedrive.ContactPoint{{Value: "a@example.com", Label: "*"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if projectContactPoints(tc.a) == projectContactPoints(tc.b) {
				t.Errorf("two different collections project identically as %q — a real change would read as a no-op",
					projectContactPoints(tc.a))
			}
		})
	}
}

func TestManageNoteHandler_DeleteIsNamedNotFallenInto(t *testing.T) {
	// notes is the only resource whose switch default would destroy.
	// Guard against a future action silently inheriting that branch by
	// asserting the default errors rather than deletes.
	if !allowedNoteActions["delete"] {
		t.Fatal("delete is no longer a known note action; this test needs rewriting")
	}
	for action := range allowedNoteActions {
		if action != "create" && action != "update" && action != "delete" {
			t.Errorf("action %q has no explicit case in manageNoteHandler and would fall through to delete", action)
		}
	}
}

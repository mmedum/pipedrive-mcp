package tools

import (
	"reflect"
	"slices"
	"strings"
	"testing"

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
		{"deal/update", pipedrive.UpdateDealRequest{}, names(dealFields)},
		{"person/update", pipedrive.UpdatePersonRequest{}, names(personFields)},
		{"organization/update", pipedrive.UpdateOrganizationRequest{}, names(organizationFields)},
		{"activity/update", pipedrive.UpdateActivityRequest{}, names(activityFields)},
		{"note/update", pipedrive.UpdateNoteRequest{}, names(noteFields)},
		{"deal/create", pipedrive.CreateDealRequest{}, names(dealFields)},
		{"person/create", pipedrive.CreatePersonRequest{}, names(personFields)},
		{"organization/create", pipedrive.CreateOrganizationRequest{}, names(organizationFields)},
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
				if !tabled[name] {
					t.Errorf("%s: %q is writable but has no fieldSpec entry — it would be written without being guarded or reported",
						tc.resource, name)
				}
			}
		})
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
	assertFieldsDiff(t, "deal", dealFields, dealAfterUpdate)
	assertFieldsDiff(t, "person", personFields, personAfterUpdate)
	assertFieldsDiff(t, "organization", organizationFields, organizationAfterUpdate)
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

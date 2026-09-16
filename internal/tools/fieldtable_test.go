package tools

import (
	"reflect"
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
	cases := []struct {
		resource string
		req      any
		tabled   []string
	}{
		{"deal", pipedrive.UpdateDealRequest{}, names(dealFields)},
		{"person", pipedrive.UpdatePersonRequest{}, names(personFields)},
		{"organization", pipedrive.UpdateOrganizationRequest{}, names(organizationFields)},
		{"activity", pipedrive.UpdateActivityRequest{}, names(activityFields)},
		{"note", pipedrive.UpdateNoteRequest{}, names(noteFields)},
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

// Collections must project their whole contents. A projection that
// returned one representative value would compare equal across a
// truncation that deleted every other entry, which is what made the
// contact-point guard ineffective before 2026-09-16.
func TestCollectionProjectionsSeeATruncation(t *testing.T) {
	full := []pipedrive.ContactPoint{
		{Value: "a@example.com", Primary: true},
		{Value: "b@example.com"},
	}
	truncated := []pipedrive.ContactPoint{{Value: "a@example.com", Primary: true}}
	if projectContactPoints(full) == projectContactPoints(truncated) {
		t.Error("dropping a secondary contact point is invisible to the guard")
	}
	if projectContactPoints(nil) != "" {
		t.Error("an empty collection must project empty, or the guard would refuse over nothing")
	}

	people := []pipedrive.ActivityParticipant{{PersonID: 1, Primary: true}, {PersonID: 2}}
	fewer := []pipedrive.ActivityParticipant{{PersonID: 1, Primary: true}}
	if projectParticipants(people) == projectParticipants(fewer) {
		t.Error("dropping a participant is invisible to the guard")
	}
	// Promoting a different primary is a real change the caller should see.
	repromoted := []pipedrive.ActivityParticipant{{PersonID: 1}, {PersonID: 2, Primary: true}}
	if projectParticipants(people) == projectParticipants(repromoted) {
		t.Error("changing which participant is primary is invisible to the guard")
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

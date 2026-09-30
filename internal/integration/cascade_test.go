//go:build integration

package integration

import (
	"slices"
	"testing"

	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
)

// What deleting a parent record does to the records hanging off it.
//
// docs/security.md said this was not documented by Pipedrive and not
// verified here, and that if it were ever established the guard to add
// was on the three parents. This is the probe that establishes it, on a
// tree of records the suite creates in its own pipeline: an
// organization, a person in it, a deal linked to both, an activity and a
// note on the deal, and a note on the parent itself. Each case deletes
// one parent and reads every other record back.
//
// What it establishes is that the other records are not deleted, and no
// more: a deal still reads with a status other than "deleted", a note
// still reads active, and a person, an organization or an activity is
// still in a listing, which leaves deleted ones out. Whether a record
// still points at the deleted parent is not checked.
func TestWrite_DeletingAParentLeavesWhatHangsOffIt(t *testing.T) {
	cases := []struct {
		parent string
		tool   string
		field  string
		id     func(s *scratch) *int64
	}{
		{"organization", "manage_organization", "org_id", func(s *scratch) *int64 { return &s.OrgID }},
		{"person", "manage_person", "person_id", func(s *scratch) *int64 { return &s.PersonID }},
		{"deal", "manage_deal", "deal_id", func(s *scratch) *int64 { return &s.DealID }},
	}
	for _, tc := range cases {
		t.Run(tc.parent, func(t *testing.T) {
			s := newScratch(t)
			id := tc.id(s)
			// A note on the parent itself. The deal already has one.
			var own int64
			if tc.parent != "deal" {
				var created noteOut
				mustCall(t, "manage_note", map[string]any{"action": "create",
					"content": "<p>mcp-test note on the parent</p>", tc.field: *id}, &created)
				own = created.Note.ID
				dropOnCleanup(t, "manage_note", "note_id", &own)
			}
			dealID := s.DealID
			mustCall(t, tc.tool, map[string]any{"action": "delete", tc.field: *id}, nil)
			*id = 0 // gone already; the teardown leaves it

			for _, child := range survivors(t, s, dealID, own) {
				if !child.alive {
					t.Errorf("deleting the %s also removed its %s: %s", tc.parent, child.what, child.how)
				} else {
					t.Logf("%s survives the %s's delete: %s", child.what, tc.parent, child.how)
				}
			}
		})
	}
}

// survivor is one record of the tree after a parent went.
type survivor struct {
	what  string
	alive bool
	how   string
}

// listed is a record looked for in a listing, which leaves deleted
// records out.
func listed(what string, found bool) survivor {
	how := "no longer listed"
	if found {
		how = "still listed"
	}
	return survivor{what, found, how}
}

// survivors reads back every record of the tree still meant to exist:
// s has the deleted parent's id zeroed, dealID is the deal the activity
// hangs off, and own the note on the parent, or zero.
func survivors(t *testing.T, s *scratch, dealID, own int64) []survivor {
	t.Helper()
	var out []survivor
	if s.DealID != 0 {
		var d dealOut
		if res := call(t, "get_deal", map[string]any{"deal_id": s.DealID}); res.IsError {
			out = append(out, survivor{"deal", false, "get_deal: " + testutil.TextContent(res)})
		} else {
			testutil.DecodeStructured(t, res.StructuredContent, &d)
			out = append(out, survivor{"deal", d.Deal.Status != "deleted", "status " + d.Deal.Status})
		}
	}
	// The newest records come first, and the tree was made moments ago.
	newest := map[string]any{"limit": 100, "sort_by": "id", "sort_direction": "desc"}
	if s.PersonID != 0 {
		var ps personsOut
		mustCall(t, "list_persons", newest, &ps)
		out = append(out, listed("person", slices.ContainsFunc(ps.Persons, func(p personRow) bool { return p.ID == s.PersonID })))
	}
	if s.OrgID != 0 {
		var orgs orgsOut
		mustCall(t, "list_organizations", newest, &orgs)
		out = append(out, listed("organization", slices.ContainsFunc(orgs.Organizations, func(o orgRow) bool { return o.ID == s.OrgID })))
	}
	var acts activitiesOut
	mustCall(t, "list_activities", map[string]any{"deal_id": dealID, "status": "all"}, &acts)
	out = append(out, listed("activity", slices.ContainsFunc(acts.Activities, func(a activityRow) bool { return a.ID == s.ActivityID })))
	for _, note := range []int64{s.NoteID, own} {
		if note != 0 {
			out = append(out, noteSurvivor(t, note))
		}
	}
	return out
}

// noteSurvivor reads one note back; a deleted note still reads, inactive.
func noteSurvivor(t *testing.T, id int64) survivor {
	t.Helper()
	var n noteOut
	res := call(t, "get_note", map[string]any{"note_id": id})
	if res.IsError {
		return survivor{"note", false, "get_note: " + testutil.TextContent(res)}
	}
	testutil.DecodeStructured(t, res.StructuredContent, &n)
	if !n.Note.ActiveFlag {
		return survivor{"note", false, "inactive"}
	}
	return survivor{"note", true, "active"}
}

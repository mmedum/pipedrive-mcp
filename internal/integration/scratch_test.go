//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
)

// The records a write probe works in, created by this suite and deleted
// afterwards.
//
// Nothing here is anybody's. A probe used to pick a real record, edit a
// field and put it back — the restore was verified, the contract was
// kept, and a real customer's deal still went to won and to lost four
// times because a status change is not a field. Even where the field
// restores perfectly, a live record edited by a test is a live record
// participating in somebody's reporting, notifications and history for
// as long as the test holds it.
//
// So the probes stopped borrowing. Every mutation below lands on a
// record with "mcp-test" in its name, in the pipeline named by
// PIPEDRIVE_TEST_PIPELINE_ID, and is deleted when the test ends.
type scratch struct {
	OrgID      int64
	PersonID   int64
	DealID     int64
	ActivityID int64
	NoteID     int64
	StageID    int64
	Title      string
}

// newScratch builds the records and registers their deletion. Deletes
// are soft — Pipedrive purges after 30 days — so even a teardown that
// fails leaves nothing permanent.
func newScratch(t *testing.T) scratch {
	t.Helper()
	pipeline := requireOwnPipeline(t)

	var stages stagesOut
	mustCall(t, "list_stages", map[string]any{"pipeline_id": pipeline}, &stages)
	stage := pickFirst(t, stages.Stages, "the test pipeline has no stages")

	stamp := time.Now().UTC().Format("20060102-150405.000")
	s := scratch{StageID: stage.ID, Title: "mcp-test " + stamp}

	// Deleted newest-first, so a parent never goes before its children.
	drop := func(tool, idField string, id *int64) {
		t.Cleanup(func() {
			if *id == 0 {
				return
			}
			if res := call(t, tool, map[string]any{"action": "delete", idField: *id}); res.IsError {
				t.Errorf("LEFT BEHIND: %s %d: %s", idField, *id, testutil.TextContent(res))
			}
		})
	}
	drop("manage_organization", "org_id", &s.OrgID)
	drop("manage_person", "person_id", &s.PersonID)
	drop("manage_deal", "deal_id", &s.DealID)
	drop("manage_activity", "activity_id", &s.ActivityID)
	drop("manage_note", "note_id", &s.NoteID)

	newID := func(tool string, args map[string]any, key string) int64 {
		t.Helper()
		var out map[string]any
		mustCall(t, tool, args, &out)
		rec, _ := out[key].(map[string]any)
		n, _ := rec["id"].(float64)
		if n == 0 {
			t.Fatalf("%s returned no id", tool)
		}
		return int64(n)
	}

	s.OrgID = newID("manage_organization",
		map[string]any{"action": "create", "name": s.Title + " org"}, "organization")
	// One call, with the parts rather than a full name. first_name and
	// emails are populated so a probe has something to overwrite.
	//
	// This used to be a create and then an update. create required
	// `name`, and Pipedrive refuses a body carrying `name` together
	// with first_name/last_name — "Cannot set 'name' and
	// 'first_name'/'last_name' at the same time" — so there was no way
	// through this tool to create a person with structured names. That
	// was a gap in the surface rather than a quirk of this fixture, and
	// building the fixture the natural way is what keeps it closed.
	s.PersonID = newID("manage_person", map[string]any{
		"action":     "create",
		"first_name": "Mcptest", "last_name": s.Title + " person",
		"org_id": s.OrgID,
		// An address nobody can receive mail at (RFC 2606).
		"emails": []map[string]any{{"value": "probe@example.invalid", "primary": true}},
	}, "person")
	s.DealID = newID("manage_deal", map[string]any{
		"action": "create", "title": s.Title + " deal",
		"pipeline_id": pipeline, "stage_id": stage.ID,
		"org_id": s.OrgID, "person_id": s.PersonID,
		"expected_close_date": "2027-01-31",
	}, "deal")
	s.ActivityID = newID("manage_activity", map[string]any{
		"action": "create", "subject": s.Title + " activity",
		"type": "call", "deal_id": s.DealID,
	}, "activity")
	s.NoteID = newID("manage_note", map[string]any{
		"action": "create", "content": "<p>mcp-test note</p>", "deal_id": s.DealID,
	}, "note")

	return s
}

package tools_test

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
	"github.com/mmedum/pipedrive-mcp/internal/tools"
)

// The delete action on the four v2 resources. One file rather than four
// additions, because the interesting assertions are the same for all of
// them and the differences between them are the point: a deal can be
// read as already deleted, the others cannot, and only an activity has
// nothing hanging off it.
//
// Pipedrive has no undo, so the assertion that matters most in every
// case below is the negative one — dry_run must not reach the client.

func TestDelete_SendsTheDeleteAndReportsIt(t *testing.T) {
	deal := &fakeDealsClient{deal: &pipedrive.Deal{ID: 7, Title: "Acme renewal", Status: "open"}}
	person := &fakePersonsClient{person: &pipedrive.Person{ID: 7, Name: "Acme contact"}}
	org := &fakeOrganizationsClient{org: &pipedrive.Organization{ID: 7, Name: "Acme"}}
	activity := &fakeActivitiesClient{activity: &pipedrive.Activity{ID: 7, Subject: "Acme call"}}

	for _, tc := range []struct {
		tool     string
		register func(*mcp.Server)
		args     map[string]any
		calls    func() int
		lastID   func() int64
	}{
		{"manage_deal", func(s *mcp.Server) { tools.RegisterDeals(s, deal, "acme", tools.RegisterOptions{}) },
			map[string]any{"action": "delete", "deal_id": 7}, func() int { return deal.deleteCalls }, func() int64 { return deal.lastDeleteID }},
		{"manage_person", func(s *mcp.Server) { tools.RegisterPersons(s, person, "acme", tools.RegisterOptions{}) },
			map[string]any{"action": "delete", "person_id": 7}, func() int { return person.deleteCalls }, func() int64 { return person.lastDeleteID }},
		{"manage_organization", func(s *mcp.Server) { tools.RegisterOrganizations(s, org, "acme", tools.RegisterOptions{}) },
			map[string]any{"action": "delete", "org_id": 7}, func() int { return org.deleteCalls }, func() int64 { return org.lastDeleteID }},
		{"manage_activity", func(s *mcp.Server) { tools.RegisterActivities(s, activity, "acme", tools.RegisterOptions{}) },
			map[string]any{"action": "delete", "activity_id": 7}, func() int { return activity.deleteCalls }, func() int64 { return activity.lastDeleteID }},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			res := testutil.CallTool(t, tc.register, tc.tool, tc.args)
			if res.IsError {
				t.Fatalf("delete returned an error result: %s", testutil.TextContent(res))
			}
			if got := tc.calls(); got != 1 {
				t.Fatalf("the client delete was called %d times, want 1", got)
			}
			if got := tc.lastID(); got != 7 {
				t.Errorf("deleted id %d, want 7", got)
			}
		})
	}
}

// The guard that matters: a rehearsal must not reach Pipedrive. A
// delete that ignored dry_run would be the one mistake this server
// cannot walk back.
//
// The fakes are built up front and read directly after the call. The
// first version of this test stashed the count in a t.Cleanup and
// checked it in another — cleanups run last-in-first-out, so the check
// ran before the copy and read zero every time. It passed while
// asserting nothing.
func TestDelete_DryRunSendsNothing(t *testing.T) {
	deal := &fakeDealsClient{deal: &pipedrive.Deal{ID: 7, Status: "open"}}
	person := &fakePersonsClient{person: &pipedrive.Person{ID: 7}}
	org := &fakeOrganizationsClient{org: &pipedrive.Organization{ID: 7}}
	activity := &fakeActivitiesClient{activity: &pipedrive.Activity{ID: 7}}

	for _, tc := range []struct {
		tool     string
		register func(*mcp.Server)
		args     map[string]any
		calls    func() int
	}{
		{"manage_deal", func(s *mcp.Server) { tools.RegisterDeals(s, deal, "acme", tools.RegisterOptions{}) },
			map[string]any{"action": "delete", "deal_id": 7, "dry_run": true}, func() int { return deal.deleteCalls }},
		{"manage_person", func(s *mcp.Server) { tools.RegisterPersons(s, person, "acme", tools.RegisterOptions{}) },
			map[string]any{"action": "delete", "person_id": 7, "dry_run": true}, func() int { return person.deleteCalls }},
		{"manage_organization", func(s *mcp.Server) { tools.RegisterOrganizations(s, org, "acme", tools.RegisterOptions{}) },
			map[string]any{"action": "delete", "org_id": 7, "dry_run": true}, func() int { return org.deleteCalls }},
		{"manage_activity", func(s *mcp.Server) { tools.RegisterActivities(s, activity, "acme", tools.RegisterOptions{}) },
			map[string]any{"action": "delete", "activity_id": 7, "dry_run": true}, func() int { return activity.deleteCalls }},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			res := testutil.CallTool(t, tc.register, tc.tool, tc.args)
			if res.IsError {
				t.Fatalf("dry-run delete returned an error result: %s", testutil.TextContent(res))
			}
			if got := tc.calls(); got != 0 {
				t.Errorf("dry_run still sent %d delete(s) upstream", got)
			}
		})
	}
}

// PIPEDRIVE_DRY_RUN is a floor: a caller can turn a rehearsal on and
// cannot turn one off. A delete is where that promise earns its keep.
func TestDelete_HonorsTheDryRunFloor(t *testing.T) {
	f := &fakeDealsClient{deal: &pipedrive.Deal{ID: 7, Status: "open"}}
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, f, "acme", tools.RegisterOptions{DryRun: true})
	}, "manage_deal", map[string]any{"action": "delete", "deal_id": 7, "dry_run": false})
	if res.IsError {
		t.Fatalf("unexpected error result: %s", testutil.TextContent(res))
	}
	if f.deleteCalls != 0 {
		t.Errorf("the floor was overridden by dry_run:false — %d delete(s) sent", f.deleteCalls)
	}
}

// expect_version carries the update_time from the read that informed
// the delete. A record that moved since is a record somebody else is
// working on.
func TestDelete_RefusesWhenTheRecordMoved(t *testing.T) {
	f := &fakeDealsClient{deal: &pipedrive.Deal{ID: 7, Status: "open", UpdateTime: "2026-09-19 10:00:00"}}
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, f, "acme", tools.RegisterOptions{})
	}, "manage_deal", map[string]any{
		"action": "delete", "deal_id": 7, "expect_version": "2026-09-18 09:00:00",
	})
	if !res.IsError {
		t.Fatal("a stale expect_version was accepted; the delete went through on a record that had moved")
	}
	if f.deleteCalls != 0 {
		t.Errorf("refused but still sent %d delete(s)", f.deleteCalls)
	}
}

// A deal is the one resource whose deleted state is readable — status
// carries it. Deleting one twice should report rather than fire again.
func TestDelete_AlreadyDeletedDealSendsNothing(t *testing.T) {
	f := &fakeDealsClient{deal: &pipedrive.Deal{ID: 7, Status: "deleted"}}
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, f, "acme", tools.RegisterOptions{})
	}, "manage_deal", map[string]any{"action": "delete", "deal_id": 7})
	if res.IsError {
		t.Fatalf("unexpected error result: %s", testutil.TextContent(res))
	}
	if f.deleteCalls != 0 {
		t.Errorf("a second delete was sent for an already-deleted deal (%d calls)", f.deleteCalls)
	}
}

func TestDelete_RejectsANonPositiveID(t *testing.T) {
	f := &fakeDealsClient{}
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, f, "acme", tools.RegisterOptions{})
	}, "manage_deal", map[string]any{"action": "delete", "deal_id": 0})
	if !res.IsError {
		t.Fatal("deal_id 0 was accepted")
	}
	if !strings.HasPrefix(testutil.TextContent(res), "[validation]") {
		t.Errorf("error = %q; want a [validation] prefix", testutil.TextContent(res))
	}
	if f.deleteCalls != 0 {
		t.Errorf("an invalid id still reached the client (%d calls)", f.deleteCalls)
	}
}

// reopen on a WON deal was broken: the request always carried
// lost_reason:"" to clear it, and Pipedrive accepts lost_reason only
// while the deal IS lost — "Lost reason and lost time must can only be
// set when status is lost" — so the whole write was rejected. The tool
// description promises reopen undoes mark_won, and it did not.
//
// Three live eval runs found this and nothing else had: the fake this
// suite drives accepts any request, so only a real API could refuse it.
// These pin the request shape so the fake can now say so too.
func TestReopen_DoesNotClearTheLostReasonOfAWonDeal(t *testing.T) {
	f := &fakeDealsClient{
		deal:       &pipedrive.Deal{ID: 7, Status: "won", Title: "Acme renewal"},
		updateDeal: &pipedrive.Deal{ID: 7, Status: "open", Title: "Acme renewal"},
	}
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, f, "acme", tools.RegisterOptions{})
	}, "manage_deal", map[string]any{"action": "reopen", "deal_id": 7})
	if res.IsError {
		t.Fatalf("reopen on a won deal returned an error: %s", testutil.TextContent(res))
	}
	if f.lastUpdateReq.LostReason != nil {
		t.Errorf("reopen sent lost_reason=%q on a won deal; Pipedrive rejects the whole write for that",
			*f.lastUpdateReq.LostReason)
	}
	if f.lastUpdateReq.Status == nil || *f.lastUpdateReq.Status != "open" {
		t.Error("reopen did not set status open")
	}
}

// And not for a lost deal either. Pipedrive validates the RESULTING
// state, so lost_reason alongside status:open is refused whatever the
// deal was before — which is why the first attempt at this fix, which
// only dropped the field for a won deal, would have left the commoner
// case broken.
func TestReopen_DoesNotClearTheLostReasonOfALostDealEither(t *testing.T) {
	f := &fakeDealsClient{
		deal:       &pipedrive.Deal{ID: 7, Status: "lost", LostReason: "Lost to competitor"},
		updateDeal: &pipedrive.Deal{ID: 7, Status: "open"},
	}
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, f, "acme", tools.RegisterOptions{})
	}, "manage_deal", map[string]any{"action": "reopen", "deal_id": 7})
	if res.IsError {
		t.Fatalf("reopen on a lost deal returned an error: %s", testutil.TextContent(res))
	}
	if f.lastUpdateReq.LostReason != nil {
		t.Errorf("reopen sent lost_reason=%q; Pipedrive refuses it alongside status:open whatever the deal was before",
			*f.lastUpdateReq.LostReason)
	}
}

// The point of naming fields rather than passing a flag: a caller who
// agreed to replace the title has NOT agreed to replace the value, and
// the guard now says so. `overwrite: true` could not express that — it
// permitted every populated field the write happened to touch, so a
// caller who meant one thing authorized all of them.
//
// This does not stop a model routing around the guard; nothing in band
// can, since anything the refusal says a model can echo back. What it
// removes is the blanket.
func TestOverwrite_NamingSomeFieldsStillRefusesTheRest(t *testing.T) {
	f := &fakeDealsClient{
		deal: &pipedrive.Deal{ID: 7, Title: "Acme renewal", Value: 5000, Status: "open"},
	}
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, f, "acme", tools.RegisterOptions{})
	}, "manage_deal", map[string]any{
		"action": "update", "deal_id": 7,
		"title": "Acme renewal 2027", "value": 9000,
		"overwrite": []string{"title"},
	})
	if !res.IsError {
		t.Fatal("naming only title permitted replacing value as well")
	}
	txt := testutil.TextContent(res)
	if !strings.Contains(txt, "value") {
		t.Errorf("refusal %q does not name the field that was not agreed to", txt)
	}
	if strings.Contains(txt, `"title"`) {
		t.Errorf("refusal %q re-lists title, which the caller did name", txt)
	}
	if f.updateCalls != 0 {
		t.Errorf("a partially-agreed write still reached upstream (%d calls)", f.updateCalls)
	}
}

// Naming every clobbered field permits the write, which is the case the
// old bool covered and must keep working.
func TestOverwrite_NamingEveryFieldPermitsTheWrite(t *testing.T) {
	f := &fakeDealsClient{
		deal:       &pipedrive.Deal{ID: 7, Title: "Acme renewal", Value: 5000, Status: "open"},
		updateDeal: &pipedrive.Deal{ID: 7, Title: "Acme renewal 2027", Value: 9000, Status: "open"},
	}
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, f, "acme", tools.RegisterOptions{})
	}, "manage_deal", map[string]any{
		"action": "update", "deal_id": 7,
		"title": "Acme renewal 2027", "value": 9000,
		"overwrite": []string{"title", "value"},
	})
	if res.IsError {
		t.Fatalf("naming every clobbered field should permit the write: %s", testutil.TextContent(res))
	}
	if f.updateCalls != 1 {
		t.Errorf("update calls = %d, want 1", f.updateCalls)
	}
}

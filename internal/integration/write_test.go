//go:build integration

package integration

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
)

// The reversible write probes. Everything here needs
// PIPEDRIVE_INTEGRATION_WRITES=1 on top of the build tag, because the
// workspace on the other end is a real CRM and Pipedrive has no undo.
//
// The contract every probe in this file keeps:
//
//  1. capture the original value before touching anything;
//  2. register the restore with undo, so t.Cleanup runs it even when
//     the body fails half way through;
//  3. verify the restore with a fresh read, not the write's echo;
//  4. only ever append to a collection and then trim it back, so
//     replace-semantics cannot drop an entry that was already there;
//  5. never write a field that was empty — v2 has no spelling that
//     empties one again (docs/architecture.md, "Clearing a field"), so
//     an empty field is not a reversible place to write;
//  6. assert on shapes, not on content.

// undo registers a probe's restore. A restore that did not take is a
// failure in its own right: the suite has changed somebody's CRM and
// nothing else is going to notice.
func undo(t *testing.T, what string, restore func() (ok bool, detail string)) {
	t.Helper()
	t.Cleanup(func() {
		ok, detail := restore()
		if !ok {
			t.Errorf("LEFT CHANGED: %s (%s)", what, detail)
		}
	})
}

func TestWrite_ActivityCompletesAndReopens(t *testing.T) {
	id := newScratch(t).ActivityID

	undo(t, fmt.Sprintf("activity %d left marked done", id), func() (bool, string) {
		if res := call(t, "manage_activity", map[string]any{
			"action": "reopen", "activity_id": id,
		}); res.IsError {
			return false, testutil.TextContent(res)
		}
		var back activityOut
		mustCall(t, "get_activity", map[string]any{"activity_id": id}, &back)
		return !back.Activity.Done, fmt.Sprintf("done=%v", back.Activity.Done)
	})

	var out writeOut
	mustCall(t, "manage_activity", map[string]any{
		"action": "complete", "activity_id": id,
	}, &out)
	if !slices.Contains(out.Changed, "done") {
		t.Errorf("changed = %v; want done", out.Changed)
	}

	var after activityOut
	mustCall(t, "get_activity", map[string]any{"activity_id": id}, &after)
	if !after.Activity.Done {
		t.Error("an independent read does not see the completion")
	}
}

// TestWrite_NoteRoundTrip is the v1 carve-out's write path: POST, PUT
// and DELETE against /api/v1/notes, none of which v2 exposes. It
// writes only a record it created itself.
func TestWrite_NoteRoundTrip(t *testing.T) {
	s := newScratch(t)

	var created noteWriteOut
	mustCall(t, "manage_note", map[string]any{
		"action":  "create",
		"content": "<p>" + probeValue("note") + "</p>",
		"deal_id": s.DealID,
	}, &created)
	id := created.Note.ID
	if id == 0 {
		t.Fatal("create returned no note id")
	}

	// A v1 delete is soft: the record stays, readable, with
	// active_flag false. That is the whole restore available, and the
	// tool description says so rather than pretending otherwise.
	undo(t, fmt.Sprintf("note %d still active", id), func() (bool, string) {
		if res := call(t, "manage_note", map[string]any{
			"action": "delete", "note_id": id,
		}); res.IsError {
			return false, testutil.TextContent(res)
		}
		var back noteOut
		mustCall(t, "get_note", map[string]any{"note_id": id}, &back)
		return !back.Note.ActiveFlag, fmt.Sprintf("active_flag=%v", back.Note.ActiveFlag)
	})

	edited := "<p>" + probeValue("note") + " edited</p>"
	var updated noteWriteOut
	mustCall(t, "manage_note", map[string]any{
		"action": "update", "note_id": id, "content": edited, "overwrite": true,
	}, &updated)
	if !slices.Contains(updated.Changed, "content") {
		t.Errorf("changed = %v; want content", updated.Changed)
	}

	var after noteOut
	mustCall(t, "get_note", map[string]any{"note_id": id}, &after)
	if !strings.Contains(after.Note.Content, "edited") {
		t.Error("an independent read does not see the edit")
	}

	// The guard, over content this test knows is populated.
	res := call(t, "manage_note", map[string]any{
		"action": "update", "note_id": id, "content": "<p>x</p>",
	})
	if !res.IsError || errClass(res) != "refused" {
		t.Errorf("update replaced populated content without overwrite (class %q)", errClass(res))
	}
}

// TestWrite_PersonFieldRoundTrips also pins the divergence between a
// rehearsal and the write it rehearses: Pipedrive derives name from
// first_name and last_name, so the dry run — which diffs the request's
// prediction — cannot know name will move, while the real write —
// which diffs what came back — reports it. Neither is wrong, and a
// fake cannot show the difference.
func TestWrite_PersonFieldRoundTrips(t *testing.T) {
	s := newScratch(t)
	var target personOut
	mustCall(t, "get_person", map[string]any{"person_id": s.PersonID}, &target)
	id, original := s.PersonID, target.Person.FirstName

	undo(t, fmt.Sprintf("person %d first_name not restored", id), func() (bool, string) {
		if res := call(t, "manage_person", map[string]any{
			"action": "update", "person_id": id, "first_name": original, "overwrite": true,
		}); res.IsError {
			return false, testutil.TextContent(res)
		}
		var back personOut
		mustCall(t, "get_person", map[string]any{"person_id": id}, &back)
		return back.Person.FirstName == original, "first_name differs"
	})

	probe := original + "-probe"

	var rehearsal writeOut
	mustCall(t, "manage_person", map[string]any{
		"action": "update", "person_id": id, "first_name": probe,
		"overwrite": true, "dry_run": true,
	}, &rehearsal)
	if slices.Contains(rehearsal.Changed, "name") {
		t.Errorf("the rehearsal predicted name = %v; it cannot know what Pipedrive derives", rehearsal.Changed)
	}

	var out writeOut
	mustCall(t, "manage_person", map[string]any{
		"action": "update", "person_id": id, "first_name": probe, "overwrite": true,
	}, &out)
	if !slices.Contains(out.Changed, "first_name") {
		t.Errorf("changed = %v; want first_name", out.Changed)
	}
	if !slices.Contains(out.Changed, "name") {
		t.Errorf("changed = %v; the write should report the derived name it moved", out.Changed)
	}

	var after personOut
	mustCall(t, "get_person", map[string]any{"person_id": id}, &after)
	if after.Person.FirstName != probe {
		t.Error("an independent read does not see the write")
	}
	if !strings.Contains(after.Person.Name, probe) {
		t.Error("name did not follow first_name upstream; the derivation this suite documents no longer holds")
	}
}

// TestWrite_PersonEmailsReplaceWholesale tests the premise the
// contact-point guard rests on. It appends an address and takes it
// away again: every write carries the whole existing set, so if
// Pipedrive really does replace rather than merge, nothing that was
// there can be lost either way.
func TestWrite_PersonEmailsReplaceWholesale(t *testing.T) {
	requireWrites(t)

	id := newScratch(t).PersonID

	// The scratch person is created with one address, which is what
	// this appends to. Read it rather than assuming: the set read back
	// is what a wholesale replace has to carry.
	var before personOut
	mustCall(t, "get_person", map[string]any{"person_id": id}, &before)
	original := before.Person.Emails
	if len(original) == 0 {
		t.Fatal("the scratch person was created without the address this probe appends to")
	}

	undo(t, fmt.Sprintf("person %d emails not restored", id), func() (bool, string) {
		if res := call(t, "manage_person", map[string]any{
			"action": "update", "person_id": id, "emails": original, "overwrite": true,
		}); res.IsError {
			return false, testutil.TextContent(res)
		}
		var back personOut
		mustCall(t, "get_person", map[string]any{"person_id": id}, &back)
		if len(back.Person.Emails) != len(original) {
			return false, fmt.Sprintf("%d addresses, want %d", len(back.Person.Emails), len(original))
		}
		for _, e := range back.Person.Emails {
			if strings.Contains(e.Value, probeEmail) {
				return false, "the probe address is still on the record"
			}
		}
		return true, ""
	})

	appended := append(slices.Clone(original), contactRow{Value: probeEmail, Label: "probe"})
	mustCall(t, "manage_person", map[string]any{
		"action": "update", "person_id": id, "emails": appended, "overwrite": true,
	}, nil)

	var after personOut
	mustCall(t, "get_person", map[string]any{"person_id": id}, &after)
	if len(after.Person.Emails) != len(original)+1 {
		t.Fatalf("emails went %d -> %d; a write carrying the whole set should have appended one",
			len(original), len(after.Person.Emails))
	}
}

// probeEmail is on example.com, which RFC 2606 reserves, so a restore
// that fails cannot leave a deliverable address on somebody's contact.
const probeEmail = "pipedrive-mcp-probe@example.com"

func TestWrite_OrganizationNameRoundTrips(t *testing.T) {
	requireWrites(t)

	sc := newScratch(t)
	var target orgOut
	mustCall(t, "get_organization", map[string]any{"org_id": sc.OrgID}, &target)
	id, original := sc.OrgID, target.Organization.Name

	undo(t, fmt.Sprintf("organization %d name not restored", id), func() (bool, string) {
		if res := call(t, "manage_organization", map[string]any{
			"action": "update", "org_id": id, "name": original, "overwrite": true,
		}); res.IsError {
			return false, testutil.TextContent(res)
		}
		var back orgOut
		mustCall(t, "get_organization", map[string]any{"org_id": id}, &back)
		return back.Organization.Name == original, "name differs"
	})

	var out writeOut
	mustCall(t, "manage_organization", map[string]any{
		"action": "update", "org_id": id, "name": original + "-probe", "overwrite": true,
	}, &out)
	if !slices.Contains(out.Changed, "name") {
		t.Errorf("changed = %v; want name", out.Changed)
	}

	var after orgOut
	mustCall(t, "get_organization", map[string]any{"org_id": id}, &after)
	if !strings.HasSuffix(after.Organization.Name, "-probe") {
		t.Error("an independent read does not see the write")
	}
}

// TestWrite_DealDateRoundTrips is patchV2 end to end. It moves a date
// that is already set rather than filling an empty one, because
// setting an empty field is a one-way trip on v2 — see rule 5 at the
// top of this file.
func TestWrite_DealDateRoundTrips(t *testing.T) {
	requireWrites(t)

	// The scratch deal is created with an expected_close_date, so this
	// has something populated to write over without borrowing one.
	// 0000-00-00 is MySQL's zero date, which is what a v2 write of ""
	// leaves behind — creating one is the problem this probe avoids.
	sc := newScratch(t)
	var target dealOut
	mustCall(t, "get_deal", map[string]any{"deal_id": sc.DealID}, &target)
	id, original, version := sc.DealID, target.Deal.ExpectedCloseDate, target.Deal.UpdateTime
	if original == "" || original == "0000-00-00" {
		t.Fatalf("the scratch deal has no usable expected_close_date (%q)", original)
	}

	undo(t, fmt.Sprintf("deal %d expected_close_date not restored", id), func() (bool, string) {
		if res := call(t, "manage_deal", map[string]any{
			"action": "update", "deal_id": id, "expected_close_date": original, "overwrite": true,
		}); res.IsError {
			return false, testutil.TextContent(res)
		}
		var back dealOut
		mustCall(t, "get_deal", map[string]any{"deal_id": id}, &back)
		return back.Deal.ExpectedCloseDate == original, "date differs"
	})

	probe := "2030-12-31"
	if original == probe {
		probe = "2030-12-30"
	}

	// Pipedrive's update_time has second granularity, and this deal was
	// created a moment ago. Writing inside the same second leaves the
	// timestamp where it was, which would read as "expect_version
	// cannot catch a concurrent edit" when the truth is that the clock
	// has not ticked. Wait for the tick rather than weaken the
	// assertion — it is the assertion that matters here.
	time.Sleep(1100 * time.Millisecond)

	var out dealWriteOut
	mustCall(t, "manage_deal", map[string]any{
		"action": "update", "deal_id": id, "expected_close_date": probe,
		"overwrite": true, "expect_version": version,
	}, &out)
	if !slices.Contains(out.Changed, "expected_close_date") {
		t.Errorf("changed = %v; want expected_close_date", out.Changed)
	}
	if out.Deal.ExpectedCloseDate != probe {
		t.Errorf("Pipedrive stored %q for a write of %q", out.Deal.ExpectedCloseDate, probe)
	}
	if out.Deal.UpdateTime == version {
		t.Error("update_time did not advance; expect_version would not catch a concurrent edit")
	}

	var after dealOut
	mustCall(t, "get_deal", map[string]any{"deal_id": id}, &after)
	if after.Deal.ExpectedCloseDate != probe {
		t.Error("an independent read does not see the write")
	}
}

// A reversible custom-field write, and the only probe that exercises the
// label→option-id path end to end: a fake's options are whatever the
// fixture says, so only the workspace can say whether a label the tools
// accepted is the one Pipedrive stored.
//
// It targets a dropdown that is ALREADY filled in, per rule 5 of the
// contract above — v2 has no spelling that empties a field again, so an
// empty one is not a reversible place to write. The restore puts the
// original label back and a fresh read confirms it.
func TestWrite_CustomFieldRoundTripsByLabel(t *testing.T) {
	requireWrites(t)

	fields, err := liveClient.ListDealFields(context.Background())
	if err != nil {
		t.Fatalf("reading deal field metadata: %v", err)
	}

	// A writable custom dropdown with at least two choices: one to move
	// to, and the one that was there to move back to.
	var target pipedrive.Field
	for _, f := range fields {
		if f.IsCustom && f.IsWritable && len(f.Options) >= 2 {
			target = f
			break
		}
	}
	if target.Key == "" {
		t.Skip("workspace has no writable custom dropdown with two choices")
	}

	// The field DEFINITION comes from the workspace, because that is
	// what this probe exists to exercise — a label resolving to an
	// option id upstream. The RECORD it writes to is this suite's own:
	// the definition is shared configuration, a deal is somebody's
	// work, and only one of those is safe to move.
	if len(target.Options) < 2 {
		t.Skip("the dropdown has too few choices to move between")
	}
	was, other := target.Options[0].Label, target.Options[1].Label
	if was == "" || other == "" {
		t.Skip("the dropdown's choices are unusable as a write target")
	}

	sc := newScratch(t)
	deal := dealRow{ID: sc.DealID}

	// Fill it first, so the overwrite guard below has something
	// populated to refuse over.
	mustCall(t, "manage_deal", map[string]any{
		"action": "update", "deal_id": deal.ID,
		"custom_fields": map[string]any{target.Name: was},
	}, nil)

	undo(t, fmt.Sprintf("deal %d left on the wrong dropdown choice", deal.ID), func() (bool, string) {
		if res := call(t, "manage_deal", map[string]any{
			"action": "update", "deal_id": deal.ID, "overwrite": true,
			"custom_fields": map[string]any{target.Name: was},
		}); res.IsError {
			return false, testutil.TextContent(res)
		}
		var back dealOut
		mustCall(t, "get_deal", map[string]any{"deal_id": deal.ID}, &back)
		got, _ := back.Deal.CustomFields[target.Name].(string)
		return got == was, "restored value does not match what was read first"
	})

	// Replacing a populated field needs overwrite, and saying so is half
	// the contract: without it the write must be refused, naming the
	// field under the name the caller used.
	refused := call(t, "manage_deal", map[string]any{
		"action": "update", "deal_id": deal.ID,
		"custom_fields": map[string]any{target.Name: other},
	})
	if !refused.IsError {
		t.Error("replacing a populated custom field was allowed without overwrite")
	} else if !strings.Contains(testutil.TextContent(refused), "overwrite") {
		t.Errorf("the refusal does not name the argument that allows it: %s", testutil.TextContent(refused))
	}

	var out struct {
		Changed []string `json:"changed"`
	}
	mustCall(t, "manage_deal", map[string]any{
		"action": "update", "deal_id": deal.ID, "overwrite": true,
		"custom_fields": map[string]any{target.Name: other},
	}, &out)
	if !slices.Contains(out.Changed, target.Name) {
		t.Errorf("changed = %v; want the custom field named in it", out.Changed)
	}

	// The echo is not the evidence: read it back.
	var after dealOut
	mustCall(t, "get_deal", map[string]any{"deal_id": deal.ID}, &after)
	got, _ := after.Deal.CustomFields[target.Name].(string)
	if got != other {
		t.Errorf("stored dropdown reads %q after the write; want the label that was sent", got)
	}
}

// reopen was broken and nothing here noticed, because the unit fakes
// accept any request and no probe reopened a real deal. Pipedrive
// rejects lost_reason alongside status:open — "Lost reason and lost
// time must can only be set when status is lost" — and it validates the
// RESULTING state, so sending it failed reopen from won AND from lost.
//
// THE FIRST VERSION OF THIS PROBE PICKED THE FIRST OPEN DEAL IN THE
// WORKSPACE and closed it, twice, to prove the transition worked. The
// field state restored; the automations that fired on every status
// change did not. A real customer's deal was marked won and lost four
// times before anyone noticed.
//
// So it builds its own deal now, in a pipeline the business does not
// use, and deletes it. Nothing here transitions a record this suite did
// not create.
func TestWrite_DealReopensFromWonAndFromLost(t *testing.T) {
	pipeline := requireOwnPipeline(t)

	var stages stagesOut
	mustCall(t, "list_stages", map[string]any{"pipeline_id": pipeline}, &stages)
	stage := pickFirst(t, stages.Stages, "the test pipeline has no stages")

	var created dealOut
	mustCall(t, "manage_deal", map[string]any{
		"action": "create", "title": "eval probe — reopen (safe to delete)",
		"pipeline_id": pipeline, "stage_id": stage.ID,
	}, &created)
	id := created.Deal.ID
	if id == 0 {
		t.Fatal("the probe deal was not created")
	}

	// Soft delete, so Pipedrive purges it after 30 days even if this
	// call is the thing that fails.
	undo(t, fmt.Sprintf("probe deal %d not deleted", id), func() (bool, string) {
		if res := call(t, "manage_deal", map[string]any{
			"action": "delete", "deal_id": id,
		}); res.IsError {
			return false, testutil.TextContent(res)
		}
		return true, ""
	})

	for _, close := range []struct{ action, want string }{
		{"mark_won", "won"},
		{"mark_lost", "lost"},
	} {
		t.Run(close.action, func(t *testing.T) {
			if res := call(t, "manage_deal", map[string]any{"action": close.action, "deal_id": id}); res.IsError {
				t.Fatalf("%s: %s", close.action, testutil.TextContent(res))
			}
			var closed dealOut
			mustCall(t, "get_deal", map[string]any{"deal_id": id}, &closed)
			if closed.Deal.Status != close.want {
				t.Fatalf("status is %q after %s, want %q", closed.Deal.Status, close.action, close.want)
			}

			if res := call(t, "manage_deal", map[string]any{"action": "reopen", "deal_id": id}); res.IsError {
				t.Fatalf("reopen from %s was refused: %s", close.want, testutil.TextContent(res))
			}
			var reopened dealOut
			mustCall(t, "get_deal", map[string]any{"deal_id": id}, &reopened)
			if reopened.Deal.Status != "open" {
				t.Errorf("status is %q after reopen from %s, want open", reopened.Deal.Status, close.want)
			}
		})
	}
}

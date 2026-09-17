//go:build integration

package integration

import (
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
)

// The guards, over real records. Nothing in this file mutates the
// workspace: every write is a dry run, and the refusals are refusals.
// That is why it runs on the tag alone.
//
// guard_spine_test.go already pins these rules against fakes, and that
// is where a rule change should fail first. What these add is the
// substrate: a record somebody actually filled in, with the fields
// populated that a fixture author would not have thought to populate,
// and an upstream that can be checked for having stayed still.

func TestGuard_RefusesAPopulatedFieldAndNamesIt(t *testing.T) {
	requireLive(t)
	deal := pickDeal(t)
	if deal.Title == "" {
		t.Skip("the first deal has no title to protect")
	}

	res := call(t, "manage_deal", map[string]any{
		"action":  "update",
		"deal_id": deal.ID,
		"title":   probeValue("title"),
		"dry_run": true,
	})
	if !res.IsError {
		t.Fatal("update replaced a populated title without overwrite")
	}
	if got := errClass(res); got != "refused" {
		t.Fatalf("class = %q; want refused (message: %s)", got, testutil.TextContent(res))
	}
	msg := testutil.TextContent(res)
	if !strings.Contains(msg, "title") {
		t.Errorf("the refusal does not name the field it is protecting: %s", msg)
	}
	if !strings.Contains(msg, "overwrite") {
		t.Errorf("the refusal does not name the argument that would allow the write: %s", msg)
	}
}

// TestGuard_OverwriteRehearsesAndSendsNothing checks the rehearsal
// against the upstream rather than against a call log. A fake can say
// Put never ran — guard_spine_test.go does — but only the workspace
// can say the record did not move, which is the claim
// docs/security.md makes to an operator.
func TestGuard_OverwriteRehearsesAndSendsNothing(t *testing.T) {
	requireLive(t)
	deal := pickDeal(t)
	if deal.Title == "" {
		t.Skip("the first deal has no title to rehearse over")
	}

	var out dealWriteOut
	mustCall(t, "manage_deal", map[string]any{
		"action":    "update",
		"deal_id":   deal.ID,
		"title":     probeValue("title"),
		"overwrite": true,
		"dry_run":   true,
	}, &out)

	if !out.DryRun {
		t.Error("the result does not report itself as a dry run")
	}
	if !slices.Contains(out.Changed, "title") {
		t.Errorf("changed = %v; a rehearsal should predict title", out.Changed)
	}

	var after dealOut
	mustCall(t, "get_deal", map[string]any{"deal_id": deal.ID}, &after)
	if after.Deal.Title != deal.Title {
		t.Fatal("the dry run wrote to the deal; its title changed")
	}
	if after.Deal.UpdateTime != deal.UpdateTime {
		t.Error("the dry run advanced update_time; something reached the workspace")
	}
}

// TestGuard_EmptyFieldNeedsNoOverwrite pins the other half of the
// rule: filling a field destroys nothing, so it needs no permission.
// It stays a dry run — v2 has no spelling that empties a field again,
// so writing to an empty one is not reversible. See
// docs/architecture.md, "Clearing a field".
func TestGuard_EmptyFieldNeedsNoOverwrite(t *testing.T) {
	requireLive(t)

	var deals dealsOut
	mustCall(t, "list_deals", map[string]any{"limit": 100}, &deals)
	target := pickWhere(t, deals.Deals,
		"no deal in the first page has an empty expected_close_date",
		func(d dealRow) bool { return d.ExpectedCloseDate == "" })

	var out dealWriteOut
	mustCall(t, "manage_deal", map[string]any{
		"action":              "update",
		"deal_id":             target.ID,
		"expected_close_date": "2030-01-01",
		"dry_run":             true,
	}, &out)

	if !slices.Contains(out.Changed, "expected_close_date") {
		t.Errorf("changed = %v; want expected_close_date", out.Changed)
	}
}

func TestGuard_StaleExpectVersionRefuses(t *testing.T) {
	requireLive(t)
	deal := pickDeal(t)

	res := call(t, "manage_deal", map[string]any{
		"action":         "update",
		"deal_id":        deal.ID,
		"title":          probeValue("title"),
		"overwrite":      true,
		"expect_version": "1970-01-01 00:00:00",
		"dry_run":        true,
	})
	if !res.IsError {
		t.Fatal("a write carrying a stale expect_version was permitted")
	}
	if got := errClass(res); got != "refused" {
		t.Errorf("class = %q; want refused (message: %s)", got, testutil.TextContent(res))
	}

	// The same call carrying the version the record actually reports
	// must pass, or the guard is refusing everything rather than
	// refusing staleness — and the format Pipedrive writes update_time
	// in is a thing only Pipedrive can confirm.
	var out dealWriteOut
	mustCall(t, "manage_deal", map[string]any{
		"action":         "update",
		"deal_id":        deal.ID,
		"title":          probeValue("title"),
		"overwrite":      true,
		"expect_version": deal.UpdateTime,
		"dry_run":        true,
	}, &out)
	if !out.DryRun {
		t.Error("the current version's rehearsal did not report itself as a dry run")
	}
}

func TestGuard_TransitionNeedsNoOverwrite(t *testing.T) {
	requireLive(t)

	var deals dealsOut
	mustCall(t, "list_deals", map[string]any{"limit": 1, "status": "open"}, &deals)
	deal := pickFirst(t, deals.Deals, "workspace has no open deals")

	var out dealWriteOut
	mustCall(t, "manage_deal", map[string]any{
		"action":  "mark_won",
		"deal_id": deal.ID,
		"dry_run": true,
	}, &out)

	// The named transitions change the field the caller named, so they
	// ask for no overwrite — and predict only that field.
	for _, f := range out.Changed {
		if f != "status" {
			t.Errorf("mark_won predicts %q; a transition should touch only status", f)
		}
	}
}

// TestGuard_DryRunCreateIsNotPersisted asks the workspace whether the
// rehearsed record exists. deals_test.go already proves the handler
// skips the upstream call; this proves nothing else created it.
func TestGuard_DryRunCreateIsNotPersisted(t *testing.T) {
	requireLive(t)
	deal := pickDeal(t)
	content := "<p>" + probeValue("rehearsed note") + "</p>"

	var out noteWriteOut
	mustCall(t, "manage_note", map[string]any{
		"action":  "create",
		"content": content,
		"deal_id": deal.ID,
		"dry_run": true,
	}, &out)
	if out.Note.ID != 0 {
		t.Errorf("a rehearsed create came back with id %d; it was persisted", out.Note.ID)
	}

	// list_notes sorts by update_time descending, so a note the
	// rehearsal had persisted would be the first row. 25 is slack.
	var notes notesOut
	mustCall(t, "list_notes", map[string]any{"deal_id": deal.ID, "limit": 25}, &notes)
	for _, n := range notes.Notes {
		if strings.Contains(n.Content, content) {
			t.Fatalf("the rehearsed note %d exists on the deal", n.ID)
		}
	}
}

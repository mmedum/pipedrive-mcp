//go:build integration

package integration

import (
	"testing"

	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
)

type leadOut struct {
	Lead struct {
		ID         string `json:"id"`
		Title      string `json:"title"`
		IsArchived bool   `json:"is_archived"`
		UpdateTime string `json:"update_time"`
		Value      *struct {
			Amount   float64 `json:"amount"`
			Currency string  `json:"currency"`
		} `json:"value"`
	} `json:"lead"`
}

// TestWrite_LeadRoundTrip creates a lead on the scratch organization,
// renames it, archives and unarchives it, and deletes it, which is
// permanent: the lead answers [not_found] afterwards.
func TestWrite_LeadRoundTrip(t *testing.T) {
	sc := newScratch(t)

	var created leadOut
	mustCall(t, "manage_lead", map[string]any{
		"action": "create", "title": sc.Title + " lead", "org_id": sc.OrgID,
		"value": map[string]any{"amount": 100, "currency": "EUR"},
	}, &created)
	id := created.Lead.ID
	t.Cleanup(func() {
		if id == "" {
			return
		}
		if res := call(t, "manage_lead", map[string]any{"action": "delete", "lead_id": id, "confirm": true}); res.IsError {
			t.Errorf("LEFT BEHIND: lead %s: %s", id, testutil.TextContent(res))
		}
	})
	if id == "" || created.Lead.Value == nil || created.Lead.Value.Amount != 100 {
		t.Fatalf("create = %+v; want a lead with its value", created.Lead)
	}

	rename := map[string]any{"action": "update", "lead_id": id, "title": sc.Title + " lead renamed"}
	if res := call(t, "manage_lead", rename); errClass(res) != "refused" {
		t.Fatalf("a rename without overwrite = %s, want [refused]", testutil.TextContent(res))
	}
	rename["overwrite"] = []string{"title"}
	rename["expect_version"] = created.Lead.UpdateTime
	mustCall(t, "manage_lead", rename, nil)

	var archived leadOut
	mustCall(t, "manage_lead", map[string]any{"action": "archive", "lead_id": id}, &archived)
	if !archived.Lead.IsArchived {
		t.Errorf("archive left is_archived false")
	}
	var unarchived leadOut
	mustCall(t, "manage_lead", map[string]any{"action": "unarchive", "lead_id": id}, &unarchived)
	if unarchived.Lead.IsArchived || unarchived.Lead.Title != sc.Title+" lead renamed" {
		t.Errorf("unarchive = %+v; want the renamed lead back, not archived", unarchived.Lead)
	}

	del := map[string]any{"action": "delete", "lead_id": id}
	if res := call(t, "manage_lead", del); errClass(res) != "refused" {
		t.Fatalf("a delete without confirm = %s, want [refused]", testutil.TextContent(res))
	}
	del["confirm"] = true
	mustCall(t, "manage_lead", del, nil)
	gone := id
	id = ""
	if res := call(t, "get_lead", map[string]any{"lead_id": gone}); errClass(res) != "not_found" {
		t.Errorf("the deleted lead reads as %s, want [not_found]", testutil.TextContent(res))
	}
}

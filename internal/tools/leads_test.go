package tools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
	"github.com/mmedum/pipedrive-mcp/internal/tools"
)

const testLeadID = "de66da40-e521-11f1-af8f-3fd52e2011b4"

type fakeLeadsClient struct {
	lead                      *pipedrive.Lead
	lastOpts                  pipedrive.ListLeadsOptions
	lastReq                   pipedrive.LeadRequest
	creates, updates, deletes int
}

func newLeadsFake() *fakeLeadsClient {
	org := int64(4)
	return &fakeLeadsClient{lead: &pipedrive.Lead{
		ID: testLeadID, Title: "Acme pilot", OwnerID: 2, OrganizationID: &org,
		Value: &pipedrive.LeadValue{Amount: 100, Currency: "EUR"}, UpdateTime: "2026-01-01T00:00:00Z",
	}}
}

func (f *fakeLeadsClient) GetLead(_ context.Context, id string) (*pipedrive.Lead, error) {
	if id != f.lead.ID {
		return nil, &pipedrive.APIError{Class: pipedrive.ErrNotFound, Status: 404}
	}
	out := *f.lead
	return &out, nil
}

func (f *fakeLeadsClient) ListLeads(_ context.Context, opts pipedrive.ListLeadsOptions) ([]pipedrive.Lead, *pipedrive.V1Pagination, error) {
	f.lastOpts = opts
	return []pipedrive.Lead{*f.lead}, &pipedrive.V1Pagination{MoreItemsInCollection: true, NextStart: 25}, nil
}

func (f *fakeLeadsClient) CreateLead(_ context.Context, req pipedrive.LeadRequest) (*pipedrive.Lead, error) {
	f.creates++
	f.lastReq = req
	return &pipedrive.Lead{ID: testLeadID, Title: *req.Title}, nil
}

func (f *fakeLeadsClient) UpdateLead(_ context.Context, _ string, req pipedrive.LeadRequest) (*pipedrive.Lead, error) {
	f.updates++
	f.lastReq = req
	if req.IsArchived != nil {
		f.lead.IsArchived = *req.IsArchived
	}
	if req.Title != nil {
		f.lead.Title = *req.Title
	}
	out := *f.lead
	return &out, nil
}

func (f *fakeLeadsClient) DeleteLead(context.Context, string) error {
	f.deletes++
	return nil
}

func (f *fakeLeadsClient) ListLeadLabels(context.Context) ([]pipedrive.LeadLabel, error) {
	return []pipedrive.LeadLabel{{ID: "a1", Name: "Hot", Color: "red"}}, nil
}

func (f *fakeLeadsClient) writes() int { return f.creates + f.updates + f.deletes }

func callLeads(t *testing.T, f *fakeLeadsClient, opts tools.RegisterOptions, tool string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	return testutil.CallToolInto(t, func(s *mcp.Server) { tools.RegisterLeads(s, f, opts) }, tool, args, out)
}

func TestListLeads_PagesThroughV1AndSortsAsV1Spells(t *testing.T) {
	fake := newLeadsFake()
	var out struct {
		NextCursor string `json:"next_cursor"`
	}
	callLeads(t, fake, tools.RegisterOptions{}, "list_leads", map[string]any{"org_id": 4}, &out)
	if fake.lastOpts.Sort != "update_time DESC" || fake.lastOpts.OrganizationID != 4 || out.NextCursor == "" {
		t.Fatalf("opts = %+v, cursor %q; want update_time DESC, the org filter and a cursor", fake.lastOpts, out.NextCursor)
	}
	callLeads(t, fake, tools.RegisterOptions{}, "list_leads", map[string]any{"cursor": out.NextCursor}, nil)
	if fake.lastOpts.Start != 25 {
		t.Errorf("the cursor decoded to start %d, want 25", fake.lastOpts.Start)
	}
	if res := callLeads(t, fake, tools.RegisterOptions{}, "list_leads", map[string]any{"archived": true, "updated_since": "2026-01-01T00:00:00Z"}, nil); !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("archived with updated_since = %q; want [validation]", contentText(res))
	}
}

func TestManageLead_CreateNeedsAPersonOrAnOrganization(t *testing.T) {
	fake := newLeadsFake()
	if res := callLeads(t, fake, tools.RegisterOptions{}, "manage_lead", map[string]any{"action": "create", "title": "X"}, nil); !strings.Contains(contentText(res), "person or an organization") || fake.creates != 0 {
		t.Fatalf("create without a link = %q", contentText(res))
	}
	if res := callLeads(t, fake, tools.RegisterOptions{}, "manage_lead", map[string]any{"action": "create", "title": "X", "org_id": 4, "value": map[string]any{"amount": 5, "currency": "eur"}}, nil); res.IsError || fake.creates != 1 || fake.lastReq.Value.Currency != "EUR" {
		t.Errorf("create = %s, req %+v; want one create with the currency upper-cased", contentText(res), fake.lastReq)
	}
}

func TestManageLead_UpdateGuardsAndArchiveDoesNot(t *testing.T) {
	fake := newLeadsFake()
	args := map[string]any{"action": "update", "lead_id": testLeadID, "title": "Acme rollout"}
	if res := callLeads(t, fake, tools.RegisterOptions{}, "manage_lead", args, nil); !strings.Contains(contentText(res), `overwrite: ["title"]`) || fake.updates != 0 {
		t.Fatalf("a rename without overwrite = %q", contentText(res))
	}
	if res := callLeads(t, fake, tools.RegisterOptions{}, "manage_lead", map[string]any{"action": "archive", "lead_id": testLeadID}, nil); res.IsError || fake.updates != 1 || fake.lastReq.IsArchived == nil || !*fake.lastReq.IsArchived || fake.lastReq.Title != nil {
		t.Errorf("archive = %s, req %+v; want is_archived alone, with no overwrite", contentText(res), fake.lastReq)
	}
}

func TestManageLead_DeleteIsPermanentSoItNeedsConfirm(t *testing.T) {
	fake := newLeadsFake()
	args := map[string]any{"action": "delete", "lead_id": testLeadID}
	if res := callLeads(t, fake, tools.RegisterOptions{}, "manage_lead", args, nil); !strings.Contains(contentText(res), "is permanent") || !strings.Contains(contentText(res), "confirm: true") {
		t.Fatalf("delete without confirm = %q", contentText(res))
	}
	args["dry_run"] = true
	if res := callLeads(t, fake, tools.RegisterOptions{}, "manage_lead", args, nil); res.IsError || fake.deletes != 0 {
		t.Fatalf("dry-run delete = %s, deletes = %d", contentText(res), fake.deletes)
	}
	delete(args, "dry_run")
	args["confirm"] = true
	if res := callLeads(t, fake, tools.RegisterOptions{}, "manage_lead", args, nil); res.IsError || fake.deletes != 1 {
		t.Errorf("confirmed delete = %s, deletes = %d", contentText(res), fake.deletes)
	}
}

func TestManageLead_RefusesBadInputAndHonorsTheFloor(t *testing.T) {
	for _, args := range []map[string]any{
		{"action": "update", "lead_id": "../users/me", "title": "x"},
		{"action": "update", "lead_id": testLeadID, "label_ids": []string{}},
		{"action": "update", "lead_id": testLeadID, "value": map[string]any{"amount": 1, "currency": "euro"}},
		{"action": "update", "lead_id": testLeadID, "expected_close_date": "soon"},
	} {
		fake := newLeadsFake()
		if res := callLeads(t, fake, tools.RegisterOptions{}, "manage_lead", args, nil); !strings.HasPrefix(contentText(res), "[validation]") || fake.writes() != 0 {
			t.Errorf("%v = %q; want [validation] and nothing sent", args, contentText(res))
		}
	}
	fake := newLeadsFake()
	for _, args := range []map[string]any{
		{"action": "create", "title": "X", "org_id": 4},
		{"action": "archive", "lead_id": testLeadID},
		{"action": "delete", "lead_id": testLeadID, "confirm": true},
	} {
		if res := callLeads(t, fake, tools.RegisterOptions{DryRun: true}, "manage_lead", args, nil); res.IsError {
			t.Errorf("%v under the floor = %s", args["action"], contentText(res))
		}
	}
	if fake.writes() != 0 {
		t.Errorf("the floor let %d writes through", fake.writes())
	}
}

func TestListLeads_RefusesACursorFromAnotherListing(t *testing.T) {
	fake := newLeadsFake()
	var live struct {
		NextCursor string `json:"next_cursor"`
	}
	callLeads(t, fake, tools.RegisterOptions{}, "list_leads", map[string]any{}, &live)
	res := callLeads(t, fake, tools.RegisterOptions{}, "list_leads", map[string]any{"archived": true, "cursor": live.NextCursor}, nil)
	if !strings.Contains(contentText(res), "not from this listing") {
		t.Errorf("a live cursor on the archive = %q; want a refusal", contentText(res))
	}
}

func TestManageLead_ATransitionCarriesNoEdit(t *testing.T) {
	fake := newLeadsFake()
	res := callLeads(t, fake, tools.RegisterOptions{}, "manage_lead", map[string]any{"action": "archive", "lead_id": testLeadID, "title": "Renamed"}, nil)
	if !strings.HasPrefix(contentText(res), "[validation]") || fake.writes() != 0 {
		t.Errorf("archive with a title = %q, writes %d; want [validation] and nothing sent", contentText(res), fake.writes())
	}
}

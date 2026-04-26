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

// fakeDealsClient lets handler tests skip the real HTTP client.
type fakeDealsClient struct {
	deal      *pipedrive.Deal
	dealErr   error
	deals     []pipedrive.Deal
	dealsNext string
	dealsErr  error
	resolver  func(map[string]any) map[string]any

	lastListOpts pipedrive.ListDealsOptions
}

func (f *fakeDealsClient) GetDeal(_ context.Context, _ int64) (*pipedrive.Deal, error) {
	return f.deal, f.dealErr
}

func (f *fakeDealsClient) ListDeals(_ context.Context, opts pipedrive.ListDealsOptions) ([]pipedrive.Deal, string, error) {
	f.lastListOpts = opts
	return f.deals, f.dealsNext, f.dealsErr
}

func (f *fakeDealsClient) ResolveDealCustomFields(_ context.Context, raw map[string]any) map[string]any {
	if f.resolver != nil {
		return f.resolver(raw)
	}
	return raw
}

// dealRow mirrors the JSON shape RegisterDeals emits. Carrying a
// parallel shape keeps the unexported handler-local struct hidden
// from test code; drift is caught by the JSON tag names.
type dealRow struct {
	ID                int64          `json:"id"`
	Title             string         `json:"title"`
	Value             float64        `json:"value"`
	Currency          string         `json:"currency"`
	Status            string         `json:"status"`
	StageID           int64          `json:"stage_id"`
	PipelineID        int64          `json:"pipeline_id"`
	OwnerID           int64          `json:"owner_id"`
	PersonID          int64          `json:"person_id"`
	OrgID             int64          `json:"org_id"`
	ExpectedCloseDate string         `json:"expected_close_date,omitempty"`
	WonTime           string         `json:"won_time,omitempty"`
	LostTime          string         `json:"lost_time,omitempty"`
	LostReason        string         `json:"lost_reason,omitempty"`
	AddTime           string         `json:"add_time,omitempty"`
	UpdateTime        string         `json:"update_time,omitempty"`
	Probability       *int           `json:"probability,omitempty"`
	CustomFields      map[string]any `json:"custom_fields,omitempty"`
	URL               string         `json:"url"`
}

func TestGetDeal_HappyPath(t *testing.T) {
	fake := &fakeDealsClient{
		deal: &pipedrive.Deal{
			ID:           42,
			Title:        "Big Deal",
			Value:        1234.5,
			Currency:     "USD",
			Status:       "open",
			StageID:      3,
			PipelineID:   2,
			CustomFields: map[string]any{"abc123": "Alice"},
		},
		resolver: func(raw map[string]any) map[string]any {
			return map[string]any{"Account Manager": raw["abc123"]}
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, fake, "acme")
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_deal",
		Arguments: map[string]any{"deal_id": 42},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out struct {
		Deal dealRow `json:"deal"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)
	if out.Deal.ID != 42 || out.Deal.Title != "Big Deal" {
		t.Errorf("deal = %+v; want id=42 title=Big Deal", out.Deal)
	}
	if out.Deal.URL != "https://acme.pipedrive.com/deal/42" {
		t.Errorf("URL = %q, want acme/deal/42", out.Deal.URL)
	}
	if out.Deal.CustomFields["Account Manager"] != "Alice" {
		t.Errorf("custom field name resolution lost: %v", out.Deal.CustomFields)
	}
}

func TestGetDeal_RejectsZeroID(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, &fakeDealsClient{}, "acme")
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_deal",
		Arguments: map[string]any{"deal_id": 0},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected isError on zero deal_id, got: %+v", res.Content)
	}
	text := contentText(res)
	if !strings.HasPrefix(text, "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", text)
	}
}

func TestGetDeal_UpstreamNotFound(t *testing.T) {
	fake := &fakeDealsClient{
		dealErr: &pipedrive.APIError{
			Class:    pipedrive.ErrNotFound,
			Status:   404,
			Message:  "Deal not found",
			Endpoint: "/api/v2/deals/99999",
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, fake, "acme")
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_deal",
		Arguments: map[string]any{"deal_id": 99999},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected isError on upstream 404, got: %+v", res.Content)
	}
	text := contentText(res)
	if !strings.HasPrefix(text, "[not_found]") {
		t.Errorf("error text = %q; want [not_found] prefix", text)
	}
}

func TestListDeals_HappyPath(t *testing.T) {
	fake := &fakeDealsClient{
		deals: []pipedrive.Deal{
			{ID: 1, Title: "A", Status: "open"},
			{ID: 2, Title: "B", Status: "open"},
		},
		dealsNext: "cursor-page-2",
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, fake, "acme")
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "list_deals",
		Arguments: map[string]any{
			"status":      "open",
			"pipeline_id": 2,
			"limit":       50,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out struct {
		Deals      []dealRow `json:"deals"`
		NextCursor string    `json:"next_cursor"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)

	if len(out.Deals) != 2 {
		t.Fatalf("got %d deals, want 2", len(out.Deals))
	}
	if out.Deals[0].URL != "https://acme.pipedrive.com/deal/1" {
		t.Errorf("URL = %q, want acme/deal/1", out.Deals[0].URL)
	}
	if out.NextCursor != "cursor-page-2" {
		t.Errorf("next_cursor = %q, want cursor-page-2", out.NextCursor)
	}
	if fake.lastListOpts.Status != "open" || fake.lastListOpts.PipelineID != 2 || fake.lastListOpts.Limit != 50 {
		t.Errorf("client received opts %+v; want status=open pipeline_id=2 limit=50", fake.lastListOpts)
	}
}

func TestListDeals_RejectsBadStatus(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, &fakeDealsClient{}, "acme")
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_deals",
		Arguments: map[string]any{"status": "ongoing"}, // not in the enum
		// Note: the v1 synonym `all_not_deleted` is also rejected on
		// v2 — see commit message; covered by the lint-time enum.
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected isError on bad status, got: %+v", res.Content)
	}
	text := contentText(res)
	if !strings.HasPrefix(text, "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", text)
	}
	if !strings.Contains(text, "ongoing") {
		t.Errorf("error text = %q; want it to mention the bad value 'ongoing'", text)
	}
}

func TestListDeals_LimitClampedToMax(t *testing.T) {
	fake := &fakeDealsClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, fake, "acme")
	})
	defer h.Close()

	_, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_deals",
		Arguments: map[string]any{"limit": 999},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if fake.lastListOpts.Limit != 100 {
		t.Errorf("client received limit=%d; want 100 (clamped from 999)", fake.lastListOpts.Limit)
	}
}

func TestListDeals_LimitDefaultWhenZero(t *testing.T) {
	fake := &fakeDealsClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, fake, "acme")
	})
	defer h.Close()

	_, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_deals",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if fake.lastListOpts.Limit != 25 {
		t.Errorf("client received limit=%d; want 25 (default)", fake.lastListOpts.Limit)
	}
}

func TestRegisterDeals_RegistersInDumpRegistry(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, &fakeDealsClient{}, "acme")
	})
	defer h.Close()

	var buf strings.Builder
	if err := tools.DumpJSON(&buf, "test"); err != nil {
		t.Fatalf("DumpJSON: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"get_deal"`, `"list_deals"`} {
		if !strings.Contains(out, want) {
			t.Errorf("dump missing %s; got: %s", want, out)
		}
	}
}

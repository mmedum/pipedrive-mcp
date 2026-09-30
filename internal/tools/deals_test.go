package tools_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
	"github.com/mmedum/pipedrive-mcp/internal/tools"
)

// fakeDealsClient lets handler tests skip the real HTTP client.
type fakeDealsClient struct {
	me         *pipedrive.User // the signed-in user a dry-run create previews as owner
	deal       *pipedrive.Deal
	dealErr    error
	deals      []pipedrive.Deal
	dealsNext  string
	dealsErr   error
	createDeal *pipedrive.Deal
	createErr  error
	resolver   func(map[string]any) map[string]any

	lastListOpts   pipedrive.ListDealsOptions
	lastCreateReq  pipedrive.CreateDealRequest
	createCallSeen bool

	updateDeal    *pipedrive.Deal
	updateErr     error
	lastUpdateReq pipedrive.UpdateDealRequest
	lastUpdateID  int64
	updateCalls   int
	getCalls      int

	deleteErr    error
	deleteCalls  int
	lastDeleteID int64

	encodeErr error
}

// ListStages has no stages to offer, so a dry-run create previews no
// stage it was not given.
func (f *fakeDealsClient) ListStages(context.Context, int64) ([]pipedrive.Stage, error) {
	return nil, errNoUser
}

// WhoAmI answers the read a dry-run create makes for its owner. The fake
// has no signed-in user unless a test names one, and a preview then
// shows no owner.
func (f *fakeDealsClient) WhoAmI(context.Context) (*pipedrive.User, error) {
	if f.me == nil {
		return nil, errNoUser
	}
	return f.me, nil
}

func (f *fakeDealsClient) UpdateDeal(_ context.Context, id int64, req pipedrive.UpdateDealRequest) (*pipedrive.Deal, error) {
	f.lastUpdateID = id
	f.lastUpdateReq = req
	f.updateCalls++
	return f.updateDeal, f.updateErr
}

func (f *fakeDealsClient) DeleteDeal(_ context.Context, id int64) error {
	f.deleteCalls++
	f.lastDeleteID = id
	return f.deleteErr
}

func (f *fakeDealsClient) GetDeal(_ context.Context, _ int64) (*pipedrive.Deal, error) {
	f.getCalls++
	return f.deal, f.dealErr
}

func (f *fakeDealsClient) ListDeals(_ context.Context, opts pipedrive.ListDealsOptions) ([]pipedrive.Deal, string, error) {
	f.lastListOpts = opts
	return f.deals, f.dealsNext, f.dealsErr
}

func (f *fakeDealsClient) CreateDeal(_ context.Context, req pipedrive.CreateDealRequest) (*pipedrive.Deal, error) {
	f.lastCreateReq = req
	f.createCallSeen = true
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.createDeal, nil
}

func (f *fakeDealsClient) ResolveDealCustomFields(_ context.Context, raw map[string]any) map[string]any {
	if f.resolver != nil {
		return f.resolver(raw)
	}
	return raw
}
func (f *fakeDealsClient) EncodeDealCustomFields(_ context.Context, in map[string]any) (pipedrive.CustomFieldWrite, error) {
	return fakeEncode(in, f.encodeErr)
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
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, fake, "acme", tools.RegisterOptions{})
	}, "get_deal", map[string]any{"deal_id": 42})
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
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, &fakeDealsClient{}, "acme", tools.RegisterOptions{})
	}, "get_deal", map[string]any{"deal_id": 0})
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
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, fake, "acme", tools.RegisterOptions{})
	}, "get_deal", map[string]any{"deal_id": 99999})
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
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, fake, "acme", tools.RegisterOptions{})
	}, "list_deals", map[string]any{
		"status":      "open",
		"pipeline_id": 2,
		"limit":       50,
	})
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
	if fake.lastListOpts.SortBy != "update_time" || fake.lastListOpts.SortDirection != "desc" {
		t.Errorf("default sort = %q %q; want update_time desc", fake.lastListOpts.SortBy, fake.lastListOpts.SortDirection)
	}
}

func TestListDeals_PassesUpdatedWindowAndSort(t *testing.T) {
	fake := &fakeDealsClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, fake, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	_, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "list_deals",
		Arguments: map[string]any{
			"updated_since":  "2026-04-01T00:00:00Z",
			"updated_until":  "2026-04-30T23:59:59Z",
			"sort_by":        "add_time",
			"sort_direction": "asc",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if fake.lastListOpts.UpdatedSince != "2026-04-01T00:00:00Z" {
		t.Errorf("updated_since not plumbed: %q", fake.lastListOpts.UpdatedSince)
	}
	if fake.lastListOpts.UpdatedUntil != "2026-04-30T23:59:59Z" {
		t.Errorf("updated_until not plumbed: %q", fake.lastListOpts.UpdatedUntil)
	}
	if fake.lastListOpts.SortBy != "add_time" || fake.lastListOpts.SortDirection != "asc" {
		t.Errorf("sort = %q %q; want add_time asc", fake.lastListOpts.SortBy, fake.lastListOpts.SortDirection)
	}
}

func TestListDeals_RejectsBadSortBy(t *testing.T) {
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, &fakeDealsClient{}, "acme", tools.RegisterOptions{})
	}, "list_deals", map[string]any{"sort_by": "title"})
	if !res.IsError {
		t.Fatal("expected isError on unsupported sort_by")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
}

func TestListDeals_RejectsBadSortDirection(t *testing.T) {
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, &fakeDealsClient{}, "acme", tools.RegisterOptions{})
	}, "list_deals", map[string]any{"sort_direction": "sideways"})
	if !res.IsError {
		t.Fatal("expected isError on unsupported sort_direction")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
}

func TestListDeals_RejectsBadStatus(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, &fakeDealsClient{}, "acme", tools.RegisterOptions{})
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
		tools.RegisterDeals(s, fake, "acme", tools.RegisterOptions{})
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
		tools.RegisterDeals(s, fake, "acme", tools.RegisterOptions{})
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
		tools.RegisterDeals(s, &fakeDealsClient{}, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	var buf strings.Builder
	if err := tools.DumpJSON(&buf, "test"); err != nil {
		t.Fatalf("DumpJSON: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"get_deal"`, `"list_deals"`, `"manage_deal"`} {
		if !strings.Contains(out, want) {
			t.Errorf("dump missing %s; got: %s", want, out)
		}
	}
}

func TestCreateDeal_HappyPath(t *testing.T) {
	fake := &fakeDealsClient{
		createDeal: &pipedrive.Deal{
			ID:         101,
			Title:      "Acme — Widget renewal",
			Value:      75000,
			Currency:   "DKK",
			Status:     "open",
			StageID:    3,
			PipelineID: 2,
			OwnerID:    7,
			OrgID:      59,
		},
	}
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, fake, "acme", tools.RegisterOptions{})
	}, "manage_deal", map[string]any{
		"action":      "create",
		"title":       "Acme — Widget renewal",
		"value":       75000,
		"currency":    "DKK",
		"pipeline_id": 2,
		"stage_id":    3,
		"org_id":      59,
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out struct {
		Deal   dealRow `json:"deal"`
		DryRun bool    `json:"dry_run"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)

	if !fake.createCallSeen {
		t.Fatal("CreateDeal was not called on the upstream client")
	}
	if out.DryRun {
		t.Error("dry_run = true on a non-dry-run handler")
	}
	if out.Deal.ID != 101 || out.Deal.Title != "Acme — Widget renewal" {
		t.Errorf("deal echo lost fields: %+v", out.Deal)
	}
	if out.Deal.URL != "https://acme.pipedrive.com/deal/101" {
		t.Errorf("URL = %q, want acme/deal/101", out.Deal.URL)
	}
	if fake.lastCreateReq.Title != "Acme — Widget renewal" ||
		fake.lastCreateReq.Value != 75000 ||
		fake.lastCreateReq.Currency != "DKK" ||
		fake.lastCreateReq.PipelineID != 2 ||
		fake.lastCreateReq.StageID != 3 ||
		fake.lastCreateReq.OrgID != 59 {
		t.Errorf("upstream request lost fields: %+v", fake.lastCreateReq)
	}
}

func TestCreateDeal_RejectsEmptyTitle(t *testing.T) {
	fake := &fakeDealsClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, fake, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "manage_deal",
		Arguments: map[string]any{
			"action": "create", "title": ""},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected isError on empty title, got: %+v", res.Content)
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
	if fake.createCallSeen {
		t.Error("CreateDeal called despite client-side validation failure")
	}
}

func TestCreateDeal_DryRunSkipsUpstream(t *testing.T) {
	fake := &fakeDealsClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, fake, "acme", tools.RegisterOptions{DryRun: true}) // dryRun = true
	})
	defer h.Close()

	prob := 65
	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "manage_deal",
		Arguments: map[string]any{
			"action":      "create",
			"title":       "Dry run probe",
			"value":       1234,
			"currency":    "USD",
			"probability": prob,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	if fake.createCallSeen {
		t.Error("CreateDeal called on dry-run path; upstream POST should be skipped")
	}
	var out struct {
		Deal   dealRow `json:"deal"`
		DryRun bool    `json:"dry_run"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)
	if !out.DryRun {
		t.Error("dry_run = false on synthetic preview")
	}
	if out.Deal.ID != 0 {
		t.Errorf("synthetic deal id = %d; want 0", out.Deal.ID)
	}
	if out.Deal.Title != "Dry run probe" || out.Deal.Value != 1234 || out.Deal.Currency != "USD" {
		t.Errorf("synthetic deal lost input fields: %+v", out.Deal)
	}
	if out.Deal.Status != "open" {
		t.Errorf("synthetic deal status = %q; want 'open' (Pipedrive default)", out.Deal.Status)
	}
	if out.Deal.Probability == nil || *out.Deal.Probability != 65 {
		t.Errorf("synthetic deal probability = %v; want 65", out.Deal.Probability)
	}
}

func TestCreateDeal_PropagatesUpstreamError(t *testing.T) {
	fake := &fakeDealsClient{
		createErr: &pipedrive.APIError{
			Class:    pipedrive.ErrValidation,
			Status:   400,
			Message:  "stage_id is required for this pipeline",
			Endpoint: "/api/v2/deals",
		},
	}
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, fake, "acme", tools.RegisterOptions{})
	}, "manage_deal", map[string]any{
		"action":      "create",
		"title":       "Bad pipeline test",
		"pipeline_id": 999,
	})
	if !res.IsError {
		t.Fatalf("expected isError on upstream 400, got: %+v", res.Content)
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
}

// errNoUser is what a fake answers when a test names no signed-in user.
var errNoUser = errors.New("fake: no signed-in user")

// defaultingDealsClient answers the reads a dry-run create makes to show
// what Pipedrive fills in, and counts them.
type defaultingDealsClient struct {
	fakeDealsClient
	whoamiCalls, stageCalls int
}

func (f *defaultingDealsClient) WhoAmI(context.Context) (*pipedrive.User, error) {
	f.whoamiCalls++
	return &pipedrive.User{ID: 7, DefaultCurrency: "EUR"}, nil
}

func (f *defaultingDealsClient) ListStages(_ context.Context, pipelineID int64) ([]pipedrive.Stage, error) {
	f.stageCalls++
	return []pipedrive.Stage{
		{ID: 40, OrderNr: 2, PipelineID: pipelineID},
		{ID: 39, OrderNr: 1, PipelineID: pipelineID, IsDeleted: true},
		{ID: 41, OrderNr: 1, PipelineID: pipelineID},
	}, nil
}

// A dry-run create fills in the owner, the currency and the first stage
// the real create would get, links nowhere, and reads only for what the
// call left out.
func TestCreateDeal_DryRunShowsPipedrivesDefaults(t *testing.T) {
	for _, tc := range []struct {
		name         string
		args         map[string]any
		want         dealRow
		whoami, stgs int
	}{
		{"left out", map[string]any{"pipeline_id": 6},
			dealRow{StageID: 41, PipelineID: 6, OwnerID: 7, Currency: "EUR"}, 1, 1},
		{"given", map[string]any{"pipeline_id": 6, "stage_id": 40, "owner_id": 9, "currency": "USD"},
			dealRow{StageID: 40, PipelineID: 6, OwnerID: 9, Currency: "USD"}, 0, 0},
		{"no pipeline", map[string]any{},
			dealRow{OwnerID: 7, Currency: "EUR"}, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &defaultingDealsClient{}
			h := testutil.Connect(t, func(s *mcp.Server) {
				tools.RegisterDeals(s, fake, "acme", tools.RegisterOptions{DryRun: true})
			})
			defer h.Close()
			args := map[string]any{"action": "create", "title": "Preview"}
			for k, v := range tc.args {
				args[k] = v
			}
			res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{Name: "manage_deal", Arguments: args})
			if err != nil || res.IsError {
				t.Fatalf("%v %s", err, contentText(res))
			}
			var out struct {
				Deal dealRow `json:"deal"`
			}
			testutil.DecodeStructured(t, res.StructuredContent, &out)
			d := out.Deal
			if d.StageID != tc.want.StageID || d.PipelineID != tc.want.PipelineID || d.OwnerID != tc.want.OwnerID ||
				d.Currency != tc.want.Currency || d.URL != "" {
				t.Errorf("preview %+v; want stage %d, pipeline %d, owner %d, currency %q and no url",
					d, tc.want.StageID, tc.want.PipelineID, tc.want.OwnerID, tc.want.Currency)
			}
			if fake.whoamiCalls != tc.whoami || fake.stageCalls != tc.stgs || fake.createCallSeen {
				t.Errorf("whoami %d, stages %d, create %v", fake.whoamiCalls, fake.stageCalls, fake.createCallSeen)
			}
		})
	}
}

// A dry-run create of a person, an organization or an activity shows the
// signed-in user as its owner when the call names none, as the real
// create makes it, and the owner it was given when it names one.
func TestCreate_DryRunShowsTheOwnerPipedriveGives(t *testing.T) {
	me := &pipedrive.User{ID: 7}
	for _, tc := range []struct {
		tool, key string
		register  func(*mcp.Server)
		args      map[string]any
	}{
		{"manage_person", "person", func(s *mcp.Server) {
			tools.RegisterPersons(s, &fakePersonsClient{me: me}, "acme", tools.RegisterOptions{DryRun: true})
		}, map[string]any{"name": "Preview"}},
		{"manage_organization", "organization", func(s *mcp.Server) {
			tools.RegisterOrganizations(s, &fakeOrganizationsClient{me: me}, "acme", tools.RegisterOptions{DryRun: true})
		}, map[string]any{"name": "Preview"}},
		{"manage_activity", "activity", func(s *mcp.Server) {
			tools.RegisterActivities(s, &fakeActivitiesClient{me: me}, "acme", tools.RegisterOptions{DryRun: true})
		}, map[string]any{"subject": "Preview", "type": "call"}},
	} {
		for _, given := range []int64{0, 9} {
			h := testutil.Connect(t, tc.register)
			args := map[string]any{"action": "create"}
			for k, v := range tc.args {
				args[k] = v
			}
			want := int64(7)
			if given != 0 {
				args["owner_id"], want = given, given
			}
			res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.tool, Arguments: args})
			if err != nil || res.IsError {
				t.Fatalf("%s: %v %s", tc.tool, err, contentText(res))
			}
			var out map[string]any
			testutil.DecodeStructured(t, res.StructuredContent, &out)
			rec, _ := out[tc.key].(map[string]any)
			if got, _ := rec["owner_id"].(float64); int64(got) != want || rec["url"] != "" {
				t.Errorf("%s given owner %d: previews owner %v and url %v", tc.tool, given, rec["owner_id"], rec["url"])
			}
			h.Close()
		}
	}
}

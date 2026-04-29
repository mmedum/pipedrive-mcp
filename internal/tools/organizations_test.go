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

type fakeOrganizationsClient struct {
	org       *pipedrive.Organization
	err       error
	orgs      []pipedrive.Organization
	orgsNxt   string
	orgsErr   error
	createOrg *pipedrive.Organization
	createErr error
	resolver  func(map[string]any) map[string]any

	lastListOpts   pipedrive.ListOrganizationsOptions
	lastCreateReq  pipedrive.CreateOrganizationRequest
	createCallSeen bool
}

func (f *fakeOrganizationsClient) GetOrganization(_ context.Context, _ int64) (*pipedrive.Organization, error) {
	return f.org, f.err
}

func (f *fakeOrganizationsClient) ListOrganizations(_ context.Context, opts pipedrive.ListOrganizationsOptions) ([]pipedrive.Organization, string, error) {
	f.lastListOpts = opts
	return f.orgs, f.orgsNxt, f.orgsErr
}

func (f *fakeOrganizationsClient) CreateOrganization(_ context.Context, req pipedrive.CreateOrganizationRequest) (*pipedrive.Organization, error) {
	f.lastCreateReq = req
	f.createCallSeen = true
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.createOrg, nil
}

func (f *fakeOrganizationsClient) ResolveOrganizationCustomFields(_ context.Context, raw map[string]any) map[string]any {
	if f.resolver != nil {
		return f.resolver(raw)
	}
	return raw
}

type orgAddressRow struct {
	Value      string `json:"value,omitempty"`
	Country    string `json:"country,omitempty"`
	Locality   string `json:"locality,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
}

type orgRow struct {
	ID           int64          `json:"id"`
	Name         string         `json:"name"`
	Address      *orgAddressRow `json:"address,omitempty"`
	OwnerID      int64          `json:"owner_id"`
	PeopleCount  int            `json:"people_count,omitempty"`
	CustomFields map[string]any `json:"custom_fields,omitempty"`
	URL          string         `json:"url"`
}

func TestGetOrganization_HappyPath(t *testing.T) {
	fake := &fakeOrganizationsClient{
		org: &pipedrive.Organization{
			ID:           47,
			Name:         "Acme Inc",
			Address:      &pipedrive.Address{Value: "123 Main St, Springfield", Country: "USA"},
			OwnerID:      13,
			PeopleCount:  4,
			CustomFields: map[string]any{"def456": "strategic"},
		},
		resolver: func(raw map[string]any) map[string]any {
			return map[string]any{"Tier": raw["def456"]}
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterOrganizations(s, fake, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_organization",
		Arguments: map[string]any{"org_id": 47},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out struct {
		Organization orgRow `json:"organization"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)

	if out.Organization.ID != 47 || out.Organization.Name != "Acme Inc" {
		t.Errorf("org = %+v; want id=47 name=\"Acme Inc\"", out.Organization)
	}
	if out.Organization.URL != "https://acme.pipedrive.com/organization/47" {
		t.Errorf("URL = %q, want acme/organization/47", out.Organization.URL)
	}
	if out.Organization.PeopleCount != 4 {
		t.Errorf("people_count = %d, want 4", out.Organization.PeopleCount)
	}
	if out.Organization.CustomFields["Tier"] != "strategic" {
		t.Errorf("custom field name resolution lost: %v", out.Organization.CustomFields)
	}
}

func TestGetOrganization_RejectsZeroID(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterOrganizations(s, &fakeOrganizationsClient{}, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_organization",
		Arguments: map[string]any{"org_id": 0},
	})
	if !res.IsError {
		t.Fatal("expected isError on zero org_id")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
}

func TestGetOrganization_UpstreamNotFound(t *testing.T) {
	fake := &fakeOrganizationsClient{
		err: &pipedrive.APIError{
			Class:    pipedrive.ErrNotFound,
			Status:   404,
			Message:  "Organization not found",
			Endpoint: "/api/v2/organizations/99999",
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterOrganizations(s, fake, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_organization",
		Arguments: map[string]any{"org_id": 99999},
	})
	if !res.IsError {
		t.Fatal("expected isError on upstream 404")
	}
	if !strings.HasPrefix(contentText(res), "[not_found]") {
		t.Errorf("error text = %q; want [not_found] prefix", contentText(res))
	}
}

func TestListOrganizations_HappyPath(t *testing.T) {
	fake := &fakeOrganizationsClient{
		orgs: []pipedrive.Organization{
			{ID: 1, Name: "AcmeCo", OwnerID: 13, CustomFields: map[string]any{"def": "strategic"}},
			{ID: 2, Name: "BetaCorp", OwnerID: 13},
		},
		orgsNxt: "cursor-page-2",
		resolver: func(raw map[string]any) map[string]any {
			if v, ok := raw["def"]; ok {
				return map[string]any{"Tier": v}
			}
			return raw
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterOrganizations(s, fake, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_organizations",
		Arguments: map[string]any{"owner_id": 13, "limit": 50},
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out struct {
		Organizations []orgRow `json:"organizations"`
		NextCursor    string   `json:"next_cursor"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)

	if len(out.Organizations) != 2 {
		t.Fatalf("got %d orgs, want 2", len(out.Organizations))
	}
	if out.Organizations[0].URL != "https://acme.pipedrive.com/organization/1" {
		t.Errorf("URL = %q, want acme/organization/1", out.Organizations[0].URL)
	}
	if out.NextCursor != "cursor-page-2" {
		t.Errorf("next_cursor = %q, want cursor-page-2", out.NextCursor)
	}
	if out.Organizations[0].CustomFields["Tier"] != "strategic" {
		t.Errorf("custom field name resolution lost: %v", out.Organizations[0].CustomFields)
	}
	if fake.lastListOpts.OwnerID != 13 || fake.lastListOpts.Limit != 50 {
		t.Errorf("client received opts %+v; want owner_id=13 limit=50", fake.lastListOpts)
	}
	if fake.lastListOpts.SortBy != "update_time" || fake.lastListOpts.SortDirection != "desc" {
		t.Errorf("default sort = %q %q; want update_time desc", fake.lastListOpts.SortBy, fake.lastListOpts.SortDirection)
	}
}

func TestListOrganizations_RejectsBadSortBy(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterOrganizations(s, &fakeOrganizationsClient{}, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_organizations",
		Arguments: map[string]any{"sort_by": "name"},
	})
	if !res.IsError {
		t.Fatal("expected isError on unsupported sort_by")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
}

func TestListOrganizations_LimitDefaultWhenZero(t *testing.T) {
	fake := &fakeOrganizationsClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterOrganizations(s, fake, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	_, _ = h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_organizations",
		Arguments: map[string]any{},
	})
	if fake.lastListOpts.Limit != 25 {
		t.Errorf("limit=%d; want 25 (default)", fake.lastListOpts.Limit)
	}
}

func TestRegisterOrganizations_RegistersInDumpRegistry(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterOrganizations(s, &fakeOrganizationsClient{}, "acme", tools.RegisterOptions{})
	})
	defer h.Close()
	var buf strings.Builder
	if err := tools.DumpJSON(&buf, "test"); err != nil {
		t.Fatalf("DumpJSON: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"get_organization"`, `"list_organizations"`, `"create_organization"`} {
		if !strings.Contains(out, want) {
			t.Errorf("dump missing %s", want)
		}
	}
}

func TestCreateOrganization_HappyPath(t *testing.T) {
	fake := &fakeOrganizationsClient{
		createOrg: &pipedrive.Organization{
			ID:      59,
			Name:    "Nordjyllands Trafikselskab",
			Address: &pipedrive.Address{Value: "John F. Kennedys Plads 1T, Aalborg, Denmark", Country: "Denmark", Locality: "Aalborg", PostalCode: "9000"},
			OwnerID: 13,
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterOrganizations(s, fake, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_organization",
		Arguments: map[string]any{
			"name":     "Nordjyllands Trafikselskab",
			"address":  "John F. Kennedys Plads 1T, Aalborg, Denmark",
			"owner_id": 13,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out struct {
		Organization orgRow `json:"organization"`
		DryRun       bool   `json:"dry_run"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)

	if !fake.createCallSeen {
		t.Fatal("CreateOrganization was not called")
	}
	if out.DryRun {
		t.Error("dry_run = true on a non-dry-run handler")
	}
	if out.Organization.ID != 59 || out.Organization.Name != "Nordjyllands Trafikselskab" {
		t.Errorf("org echo lost fields: %+v", out.Organization)
	}
	if out.Organization.URL != "https://acme.pipedrive.com/organization/59" {
		t.Errorf("URL = %q, want acme/organization/59", out.Organization.URL)
	}
	if out.Organization.Address == nil || out.Organization.Address.Country != "Denmark" {
		t.Errorf("server-parsed address lost: %+v", out.Organization.Address)
	}
	if fake.lastCreateReq.Name != "Nordjyllands Trafikselskab" ||
		fake.lastCreateReq.Address != "John F. Kennedys Plads 1T, Aalborg, Denmark" ||
		fake.lastCreateReq.OwnerID != 13 {
		t.Errorf("upstream request lost fields: %+v", fake.lastCreateReq)
	}
}

func TestCreateOrganization_RejectsEmptyName(t *testing.T) {
	fake := &fakeOrganizationsClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterOrganizations(s, fake, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "create_organization",
		Arguments: map[string]any{"name": ""},
	})
	if !res.IsError {
		t.Fatal("expected isError on empty name")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
	if fake.createCallSeen {
		t.Error("CreateOrganization called despite client-side validation failure")
	}
}

func TestCreateOrganization_DryRunSkipsUpstream(t *testing.T) {
	fake := &fakeOrganizationsClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterOrganizations(s, fake, "acme", tools.RegisterOptions{DryRun: true}) // dryRun = true
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_organization",
		Arguments: map[string]any{
			"name":    "Dry run probe",
			"address": "Somewhere on the moon",
		},
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	if fake.createCallSeen {
		t.Error("CreateOrganization called on dry-run path; upstream POST should be skipped")
	}
	var out struct {
		Organization orgRow `json:"organization"`
		DryRun       bool   `json:"dry_run"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)
	if !out.DryRun {
		t.Error("dry_run = false on synthetic preview")
	}
	if out.Organization.ID != 0 {
		t.Errorf("synthetic id = %d; want 0", out.Organization.ID)
	}
	if out.Organization.Name != "Dry run probe" {
		t.Errorf("synthetic org lost name: %+v", out.Organization)
	}
	// Synthetic preview wraps the input string as Address.Value; no
	// server-side parsing happens on the dry-run path.
	if out.Organization.Address == nil || out.Organization.Address.Value != "Somewhere on the moon" {
		t.Errorf("synthetic address Value = %+v; want \"Somewhere on the moon\"", out.Organization.Address)
	}
}

func TestCreateOrganization_PropagatesUpstreamError(t *testing.T) {
	fake := &fakeOrganizationsClient{
		createErr: &pipedrive.APIError{
			Class:    pipedrive.ErrValidation,
			Status:   400,
			Message:  "owner_id must be a valid user",
			Endpoint: "/api/v2/organizations",
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterOrganizations(s, fake, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_organization",
		Arguments: map[string]any{
			"name":     "Bad owner",
			"owner_id": 999999,
		},
	})
	if !res.IsError {
		t.Fatal("expected isError on upstream 400")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
}

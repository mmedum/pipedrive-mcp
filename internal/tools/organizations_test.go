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
	org      *pipedrive.Organization
	err      error
	resolver func(map[string]any) map[string]any
}

func (f *fakeOrganizationsClient) GetOrganization(_ context.Context, _ int64) (*pipedrive.Organization, error) {
	return f.org, f.err
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
		tools.RegisterOrganizations(s, fake, "acme")
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
		tools.RegisterOrganizations(s, &fakeOrganizationsClient{}, "acme")
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
		tools.RegisterOrganizations(s, fake, "acme")
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

func TestRegisterOrganizations_RegistersInDumpRegistry(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterOrganizations(s, &fakeOrganizationsClient{}, "acme")
	})
	defer h.Close()
	var buf strings.Builder
	if err := tools.DumpJSON(&buf, "test"); err != nil {
		t.Fatalf("DumpJSON: %v", err)
	}
	if !strings.Contains(buf.String(), `"get_organization"`) {
		t.Errorf("dump missing 'get_organization'; got: %s", buf.String())
	}
}

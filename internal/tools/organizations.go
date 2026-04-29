package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

type organizationsClient interface {
	GetOrganization(ctx context.Context, id int64) (*pipedrive.Organization, error)
	ListOrganizations(ctx context.Context, opts pipedrive.ListOrganizationsOptions) ([]pipedrive.Organization, string, error)
	CreateOrganization(ctx context.Context, req pipedrive.CreateOrganizationRequest) (*pipedrive.Organization, error)
	ResolveOrganizationCustomFields(ctx context.Context, raw map[string]any) map[string]any
}

// allowedOrgSortFields enumerates Pipedrive v2's allowed sort_by
// values for /organizations. v2 supports id / update_time / add_time
// only — matches the shared commonV2TimestampSortFields base.
var allowedOrgSortFields = commonV2TimestampSortFields

// addressRow is an intentional subset of pipedrive.Address for the
// LLM-facing surface: street-level components (route, street_number,
// admin_area_level_1/2, sublocality) are omitted as low-signal for
// the kinds of questions the LLM answers from a search result. Drop
// the parallel struct and surface pipedrive.Address directly only if
// those components start mattering.
type addressRow struct {
	Value      string `json:"value,omitempty" jsonschema:"formatted human-readable address"`
	Country    string `json:"country,omitempty" jsonschema:"country, if Pipedrive parsed one"`
	Locality   string `json:"locality,omitempty" jsonschema:"city / locality, if Pipedrive parsed one"`
	PostalCode string `json:"postal_code,omitempty" jsonschema:"postal/ZIP code, if Pipedrive parsed one"`
}

type organizationSummary struct {
	ID           int64          `json:"id" jsonschema:"the organization's numeric id"`
	Name         string         `json:"name" jsonschema:"the organization's display name"`
	Address      *addressRow    `json:"address,omitempty" jsonschema:"structured address with the formatted string + parsed components, when present"`
	OwnerID      int64          `json:"owner_id" jsonschema:"id of the user who owns this record"`
	PeopleCount  int            `json:"people_count,omitempty" jsonschema:"number of persons linked to this org"`
	AddTime      string         `json:"add_time,omitempty" jsonschema:"timestamp the organization was created"`
	UpdateTime   string         `json:"update_time,omitempty" jsonschema:"timestamp the organization was last updated"`
	CustomFields map[string]any `json:"custom_fields,omitempty" jsonschema:"custom fields keyed by human-readable name"`
	URL          string         `json:"url" jsonschema:"link to the organization in the Pipedrive web UI"`
}

type getOrganizationInput struct {
	OrgID int64 `json:"org_id" jsonschema:"the organization's numeric id"`
}

type getOrganizationOutput struct {
	Organization organizationSummary `json:"organization" jsonschema:"the requested organization"`
}

type listOrganizationsInput struct {
	OwnerID       int64  `json:"owner_id,omitempty" jsonschema:"return only organizations owned by this user id; 0 = no filter"`
	UpdatedSince  string `json:"updated_since,omitempty" jsonschema:"RFC3339 timestamp; return only organizations updated at or after this time (e.g. 2026-04-01T00:00:00Z)"`
	UpdatedUntil  string `json:"updated_until,omitempty" jsonschema:"RFC3339 timestamp; return only organizations updated at or before this time"`
	SortBy        string `json:"sort_by,omitempty" jsonschema:"id | update_time | add_time. Default 'update_time' (most-recently-touched first)."`
	SortDirection string `json:"sort_direction,omitempty" jsonschema:"asc | desc. Default 'desc' when sort_by is omitted; 'asc' otherwise."`
	Limit         int    `json:"limit,omitempty" jsonschema:"page size; default 25, max 100"`
	Cursor        string `json:"cursor,omitempty" jsonschema:"opaque pagination token from a previous list_organizations response; omit for the first page"`
}

type listOrganizationsOutput struct {
	Organizations []organizationSummary `json:"organizations" jsonschema:"matching organizations on this page"`
	NextCursor    string                `json:"next_cursor,omitempty" jsonschema:"pass to the next list_organizations call to get the next page; empty when there are no more pages"`
}

type createOrganizationInput struct {
	Name    string `json:"name" jsonschema:"the organization's display name; required, non-empty. If the user did not give you a name, ask — do NOT invent one."`
	Address string `json:"address,omitempty" jsonschema:"single-line address as the user dictated it (e.g. '123 Main St, San Francisco, CA 94103'). Pipedrive parses it server-side into structured country/locality/postal_code on the response. Do NOT pre-parse into JSON or split into components."`
	OwnerID int64  `json:"owner_id,omitempty" jsonschema:"id of the user to own the record; omit to default to the API-token user"`
}

type createOrganizationOutput struct {
	Organization organizationSummary `json:"organization" jsonschema:"the newly-created organization as Pipedrive echoes it. When dry_run is true, this is a synthetic record with id=0 reflecting what would have been created."`
	DryRun       bool                `json:"dry_run,omitempty" jsonschema:"true when PIPEDRIVE_DRY_RUN was set on the server: no upstream POST was issued"`
}

// RegisterOrganizations wires get_organization, list_organizations,
// and create_organization into the MCP server. dryRun mirrors the
// server-wide PIPEDRIVE_DRY_RUN env: when true, create_organization
// returns a synthetic preview without firing the upstream POST.
func RegisterOrganizations(s *mcp.Server, c organizationsClient, companyDomain string, dryRun bool) {
	readOnly := mcp.ToolAnnotations{ReadOnlyHint: true}

	AddTool(s, &mcp.Tool{
		Name:        "get_organization",
		Description: "Fetch a single Pipedrive organization by org_id. Returns id, name, formatted address, owner_id, people_count (linked persons), add/update timestamps, and any custom fields resolved by name. Unknown org_id returns a [not_found] error. To find an organization by name, call `search` first to resolve the id.",
		Annotations: &readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getOrganizationInput) (*mcp.CallToolResult, getOrganizationOutput, error) {
		if err := validatePositiveID(in.OrgID, "org_id"); err != nil {
			return errorResult(err), getOrganizationOutput{}, nil
		}
		o, err := c.GetOrganization(ctx, in.OrgID)
		if err != nil {
			return errorResult(err), getOrganizationOutput{}, nil
		}
		resolved := c.ResolveOrganizationCustomFields(ctx, o.CustomFields)
		return nil, getOrganizationOutput{Organization: summarizeOrganization(companyDomain, o, resolved)}, nil
	})

	AddTool(s, &mcp.Tool{
		Name:        "list_organizations",
		Description: "List Pipedrive organizations filtered by owner or update window. Returns id, name, formatted address (with parsed country/locality/postal_code when present), owner_id, people_count, add/update timestamps, and any custom fields resolved by name. Default sort is update_time desc — most-recently-touched first, ideal for 'which accounts have we been working on lately'. Default limit is 25, max 100. For more results, pass the next_cursor from the previous response. To find an organization by name (rather than ID), call `search` with type=organization — search is the natural-language gateway, list_organizations is the precision filter when the IDs are already known.",
		Annotations: &readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listOrganizationsInput) (*mcp.CallToolResult, listOrganizationsOutput, error) {
		if err := validateEnum(in.SortBy, "sort_by", allowedOrgSortFields); err != nil {
			return errorResult(err), listOrganizationsOutput{}, nil
		}
		if err := validateEnum(in.SortDirection, "sort_direction", allowedSortDirections); err != nil {
			return errorResult(err), listOrganizationsOutput{}, nil
		}

		sortBy, sortDir := effectiveSort(in.SortBy, in.SortDirection)
		opts := pipedrive.ListOrganizationsOptions{
			OwnerID:       in.OwnerID,
			UpdatedSince:  in.UpdatedSince,
			UpdatedUntil:  in.UpdatedUntil,
			SortBy:        sortBy,
			SortDirection: sortDir,
			Limit:         clampLimit(in.Limit),
			Cursor:        in.Cursor,
		}
		orgs, next, err := c.ListOrganizations(ctx, opts)
		if err != nil {
			return errorResult(err), listOrganizationsOutput{}, nil
		}
		out := listOrganizationsOutput{
			Organizations: make([]organizationSummary, 0, len(orgs)),
			NextCursor:    next,
		}
		for i := range orgs {
			resolved := c.ResolveOrganizationCustomFields(ctx, orgs[i].CustomFields)
			out.Organizations = append(out.Organizations, summarizeOrganization(companyDomain, &orgs[i], resolved))
		}
		return nil, out, nil
	})

	AddTool(s, &mcp.Tool{
		Name:        "create_organization",
		Description: "Create a new Pipedrive organization. Required: `name`. If the user did not give you a name, ask — do NOT invent one. Honours PIPEDRIVE_DRY_RUN=true on the server by returning a synthetic preview (dry_run=true, id=0) without issuing the POST. Custom fields are not writable through this tool yet.",
	}, createOrganizationHandler(c, companyDomain, dryRun))
}

func createOrganizationHandler(c organizationsClient, companyDomain string, dryRun bool) mcp.ToolHandlerFor[createOrganizationInput, createOrganizationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in createOrganizationInput) (*mcp.CallToolResult, createOrganizationOutput, error) {
		if in.Name == "" {
			return errorResult(fmt.Errorf("%w: name must not be empty", pipedrive.ErrValidation)), createOrganizationOutput{}, nil
		}
		req := pipedrive.CreateOrganizationRequest{
			Name:    in.Name,
			Address: in.Address,
			OwnerID: in.OwnerID,
		}
		if dryRun {
			return nil, createOrganizationOutput{
				Organization: summarizeOrganization(companyDomain, syntheticOrgFromRequest(req), nil),
				DryRun:       true,
			}, nil
		}
		o, err := c.CreateOrganization(ctx, req)
		if err != nil {
			return errorResult(err), createOrganizationOutput{}, nil
		}
		resolved := c.ResolveOrganizationCustomFields(ctx, o.CustomFields)
		return nil, createOrganizationOutput{Organization: summarizeOrganization(companyDomain, o, resolved)}, nil
	}
}

// syntheticOrgFromRequest builds a placeholder Organization mirroring
// the CreateOrganizationRequest, used only on the dry-run path so
// create_organization's output schema stays consistent. ID=0 +
// dry_run=true tells the LLM nothing was actually persisted. Address
// is wrapped as Address.Value; server-side parsing of country/
// locality/postal_code only happens on the real upstream call.
func syntheticOrgFromRequest(req pipedrive.CreateOrganizationRequest) *pipedrive.Organization {
	o := &pipedrive.Organization{
		Name:    req.Name,
		OwnerID: req.OwnerID,
	}
	if req.Address != "" {
		o.Address = &pipedrive.Address{Value: req.Address}
	}
	return o
}

func summarizeOrganization(domain string, o *pipedrive.Organization, customFields map[string]any) organizationSummary {
	out := organizationSummary{
		ID:           o.ID,
		Name:         o.Name,
		OwnerID:      o.OwnerID,
		PeopleCount:  o.PeopleCount,
		AddTime:      o.AddTime,
		UpdateTime:   o.UpdateTime,
		CustomFields: customFields,
		URL:          pipedrive.WebURL(domain, pipedrive.WebURLOrganization, o.ID),
	}
	if o.Address != nil {
		out.Address = &addressRow{
			Value:      o.Address.Value,
			Country:    o.Address.Country,
			Locality:   o.Address.Locality,
			PostalCode: o.Address.PostalCode,
		}
	}
	return out
}

package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

type organizationsClient interface {
	GetOrganization(ctx context.Context, id int64) (*pipedrive.Organization, error)
	ResolveOrganizationCustomFields(ctx context.Context, raw map[string]any) map[string]any
}

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

// RegisterOrganizations wires get_organization into the MCP server.
func RegisterOrganizations(s *mcp.Server, c organizationsClient, companyDomain string) {
	readOnly := mcp.ToolAnnotations{ReadOnlyHint: true}

	AddTool(s, &mcp.Tool{
		Name:        "get_organization",
		Description: "Fetch a single Pipedrive organization by org_id. Returns id, name, formatted address, owner_id, people_count (linked persons), add/update timestamps, and any custom fields resolved by name. Unknown org_id returns a [not_found] error. To find an organization by name, call `search` first to resolve the id.",
		Annotations: &readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getOrganizationInput) (*mcp.CallToolResult, getOrganizationOutput, error) {
		if in.OrgID <= 0 {
			err := fmt.Errorf("%w: org_id must be a positive integer", pipedrive.ErrValidation)
			return errorResult(err), getOrganizationOutput{}, nil
		}
		o, err := c.GetOrganization(ctx, in.OrgID)
		if err != nil {
			return errorResult(err), getOrganizationOutput{}, nil
		}
		resolved := c.ResolveOrganizationCustomFields(ctx, o.CustomFields)
		return nil, getOrganizationOutput{Organization: summarizeOrganization(companyDomain, o, resolved)}, nil
	})
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
	if o.Address != nil && (o.Address.Value != "" || o.Address.Country != "") {
		out.Address = &addressRow{
			Value:      o.Address.Value,
			Country:    o.Address.Country,
			Locality:   o.Address.Locality,
			PostalCode: o.Address.PostalCode,
		}
	}
	return out
}

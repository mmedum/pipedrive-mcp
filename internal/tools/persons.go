package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

type personsClient interface {
	GetPerson(ctx context.Context, id int64) (*pipedrive.Person, error)
	ResolvePersonCustomFields(ctx context.Context, raw map[string]any) map[string]any
}

type contactPointRow struct {
	Value   string `json:"value" jsonschema:"the email/phone value"`
	Primary bool   `json:"primary" jsonschema:"true if this is the primary entry"`
	Label   string `json:"label,omitempty" jsonschema:"optional label, e.g. work | home | mobile"`
}

type personSummary struct {
	ID           int64             `json:"id" jsonschema:"the person's numeric id"`
	Name         string            `json:"name" jsonschema:"full name (first + last)"`
	FirstName    string            `json:"first_name,omitempty" jsonschema:"first / given name"`
	LastName     string            `json:"last_name,omitempty" jsonschema:"last / family name"`
	Emails       []contactPointRow `json:"emails,omitempty" jsonschema:"all email addresses on the record"`
	Phones       []contactPointRow `json:"phones,omitempty" jsonschema:"all phone numbers on the record"`
	OrgID        int64             `json:"org_id" jsonschema:"id of the linked organization, 0 if none"`
	OwnerID      int64             `json:"owner_id" jsonschema:"id of the user who owns this record"`
	AddTime      string            `json:"add_time,omitempty" jsonschema:"timestamp the person was created"`
	UpdateTime   string            `json:"update_time,omitempty" jsonschema:"timestamp the person was last updated"`
	CustomFields map[string]any    `json:"custom_fields,omitempty" jsonschema:"custom fields keyed by human-readable name"`
	URL          string            `json:"url" jsonschema:"link to the person in the Pipedrive web UI"`
}

type getPersonInput struct {
	PersonID int64 `json:"person_id" jsonschema:"the person's numeric id"`
}

type getPersonOutput struct {
	Person personSummary `json:"person" jsonschema:"the requested person"`
}

// RegisterPersons wires get_person into the MCP server.
func RegisterPersons(s *mcp.Server, c personsClient, companyDomain string) {
	readOnly := mcp.ToolAnnotations{ReadOnlyHint: true}

	AddTool(s, &mcp.Tool{
		Name:        "get_person",
		Description: "Fetch a single Pipedrive person by person_id. Returns id, name, first_name, last_name, all emails (with primary flag and label), all phones, owner_id, linked org_id, add/update timestamps, and any custom fields resolved by name. Unknown person_id returns a [not_found] error. To find a person by name, call `search` first to resolve the id.",
		Annotations: &readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getPersonInput) (*mcp.CallToolResult, getPersonOutput, error) {
		if in.PersonID <= 0 {
			err := fmt.Errorf("%w: person_id must be a positive integer", pipedrive.ErrValidation)
			return errorResult(err), getPersonOutput{}, nil
		}
		p, err := c.GetPerson(ctx, in.PersonID)
		if err != nil {
			return errorResult(err), getPersonOutput{}, nil
		}
		resolved := c.ResolvePersonCustomFields(ctx, p.CustomFields)
		return nil, getPersonOutput{Person: summarizePerson(companyDomain, p, resolved)}, nil
	})
}

func summarizePerson(domain string, p *pipedrive.Person, customFields map[string]any) personSummary {
	emails := make([]contactPointRow, 0, len(p.Emails))
	for _, e := range p.Emails {
		emails = append(emails, contactPointRow{Value: e.Value, Primary: e.Primary, Label: e.Label})
	}
	phones := make([]contactPointRow, 0, len(p.Phones))
	for _, ph := range p.Phones {
		phones = append(phones, contactPointRow{Value: ph.Value, Primary: ph.Primary, Label: ph.Label})
	}
	return personSummary{
		ID:           p.ID,
		Name:         p.Name,
		FirstName:    p.FirstName,
		LastName:     p.LastName,
		Emails:       emails,
		Phones:       phones,
		OrgID:        p.OrgID,
		OwnerID:      p.OwnerID,
		AddTime:      p.AddTime,
		UpdateTime:   p.UpdateTime,
		CustomFields: customFields,
		URL:          pipedrive.WebURL(domain, pipedrive.WebURLPerson, p.ID),
	}
}

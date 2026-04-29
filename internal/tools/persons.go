package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

type personsClient interface {
	GetPerson(ctx context.Context, id int64) (*pipedrive.Person, error)
	ListPersons(ctx context.Context, opts pipedrive.ListPersonsOptions) ([]pipedrive.Person, string, error)
	CreatePerson(ctx context.Context, req pipedrive.CreatePersonRequest) (*pipedrive.Person, error)
	ResolvePersonCustomFields(ctx context.Context, raw map[string]any) map[string]any
}

// allowedPersonSortFields enumerates Pipedrive v2's allowed sort_by
// values for /persons. v2 supports id / update_time / add_time only —
// matches the shared commonV2TimestampSortFields base.
var allowedPersonSortFields = commonV2TimestampSortFields

// personSummary surfaces emails/phones as the same pipedrive.ContactPoint
// shape directly — the byte-identical row type doesn't earn its keep
// behind a parallel struct.
type personSummary struct {
	ID           int64                    `json:"id" jsonschema:"the person's numeric id"`
	Name         string                   `json:"name" jsonschema:"full name (first + last)"`
	FirstName    string                   `json:"first_name,omitempty" jsonschema:"first / given name"`
	LastName     string                   `json:"last_name,omitempty" jsonschema:"last / family name"`
	Emails       []pipedrive.ContactPoint `json:"emails,omitempty" jsonschema:"all email addresses on the record"`
	Phones       []pipedrive.ContactPoint `json:"phones,omitempty" jsonschema:"all phone numbers on the record"`
	OrgID        int64                    `json:"org_id" jsonschema:"id of the linked organization, 0 if none"`
	OwnerID      int64                    `json:"owner_id" jsonschema:"id of the user who owns this record"`
	AddTime      string                   `json:"add_time,omitempty" jsonschema:"timestamp the person was created"`
	UpdateTime   string                   `json:"update_time,omitempty" jsonschema:"timestamp the person was last updated"`
	CustomFields map[string]any           `json:"custom_fields,omitempty" jsonschema:"custom fields keyed by human-readable name"`
	URL          string                   `json:"url" jsonschema:"link to the person in the Pipedrive web UI"`
}

type getPersonInput struct {
	PersonID int64 `json:"person_id" jsonschema:"the person's numeric id"`
}

type getPersonOutput struct {
	Person personSummary `json:"person" jsonschema:"the requested person"`
}

type listPersonsInput struct {
	OwnerID       int64  `json:"owner_id,omitempty" jsonschema:"return only persons owned by this user id; 0 = no filter"`
	OrgID         int64  `json:"org_id,omitempty" jsonschema:"return only persons linked to this organization id; 0 = no filter"`
	UpdatedSince  string `json:"updated_since,omitempty" jsonschema:"RFC3339 timestamp; return only persons updated at or after this time (e.g. 2026-04-01T00:00:00Z)"`
	UpdatedUntil  string `json:"updated_until,omitempty" jsonschema:"RFC3339 timestamp; return only persons updated at or before this time"`
	SortBy        string `json:"sort_by,omitempty" jsonschema:"id | update_time | add_time. Default 'update_time' (most-recently-touched first)."`
	SortDirection string `json:"sort_direction,omitempty" jsonschema:"asc | desc. Default 'desc' when sort_by is omitted; 'asc' otherwise."`
	Limit         int    `json:"limit,omitempty" jsonschema:"page size; default 25, max 100"`
	Cursor        string `json:"cursor,omitempty" jsonschema:"opaque pagination token from a previous list_persons response; omit for the first page"`
}

type listPersonsOutput struct {
	Persons    []personSummary `json:"persons" jsonschema:"matching persons on this page"`
	NextCursor string          `json:"next_cursor,omitempty" jsonschema:"pass to the next list_persons call to get the next page; empty when there are no more pages"`
}

type createPersonInput struct {
	Name      string                   `json:"name" jsonschema:"the person's full name; required, non-empty. If the user did not give you a name, ask — do NOT invent one."`
	FirstName string                   `json:"first_name,omitempty" jsonschema:"first / given name; optional. Pipedrive will combine first_name + last_name into name if name is set; passing both is fine."`
	LastName  string                   `json:"last_name,omitempty" jsonschema:"last / family name; optional"`
	Emails    []pipedrive.ContactPoint `json:"emails,omitempty" jsonschema:"email addresses. Each entry: {value, primary, label}. Set primary=true on at most one; label is free-text (e.g. 'work', 'home')."`
	Phones    []pipedrive.ContactPoint `json:"phones,omitempty" jsonschema:"phone numbers. Same shape as emails."`
	OrgID     int64                    `json:"org_id,omitempty" jsonschema:"id of the organization to link the person to; 0 = no link. Use search(type=organization) to resolve a name."`
	OwnerID   int64                    `json:"owner_id,omitempty" jsonschema:"id of the user to own the record; omit to default to the API-token user"`
}

type createPersonOutput struct {
	Person personSummary `json:"person" jsonschema:"the newly-created person as Pipedrive echoes it. When dry_run is true, this is a synthetic record with id=0 reflecting what would have been created."`
	DryRun bool          `json:"dry_run,omitempty" jsonschema:"true when PIPEDRIVE_DRY_RUN was set on the server: no upstream POST was issued"`
}

// RegisterPersons wires get_person, list_persons, and create_person
// into the MCP server. dryRun mirrors the server-wide
// PIPEDRIVE_DRY_RUN env: when true, create_person returns a synthetic
// preview without firing the upstream POST.
func RegisterPersons(s *mcp.Server, c personsClient, companyDomain string, dryRun bool) {
	readOnly := mcp.ToolAnnotations{ReadOnlyHint: true}

	AddTool(s, &mcp.Tool{
		Name:        "get_person",
		Description: "Fetch a single Pipedrive person by person_id. Returns id, name, first_name, last_name, all emails (with primary flag and label), all phones, owner_id, linked org_id, add/update timestamps, and any custom fields resolved by name. Unknown person_id returns a [not_found] error. To find a person by name, call `search` first to resolve the id.",
		Annotations: &readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getPersonInput) (*mcp.CallToolResult, getPersonOutput, error) {
		if err := validatePositiveID(in.PersonID, "person_id"); err != nil {
			return errorResult(err), getPersonOutput{}, nil
		}
		p, err := c.GetPerson(ctx, in.PersonID)
		if err != nil {
			return errorResult(err), getPersonOutput{}, nil
		}
		resolved := c.ResolvePersonCustomFields(ctx, p.CustomFields)
		return nil, getPersonOutput{Person: summarizePerson(companyDomain, p, resolved)}, nil
	})

	AddTool(s, &mcp.Tool{
		Name:        "list_persons",
		Description: "List Pipedrive persons filtered by owner, linked organization, or update window. Returns id, name, first_name, last_name, emails, phones, owner_id, linked org_id, add/update timestamps, and any custom fields resolved by name. Default sort is update_time desc — most-recently-touched first, ideal for 'who at company X have we been talking to lately'. Default limit is 25, max 100. For more results, pass the next_cursor from the previous response. To find a person by name (rather than ID), call `search` with type=person — search is the natural-language gateway, list_persons is the precision filter when the IDs are already known.",
		Annotations: &readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listPersonsInput) (*mcp.CallToolResult, listPersonsOutput, error) {
		if err := validateEnum(in.SortBy, "sort_by", allowedPersonSortFields); err != nil {
			return errorResult(err), listPersonsOutput{}, nil
		}
		if err := validateEnum(in.SortDirection, "sort_direction", allowedSortDirections); err != nil {
			return errorResult(err), listPersonsOutput{}, nil
		}

		sortBy, sortDir := effectiveSort(in.SortBy, in.SortDirection)
		opts := pipedrive.ListPersonsOptions{
			OwnerID:       in.OwnerID,
			OrgID:         in.OrgID,
			UpdatedSince:  in.UpdatedSince,
			UpdatedUntil:  in.UpdatedUntil,
			SortBy:        sortBy,
			SortDirection: sortDir,
			Limit:         clampLimit(in.Limit),
			Cursor:        in.Cursor,
		}
		persons, next, err := c.ListPersons(ctx, opts)
		if err != nil {
			return errorResult(err), listPersonsOutput{}, nil
		}
		out := listPersonsOutput{
			Persons:    make([]personSummary, 0, len(persons)),
			NextCursor: next,
		}
		for i := range persons {
			resolved := c.ResolvePersonCustomFields(ctx, persons[i].CustomFields)
			out.Persons = append(out.Persons, summarizePerson(companyDomain, &persons[i], resolved))
		}
		return nil, out, nil
	})

	AddTool(s, &mcp.Tool{
		Name:        "create_person",
		Description: "Create a new Pipedrive person (contact). Required: `name`. If the user did not give you a name, ask — do NOT invent one. Honours PIPEDRIVE_DRY_RUN=true on the server by returning a synthetic preview (dry_run=true, id=0) without issuing the POST. Custom fields are not writable through this tool yet.",
	}, createPersonHandler(c, companyDomain, dryRun))
}

func createPersonHandler(c personsClient, companyDomain string, dryRun bool) mcp.ToolHandlerFor[createPersonInput, createPersonOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in createPersonInput) (*mcp.CallToolResult, createPersonOutput, error) {
		if in.Name == "" {
			return errorResult(fmt.Errorf("%w: name must not be empty", pipedrive.ErrValidation)), createPersonOutput{}, nil
		}
		req := pipedrive.CreatePersonRequest{
			Name:      in.Name,
			FirstName: in.FirstName,
			LastName:  in.LastName,
			Emails:    in.Emails,
			Phones:    in.Phones,
			OrgID:     in.OrgID,
			OwnerID:   in.OwnerID,
		}
		if dryRun {
			return nil, createPersonOutput{
				Person: summarizePerson(companyDomain, syntheticPersonFromRequest(req), nil),
				DryRun: true,
			}, nil
		}
		p, err := c.CreatePerson(ctx, req)
		if err != nil {
			return errorResult(err), createPersonOutput{}, nil
		}
		resolved := c.ResolvePersonCustomFields(ctx, p.CustomFields)
		return nil, createPersonOutput{Person: summarizePerson(companyDomain, p, resolved)}, nil
	}
}

// syntheticPersonFromRequest builds a placeholder Person that mirrors
// the CreatePersonRequest, used only on the dry-run path so
// create_person's output schema stays consistent. ID=0 + dry_run=true
// tells the LLM nothing was actually persisted.
func syntheticPersonFromRequest(req pipedrive.CreatePersonRequest) *pipedrive.Person {
	return &pipedrive.Person{
		Name:      req.Name,
		FirstName: req.FirstName,
		LastName:  req.LastName,
		Emails:    req.Emails,
		Phones:    req.Phones,
		OrgID:     req.OrgID,
		OwnerID:   req.OwnerID,
	}
}

func summarizePerson(domain string, p *pipedrive.Person, customFields map[string]any) personSummary {
	return personSummary{
		ID:           p.ID,
		Name:         p.Name,
		FirstName:    p.FirstName,
		LastName:     p.LastName,
		Emails:       p.Emails,
		Phones:       p.Phones,
		OrgID:        p.OrgID,
		OwnerID:      p.OwnerID,
		AddTime:      p.AddTime,
		UpdateTime:   p.UpdateTime,
		CustomFields: customFields,
		URL:          pipedrive.WebURL(domain, pipedrive.WebURLPerson, p.ID),
	}
}

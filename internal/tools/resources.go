package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

// MCP resources mirroring the get_ tools, the way google-drive's
// gdrive://<id> and google-docs' gdocs://<id> mirror theirs. A client
// that attaches records rather than calling tools reads the same
// content through a URI.
//
// The resources are deliberately a mirror and not a second
// implementation: each handler calls the same summarize* function its
// tool does, so the two can never drift into reporting different
// things about the same record. What they cannot express is the
// tools' options — include_notes, include_attendees, custom-field
// resolution on a list — so a caller who needs those still calls the
// tool. The descriptions say so.
const resourceScheme = "pipedrive://"

// resourcesClient is the read surface the resource handlers need. It
// is the union of the get_ paths only: resources address one record by
// id, so nothing here lists or searches.
type resourcesClient interface {
	GetDeal(ctx context.Context, id int64) (*pipedrive.Deal, error)
	GetPerson(ctx context.Context, id int64) (*pipedrive.Person, error)
	GetOrganization(ctx context.Context, id int64) (*pipedrive.Organization, error)
	GetActivity(ctx context.Context, id int64, opts pipedrive.GetActivityOptions) (*pipedrive.Activity, error)
	GetNote(ctx context.Context, id int64) (*pipedrive.Note, error)
	ResolveDealCustomFields(ctx context.Context, raw map[string]any) map[string]any
	ResolvePersonCustomFields(ctx context.Context, raw map[string]any) map[string]any
	ResolveOrganizationCustomFields(ctx context.Context, raw map[string]any) map[string]any
}

// RegisterResources wires one resource template per record type.
func RegisterResources(s *mcp.Server, c resourcesClient, companyDomain string) {
	type tmpl struct {
		collection  string
		title       string
		description string
		fetch       func(context.Context, int64) (any, error)
	}

	templates := []tmpl{
		{
			collection:  "deals",
			title:       "Pipedrive deal",
			description: "One deal by id, with its custom fields resolved to the names the workspace gives them. The same record get_deal returns.",
			fetch: func(ctx context.Context, id int64) (any, error) {
				d, err := c.GetDeal(ctx, id)
				if err != nil {
					return nil, err
				}
				return summarizeDeal(companyDomain, d, c.ResolveDealCustomFields(ctx, d.CustomFields)), nil
			},
		},
		{
			collection:  "persons",
			title:       "Pipedrive person",
			description: "One contact by id, with emails, phones, the organization they belong to, and custom fields resolved by name. The same record get_person returns.",
			fetch: func(ctx context.Context, id int64) (any, error) {
				p, err := c.GetPerson(ctx, id)
				if err != nil {
					return nil, err
				}
				return summarizePerson(companyDomain, p, c.ResolvePersonCustomFields(ctx, p.CustomFields)), nil
			},
		},
		{
			collection:  "organizations",
			title:       "Pipedrive organization",
			description: "One organization by id, with its parsed address and custom fields resolved by name. The same record get_organization returns.",
			fetch: func(ctx context.Context, id int64) (any, error) {
				o, err := c.GetOrganization(ctx, id)
				if err != nil {
					return nil, err
				}
				return summarizeOrganization(companyDomain, o, c.ResolveOrganizationCustomFields(ctx, o.CustomFields)), nil
			},
		},
		{
			collection:  "activities",
			title:       "Pipedrive activity",
			description: "One activity by id. The same record get_activity returns, without its attendees — that needs get_activity(include_attendees=true), because a resource URI carries no options.",
			fetch: func(ctx context.Context, id int64) (any, error) {
				a, err := c.GetActivity(ctx, id, pipedrive.GetActivityOptions{})
				if err != nil {
					return nil, err
				}
				return summarizeActivity(companyDomain, a), nil
			},
		},
		{
			collection:  "notes",
			title:       "Pipedrive note",
			description: "One note by id, with its HTML content and the record it hangs off. The same record get_note returns. A soft-deleted note still reads here, with active_flag false.",
			fetch: func(ctx context.Context, id int64) (any, error) {
				n, err := c.GetNote(ctx, id)
				if err != nil {
					return nil, err
				}
				return summarizeNote(n), nil
			},
		},
	}

	for _, t := range templates {
		s.AddResourceTemplate(&mcp.ResourceTemplate{
			Name:        t.collection,
			Title:       t.title,
			Description: t.description,
			MIMEType:    "application/json",
			URITemplate: resourceScheme + t.collection + "/{id}",
		}, recordResourceHandler(t.collection, t.fetch))
	}
}

// recordResourceHandler parses the id out of the URI and renders the
// summary as JSON. Errors come back as ordinary Go errors rather than
// the tools' [class]-tagged results: resources have no isError channel,
// so the SDK turns these into JSON-RPC errors, which is the right one
// of the two channels here.
func recordResourceHandler(collection string, fetch func(context.Context, int64) (any, error)) mcp.ResourceHandler {
	prefix := resourceScheme + collection + "/"
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		id, err := resourceID(req.Params.URI, prefix)
		if err != nil {
			return nil, err
		}
		record, err := fetch(ctx, id)
		if err != nil {
			// Reuse the tools' classifier so a resource read and a tool
			// call describe the same upstream failure the same way.
			return nil, fmt.Errorf("%s", errorText(err))
		}
		body, err := json.Marshal(record)
		if err != nil {
			return nil, fmt.Errorf("marshal %s resource: %w", collection, err)
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      req.Params.URI,
				MIMEType: "application/json",
				Text:     string(body),
			}},
		}, nil
	}
}

func resourceID(uri, prefix string) (int64, error) {
	rest, ok := strings.CutPrefix(uri, prefix)
	if !ok {
		return 0, fmt.Errorf("resource uri %q does not start with %q", uri, prefix)
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("resource uri %q: %q is not a positive record id", uri, rest)
	}
	return id, nil
}

package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

type searchClient interface {
	ItemSearch(ctx context.Context, opts pipedrive.SearchOptions) ([]pipedrive.SearchHit, string, error)
}

// `lead` is included even though Phase 1 doesn't ship a lead resource
// yet — it lets the LLM at least find leads by name. Sourced from
// pipedrive.ItemType constants so the enum has one home.
var allowedSearchTypes = map[string]bool{
	string(pipedrive.ItemTypeDeal):         true,
	string(pipedrive.ItemTypePerson):       true,
	string(pipedrive.ItemTypeOrganization): true,
	string(pipedrive.ItemTypeProduct):      true,
	string(pipedrive.ItemTypeFile):         true,
	string(pipedrive.ItemTypeLead):         true,
}

const searchMinTermLen = 2

type searchHit struct {
	ID      int64          `json:"id" jsonschema:"the matched record's numeric id"`
	Type    string         `json:"type" jsonschema:"deal | person | organization | product | file | lead"`
	Name    string         `json:"name" jsonschema:"the record's human-readable name (deal title for deals, full name for everything else)"`
	Score   float64        `json:"score" jsonschema:"Pipedrive's relevance score; higher is a better match"`
	Details map[string]any `json:"details,omitempty" jsonschema:"type-specific extras: org country/city, person email/phone, deal value/currency/status, etc."`
}

type searchInput struct {
	Term       string   `json:"term" jsonschema:"free-text search term; minimum 2 characters (1 if exact_match=true)"`
	Types      []string `json:"types,omitempty" jsonschema:"restrict to these item types: deal | person | organization | product | file | lead. Omit to search all."`
	ExactMatch bool     `json:"exact_match,omitempty" jsonschema:"true = require an exact (case-insensitive) match on the full term; false = partial match (default)"`
	Limit      int      `json:"limit,omitempty" jsonschema:"page size; default 25, max 100"`
	Cursor     string   `json:"cursor,omitempty" jsonschema:"opaque pagination token from a previous search response; omit for the first page"`
}

type searchOutput struct {
	Hits       []searchHit `json:"hits" jsonschema:"matching records sorted by relevance score, descending"`
	Truncated  bool        `json:"truncated" jsonschema:"true when more results exist beyond this page; either pass next_cursor for the next page, or refine the term / narrow the types to focus the search. Do NOT treat the returned hits as exhaustive when this is true."`
	NextCursor string      `json:"next_cursor,omitempty" jsonschema:"pass to the next search call to fetch the next page; empty when there are no more pages"`
}

// RegisterSearch wires the search tool into the MCP server.
func RegisterSearch(s *mcp.Server, c searchClient) {
	readOnly := mcp.ToolAnnotations{ReadOnlyHint: true}

	AddTool(s, &mcp.Tool{
		Name: "search",
		Description: "Search Pipedrive for deals, persons, organizations, products, files, or leads matching a free-text term. " +
			"Use this to resolve a name to a numeric id BEFORE calling get_deal, list_deals, or other id-keyed tools — " +
			"e.g. when the user asks about \"deals for Acme\", call search(term=\"Acme\", types=[\"organization\"]) first to get the org_id, " +
			"then call list_deals(org_id=...). Returns id, type, name, relevance score, and type-specific details per hit. " +
			"Default limit is 25, max 100. " +
			"When `truncated` is true, more results exist — paginate via `next_cursor` or refine the term; do not treat the page as exhaustive. " +
			"Limitations: Pipedrive's search covers built-in fields and only varchar / monetary / phone / address custom-field types. " +
			"For filters by stage, owner, value, dates, or other custom-field types, use list_deals with structured parameters instead.",
		Annotations: &readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, searchOutput, error) {
		minLen := searchMinTermLen
		if in.ExactMatch {
			minLen = 1
		}
		if len(in.Term) < minLen {
			err := fmt.Errorf("%w: term must be at least %d character(s)", pipedrive.ErrValidation, minLen)
			return errorResult(err), searchOutput{}, nil
		}
		for _, t := range in.Types {
			if err := validateEnum(t, "type", allowedSearchTypes); err != nil {
				return errorResult(err), searchOutput{}, nil
			}
		}
		hits, next, err := c.ItemSearch(ctx, pipedrive.SearchOptions{
			Term:       in.Term,
			ItemTypes:  in.Types,
			ExactMatch: in.ExactMatch,
			Limit:      clampLimit(in.Limit),
			Cursor:     in.Cursor,
		})
		if err != nil {
			return errorResult(err), searchOutput{}, nil
		}
		out := searchOutput{
			Hits:       make([]searchHit, 0, len(hits)),
			NextCursor: next,
			// Pipedrive's empty next_cursor is the authoritative "no
			// more results" signal — including the case where the
			// page happens to be exactly `limit` deep. Trusting it
			// avoids a false-positive truncated=true on full pages
			// that are actually exhaustive.
			Truncated: next != "",
		}
		for _, h := range hits {
			out.Hits = append(out.Hits, summarizeHit(h))
		}
		return nil, out, nil
	})
}

// summarizeHit translates Pipedrive's raw item map into the
// LLM-facing searchHit shape. Lives in the tools package because
// the per-type field choice (deal `title` vs everything else's
// `name`) is a presentation decision; the pipedrive package
// returns the raw map so it stays HTTP-only.
func summarizeHit(h pipedrive.SearchHit) searchHit {
	out := searchHit{
		Score: h.Score,
		ID:    pipedrive.ItemInt64(h.Item, "id"),
		Type:  pipedrive.ItemString(h.Item, "type"),
	}
	if out.Type == string(pipedrive.ItemTypeDeal) {
		out.Name = pipedrive.ItemString(h.Item, "title")
	} else {
		out.Name = pipedrive.ItemString(h.Item, "name")
	}
	for k, v := range h.Item {
		switch k {
		case "id", "type", "name", "title":
			continue
		default:
			if out.Details == nil {
				out.Details = make(map[string]any, len(h.Item))
			}
			out.Details[k] = v
		}
	}
	return out
}

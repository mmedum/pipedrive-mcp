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

// allowedSearchTypes mirrors Pipedrive's /itemSearch item_types.
// `lead` is included in v2 even though Phase 1 doesn't ship a lead
// resource yet — let the LLM at least find leads by name.
var allowedSearchTypes = map[string]bool{
	"deal":         true,
	"person":       true,
	"organization": true,
	"product":      true,
	"file":         true,
	"lead":         true,
}

const (
	searchDefaultLimit = 25
	searchMaxLimit     = 100
	searchMinTermLen   = 2
)

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
			"e.g. when the user asks about \"deals for GLS\", call search(term=\"GLS\", types=[\"organization\"]) first to get the org_id, " +
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
			if !allowedSearchTypes[t] {
				err := fmt.Errorf("%w: type %q is not one of deal|person|organization|product|file|lead", pipedrive.ErrValidation, t)
				return errorResult(err), searchOutput{}, nil
			}
		}
		limit := in.Limit
		if limit <= 0 {
			limit = searchDefaultLimit
		}
		if limit > searchMaxLimit {
			limit = searchMaxLimit
		}
		hits, next, err := c.ItemSearch(ctx, pipedrive.SearchOptions{
			Term:       in.Term,
			ItemTypes:  in.Types,
			ExactMatch: in.ExactMatch,
			Limit:      limit,
			Cursor:     in.Cursor,
		})
		if err != nil {
			return errorResult(err), searchOutput{}, nil
		}
		out := searchOutput{
			Hits:       make([]searchHit, 0, len(hits)),
			NextCursor: next,
			// Truncated is the explicit "page not exhaustive" signal:
			// a non-empty next_cursor or a full page both indicate more
			// results upstream that the LLM must not treat as absent.
			Truncated: next != "" || len(hits) >= limit,
		}
		for _, h := range hits {
			out.Hits = append(out.Hits, searchHit{
				ID:      h.ID,
				Type:    h.Type,
				Name:    h.Name,
				Score:   h.Score,
				Details: h.Details,
			})
		}
		return nil, out, nil
	})
}

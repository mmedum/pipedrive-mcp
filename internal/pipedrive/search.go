package pipedrive

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// SearchHit is one record returned by /api/v2/itemSearch. Pipedrive's
// raw response wraps the per-type record under `item` with shape
// varying by type (`title` vs `name`, type-specific extras); we
// flatten that into a uniform shape: id + type + a unified `Name`
// field (uses `title` for deals, `name` for everything else) +
// the relevance score, and pass through the remaining fields as
// Details so the LLM doesn't lose anything potentially useful.
type SearchHit struct {
	ID      int64          `json:"id"`
	Type    string         `json:"type"`
	Name    string         `json:"name"`
	Score   float64        `json:"score"`
	Details map[string]any `json:"details,omitempty"`
}

// SearchOptions configures /itemSearch. Term is required and must be
// at least 2 characters (1 if ExactMatch is set). Empty ItemTypes
// means "search all of deal | person | organization | product | file
// | lead". Limit clamped by the tool layer.
type SearchOptions struct {
	Term       string
	ItemTypes  []string
	ExactMatch bool
	Limit      int
	Cursor     string
}

type itemSearchResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Items []struct {
			ResultScore float64        `json:"result_score"`
			Item        map[string]any `json:"item"`
		} `json:"items"`
	} `json:"data"`
	AdditionalData AdditionalData `json:"additional_data"`
}

// ItemSearch issues a free-text search across the requested item
// types. Returns the page plus the next cursor (empty string = end).
// Pipedrive caps responses at 100; the caller should surface the
// `truncated` signal to the LLM (next_cursor non-empty OR len(hits)
// equal to the requested limit) so a silent undercount can't slip
// through.
func (c *Client) ItemSearch(ctx context.Context, opts SearchOptions) ([]SearchHit, string, error) {
	q := url.Values{}
	q.Set("term", opts.Term)
	if len(opts.ItemTypes) > 0 {
		q.Set("item_types", strings.Join(opts.ItemTypes, ","))
	}
	if opts.ExactMatch {
		q.Set("exact_match", "true")
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Cursor != "" {
		q.Set("cursor", opts.Cursor)
	}

	var resp itemSearchResponse
	if err := c.do(ctx, buildPath("/itemSearch", q), &resp); err != nil {
		return nil, "", err
	}

	hits := make([]SearchHit, 0, len(resp.Data.Items))
	for _, it := range resp.Data.Items {
		hits = append(hits, flattenSearchItem(it.ResultScore, it.Item))
	}
	return hits, resp.AdditionalData.NextCursor, nil
}

func flattenSearchItem(score float64, item map[string]any) SearchHit {
	h := SearchHit{Score: score}
	if t, ok := item["type"].(string); ok {
		h.Type = t
	}
	if id, ok := item["id"].(float64); ok {
		h.ID = int64(id)
	}
	// Deals use `title` as the human-facing name; every other
	// resource type uses `name`.
	if h.Type == "deal" {
		if title, ok := item["title"].(string); ok {
			h.Name = title
		}
	} else if name, ok := item["name"].(string); ok {
		h.Name = name
	}
	// Pass through every other field as Details so the LLM keeps
	// access to org city/country, person email/phone, deal value,
	// etc., without us having to enumerate per-type schemas.
	details := make(map[string]any, len(item))
	for k, v := range item {
		switch k {
		case "id", "type", "name", "title":
			continue
		default:
			details[k] = v
		}
	}
	if len(details) > 0 {
		h.Details = details
	}
	return h
}

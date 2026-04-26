package pipedrive

import (
	"context"
	"net/url"
	"strings"
)

// ItemType is the closed set of item types Pipedrive's /itemSearch
// endpoint accepts. The constants are reused by the tools package
// for input validation so the enum has a single source of truth.
type ItemType string

// ItemType values matching Pipedrive's /itemSearch `item_types` enum.
const (
	ItemTypeDeal         ItemType = "deal"
	ItemTypePerson       ItemType = "person"
	ItemTypeOrganization ItemType = "organization"
	ItemTypeProduct      ItemType = "product"
	ItemTypeFile         ItemType = "file"
	ItemTypeLead         ItemType = "lead"
)

// SearchHit is one record returned by /api/v2/itemSearch. The pipedrive
// layer keeps the per-type record as a raw map so the tools layer can
// decide how to render it for the LLM (deal `title` vs person `name`,
// strip-list of internal fields, etc.) without leaking presentation
// concerns into the HTTP layer.
type SearchHit struct {
	Score float64
	Item  map[string]any
}

// SearchOptions filters an /itemSearch call. Term is required and
// must be at least 2 characters (1 if ExactMatch). Empty ItemTypes
// means "search all of deal | person | organization | product | file
// | lead". Limit is clamped by the tool layer.
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
// types. NextCursor is the authoritative "more results upstream"
// signal; an empty string means the page is the last. Tool callers
// must surface that to the LLM (see internal/tools/search.go).
func (c *Client) ItemSearch(ctx context.Context, opts SearchOptions) ([]SearchHit, string, error) {
	q := url.Values{}
	q.Set("term", opts.Term)
	if len(opts.ItemTypes) > 0 {
		q.Set("item_types", strings.Join(opts.ItemTypes, ","))
	}
	if opts.ExactMatch {
		q.Set("exact_match", "true")
	}
	setLimitCursor(q, opts.Limit, opts.Cursor)

	var resp itemSearchResponse
	if err := c.do(ctx, buildPath("/itemSearch", q), &resp); err != nil {
		return nil, "", err
	}
	hits := make([]SearchHit, 0, len(resp.Data.Items))
	for _, it := range resp.Data.Items {
		hits = append(hits, SearchHit{Score: it.ResultScore, Item: it.Item})
	}
	return hits, resp.AdditionalData.NextCursor, nil
}

// ItemInt64 reads a numeric field from a SearchHit's Item map,
// tolerating both the float64 form Go's stdlib JSON decoder produces
// for arbitrary numbers and any direct int64 / int forms a future
// caller might pass through. Returns 0 for missing or non-numeric
// values; callers that need to distinguish "missing" from "zero"
// should check key existence themselves.
func ItemInt64(item map[string]any, key string) int64 {
	switch v := item[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

// ItemString reads a string field from a SearchHit's Item map.
// Returns "" for missing or non-string values.
func ItemString(item map[string]any, key string) string {
	if s, ok := item[key].(string); ok {
		return s
	}
	return ""
}

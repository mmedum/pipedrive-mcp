package pipedrive

import (
	"context"
	"net/url"
	"strconv"
)

// ListDealsOptions filters a /deals list call. Zero values mean "no
// filter on this dimension". Limit is clamped to [1, 500] by Pipedrive;
// the tool layer applies its own (smaller) cap before calling here.
type ListDealsOptions struct {
	Status        string // open | won | lost | deleted | all_not_deleted
	PipelineID    int64
	StageID       int64
	OwnerID       int64
	PersonID      int64
	OrgID         int64
	UpdatedSince  string // RFC3339-ish, Pipedrive's "2026-04-26 10:00:00" form
	UpdatedUntil  string
	Limit         int
	Cursor        string // opaque pagination token from a previous response
	IncludeCustom bool   // include custom_fields in the response
}

type dealResponse struct {
	Success bool `json:"success"`
	Data    Deal `json:"data"`
}

type dealsResponse struct {
	Success        bool           `json:"success"`
	Data           []Deal         `json:"data"`
	AdditionalData AdditionalData `json:"additional_data"`
}

// GetDeal fetches a single deal by ID. custom_fields are nested under
// the deal's `custom_fields` object per Pipedrive v2 — caller resolves
// hash keys to names via the per-Client FieldCache.
func (c *Client) GetDeal(ctx context.Context, id int64) (*Deal, error) {
	q := url.Values{}
	q.Set("include_fields", "custom_fields")
	var resp dealResponse
	if err := c.do(ctx, buildPath("/deals/"+strconv.FormatInt(id, 10), q), &resp); err != nil {
		return nil, err
	}
	d := resp.Data
	return &d, nil
}

// ListDeals issues a /deals list with the given filters. Returns the
// page plus the next cursor (empty string = end of results). Cursor
// pagination is opaque; callers pass whatever NextCursor was returned
// on the prior page.
func (c *Client) ListDeals(ctx context.Context, opts ListDealsOptions) ([]Deal, string, error) {
	q := url.Values{}
	if opts.Status != "" {
		q.Set("status", opts.Status)
	}
	if opts.PipelineID > 0 {
		q.Set("pipeline_id", strconv.FormatInt(opts.PipelineID, 10))
	}
	if opts.StageID > 0 {
		q.Set("stage_id", strconv.FormatInt(opts.StageID, 10))
	}
	if opts.OwnerID > 0 {
		q.Set("owner_id", strconv.FormatInt(opts.OwnerID, 10))
	}
	if opts.PersonID > 0 {
		q.Set("person_id", strconv.FormatInt(opts.PersonID, 10))
	}
	if opts.OrgID > 0 {
		q.Set("org_id", strconv.FormatInt(opts.OrgID, 10))
	}
	if opts.UpdatedSince != "" {
		q.Set("updated_since", opts.UpdatedSince)
	}
	if opts.UpdatedUntil != "" {
		q.Set("updated_until", opts.UpdatedUntil)
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Cursor != "" {
		q.Set("cursor", opts.Cursor)
	}
	if opts.IncludeCustom {
		q.Set("include_fields", "custom_fields")
	}

	var resp dealsResponse
	if err := c.do(ctx, buildPath("/deals", q), &resp); err != nil {
		return nil, "", err
	}
	return resp.Data, resp.AdditionalData.NextCursor, nil
}

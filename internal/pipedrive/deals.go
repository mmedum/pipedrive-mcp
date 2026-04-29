package pipedrive

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// ListDealsOptions filters a /deals list call. Zero values mean "no
// filter on this dimension". Limit is clamped to [1, 500] by Pipedrive;
// the tool layer applies its own (smaller) cap before calling here.
//
// Pipedrive v2 returns custom_fields nested in every deal record by
// default — there is no opt-in query parameter, and supplying
// `include_fields=custom_fields` is rejected with a 400.
type ListDealsOptions struct {
	Status        string // open | won | lost | deleted
	PipelineID    int64
	StageID       int64
	OwnerID       int64
	PersonID      int64
	OrgID         int64
	UpdatedSince  string // RFC3339
	UpdatedUntil  string // RFC3339
	SortBy        string // id | update_time | add_time
	SortDirection string // asc | desc
	Limit         int
	Cursor        string // opaque pagination token from a previous response
}

// CreateDealRequest is the JSON body for POST /api/v2/deals. v2
// requires `title` only; everything else has Pipedrive-side defaults
// (currency = workspace default, value = 0, owner_id = the API
// token's user, status = open, stage_id = first stage of the default
// pipeline, ...). The tool layer enforces title non-empty client-side
// so a typo surfaces as [validation] instead of an upstream 400.
//
// Custom fields are intentionally omitted from this v0 — writing
// them needs the inverse name→hash resolver on FieldCache, which is
// a separate slice. Callers wanting to set custom fields today can
// edit the deal in the Pipedrive UI after creation.
type CreateDealRequest struct {
	Title             string  `json:"title"`
	Value             float64 `json:"value,omitempty"`
	Currency          string  `json:"currency,omitempty"`
	PipelineID        int64   `json:"pipeline_id,omitempty"`
	StageID           int64   `json:"stage_id,omitempty"`
	OwnerID           int64   `json:"owner_id,omitempty"`
	PersonID          int64   `json:"person_id,omitempty"`
	OrgID             int64   `json:"org_id,omitempty"`
	ExpectedCloseDate string  `json:"expected_close_date,omitempty"` // YYYY-MM-DD
	Probability       *int    `json:"probability,omitempty"`         // 0-100; nil = use stage default
}

// GetDeal fetches a single deal by ID. custom_fields are nested under
// the deal's `custom_fields` object per Pipedrive v2 — caller resolves
// hash keys to names via the per-Client FieldCache.
func (c *Client) GetDeal(ctx context.Context, id int64) (*Deal, error) {
	var resp itemEnvelope[Deal]
	if err := c.do(ctx, "/deals/"+strconv.FormatInt(id, 10), &resp); err != nil {
		return nil, err
	}
	d := resp.Data
	return &d, nil
}

// CreateDeal posts a new deal via /api/v2/deals. Returns the created
// deal as Pipedrive echoes it (full record with id, defaults
// resolved, and custom_fields nested as usual).
func (c *Client) CreateDeal(ctx context.Context, req CreateDealRequest) (*Deal, error) {
	if req.Title == "" {
		return nil, fmt.Errorf("%w: title must not be empty", ErrValidation)
	}
	var resp itemEnvelope[Deal]
	if err := c.postV2(ctx, "/deals", req, &resp); err != nil {
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
	if opts.SortBy != "" {
		q.Set("sort_by", opts.SortBy)
	}
	if opts.SortDirection != "" {
		q.Set("sort_direction", opts.SortDirection)
	}
	setLimitCursor(q, opts.Limit, opts.Cursor)

	var resp listEnvelope[Deal]
	if err := c.do(ctx, buildPath("/deals", q), &resp); err != nil {
		return nil, "", err
	}
	return resp.Data, resp.AdditionalData.NextCursor, nil
}

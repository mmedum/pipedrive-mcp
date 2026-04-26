package pipedrive

import (
	"context"
	"net/url"
	"strconv"
)

// ListActivitiesOptions filters a /activities list call. Zero values
// mean "no filter on this dimension".
//
// v2 dropped the v1 `type` and `due_date` query params (filtering by
// activity-type or absolute due-date is not supported on the list
// endpoint — callers can sort by due_date instead). v2 list params are
// drawn from Pipedrive's official spec: filter_id, owner_id, deal_id,
// lead_id, person_id, org_id, done, updated_since, updated_until,
// sort_by, sort_direction, include_fields, limit, cursor.
//
// Done is a tri-state: nil = no filter (the default), &true = only
// completed, &false = only open. The tool layer maps a human-friendly
// `status: open|done|all` enum to this field.
//
// IncludeAttendees toggles `include_fields=attendees`; on by default
// when the consumer wants the attendees array populated. Pipedrive
// returns it only when explicitly requested.
type ListActivitiesOptions struct {
	OwnerID          int64
	DealID           int64
	LeadID           string
	PersonID         int64
	OrgID            int64
	Done             *bool
	UpdatedSince     string // RFC3339, e.g. 2026-01-01T00:00:00Z
	UpdatedUntil     string // RFC3339
	SortBy           string // id | update_time | add_time | due_date
	SortDirection    string // asc | desc
	IncludeAttendees bool
	Limit            int
	Cursor           string
}

// GetActivityOptions controls the per-request shape of GetActivity.
// IncludeAttendees toggles `include_fields=attendees`. Future opt-ins
// (additional `include_fields` values Pipedrive may add) extend this
// struct without breaking the call signature.
type GetActivityOptions struct {
	IncludeAttendees bool
}

// GetActivity fetches a single activity by ID.
func (c *Client) GetActivity(ctx context.Context, id int64, opts GetActivityOptions) (*Activity, error) {
	q := url.Values{}
	if opts.IncludeAttendees {
		q.Set("include_fields", "attendees")
	}
	var resp itemEnvelope[Activity]
	if err := c.do(ctx, buildPath("/activities/"+strconv.FormatInt(id, 10), q), &resp); err != nil {
		return nil, err
	}
	a := resp.Data
	return &a, nil
}

// ListActivities issues a /activities list with the given filters.
// Returns the page plus the next cursor (empty string = end of
// results). Cursor pagination is opaque; callers pass whatever
// NextCursor was returned on the prior page.
func (c *Client) ListActivities(ctx context.Context, opts ListActivitiesOptions) ([]Activity, string, error) {
	q := url.Values{}
	if opts.OwnerID > 0 {
		q.Set("owner_id", strconv.FormatInt(opts.OwnerID, 10))
	}
	if opts.DealID > 0 {
		q.Set("deal_id", strconv.FormatInt(opts.DealID, 10))
	}
	if opts.LeadID != "" {
		q.Set("lead_id", opts.LeadID)
	}
	if opts.PersonID > 0 {
		q.Set("person_id", strconv.FormatInt(opts.PersonID, 10))
	}
	if opts.OrgID > 0 {
		q.Set("org_id", strconv.FormatInt(opts.OrgID, 10))
	}
	if opts.Done != nil {
		q.Set("done", strconv.FormatBool(*opts.Done))
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
	if opts.IncludeAttendees {
		q.Set("include_fields", "attendees")
	}
	setLimitCursor(q, opts.Limit, opts.Cursor)

	var resp listEnvelope[Activity]
	if err := c.do(ctx, buildPath("/activities", q), &resp); err != nil {
		return nil, "", err
	}
	return resp.Data, resp.AdditionalData.NextCursor, nil
}

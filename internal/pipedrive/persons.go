package pipedrive

import (
	"context"
	"net/url"
	"strconv"
)

// ListPersonsOptions filters a /persons list call. Zero values mean
// "no filter on this dimension". v2 supports more params (filter_id,
// ids, deal_id, include_fields, custom_fields) — wire only what the
// LLM-facing tools actually expose to keep the surface narrow.
type ListPersonsOptions struct {
	OwnerID       int64
	OrgID         int64
	UpdatedSince  string // RFC3339
	UpdatedUntil  string // RFC3339
	SortBy        string // id | update_time | add_time
	SortDirection string // asc | desc
	Limit         int
	Cursor        string
}

// GetPerson fetches a single person by ID. custom_fields are nested
// under the person's `custom_fields` object per Pipedrive v2 — caller
// resolves hash keys to names via the per-Client FieldCache.
func (c *Client) GetPerson(ctx context.Context, id int64) (*Person, error) {
	var resp itemEnvelope[Person]
	if err := c.do(ctx, "/persons/"+strconv.FormatInt(id, 10), &resp); err != nil {
		return nil, err
	}
	p := resp.Data
	return &p, nil
}

// ListPersons issues a /persons list with the given filters. Returns
// the page plus the next cursor (empty string = end of results).
func (c *Client) ListPersons(ctx context.Context, opts ListPersonsOptions) ([]Person, string, error) {
	q := url.Values{}
	if opts.OwnerID > 0 {
		q.Set("owner_id", strconv.FormatInt(opts.OwnerID, 10))
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

	var resp listEnvelope[Person]
	if err := c.do(ctx, buildPath("/persons", q), &resp); err != nil {
		return nil, "", err
	}
	return resp.Data, resp.AdditionalData.NextCursor, nil
}

// ListPersonFields returns the field metadata for persons. Used by
// the per-Client person field cache.
func (c *Client) ListPersonFields(ctx context.Context) ([]Field, error) {
	var resp listEnvelope[Field]
	if err := c.do(ctx, "/personFields", &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// ResolvePersonCustomFields returns a copy of raw with hash keys
// replaced by their human-readable names. Sole entry point tool
// packages need; the underlying cache is unexported.
func (c *Client) ResolvePersonCustomFields(ctx context.Context, raw map[string]any) map[string]any {
	return c.personFields.Resolve(ctx, raw)
}

// WarmPersonFields eagerly triggers the cache load.
func (c *Client) WarmPersonFields(ctx context.Context) {
	_ = c.personFields.Load(ctx)
}

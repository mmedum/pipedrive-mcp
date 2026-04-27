package pipedrive

import (
	"context"
	"net/url"
	"strconv"
)

// ListOrganizationsOptions filters a /organizations list call. v2's
// /organizations does not accept owner-of-deals or person-count
// filters; the available list-time dimensions are owner + update
// window. Zero values mean "no filter on this dimension".
type ListOrganizationsOptions struct {
	OwnerID       int64
	UpdatedSince  string // RFC3339
	UpdatedUntil  string // RFC3339
	SortBy        string // id | update_time | add_time
	SortDirection string // asc | desc
	Limit         int
	Cursor        string
}

// GetOrganization fetches a single organization by ID. custom_fields
// are nested under the org's `custom_fields` object per Pipedrive v2 —
// caller resolves hash keys to names via the per-Client FieldCache.
func (c *Client) GetOrganization(ctx context.Context, id int64) (*Organization, error) {
	var resp itemEnvelope[Organization]
	if err := c.do(ctx, "/organizations/"+strconv.FormatInt(id, 10), &resp); err != nil {
		return nil, err
	}
	o := resp.Data
	return &o, nil
}

// ListOrganizations issues a /organizations list with the given
// filters. Returns the page plus the next cursor (empty string = end).
func (c *Client) ListOrganizations(ctx context.Context, opts ListOrganizationsOptions) ([]Organization, string, error) {
	q := url.Values{}
	if opts.OwnerID > 0 {
		q.Set("owner_id", strconv.FormatInt(opts.OwnerID, 10))
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

	var resp listEnvelope[Organization]
	if err := c.do(ctx, buildPath("/organizations", q), &resp); err != nil {
		return nil, "", err
	}
	return resp.Data, resp.AdditionalData.NextCursor, nil
}

// ListOrganizationFields returns the field metadata for organizations.
func (c *Client) ListOrganizationFields(ctx context.Context) ([]Field, error) {
	var resp listEnvelope[Field]
	if err := c.do(ctx, "/organizationFields", &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// ResolveOrganizationCustomFields delegates to the per-Client org
// field cache.
func (c *Client) ResolveOrganizationCustomFields(ctx context.Context, raw map[string]any) map[string]any {
	return c.organizationFields.Resolve(ctx, raw)
}

// WarmOrganizationFields eagerly triggers the cache load.
func (c *Client) WarmOrganizationFields(ctx context.Context) {
	_ = c.organizationFields.Load(ctx)
}

package pipedrive

import (
	"context"
	"fmt"
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

// CreateOrganizationRequest is the JSON body for POST
// /api/v2/organizations. v2 requires `name`. Address is a single-line
// string on input — Pipedrive parses it into the structured response
// shape (Address.Country / Locality / PostalCode) server-side.
type CreateOrganizationRequest struct {
	Name    string `json:"name"`
	OwnerID int64  `json:"owner_id,omitempty"`
	Address string `json:"address,omitempty"` // single-line; server-parsed
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

// CreateOrganization posts a new organization via /api/v2/organizations.
// Returns the created org as Pipedrive echoes it (full record with
// id, structured Address parsed server-side, and custom_fields).
func (c *Client) CreateOrganization(ctx context.Context, req CreateOrganizationRequest) (*Organization, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("%w: name must not be empty", ErrValidation)
	}
	var resp itemEnvelope[Organization]
	if err := c.postV2(ctx, "/organizations", req, &resp); err != nil {
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

// ReloadOrganizationFields clears and re-fetches the organization-field
// cache, returning the count of fields now cached. See ReloadDealFields
// for the rationale.
func (c *Client) ReloadOrganizationFields(ctx context.Context) (int, error) {
	c.organizationFields.Reload()
	if err := c.organizationFields.Load(ctx); err != nil {
		return 0, err
	}
	return c.organizationFields.Count(), nil
}

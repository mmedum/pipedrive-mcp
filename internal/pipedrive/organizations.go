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

	// CustomFields is what FieldCache.Encode produces; see
	// CustomFieldWrite.Values for the shape. Nil omits the object,
	// leaving every custom field as it is.
	CustomFields map[string]any `json:"custom_fields,omitempty"`
}

// UpdateOrganizationRequest is the JSON body for
// PATCH /api/v2/organizations/{id}. Pointers so nil omits the field and
// it keeps its stored value. Clearing a field is not supported — see
// UpdateDealRequest.
type UpdateOrganizationRequest struct {
	Name    *string `json:"name,omitempty"`
	OwnerID *int64  `json:"owner_id,omitempty"`
	Address *string `json:"address,omitempty"` // single-line; server-parsed

	// CustomFields is what FieldCache.Encode produces; see
	// CustomFieldWrite.Values for the shape. Nil omits the object,
	// leaving every custom field as it is.
	CustomFields map[string]any `json:"custom_fields,omitempty"`
}

// UpdateOrganization edits an organization via
// PATCH /api/v2/organizations/{id} and returns the record Pipedrive
// echoes back, with Address re-parsed server-side.
func (c *Client) UpdateOrganization(ctx context.Context, id int64, req UpdateOrganizationRequest) (*Organization, error) {
	var resp itemEnvelope[Organization]
	if err := c.patchV2(ctx, "/organizations/"+strconv.FormatInt(id, 10), req, &resp); err != nil {
		return nil, err
	}
	o := resp.Data
	return &o, nil
}

// GetOrganization fetches a single organization by ID. custom_fields
// are nested under the org's `custom_fields` object per Pipedrive v2 —
// caller resolves hash keys to names via the per-Client FieldCache.
//
// Organization does not decode is_deleted, which the v2 response
// declares, so an organization read here never shows as deleted. What
// Pipedrive answers for a deleted one is unchecked: no test reads one.
// https://developers.pipedrive.com/docs/api/v1/Organizations#getOrganization
func (c *Client) GetOrganization(ctx context.Context, id int64) (*Organization, error) {
	q := url.Values{}
	q.Set("include_fields", orgIncludeFields)
	var resp itemEnvelope[Organization]
	if err := c.do(ctx, buildPath("/organizations/"+strconv.FormatInt(id, 10), q), &resp); err != nil {
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
	q.Set("include_fields", orgIncludeFields)
	setLimitCursor(q, opts.Limit, opts.Cursor)

	var resp listEnvelope[Organization]
	if err := c.do(ctx, buildPath("/organizations", q), &resp); err != nil {
		return nil, "", err
	}
	return resp.Data, resp.AdditionalData.NextCursor, nil
}

// orgIncludeFields is the include_fields value every organization read
// sends. people_count is NOT in v2's default organization response: it
// is one value of the include_fields enum, and a read that does not ask
// for it gets a row without the key. No integration test reads it.
// https://developers.pipedrive.com/docs/api/v1/Organizations#getOrganizations
const orgIncludeFields = "people_count"

// ListOrganizationFields returns the field metadata for organizations.
// It reads one page, as ListDealFields does.
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

// EncodeOrganizationCustomFields turns a caller's custom-field map — workspace
// names, dropdown labels — into the hash keys and option ids a write
// body carries. What it refuses, and how, is FieldCache.Encode's to
// say — do not restate it here.
func (c *Client) EncodeOrganizationCustomFields(ctx context.Context, in map[string]any) (CustomFieldWrite, error) {
	return c.organizationFields.Encode(ctx, in)
}

// WarmOrganizationFields eagerly triggers the cache load.
func (c *Client) WarmOrganizationFields(ctx context.Context) {
	_ = c.organizationFields.Load(ctx)
}

// ReloadOrganizationFields clears and re-fetches the organization-field
// cache, returning the count of fields cached after the reload, as
// ReloadDealFields does for deals.
func (c *Client) ReloadOrganizationFields(ctx context.Context) (int, error) {
	c.organizationFields.Reload()
	if err := c.organizationFields.Load(ctx); err != nil {
		return 0, err
	}
	return c.organizationFields.Count(), nil
}

// DeleteOrganization marks an organization as deleted, softly, as
// DeleteDeal does a deal: Pipedrive purges it after 30 days.
// https://developers.pipedrive.com/docs/api/v1/Organizations#deleteOrganization
func (c *Client) DeleteOrganization(ctx context.Context, id int64) error {
	return c.deleteV2(ctx, "/organizations/"+strconv.FormatInt(id, 10))
}

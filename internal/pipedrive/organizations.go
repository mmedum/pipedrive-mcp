package pipedrive

import (
	"context"
	"strconv"
)

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

// ReloadOrganizationFields clears the cache so the next access refetches.
func (c *Client) ReloadOrganizationFields() {
	c.organizationFields.Reload()
}

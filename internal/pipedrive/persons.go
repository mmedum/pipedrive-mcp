package pipedrive

import (
	"context"
	"strconv"
)

type personResponse struct {
	Success bool   `json:"success"`
	Data    Person `json:"data"`
}

type personFieldsResponse struct {
	Success bool    `json:"success"`
	Data    []Field `json:"data"`
}

// GetPerson fetches a single person by ID. custom_fields are nested
// under the person's `custom_fields` object per Pipedrive v2 — caller
// resolves hash keys to names via the per-Client FieldCache.
func (c *Client) GetPerson(ctx context.Context, id int64) (*Person, error) {
	var resp personResponse
	if err := c.do(ctx, "/persons/"+strconv.FormatInt(id, 10), &resp); err != nil {
		return nil, err
	}
	p := resp.Data
	return &p, nil
}

// ListPersonFields returns the field metadata for persons. Used by
// the per-Client person field cache. Unpaginated and bounded.
func (c *Client) ListPersonFields(ctx context.Context) ([]Field, error) {
	var resp personFieldsResponse
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

// WarmPersonFields eagerly triggers the person-field cache load so
// the first user-facing get_person call doesn't pay the
// /personFields round-trip on the critical path.
func (c *Client) WarmPersonFields(ctx context.Context) {
	_ = c.personFields.Load(ctx)
}

// ReloadPersonFields clears the cache so the next access refetches.
// Hook for the Phase 1.8 refresh_field_cache tool.
func (c *Client) ReloadPersonFields() {
	c.personFields.Reload()
}

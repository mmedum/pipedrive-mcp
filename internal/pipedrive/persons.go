package pipedrive

import (
	"context"
	"strconv"
)

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

// ReloadPersonFields clears the cache so the next access refetches.
func (c *Client) ReloadPersonFields() {
	c.personFields.Reload()
}

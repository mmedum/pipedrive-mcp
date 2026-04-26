package pipedrive

import "context"

type dealFieldsResponse struct {
	Success bool    `json:"success"`
	Data    []Field `json:"data"`
}

// ListDealFields returns the field metadata for deals. Used by the
// auth probe (limit=1 form is in probe.go) and by the per-Client
// deal field cache. The full call is unpaginated and bounded — a
// workspace can have hundreds of custom fields but never enough to
// matter for memory.
func (c *Client) ListDealFields(ctx context.Context) ([]Field, error) {
	var resp dealFieldsResponse
	if err := c.do(ctx, "/dealFields", &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// ResolveDealCustomFields returns a copy of raw with hash keys
// replaced by their human-readable names. Sole entry point tool
// packages need; the underlying cache is unexported.
func (c *Client) ResolveDealCustomFields(ctx context.Context, raw map[string]any) map[string]any {
	return c.dealFields.Resolve(ctx, raw)
}

// WarmDealFields eagerly triggers the deal-field cache load so the
// first user-facing get_deal/list_deals call doesn't pay the
// /dealFields round-trip on the critical path. Errors are silently
// swallowed: a failed warm-up just means the first real call pays
// the latency, exactly as the lazy path would.
func (c *Client) WarmDealFields(ctx context.Context) {
	_ = c.dealFields.Load(ctx)
}

// ReloadDealFields clears the cache so the next access refetches.
// Hook for the Phase 1.9 refresh_field_cache tool.
func (c *Client) ReloadDealFields() {
	c.dealFields.Reload()
}

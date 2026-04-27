package pipedrive

import "context"

// ListDealFields returns the field metadata for deals. Used by the
// auth probe (limit=1 form is in probe.go) and by the per-Client
// deal field cache.
func (c *Client) ListDealFields(ctx context.Context) ([]Field, error) {
	var resp listEnvelope[Field]
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

// ReloadDealFields clears and re-fetches the deal-field cache,
// returning the count of fields now cached. Used by the
// refresh_field_cache tool to pick up custom-field renames or
// additions without restarting the server. Reload is followed by an
// eager Load so the next get_deal/list_deals call sees fresh data
// without paying the round-trip itself.
func (c *Client) ReloadDealFields(ctx context.Context) (int, error) {
	c.dealFields.Reload()
	if err := c.dealFields.Load(ctx); err != nil {
		return 0, err
	}
	return c.dealFields.Count(), nil
}

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

// ResolveDealCustomFields delegates to the per-Client deal field
// cache, returning a copy of raw with hash keys replaced by names.
// Convenience adapter so tool packages can depend on a single
// dealsClient interface that also covers GetDeal/ListDeals, instead
// of plumbing the cache through separately.
func (c *Client) ResolveDealCustomFields(ctx context.Context, raw map[string]any) map[string]any {
	return c.DealFields.Resolve(ctx, raw)
}

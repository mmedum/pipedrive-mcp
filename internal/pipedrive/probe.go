package pipedrive

import "context"

// ProbeAuth confirms the API token is valid for the workspace by
// hitting GET /api/v2/dealFields?limit=1. That endpoint is chosen
// because it is confirmed to exist on Pipedrive API v2 (/api/v2/users
// does not), every workspace has at least one deal field, and the
// payload is tiny.
func (c *Client) ProbeAuth(ctx context.Context) error {
	return c.do(ctx, apiV2, "GET", "/dealFields?limit=1", nil, nil)
}

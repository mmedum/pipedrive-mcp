package pipedrive

import "context"

// ProbeAuth confirms the API token is valid for the workspace with
// GET /api/v2/dealFields?limit=1. The endpoint exists on v2, where
// /api/v2/users does not; every workspace has a deal field, and the
// answer is tiny. Checked by the integration test
// TestProbeAuth_SucceedsAgainstTheWorkspace.
// https://developers.pipedrive.com/docs/api/v1/DealFields#getDealFields
func (c *Client) ProbeAuth(ctx context.Context) error {
	return c.do(ctx, "/dealFields?limit=1", nil)
}

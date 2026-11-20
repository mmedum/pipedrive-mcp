package pipedrive

import "context"

// ListPipelines returns every pipeline the API token's user can see.
// https://developers.pipedrive.com/docs/api/v1/Pipelines#getPipelines
func (c *Client) ListPipelines(ctx context.Context) ([]Pipeline, error) {
	return readAll[Pipeline](ctx, c, "/pipelines", nil)
}

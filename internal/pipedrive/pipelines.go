package pipedrive

import "context"

// ListPipelines returns the pipelines the API token's user can see. It
// reads one page, Pipedrive's default of 100 pipelines, and does not
// follow next_cursor, on the assumption that a workspace rarely has more
// than twenty.
// https://developers.pipedrive.com/docs/api/v1/Pipelines#getPipelines
func (c *Client) ListPipelines(ctx context.Context) ([]Pipeline, error) {
	var resp listEnvelope[Pipeline]
	if err := c.do(ctx, "/pipelines", &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

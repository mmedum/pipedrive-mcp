package pipedrive

import "context"

type pipelinesResponse struct {
	Success bool       `json:"success"`
	Data    []Pipeline `json:"data"`
}

// ListPipelines returns every pipeline the API token's user can see.
// Pipedrive workspaces typically have a small handful (≤ 20), so no
// pagination wrapper is needed at this scale; if a workspace ever
// exceeds the response cap, we'll add cursor support here.
func (c *Client) ListPipelines(ctx context.Context) ([]Pipeline, error) {
	var resp pipelinesResponse
	if err := c.do(ctx, "/pipelines", &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

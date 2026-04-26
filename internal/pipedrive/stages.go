package pipedrive

import (
	"context"
	"fmt"
)

type stagesResponse struct {
	Success bool    `json:"success"`
	Data    []Stage `json:"data"`
}

// ListStages returns the stages, optionally filtered to one pipeline.
// Pass pipelineID = 0 to return stages across every pipeline.
func (c *Client) ListStages(ctx context.Context, pipelineID int64) ([]Stage, error) {
	path := "/stages"
	if pipelineID > 0 {
		path = fmt.Sprintf("/stages?pipeline_id=%d", pipelineID)
	}
	var resp stagesResponse
	if err := c.do(ctx, path, &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

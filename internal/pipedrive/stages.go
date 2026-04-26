package pipedrive

import (
	"context"
	"net/url"
	"strconv"
)

// ListStages returns the stages, optionally filtered to one pipeline.
// Pass pipelineID = 0 to return stages across every pipeline.
func (c *Client) ListStages(ctx context.Context, pipelineID int64) ([]Stage, error) {
	q := url.Values{}
	if pipelineID > 0 {
		q.Set("pipeline_id", strconv.FormatInt(pipelineID, 10))
	}
	var resp listEnvelope[Stage]
	if err := c.do(ctx, buildPath("/stages", q), &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

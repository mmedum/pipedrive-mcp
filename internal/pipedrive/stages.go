package pipedrive

import (
	"context"
	"net/url"
	"strconv"
)

// ListStages returns the stages, optionally filtered to one pipeline;
// pipelineID 0 returns every pipeline's. /stages honors pipeline_id, as
// the integration test TestListStages_BelongToThePipelineAsked checks.
// It reads one page, as ListPipelines does.
// https://developers.pipedrive.com/docs/api/v1/Stages#getStages
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

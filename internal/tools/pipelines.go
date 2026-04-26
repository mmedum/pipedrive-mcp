package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

// pipelinesClient is the subset of *pipedrive.Client these tools call.
// Defining it as an interface keeps tool-handler tests independent of
// the real HTTP client (tests pass a fake).
type pipelinesClient interface {
	ListPipelines(ctx context.Context) ([]pipedrive.Pipeline, error)
	// ListStages returns stages, optionally filtered to a single pipeline.
	// pipelineID == 0 is the "all pipelines" sentinel — Pipedrive's
	// /api/v2/stages endpoint omits the query param entirely in that case.
	ListStages(ctx context.Context, pipelineID int64) ([]pipedrive.Stage, error)
}

type pipelineSummary struct {
	ID      int64  `json:"id" jsonschema:"the pipeline's numeric id"`
	Name    string `json:"name" jsonschema:"human-readable pipeline name"`
	OrderNr int    `json:"order_nr" jsonschema:"display order; lower numbers come first in Pipedrive's UI"`
	Active  bool   `json:"active" jsonschema:"true if the pipeline is currently active"`
	URL     string `json:"url" jsonschema:"link to the pipeline in the Pipedrive web UI"`
}

type stageSummary struct {
	ID              int64  `json:"id" jsonschema:"the stage's numeric id"`
	Name            string `json:"name" jsonschema:"human-readable stage name"`
	OrderNr         int    `json:"order_nr" jsonschema:"display order within the pipeline; lower comes first"`
	Active          bool   `json:"active" jsonschema:"true if the stage is currently active"`
	PipelineID      int64  `json:"pipeline_id" jsonschema:"id of the pipeline this stage belongs to"`
	DealProbability int    `json:"deal_probability" jsonschema:"Pipedrive's default deal-probability for this stage (0-100)"`
}

// listPipelinesInput has no fields — the tool returns every pipeline
// the API token's user can see. Defined as an empty struct so the
// SDK generates a schema with no required parameters.
type listPipelinesInput struct{}

type listPipelinesOutput struct {
	Pipelines []pipelineSummary `json:"pipelines" jsonschema:"every pipeline the API token's user can see"`
}

type listStagesInput struct {
	PipelineID int64 `json:"pipeline_id,omitempty" jsonschema:"optional: when set, return only stages in this pipeline. Omit (or pass 0) to return stages across every pipeline."`
}

type listStagesOutput struct {
	Stages []stageSummary `json:"stages" jsonschema:"matching stages, sorted by Pipedrive's order_nr within each pipeline"`
}

func pipelineVisible(pipes []pipedrive.Pipeline, id int64) bool {
	for _, p := range pipes {
		if p.ID == id {
			return true
		}
	}
	return false
}

func pipelineNotFound(id int64) *pipedrive.APIError {
	return &pipedrive.APIError{
		Class:    pipedrive.ErrNotFound,
		Status:   404,
		Message:  fmt.Sprintf("pipeline %d does not exist or is not visible to the API token's user", id),
		Endpoint: "/api/v2/pipelines",
	}
}

// RegisterPipelines wires list_pipelines and list_stages into the MCP
// server. The companyDomain is needed for URL injection on outputs;
// pass cfg.CompanyDomain from the server constructor.
func RegisterPipelines(s *mcp.Server, c pipelinesClient, companyDomain string) {
	readOnly := mcp.ToolAnnotations{ReadOnlyHint: true}

	AddTool(s, &mcp.Tool{
		Name:        "list_pipelines",
		Description: "List every Pipedrive pipeline the API token's user can see. Returns id, name, display order, active flag, and a URL to the Pipedrive UI for each. No filtering or pagination — Pipedrive workspaces typically have under 20 pipelines total.",
		Annotations: &readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ listPipelinesInput) (*mcp.CallToolResult, listPipelinesOutput, error) {
		got, err := c.ListPipelines(ctx)
		if err != nil {
			return errorResult(err), listPipelinesOutput{}, nil
		}
		out := listPipelinesOutput{Pipelines: make([]pipelineSummary, 0, len(got))}
		for _, p := range got {
			out.Pipelines = append(out.Pipelines, pipelineSummary{
				ID:      p.ID,
				Name:    p.Name,
				OrderNr: p.OrderNr,
				Active:  p.Active,
				URL:     pipedrive.WebURL(companyDomain, pipedrive.WebURLPipeline, p.ID),
			})
		}
		return nil, out, nil
	})

	AddTool(s, &mcp.Tool{
		Name:        "list_stages",
		Description: "List Pipedrive stages, optionally filtered to a single pipeline. A stage represents a step in a pipeline (e.g. \"Lead In\", \"Negotiation\", \"Closed Won\"). Returns id, name, order_nr, active flag, owning pipeline_id, and Pipedrive's default deal_probability (0-100) for each stage.",
		Annotations: &readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listStagesInput) (*mcp.CallToolResult, listStagesOutput, error) {
		// Pipedrive's /api/v2/stages?pipeline_id=N returns an empty array
		// for pipelines that don't exist or aren't visible to the token's
		// user — same shape as a real-but-empty pipeline. The LLM can't
		// tell those cases apart from the response alone, so validate the
		// filter against ListPipelines and surface a [not_found] error
		// when the ID doesn't match. Phase 1's pipelines list is bounded
		// (workspaces typically have under 20), so the extra call is
		// cheap; a future cache lands with the custom-field cache.
		if in.PipelineID > 0 {
			pipes, err := c.ListPipelines(ctx)
			if err != nil {
				return errorResult(err), listStagesOutput{}, nil
			}
			if !pipelineVisible(pipes, in.PipelineID) {
				return errorResult(pipelineNotFound(in.PipelineID)), listStagesOutput{}, nil
			}
		}
		got, err := c.ListStages(ctx, in.PipelineID)
		if err != nil {
			return errorResult(err), listStagesOutput{}, nil
		}
		out := listStagesOutput{Stages: make([]stageSummary, 0, len(got))}
		for _, st := range got {
			out.Stages = append(out.Stages, stageSummary{
				ID:              st.ID,
				Name:            st.Name,
				OrderNr:         st.OrderNr,
				Active:          st.Active,
				PipelineID:      st.PipelineID,
				DealProbability: st.DealProbability,
			})
		}
		return nil, out, nil
	})
}

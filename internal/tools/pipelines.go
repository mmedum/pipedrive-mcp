package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

// pipelinesClient is the subset of *pipedrive.Client these tools call.
// Defining it as an interface keeps tool-handler tests independent of
// the real HTTP client (tests pass a fake).
type pipelinesClient interface {
	ListPipelines(ctx context.Context) ([]pipedrive.Pipeline, error)
	ListStages(ctx context.Context, pipelineID int64) ([]pipedrive.Stage, error)
}

// PipelineSummary is the LLM-facing shape of a single pipeline.
// The url field points back at the Pipedrive web UI for human follow-up.
type PipelineSummary struct {
	ID      int64  `json:"id" jsonschema:"the pipeline's numeric id"`
	Name    string `json:"name" jsonschema:"human-readable pipeline name"`
	OrderNr int    `json:"order_nr" jsonschema:"display order; lower numbers come first in Pipedrive's UI"`
	Active  bool   `json:"active" jsonschema:"true if the pipeline is currently active"`
	URL     string `json:"url" jsonschema:"link to the pipeline in the Pipedrive web UI"`
}

// StageSummary is the LLM-facing shape of a single stage.
type StageSummary struct {
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
	Pipelines []PipelineSummary `json:"pipelines" jsonschema:"every pipeline the API token's user can see"`
}

type listStagesInput struct {
	PipelineID int64 `json:"pipeline_id,omitempty" jsonschema:"optional: when set, return only stages in this pipeline. Omit (or pass 0) to return stages across every pipeline."`
}

type listStagesOutput struct {
	Stages []StageSummary `json:"stages" jsonschema:"matching stages, sorted by Pipedrive's order_nr within each pipeline"`
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
		out := listPipelinesOutput{Pipelines: make([]PipelineSummary, 0, len(got))}
		for _, p := range got {
			out.Pipelines = append(out.Pipelines, PipelineSummary{
				ID:      p.ID,
				Name:    p.Name,
				OrderNr: p.OrderNr,
				Active:  p.Active,
				URL:     pipelineURL(companyDomain, p.ID),
			})
		}
		return nil, out, nil
	})

	AddTool(s, &mcp.Tool{
		Name:        "list_stages",
		Description: "List Pipedrive stages, optionally filtered to a single pipeline. A stage represents a step in a pipeline (e.g. \"Lead In\", \"Negotiation\", \"Closed Won\"). Returns id, name, order_nr, active flag, owning pipeline_id, and Pipedrive's default deal_probability (0-100) for each stage.",
		Annotations: &readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listStagesInput) (*mcp.CallToolResult, listStagesOutput, error) {
		got, err := c.ListStages(ctx, in.PipelineID)
		if err != nil {
			return errorResult(err), listStagesOutput{}, nil
		}
		out := listStagesOutput{Stages: make([]StageSummary, 0, len(got))}
		for _, st := range got {
			out.Stages = append(out.Stages, StageSummary{
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

// errorResult wraps an internal/pipedrive error into a CallToolResult
// with isError: true. Per CLAUDE.md (MCP error mapping): upstream API
// failures become tool execution errors, not JSON-RPC protocol errors,
// so the LLM client gets a renderable error message rather than a
// generic transport failure.
func errorResult(err error) *mcp.CallToolResult {
	msg := err.Error()
	// Strip the `pipedrive: ` prefix for shorter LLM-facing text.
	const prefix = "pipedrive: "
	if len(msg) >= len(prefix) && msg[:len(prefix)] == prefix {
		msg = msg[len(prefix):]
	}
	// Surface the typed class as a leading word so the LLM can branch.
	var class string
	switch {
	case errors.Is(err, pipedrive.ErrUnauthorized):
		class = "auth"
	case errors.Is(err, pipedrive.ErrForbiddenPermission):
		class = "permission"
	case errors.Is(err, pipedrive.ErrForbiddenBusinessRule):
		class = "business_rule"
	case errors.Is(err, pipedrive.ErrNotFound):
		class = "not_found"
	case errors.Is(err, pipedrive.ErrRateLimited):
		class = "rate_limited"
	case errors.Is(err, pipedrive.ErrServerError):
		class = "server_error"
	case errors.Is(err, pipedrive.ErrValidation):
		class = "validation"
	default:
		class = "error"
	}
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("[%s] %s", class, msg)},
		},
	}
}

func pipelineURL(domain string, id int64) string {
	return fmt.Sprintf("https://%s.pipedrive.com/pipeline/%d", domain, id)
}

package tools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
	"github.com/mmedum/pipedrive-mcp/internal/tools"
)

// fakePipelinesClient lets handler tests skip the real HTTP client.
type fakePipelinesClient struct {
	pipelines    []pipedrive.Pipeline
	pipelinesErr error

	stages          []pipedrive.Stage
	stagesErr       error
	lastPipelineArg int64

	listPipelinesCalls int
}

func (f *fakePipelinesClient) ListPipelines(_ context.Context) ([]pipedrive.Pipeline, error) {
	f.listPipelinesCalls++
	return f.pipelines, f.pipelinesErr
}

func (f *fakePipelinesClient) ListStages(_ context.Context, pipelineID int64) ([]pipedrive.Stage, error) {
	f.lastPipelineArg = pipelineID
	return f.stages, f.stagesErr
}

// pipelineRow / stageRow mirror the JSON shape RegisterPipelines emits.
// We can't reference the unexported handler-local structs directly, so
// the tests carry a parallel shape. Drift is caught by the JSON tag
// names, not by Go type identity.
type pipelineRow struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	OrderNr int    `json:"order_nr"`
	Active  bool   `json:"active"`
	URL     string `json:"url"`
}

func TestListPipelines_HappyPath(t *testing.T) {
	fake := &fakePipelinesClient{
		pipelines: []pipedrive.Pipeline{
			{ID: 1, Name: "Sales", OrderNr: 0, Active: true},
			{ID: 2, Name: "Renewals", OrderNr: 1, Active: false},
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterPipelines(s, fake, "acme")
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "list_pipelines",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out struct {
		Pipelines []pipelineRow `json:"pipelines"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)
	if len(out.Pipelines) != 2 {
		t.Fatalf("got %d pipelines, want 2", len(out.Pipelines))
	}
	if out.Pipelines[0].URL != "https://acme.pipedrive.com/pipeline/1" {
		t.Errorf("URL = %q, want acme/pipeline/1", out.Pipelines[0].URL)
	}
	if out.Pipelines[1].Active {
		t.Errorf("pipeline[1].Active = true, want false")
	}
}

func TestListPipelines_UpstreamError(t *testing.T) {
	fake := &fakePipelinesClient{
		pipelinesErr: &pipedrive.APIError{
			Class:    pipedrive.ErrUnauthorized,
			Status:   401,
			Message:  "Invalid token",
			Endpoint: "/api/v2/pipelines",
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterPipelines(s, fake, "acme")
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "list_pipelines",
	})
	if err != nil {
		t.Fatalf("CallTool transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected isError=true on upstream 401, got: %+v", res.Content)
	}
	text := contentText(res)
	if !strings.HasPrefix(text, "[auth]") {
		t.Errorf("error text = %q; want [auth] prefix", text)
	}
}

func TestListStages_FilterPassedThrough(t *testing.T) {
	fake := &fakePipelinesClient{
		pipelines: []pipedrive.Pipeline{
			{ID: 5, Name: "Sales", Active: true},
		},
		stages: []pipedrive.Stage{
			{ID: 10, Name: "Lead In", OrderNr: 0, Active: true, PipelineID: 5, DealProbability: 10},
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterPipelines(s, fake, "acme")
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_stages",
		Arguments: map[string]any{"pipeline_id": 5},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	if fake.lastPipelineArg != 5 {
		t.Errorf("client received pipelineID=%d, want 5", fake.lastPipelineArg)
	}
	if fake.listPipelinesCalls != 1 {
		t.Errorf("listPipelinesCalls = %d, want 1 (existence check)", fake.listPipelinesCalls)
	}
}

func TestListStages_PipelineNotFound(t *testing.T) {
	fake := &fakePipelinesClient{
		pipelines: []pipedrive.Pipeline{
			{ID: 1, Name: "Sales", Active: true},
		},
		// Stages slice is intentionally non-empty: if the validation
		// short-circuit ever regresses, this test surfaces it (we'd
		// see stage_count=1 instead of an error).
		stages: []pipedrive.Stage{
			{ID: 10, Name: "Should Not Be Returned", PipelineID: 1},
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterPipelines(s, fake, "acme")
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_stages",
		Arguments: map[string]any{"pipeline_id": 99999},
	})
	if err != nil {
		t.Fatalf("CallTool transport error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected isError=true on unknown pipeline, got: %+v", res.Content)
	}
	text := contentText(res)
	if !strings.HasPrefix(text, "[not_found]") {
		t.Errorf("error text = %q; want [not_found] prefix", text)
	}
	if !strings.Contains(text, "99999") {
		t.Errorf("error text = %q; want it to mention the unknown id 99999", text)
	}
}

func TestListStages_OmittedFilterMeansAll(t *testing.T) {
	fake := &fakePipelinesClient{
		stages: []pipedrive.Stage{},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterPipelines(s, fake, "acme")
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_stages",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	if fake.lastPipelineArg != 0 {
		t.Errorf("client received pipelineID=%d, want 0 (all pipelines)", fake.lastPipelineArg)
	}
	if fake.listPipelinesCalls != 0 {
		t.Errorf("listPipelinesCalls = %d, want 0 — pipeline_id=0 should skip the existence check", fake.listPipelinesCalls)
	}
}

func TestListPipelines_RegistersInDumpRegistry(t *testing.T) {
	// Sanity: after Register runs, the dump-schemas registry has our
	// tools recorded so --dump-schemas (and the CI schema-diff gate)
	// sees them. We assert presence, not exclusivity, so other tests
	// in this binary can register their own tools without ordering.
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterPipelines(s, &fakePipelinesClient{}, "acme")
	})
	defer h.Close()

	var buf strings.Builder
	if err := tools.DumpJSON(&buf, "test"); err != nil {
		t.Fatalf("DumpJSON: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"list_pipelines"`, `"list_stages"`} {
		if !strings.Contains(out, want) {
			t.Errorf("dump missing %s; got: %s", want, out)
		}
	}
}

func contentText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

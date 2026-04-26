package tools_test

import (
	"context"
	"encoding/json"
	"errors"
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
}

func (f *fakePipelinesClient) ListPipelines(_ context.Context) ([]pipedrive.Pipeline, error) {
	return f.pipelines, f.pipelinesErr
}

func (f *fakePipelinesClient) ListStages(_ context.Context, pipelineID int64) ([]pipedrive.Stage, error) {
	f.lastPipelineArg = pipelineID
	return f.stages, f.stagesErr
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
		Pipelines []tools.PipelineSummary `json:"pipelines"`
	}
	if err := decodeStructured(res.StructuredContent, &out); err != nil {
		t.Fatalf("decode StructuredContent: %v (raw: %#v)", err, res.StructuredContent)
	}
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
}

func TestListPipelines_RegistersInDumpRegistry(t *testing.T) {
	// Sanity: after Register runs, Default dump-schemas registry has
	// our tools recorded so --dump-schemas (and the CI schema-diff
	// gate) sees them.
	tools.Default = tools.New() // reset for test isolation
	t.Cleanup(func() { tools.Default = tools.New() })

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

// decodeStructured re-encodes the SDK's StructuredContent (a map[string]any
// after the JSON-RPC roundtrip) into the test's expected type.
func decodeStructured(v any, into any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, into)
}

// Sanity check that errors.Is wiring stays consistent with the tool's
// error-class mapping.
func TestErrorClassWiring(t *testing.T) {
	// This is here as a marker; the real coverage is in
	// TestListPipelines_UpstreamError and similar.
	if !errors.Is(&pipedrive.APIError{Class: pipedrive.ErrNotFound}, pipedrive.ErrNotFound) {
		t.Fatal("APIError.Unwrap broken")
	}
}

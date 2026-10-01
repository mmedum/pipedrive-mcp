package tools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
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
			{ID: 1, Name: "Sales", OrderNr: 0},
			{ID: 2, Name: "Renewals", OrderNr: 1, IsDeleted: true},
		},
	}
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterPipelines(s, fake, "acme")
	}, "list_pipelines", nil)
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
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterPipelines(s, fake, "acme")
	}, "list_pipelines", nil)
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
			{ID: 5, Name: "Sales"},
		},
		stages: []pipedrive.Stage{
			{ID: 10, Name: "Lead In", OrderNr: 0, PipelineID: 5, DealProbability: 10},
		},
	}
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterPipelines(s, fake, "acme")
	}, "list_stages", map[string]any{"pipeline_id": 5})
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
			{ID: 1, Name: "Sales"},
		},
		// Stages slice is intentionally non-empty: even though
		// list_stages now fans the validation and the listing out in
		// parallel, an unknown pipeline must still surface as
		// [not_found] with the stage data discarded — this slot would
		// otherwise leak into the response.
		stages: []pipedrive.Stage{
			{ID: 10, Name: "Should Not Be Returned", PipelineID: 1},
		},
	}
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterPipelines(s, fake, "acme")
	}, "list_stages", map[string]any{"pipeline_id": 99999})
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
	if strings.Contains(text, "Should Not Be Returned") {
		t.Errorf("error response leaked stage data: %q", text)
	}
}

func TestListStages_OmittedFilterMeansAll(t *testing.T) {
	fake := &fakePipelinesClient{
		stages: []pipedrive.Stage{},
	}
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterPipelines(s, fake, "acme")
	}, "list_stages", map[string]any{})
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

// Every Register records its tools in the dump registry, which is what
// --dump-schemas and the schema-diff gate read. Each row swaps in an
// empty registry, so a Register that skips it cannot hide behind tools
// another test registered.
func TestRegister_RecordsItsToolsForTheSchemaDump(t *testing.T) {
	for _, tc := range []struct {
		name     string
		register func(*mcp.Server)
		want     []string
	}{
		{"deals", func(s *mcp.Server) { tools.RegisterDeals(s, &fakeDealsClient{}, "acme", tools.RegisterOptions{}) },
			[]string{"get_deal", "list_deals", "manage_deal"}},
		{"persons", func(s *mcp.Server) { tools.RegisterPersons(s, &fakePersonsClient{}, "acme", tools.RegisterOptions{}) },
			[]string{"get_person", "list_persons", "manage_person"}},
		{"organizations", func(s *mcp.Server) {
			tools.RegisterOrganizations(s, &fakeOrganizationsClient{}, "acme", tools.RegisterOptions{})
		}, []string{"get_organization", "list_organizations", "manage_organization"}},
		{"activities", func(s *mcp.Server) {
			tools.RegisterActivities(s, &fakeActivitiesClient{}, "acme", tools.RegisterOptions{})
		}, []string{"get_activity", "list_activities", "manage_activity"}},
		{"notes", func(s *mcp.Server) { tools.RegisterNotes(s, &fakeNotesClient{}, tools.RegisterOptions{}) },
			[]string{"get_note", "list_notes", "manage_note"}},
		{"pipelines", func(s *mcp.Server) { tools.RegisterPipelines(s, &fakePipelinesClient{}, "acme") },
			[]string{"list_pipelines", "list_stages"}},
		{"search", func(s *mcp.Server) { tools.RegisterSearch(s, &fakeSearchClient{}) }, []string{"search"}},
		{"whoami", func(s *mcp.Server) { tools.RegisterWhoAmI(s, &fakeWhoAmIClient{}) }, []string{"whoami"}},
		{"cache", func(s *mcp.Server) { tools.RegisterCache(s, &fakeCacheClient{}) }, []string{"refresh_field_cache"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := tools.Default
			tools.Default = tools.New()
			t.Cleanup(func() { tools.Default = saved })

			h := testutil.Connect(t, tc.register)
			defer h.Close()

			var buf bytes.Buffer
			if err := tools.DumpJSON(&buf, "test"); err != nil {
				t.Fatalf("DumpJSON: %v", err)
			}
			var doc struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			}
			if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
				t.Fatalf("decode dump: %v", err)
			}
			got := make([]string, 0, len(doc.Tools))
			for _, tool := range doc.Tools {
				got = append(got, tool.Name)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("registering %s dumped tools %v; want %v", tc.name, got, tc.want)
			}
		})
	}
}

func contentText(res *mcp.CallToolResult) string {
	return testutil.TextContent(res)
}

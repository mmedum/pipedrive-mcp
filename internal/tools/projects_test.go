package tools_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
	"github.com/mmedum/pipedrive-mcp/internal/tools"
)

type fakeProjectsClient struct {
	project                             *pipedrive.Project
	lastOpts                            pipedrive.ListProjectsOptions
	creates, updates, archives, deletes int
	lastReq                             pipedrive.ProjectRequest
}

func newProjectsFake() *fakeProjectsClient {
	return &fakeProjectsClient{project: &pipedrive.Project{
		ID: 9, Title: "Rollout", Status: "open", BoardID: 1, PhaseID: 2, DealIDs: []int64{3, 4}, UpdateTime: "2026-01-01T00:00:00Z",
	}}
}

func (f *fakeProjectsClient) GetProject(context.Context, int64) (*pipedrive.Project, error) {
	out := *f.project
	return &out, nil
}

func (f *fakeProjectsClient) ListProjects(_ context.Context, opts pipedrive.ListProjectsOptions) ([]pipedrive.Project, string, error) {
	f.lastOpts = opts
	return []pipedrive.Project{*f.project}, "", nil
}

func (f *fakeProjectsClient) CreateProject(_ context.Context, req pipedrive.ProjectRequest) (*pipedrive.Project, error) {
	f.creates++
	f.lastReq = req
	return &pipedrive.Project{ID: 10, Title: *req.Title, Status: "open", BoardID: 1, PhaseID: 1}, nil
}

func (f *fakeProjectsClient) UpdateProject(_ context.Context, _ int64, req pipedrive.ProjectRequest) (*pipedrive.Project, error) {
	f.updates++
	f.lastReq = req
	if req.DealIDs != nil {
		f.project.DealIDs = req.DealIDs
	}
	out := *f.project
	return &out, nil
}

func (f *fakeProjectsClient) ArchiveProject(context.Context, int64) (*pipedrive.Project, error) {
	f.archives++
	f.project.ArchiveTime = "2026-02-01T00:00:00Z"
	out := *f.project
	return &out, nil
}

func (f *fakeProjectsClient) DeleteProject(context.Context, int64) error {
	f.deletes++
	return nil
}

func (f *fakeProjectsClient) ListProjectBoards(context.Context) ([]pipedrive.ProjectBoard, error) {
	return []pipedrive.ProjectBoard{{ID: 2, Name: "Delivery", OrderNr: 2}, {ID: 1, Name: "Onboarding", OrderNr: 1}}, nil
}

func (f *fakeProjectsClient) ListProjectPhases(_ context.Context, board int64) ([]pipedrive.ProjectPhase, error) {
	return []pipedrive.ProjectPhase{{ID: board*10 + 2, Name: "Doing", OrderNr: 2}, {ID: board*10 + 1, Name: "To do", OrderNr: 1}}, nil
}

func (f *fakeProjectsClient) writes() int { return f.creates + f.updates + f.archives + f.deletes }

func callProjects(t *testing.T, f *fakeProjectsClient, opts tools.RegisterOptions, tool string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	return testutil.CallToolInto(t, func(s *mcp.Server) { tools.RegisterProjects(s, f, opts) }, tool, args, out)
}

func TestListProjectBoards_BoardsAndPhasesInOrder(t *testing.T) {
	var out struct {
		Boards []struct {
			ID     int64 `json:"id"`
			Phases []struct {
				ID int64 `json:"id"`
			} `json:"phases"`
		} `json:"boards"`
	}
	callProjects(t, newProjectsFake(), tools.RegisterOptions{}, "list_project_boards", map[string]any{}, &out)
	if len(out.Boards) != 2 || out.Boards[0].ID != 1 || out.Boards[0].Phases[0].ID != 11 || out.Boards[1].Phases[1].ID != 22 {
		t.Errorf("boards = %+v; want boards and their phases by order_nr", out.Boards)
	}
}

func TestListProjects_TheArchiveTakesStatusAndPhaseAlone(t *testing.T) {
	fake := newProjectsFake()
	res := callProjects(t, fake, tools.RegisterOptions{}, "list_projects", map[string]any{"archived": true, "deal_id": 3}, nil)
	if !res.IsError || !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("archived with deal_id = %q; want [validation]", contentText(res))
	}
	if res = callProjects(t, fake, tools.RegisterOptions{}, "list_projects", map[string]any{"archived": true, "phase_id": 2}, nil); res.IsError || !fake.lastOpts.Archived {
		t.Errorf("archived with phase_id = %q, opts %+v", contentText(res), fake.lastOpts)
	}
}

func TestManageProject_UpdateGuardsTheLinkListsAsSets(t *testing.T) {
	fake := newProjectsFake()
	// The same deals in another order change nothing.
	var out struct {
		Changed []string `json:"changed"`
	}
	if res := callProjects(t, fake, tools.RegisterOptions{}, "manage_project", map[string]any{"action": "update", "project_id": 9, "deal_ids": []int64{4, 3}}, &out); res.IsError || fake.updates != 0 || len(out.Changed) != 0 {
		t.Fatalf("a reorder = %s, %+v, updates = %d; want a no-op", contentText(res), out, fake.updates)
	}
	args := map[string]any{"action": "update", "project_id": 9, "deal_ids": []int64{3}}
	if res := callProjects(t, fake, tools.RegisterOptions{}, "manage_project", args, nil); !strings.Contains(contentText(res), `overwrite: ["deal_ids"]`) {
		t.Fatalf("dropping a deal without overwrite = %q; want a refusal naming deal_ids", contentText(res))
	}
	args["overwrite"] = []string{"deal_ids"}
	if res := callProjects(t, fake, tools.RegisterOptions{}, "manage_project", args, &out); res.IsError || fake.updates != 1 || !reflect.DeepEqual(out.Changed, []string{"deal_ids"}) {
		t.Errorf("update = %s, %+v; want deal_ids changed", contentText(res), out)
	}
}

func TestManageProject_ArchiveAndDeleteGuardAsTheySay(t *testing.T) {
	fake := newProjectsFake()
	if res := callProjects(t, fake, tools.RegisterOptions{}, "manage_project", map[string]any{"action": "archive", "project_id": 9, "dry_run": true}, nil); res.IsError || fake.archives != 0 {
		t.Fatalf("dry-run archive = %s, archives = %d", contentText(res), fake.archives)
	}
	if res := callProjects(t, fake, tools.RegisterOptions{}, "manage_project", map[string]any{"action": "archive", "project_id": 9}, nil); res.IsError || fake.archives != 1 {
		t.Fatalf("archive = %s, archives = %d; want one, with no overwrite or confirm", contentText(res), fake.archives)
	}
	// Archived already: reported as it stands, not archived twice.
	if res := callProjects(t, fake, tools.RegisterOptions{}, "manage_project", map[string]any{"action": "archive", "project_id": 9}, nil); res.IsError || fake.archives != 1 {
		t.Errorf("a second archive = %s, archives = %d; want no second call", contentText(res), fake.archives)
	}
	if res := callProjects(t, fake, tools.RegisterOptions{}, "manage_project", map[string]any{"action": "delete", "project_id": 9, "expect_version": "2025-01-01T00:00:00Z"}, nil); !strings.Contains(contentText(res), "changed since you read it") {
		t.Errorf("delete with a stale version = %q", contentText(res))
	}
	if res := callProjects(t, fake, tools.RegisterOptions{}, "manage_project", map[string]any{"action": "delete", "project_id": 9}, nil); res.IsError || fake.deletes != 1 {
		t.Errorf("delete = %s, deletes = %d; want one soft delete", contentText(res), fake.deletes)
	}
}

func TestManageProject_RefusesBadInputAndHonorsTheFloor(t *testing.T) {
	for _, args := range []map[string]any{
		{"action": "create", "title": " "},
		{"action": "update", "project_id": 9, "status": "deleted"},
		{"action": "update", "project_id": 9, "end_date": "31/12/2026"},
	} {
		fake := newProjectsFake()
		if res := callProjects(t, fake, tools.RegisterOptions{}, "manage_project", args, nil); !strings.HasPrefix(contentText(res), "[validation]") || fake.writes() != 0 {
			t.Errorf("%v = %q; want [validation] and nothing sent", args, contentText(res))
		}
	}
	fake := newProjectsFake()
	floor := tools.RegisterOptions{DryRun: true}
	for _, args := range []map[string]any{
		{"action": "create", "title": "X"},
		{"action": "archive", "project_id": 9},
		{"action": "delete", "project_id": 9},
	} {
		if res := callProjects(t, fake, floor, "manage_project", args, nil); res.IsError {
			t.Errorf("%v under the floor = %s", args["action"], contentText(res))
		}
	}
	if fake.writes() != 0 {
		t.Errorf("the floor let %d writes through", fake.writes())
	}
}

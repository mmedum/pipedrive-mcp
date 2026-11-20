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

const testGoalID = "8b8015df2457afcd0a084a40e8286f3a"

type fakeGoalsClient struct {
	goal                      *pipedrive.Goal
	lastFind                  pipedrive.FindGoalsOptions
	lastReq                   pipedrive.GoalRequest
	lastPeriod                [2]string
	creates, updates, deletes int
}

func newGoalsFake() *fakeGoalsClient {
	end := "2030-12-31"
	return &fakeGoalsClient{goal: &pipedrive.Goal{
		ID: testGoalID, Title: "Q1 wins", Type: pipedrive.GoalType{Name: "deals_won", Params: pipedrive.GoalParams{PipelineID: []int64{6}}},
		Assignee: pipedrive.GoalAssignee{ID: 3, Type: "person"}, Interval: "monthly",
		Duration:        pipedrive.GoalDuration{Start: "2030-01-01", End: &end},
		ExpectedOutcome: pipedrive.GoalOutcome{Target: 5, TrackingMetric: "quantity"},
	}}
}

func (f *fakeGoalsClient) FindGoals(_ context.Context, opts pipedrive.FindGoalsOptions) ([]pipedrive.Goal, error) {
	f.lastFind = opts
	return []pipedrive.Goal{*f.goal}, nil
}

func (f *fakeGoalsClient) GoalResult(_ context.Context, id, start, end string) (*pipedrive.Goal, float64, error) {
	if id != f.goal.ID {
		return nil, 0, &pipedrive.APIError{Class: pipedrive.ErrNotFound, Status: 404}
	}
	f.lastPeriod = [2]string{start, end}
	out := *f.goal
	return &out, 2.5, nil
}

func (f *fakeGoalsClient) CreateGoal(_ context.Context, req pipedrive.GoalRequest) (*pipedrive.Goal, error) {
	f.creates++
	f.lastReq = req
	return &pipedrive.Goal{ID: testGoalID, Title: *req.Title}, nil
}

func (f *fakeGoalsClient) UpdateGoal(_ context.Context, _ string, req pipedrive.GoalRequest) (*pipedrive.Goal, error) {
	f.updates++
	f.lastReq = req
	if req.ExpectedOutcome != nil {
		f.goal.ExpectedOutcome = *req.ExpectedOutcome
	}
	out := *f.goal
	return &out, nil
}

func (f *fakeGoalsClient) DeleteGoal(context.Context, string) error {
	f.deletes++
	return nil
}

func (f *fakeGoalsClient) writes() int { return f.creates + f.updates + f.deletes }

func callGoals(t *testing.T, f *fakeGoalsClient, opts tools.RegisterOptions, tool string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	return testutil.CallToolInto(t, func(s *mcp.Server) { tools.RegisterGoals(s, f, opts) }, tool, args, out)
}

func TestGetGoal_MeasuresFromTheGoalsStartByDefault(t *testing.T) {
	fake := newGoalsFake()
	var out struct {
		Progress    float64 `json:"progress"`
		PeriodStart string  `json:"period_start"`
	}
	if res := callGoals(t, fake, tools.RegisterOptions{}, "get_goal", map[string]any{"goal_id": testGoalID}, &out); res.IsError {
		t.Fatalf("get_goal = %s", contentText(res))
	}
	if out.PeriodStart != "2030-01-01" || fake.lastPeriod[0] != "2030-01-01" || out.Progress != 2.5 {
		t.Errorf("get_goal = %+v, period %v; want progress from the goal's start", out, fake.lastPeriod)
	}
	if res := callGoals(t, fake, tools.RegisterOptions{}, "get_goal", map[string]any{"goal_id": "../deals"}, nil); !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("a path-shaped goal_id = %q; want [validation]", contentText(res))
	}
}

func TestManageGoal_CreateNeedsTheWholeGoalAndAConsistentShape(t *testing.T) {
	base := map[string]any{"action": "create", "title": "Wins", "type_name": "deals_won", "assignee_id": 3, "assignee_type": "person",
		"target": 5, "tracking_metric": "quantity", "start": "2030-01-01", "interval": "monthly"}
	for name, change := range map[string]map[string]any{
		"a missing interval":          {"interval": nil},
		"a sum without a currency":    {"tracking_metric": "sum"},
		"a quantity with a currency":  {"currency_id": 1},
		"a stage on deals_won":        {"stage_id": 4},
		"activity types on deals_won": {"activity_type_ids": []int64{1}},
		"an unknown type":             {"type_name": "calls_made"},
	} {
		args := map[string]any{}
		for k, v := range base {
			args[k] = v
		}
		for k, v := range change {
			if v == nil {
				delete(args, k)
			} else {
				args[k] = v
			}
		}
		fake := newGoalsFake()
		if res := callGoals(t, fake, tools.RegisterOptions{}, "manage_goal", args, nil); !strings.HasPrefix(contentText(res), "[validation]") || fake.creates != 0 {
			t.Errorf("%s = %q; want [validation] and nothing sent", name, contentText(res))
		}
	}
	fake := newGoalsFake()
	if res := callGoals(t, fake, tools.RegisterOptions{}, "manage_goal", base, nil); res.IsError || fake.creates != 1 || fake.lastReq.Type == nil || fake.lastReq.ExpectedOutcome == nil {
		t.Errorf("create = %s, req %+v; want one create with its nested objects", contentText(res), fake.lastReq)
	}
}

func TestManageGoal_UpdateSendsTheWholeNestedObjectItTouches(t *testing.T) {
	fake := newGoalsFake()
	args := map[string]any{"action": "update", "goal_id": testGoalID, "target": 8}
	if res := callGoals(t, fake, tools.RegisterOptions{}, "manage_goal", args, nil); !strings.Contains(contentText(res), `overwrite: ["target"]`) {
		t.Fatalf("a target change without overwrite = %q", contentText(res))
	}
	args["overwrite"] = []string{"target"}
	if res := callGoals(t, fake, tools.RegisterOptions{}, "manage_goal", args, nil); res.IsError || fake.updates != 1 {
		t.Fatalf("update = %s", contentText(res))
	}
	o := fake.lastReq.ExpectedOutcome
	if o == nil || o.Target != 8 || o.TrackingMetric != "quantity" || fake.lastReq.Type != nil || fake.lastReq.Title != nil {
		t.Errorf("request = %+v; want the whole outcome and nothing else", fake.lastReq)
	}
}

func TestManageGoal_DeleteIsPermanentSoItNeedsConfirm(t *testing.T) {
	fake := newGoalsFake()
	args := map[string]any{"action": "delete", "goal_id": testGoalID}
	if res := callGoals(t, fake, tools.RegisterOptions{}, "manage_goal", args, nil); !strings.Contains(contentText(res), "is permanent") {
		t.Fatalf("delete without confirm = %q", contentText(res))
	}
	args["confirm"] = true
	if res := callGoals(t, fake, tools.RegisterOptions{DryRun: true}, "manage_goal", args, nil); res.IsError || fake.writes() != 0 {
		t.Fatalf("a delete under the floor = %s, deletes %d", contentText(res), fake.deletes)
	}
	if res := callGoals(t, fake, tools.RegisterOptions{}, "manage_goal", args, nil); res.IsError || fake.deletes != 1 {
		t.Errorf("confirmed delete = %s, deletes = %d", contentText(res), fake.deletes)
	}
}

func TestManageGoal_ATypeChangeDropsTheParamsTheNewTypeDoesNotTake(t *testing.T) {
	fake := newGoalsFake()
	stage := int64(4)
	fake.goal.Type = pipedrive.GoalType{Name: "deals_progressed", Params: pipedrive.GoalParams{PipelineID: []int64{6}, StageID: &stage}}
	args := map[string]any{"action": "update", "goal_id": testGoalID, "type_name": "deals_won", "overwrite": []string{"type_name", "stage_id"}}
	if res := callGoals(t, fake, tools.RegisterOptions{}, "manage_goal", args, nil); res.IsError || fake.updates != 1 {
		t.Fatalf("type change = %s", contentText(res))
	}
	if typ := fake.lastReq.Type; typ == nil || typ.Name != "deals_won" || typ.Params.StageID != nil || len(typ.Params.PipelineID) != 1 {
		t.Errorf("request type = %+v; want deals_won, its pipelines, and no stage", fake.lastReq.Type)
	}
}

func TestGoals_RefuseAPeriodOutOfOrderOrHalfGiven(t *testing.T) {
	fake := newGoalsFake()
	for tool, args := range map[string]map[string]any{
		"list_goals": {"period_start": "2030-01-01"},
		"get_goal":   {"goal_id": testGoalID, "period_start": "2030-06-01", "period_end": "2030-01-01"},
	} {
		if res := callGoals(t, fake, tools.RegisterOptions{}, tool, args, nil); !strings.HasPrefix(contentText(res), "[validation]") {
			t.Errorf("%s %v = %q; want [validation]", tool, args, contentText(res))
		}
	}
}

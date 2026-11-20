package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

type goalsClient interface {
	FindGoals(ctx context.Context, opts pipedrive.FindGoalsOptions) ([]pipedrive.Goal, error)
	GoalResult(ctx context.Context, id, start, end string) (*pipedrive.Goal, int64, error)
	CreateGoal(ctx context.Context, req pipedrive.GoalRequest) (*pipedrive.Goal, error)
	UpdateGoal(ctx context.Context, id string, req pipedrive.GoalRequest) (*pipedrive.Goal, error)
	DeleteGoal(ctx context.Context, id string) error
}

var (
	allowedGoalTypes = map[string]bool{
		"deals_won": true, "deals_progressed": true, "activities_completed": true,
		"activities_added": true, "deals_started": true, "revenue_forecast": true,
	}
	allowedGoalAssignees = map[string]bool{"person": true, "company": true, "team": true}
	allowedGoalMetrics   = map[string]bool{"quantity": true, "sum": true}
	allowedGoalIntervals = map[string]bool{"weekly": true, "monthly": true, "quarterly": true, "yearly": true}
	allowedGoalActions   = map[string]bool{"create": true, "update": true, "delete": true}
)

type goalSummary struct {
	ID              string  `json:"id" jsonschema:"the goal's id, a hex string; empty on a dry-run create"`
	Title           string  `json:"title" jsonschema:"the goal's title"`
	TypeName        string  `json:"type_name" jsonschema:"what the goal counts: deals_won | deals_progressed | activities_completed | activities_added | deals_started | revenue_forecast"`
	PipelineIDs     []int64 `json:"pipeline_ids,omitempty" jsonschema:"the pipelines it counts in; absent means every pipeline"`
	StageID         int64   `json:"stage_id,omitempty" jsonschema:"the stage a deals_progressed goal counts"`
	ActivityTypeIDs []int64 `json:"activity_type_ids,omitempty" jsonschema:"the activity types it counts; absent means every type"`
	AssigneeID      int64   `json:"assignee_id" jsonschema:"the person, company or team the goal belongs to"`
	AssigneeType    string  `json:"assignee_type" jsonschema:"person | company | team"`
	Target          float64 `json:"target" jsonschema:"the number to reach"`
	TrackingMetric  string  `json:"tracking_metric" jsonschema:"quantity, a count, or sum, an amount in currency_id"`
	CurrencyID      int64   `json:"currency_id,omitempty" jsonschema:"the currency of a sum goal"`
	Start           string  `json:"start" jsonschema:"YYYY-MM-DD the goal starts"`
	End             string  `json:"end,omitempty" jsonschema:"YYYY-MM-DD the goal ends; empty for an open-ended goal"`
	Interval        string  `json:"interval" jsonschema:"weekly | monthly | quarterly | yearly"`
	IsActive        bool    `json:"is_active" jsonschema:"true while the goal's period has started and not ended"`
	Version         string  `json:"version" jsonschema:"a fingerprint of the goal's settings; pass it as expect_version to refuse a write if the goal changed since you read it"`
}

type listGoalsInput struct {
	Title        string `json:"title,omitempty" jsonschema:"return only goals with exactly this title"`
	IsActive     *bool  `json:"is_active,omitempty" jsonschema:"true for goals running now, false for the rest; omit for both"`
	AssigneeID   int64  `json:"assignee_id,omitempty" jsonschema:"return only this person's, company's or team's goals; without it and without assignee_type or type_name, Pipedrive returns the token user's own goals"`
	AssigneeType string `json:"assignee_type,omitempty" jsonschema:"person | company | team; returns everyone's goals of that kind"`
	TypeName     string `json:"type_name,omitempty" jsonschema:"return only goals counting this: deals_won | deals_progressed | activities_completed | activities_added | deals_started | revenue_forecast"`
	PeriodStart  string `json:"period_start,omitempty" jsonschema:"YYYY-MM-DD; return goals running in the period from this date"`
	PeriodEnd    string `json:"period_end,omitempty" jsonschema:"YYYY-MM-DD; the end of that period"`
}

type listGoalsOutput struct {
	Goals []goalSummary `json:"goals" jsonschema:"every matching goal; this list does not page"`
}

type getGoalInput struct {
	GoalID      string `json:"goal_id" jsonschema:"the goal's id; list_goals reports it"`
	PeriodStart string `json:"period_start,omitempty" jsonschema:"YYYY-MM-DD to measure progress from; default the goal's own start"`
	PeriodEnd   string `json:"period_end,omitempty" jsonschema:"YYYY-MM-DD to measure progress to; default today"`
}

type getGoalOutput struct {
	Goal        goalSummary `json:"goal" jsonschema:"the goal"`
	Progress    int64       `json:"progress" jsonschema:"how far the goal got in the period, in its tracking metric"`
	PeriodStart string      `json:"period_start" jsonschema:"the period progress was measured over"`
	PeriodEnd   string      `json:"period_end" jsonschema:"the end of that period"`
}

type manageGoalInput struct {
	Action          string   `json:"action" jsonschema:"create, update or delete"`
	GoalID          string   `json:"goal_id,omitempty" jsonschema:"the goal to act on, required by update and delete; list_goals reports it"`
	Title           *string  `json:"title,omitempty" jsonschema:"the goal's title, required by create. Omit to leave it as it is"`
	TypeName        *string  `json:"type_name,omitempty" jsonschema:"what the goal counts, required by create: deals_won | deals_progressed | activities_completed | activities_added | deals_started | revenue_forecast"`
	PipelineIDs     []int64  `json:"pipeline_ids,omitempty" jsonschema:"the pipelines it counts in; omit on create for every pipeline. A write REPLACES the whole list"`
	StageID         *int64   `json:"stage_id,omitempty" jsonschema:"the stage a deals_progressed goal counts, and for no other type"`
	ActivityTypeIDs []int64  `json:"activity_type_ids,omitempty" jsonschema:"the activity types an activities_completed or activities_added goal counts; omit on create for every type"`
	AssigneeID      *int64   `json:"assignee_id,omitempty" jsonschema:"the person, company or team the goal belongs to, required by create; whoami gives your own user id"`
	AssigneeType    *string  `json:"assignee_type,omitempty" jsonschema:"person | company | team, required by create"`
	Target          *float64 `json:"target,omitempty" jsonschema:"the number to reach, required by create"`
	TrackingMetric  *string  `json:"tracking_metric,omitempty" jsonschema:"quantity, a count, or sum, an amount, required by create; a sum needs currency_id"`
	CurrencyID      *int64   `json:"currency_id,omitempty" jsonschema:"the currency of a sum goal, and of no other"`
	Start           *string  `json:"start,omitempty" jsonschema:"YYYY-MM-DD the goal starts, required by create"`
	End             *string  `json:"end,omitempty" jsonschema:"YYYY-MM-DD the goal ends; omit on create for an open-ended goal"`
	Interval        *string  `json:"interval,omitempty" jsonschema:"weekly | monthly | quarterly | yearly, required by create"`
	DryRun          bool     `json:"dry_run,omitempty" jsonschema:"report what the write would find and change, and send nothing"`
	Overwrite       []string `json:"overwrite,omitempty" jsonschema:"the fields this update may replace, named exactly as the refusal listed them, e.g. [\"target\"]. A refusal is NOT a retry signal: name a field only when the user asked for what is there to be replaced"`
	ExpectVersion   string   `json:"expect_version,omitempty" jsonschema:"the goal's version from the read that informed this write; update and delete are refused if it changed since"`
	Confirm         bool     `json:"confirm,omitempty" jsonschema:"true to delete for good. Pipedrive keeps no copy of a deleted goal and nothing here restores it; set it only when the user asked for this goal to go"`
}

type manageGoalOutput struct {
	Action  string      `json:"action" jsonschema:"the action that ran"`
	Goal    goalSummary `json:"goal" jsonschema:"the goal as Pipedrive stored it. For delete, as it was just before. When dry_run is true nothing was persisted and a created goal has no id"`
	Changed []string    `json:"changed,omitempty" jsonschema:"names of the fields this write altered, or on a dry run would alter"`
	Deleted bool        `json:"deleted,omitempty" jsonschema:"true when the goal was deleted, or on a dry run would be"`
	DryRun  bool        `json:"dry_run,omitempty" jsonschema:"true when nothing was sent upstream"`
}

// goalFields drives the update's diff and overwrite guard (D-38). The id
// lists sort, since they are sets.
var goalFields = []fieldSpec[pipedrive.Goal]{
	{"title", func(g *pipedrive.Goal) string { return g.Title }},
	{"type_name", func(g *pipedrive.Goal) string { return g.Type.Name }},
	{"pipeline_ids", func(g *pipedrive.Goal) string { return projectIDSet(g.Type.Params.PipelineID) }},
	{"stage_id", func(g *pipedrive.Goal) string { return projectOptID(g.Type.Params.StageID) }},
	{"activity_type_ids", func(g *pipedrive.Goal) string { return projectIDSet(g.Type.Params.ActivityTypeID) }},
	{"assignee_id", func(g *pipedrive.Goal) string { return projectID(g.Assignee.ID) }},
	{"assignee_type", func(g *pipedrive.Goal) string { return g.Assignee.Type }},
	{"target", func(g *pipedrive.Goal) string { return projectMoney(g.ExpectedOutcome.Target) }},
	{"tracking_metric", func(g *pipedrive.Goal) string { return g.ExpectedOutcome.TrackingMetric }},
	{"currency_id", func(g *pipedrive.Goal) string { return projectOptID(g.ExpectedOutcome.CurrencyID) }},
	{"start", func(g *pipedrive.Goal) string { return g.Duration.Start }},
	{"end", func(g *pipedrive.Goal) string { return deref(g.Duration.End) }},
	{"interval", func(g *pipedrive.Goal) string { return g.Interval }},
}

// goalVersion stands in for the update_time a goal does not have: any
// change to a field the table holds changes it.
func goalVersion(g *pipedrive.Goal) string {
	var b strings.Builder
	for _, f := range goalFields {
		b.WriteString(strconv.Quote(f.Get(g)))
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:6])
}

// RegisterGoals wires the goal tools. They run on Pipedrive v1, which v2
// does not replace (D-105), and sit outside the compatibility promise
// (D-04).
func RegisterGoals(s *mcp.Server, c goalsClient, opts RegisterOptions) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_goals",
		Description: "List goals: what each counts (deals won, progressed or started, activities added or completed, or revenue forecast), in which pipelines, who it belongs to, its target, period and interval. IMPORTANT: with no assignee_id, assignee_type or type_name, Pipedrive returns only the token user's own goals; pass one to see others'. This list does not page. Goals come from Pipedrive v1. get_goal reports one goal's progress.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listGoalsInput) (*mcp.CallToolResult, listGoalsOutput, error) {
		if err := validateListGoals(in); err != nil {
			return errorResult(err), listGoalsOutput{}, nil
		}
		goals, err := c.FindGoals(ctx, pipedrive.FindGoalsOptions{
			Title: in.Title, IsActive: in.IsActive, AssigneeID: in.AssigneeID, AssigneeType: in.AssigneeType,
			TypeName: in.TypeName, PeriodStart: in.PeriodStart, PeriodEnd: in.PeriodEnd,
		})
		if err != nil {
			return errorResult(err), listGoalsOutput{}, nil
		}
		out := listGoalsOutput{Goals: make([]goalSummary, 0, len(goals))}
		for i := range goals {
			out.Goals = append(out.Goals, summarizeGoal(&goals[i]))
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_goal",
		Description: "Fetch one goal by goal_id and its progress over a period: how far it got, in its own tracking metric, from period_start to period_end, which default to the goal's start and today. Goals come from Pipedrive v1. list_goals finds the id.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getGoalInput) (*mcp.CallToolResult, getGoalOutput, error) {
		if err := validateGoalID(in.GoalID); err != nil {
			return errorResult(err), getGoalOutput{}, nil
		}
		start, end := in.PeriodStart, in.PeriodEnd
		if end == "" {
			end = time.Now().UTC().Format(time.DateOnly)
		}
		if start == "" {
			// The goal's own start, which a first read on today's date gives.
			g, _, err := c.GoalResult(ctx, in.GoalID, end, end)
			if err != nil {
				return errorResult(err), getGoalOutput{}, nil
			}
			start = g.Duration.Start
		}
		if err := validateDates(map[string]string{"period_start": start, "period_end": end}); err != nil {
			return errorResult(err), getGoalOutput{}, nil
		}
		g, progress, err := c.GoalResult(ctx, in.GoalID, start, end)
		if err != nil {
			return errorResult(err), getGoalOutput{}, nil
		}
		return nil, getGoalOutput{Goal: summarizeGoal(g), Progress: progress, PeriodStart: start, PeriodEnd: end}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "manage_goal",
		Description: "Create a goal, edit one, or delete one. create takes title, type_name, assignee_id, assignee_type, target, tracking_metric, start and interval; a sum goal also takes currency_id, a deals_progressed goal a stage_id. update and delete take goal_id and read the goal first. An update refuses to replace ANY field that already holds a value unless you name it in overwrite, and the refusal names each one; pipeline_ids and activity_type_ids are whole lists. Deleting is PERMANENT: Pipedrive keeps no copy, so delete needs confirm: true, and dry_run shows what would go without it. Pipedrive creates a report to track each new goal. Goals come from Pipedrive v1, and these tools leave in a minor release if Pipedrive retires it.",
		Annotations: mutatingAnnotations(),
	}, manageGoalHandler(c, opts.DryRun))
}

func validateListGoals(in listGoalsInput) error {
	if err := validateEnum(in.AssigneeType, "assignee_type", allowedGoalAssignees); err != nil {
		return err
	}
	if err := validateEnum(in.TypeName, "type_name", allowedGoalTypes); err != nil {
		return err
	}
	return validateDates(map[string]string{"period_start": in.PeriodStart, "period_end": in.PeriodEnd})
}

// validateDates checks every non-empty YYYY-MM-DD value.
func validateDates(dates map[string]string) error {
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range dates {
			if !yield(k) {
				return
			}
		}
	}) {
		if d := dates[name]; d != "" {
			if _, err := time.Parse(time.DateOnly, d); err != nil {
				return fmt.Errorf("%w: %s %q is not a YYYY-MM-DD date", pipedrive.ErrValidation, name, d)
			}
		}
	}
	return nil
}

// validateGoalID refuses an id that cannot be a goal's before it reaches
// a path: Pipedrive's are hex strings.
func validateGoalID(id string) error {
	if id == "" || len(id) > 64 || strings.Trim(id, "0123456789abcdef") != "" {
		return fmt.Errorf("%w: goal_id %q is not a goal's id; list_goals reports it", pipedrive.ErrValidation, id)
	}
	return nil
}

func summarizeGoal(g *pipedrive.Goal) goalSummary {
	return goalSummary{
		ID: g.ID, Title: g.Title, TypeName: g.Type.Name,
		PipelineIDs: slices.Clone(g.Type.Params.PipelineID), StageID: deref(g.Type.Params.StageID),
		ActivityTypeIDs: slices.Clone(g.Type.Params.ActivityTypeID),
		AssigneeID:      g.Assignee.ID, AssigneeType: g.Assignee.Type,
		Target: g.ExpectedOutcome.Target, TrackingMetric: g.ExpectedOutcome.TrackingMetric, CurrencyID: deref(g.ExpectedOutcome.CurrencyID),
		Start: g.Duration.Start, End: deref(g.Duration.End), Interval: g.Interval, IsActive: g.IsActive,
		Version: goalVersion(g),
	}
}

func manageGoalHandler(c goalsClient, dryRun bool) mcp.ToolHandlerFor[manageGoalInput, manageGoalOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in manageGoalInput) (*mcp.CallToolResult, manageGoalOutput, error) {
		if err := validateAction(in.Action, allowedGoalActions); err != nil {
			return errorResult(err), manageGoalOutput{}, nil
		}
		if err := validateGoalInput(in); err != nil {
			return errorResult(err), manageGoalOutput{}, nil
		}
		in.DryRun = in.DryRun || dryRun
		var (
			res *mcp.CallToolResult
			out manageGoalOutput
		)
		switch in.Action {
		case "create":
			res, out = createGoalAction(ctx, c, in)
		case "update":
			res, out = updateGoalAction(ctx, c, in)
		case "delete":
			res, out = deleteGoalAction(ctx, c, in)
		default:
			res = errorResult(fmt.Errorf("%w: action %q has no handler", pipedrive.ErrValidation, in.Action))
		}
		if res == nil {
			out.Action = in.Action
			out.DryRun = in.DryRun
		}
		return res, out, nil
	}
}

func validateGoalInput(in manageGoalInput) error {
	if in.Action != "create" {
		if err := validateGoalID(in.GoalID); err != nil {
			return err
		}
	}
	for name, v := range map[string]*string{"type_name": in.TypeName, "assignee_type": in.AssigneeType, "tracking_metric": in.TrackingMetric, "interval": in.Interval} {
		allowed := map[string]map[string]bool{"type_name": allowedGoalTypes, "assignee_type": allowedGoalAssignees, "tracking_metric": allowedGoalMetrics, "interval": allowedGoalIntervals}[name]
		if v != nil {
			if err := validateEnum(*v, name, allowed); err != nil {
				return err
			}
		}
	}
	for name, id := range map[string]*int64{"assignee_id": in.AssigneeID, "stage_id": in.StageID, "currency_id": in.CurrencyID} {
		if id != nil {
			if err := validatePositiveID(*id, name); err != nil {
				return err
			}
		}
	}
	if in.Target != nil && !(*in.Target > 0) {
		return fmt.Errorf("%w: target must be above zero", pipedrive.ErrValidation)
	}
	dates := map[string]string{}
	if in.Start != nil {
		dates["start"] = *in.Start
	}
	if in.End != nil {
		dates["end"] = *in.End
	}
	return validateDates(dates)
}

// goalAfter overlays the input on a goal. It builds each nested object
// whole, so a write sends a goal type with its params and an outcome
// with its currency.
func goalAfter(before pipedrive.Goal, in manageGoalInput) pipedrive.Goal {
	after := before
	setIf(&after.Title, in.Title)
	setIf(&after.Type.Name, in.TypeName)
	if in.PipelineIDs != nil {
		after.Type.Params.PipelineID = slices.Clone(in.PipelineIDs)
	}
	if in.StageID != nil {
		after.Type.Params.StageID = clone(in.StageID)
	}
	if in.ActivityTypeIDs != nil {
		after.Type.Params.ActivityTypeID = slices.Clone(in.ActivityTypeIDs)
	}
	setIf(&after.Assignee.ID, in.AssigneeID)
	setIf(&after.Assignee.Type, in.AssigneeType)
	setIf(&after.ExpectedOutcome.Target, in.Target)
	setIf(&after.ExpectedOutcome.TrackingMetric, in.TrackingMetric)
	if in.CurrencyID != nil {
		after.ExpectedOutcome.CurrencyID = clone(in.CurrencyID)
	}
	setIf(&after.Duration.Start, in.Start)
	if in.End != nil {
		after.Duration.End = clone(in.End)
	}
	setIf(&after.Interval, in.Interval)
	return after
}

// goalRequestFor sends the nested objects the input touches, each whole.
func goalRequestFor(after pipedrive.Goal, in manageGoalInput) pipedrive.GoalRequest {
	req := pipedrive.GoalRequest{Title: in.Title, Interval: in.Interval}
	if in.TypeName != nil || in.PipelineIDs != nil || in.StageID != nil || in.ActivityTypeIDs != nil {
		req.Type = clone(&after.Type)
	}
	if in.AssigneeID != nil || in.AssigneeType != nil {
		req.Assignee = clone(&after.Assignee)
	}
	if in.Target != nil || in.TrackingMetric != nil || in.CurrencyID != nil {
		req.ExpectedOutcome = clone(&after.ExpectedOutcome)
	}
	if in.Start != nil || in.End != nil {
		req.Duration = clone(&after.Duration)
	}
	return req
}

// checkGoalShape holds the goal a write would leave to the rules
// Pipedrive's reference states: a sum goal has a currency and a quantity
// goal none, a stage only on deals_progressed, activity types only on
// the activity goals.
func checkGoalShape(g pipedrive.Goal) error {
	switch {
	case g.ExpectedOutcome.TrackingMetric == "sum" && g.ExpectedOutcome.CurrencyID == nil:
		return fmt.Errorf("%w: a sum goal needs currency_id", pipedrive.ErrValidation)
	case g.ExpectedOutcome.TrackingMetric == "quantity" && g.ExpectedOutcome.CurrencyID != nil:
		return fmt.Errorf("%w: currency_id is for a sum goal; this one counts a quantity", pipedrive.ErrValidation)
	case g.Type.Params.StageID != nil && g.Type.Name != "deals_progressed":
		return fmt.Errorf("%w: stage_id is for a deals_progressed goal", pipedrive.ErrValidation)
	case len(g.Type.Params.ActivityTypeID) > 0 && g.Type.Name != "activities_completed" && g.Type.Name != "activities_added":
		return fmt.Errorf("%w: activity_type_ids is for an activities_completed or activities_added goal", pipedrive.ErrValidation)
	}
	return nil
}

func createGoalAction(ctx context.Context, c goalsClient, in manageGoalInput) (*mcp.CallToolResult, manageGoalOutput) {
	if in.Title == nil || in.TypeName == nil || in.AssigneeID == nil || in.AssigneeType == nil ||
		in.Target == nil || in.TrackingMetric == nil || in.Start == nil || in.Interval == nil {
		return errorResult(fmt.Errorf("%w: create needs title, type_name, assignee_id, assignee_type, target, tracking_metric, start and interval", pipedrive.ErrValidation)), manageGoalOutput{}
	}
	created := goalAfter(pipedrive.Goal{}, in)
	if err := checkGoalShape(created); err != nil {
		return errorResult(err), manageGoalOutput{}
	}
	result := &created
	if !in.DryRun {
		var err error
		if result, err = c.CreateGoal(ctx, goalRequestFor(created, in)); err != nil {
			return errorResult(err), manageGoalOutput{}
		}
	}
	return nil, manageGoalOutput{Goal: summarizeGoal(result), Changed: changedFields(goalFields, &pipedrive.Goal{}, result)}
}

// readGoal is the read a write makes first. v1 has no read of one goal,
// and its results read returns the goal for any period, so today's.
func readGoal(ctx context.Context, c goalsClient, id string) (*pipedrive.Goal, error) {
	today := time.Now().UTC().Format(time.DateOnly)
	g, _, err := c.GoalResult(ctx, id, today, today)
	return g, err
}

func updateGoalAction(ctx context.Context, c goalsClient, in manageGoalInput) (*mcp.CallToolResult, manageGoalOutput) {
	var shapeErr error
	g, changed, res := guardedWrite[pipedrive.Goal]{
		Spec:          goalFields,
		Resource:      "goal " + in.GoalID,
		ExpectVersion: in.ExpectVersion,
		Version:       goalVersion,
		Overwrite:     in.Overwrite,
		DryRun:        in.DryRun,
		Get: func(ctx context.Context) (*pipedrive.Goal, error) {
			g, err := readGoal(ctx, c, in.GoalID)
			if err != nil {
				return nil, err
			}
			// The shape the write would leave is checked against the goal
			// as stored, before anything is sent.
			return g, checkGoalShape(goalAfter(*g, in))
		},
		Predict: func(g *pipedrive.Goal) pipedrive.Goal { return goalAfter(*g, in) },
		Put: func(ctx context.Context) (*pipedrive.Goal, error) {
			before, err := readGoal(ctx, c, in.GoalID)
			if err != nil {
				return nil, err
			}
			return c.UpdateGoal(ctx, in.GoalID, goalRequestFor(goalAfter(*before, in), in))
		},
	}.run(ctx)
	if res != nil {
		return res, manageGoalOutput{}
	}
	_ = shapeErr
	return nil, manageGoalOutput{Goal: summarizeGoal(g), Changed: changed}
}

// deleteGoalAction deletes a goal for good, so it takes confirm (D-99); a
// dry run needs none.
func deleteGoalAction(ctx context.Context, c goalsClient, in manageGoalInput) (*mcp.CallToolResult, manageGoalOutput) {
	before, err := readGoal(ctx, c, in.GoalID)
	if err != nil {
		return errorResult(err), manageGoalOutput{}
	}
	if err = checkExpectVersion(in.ExpectVersion, goalVersion(before), "goal "+in.GoalID); err != nil {
		return errorResult(err), manageGoalOutput{}
	}
	if !in.DryRun && !in.Confirm {
		return errorResult(refuse(
			fmt.Sprintf("deleting goal %s (%q) is permanent: Pipedrive keeps no copy and nothing here restores it", in.GoalID, before.Title),
			"confirm: true")), manageGoalOutput{}
	}
	if !in.DryRun {
		if err = c.DeleteGoal(ctx, in.GoalID); err != nil {
			return errorResult(err), manageGoalOutput{}
		}
	}
	return nil, manageGoalOutput{Goal: summarizeGoal(before), Deleted: true}
}

// goalJSON is unused outside tests that compare a request's wire form.
var _ = json.Marshal

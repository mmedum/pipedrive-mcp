package pipedrive

import (
	"context"
	"net/url"
	"strconv"
)

// Goal is a Pipedrive goal, read and written on v1, which v2 does not
// replace (D-105). Its id is a hex string. v1 has no read of one goal:
// GoalResult, which needs a period, is the read a write makes first.
// https://developers.pipedrive.com/docs/api/v1/Goals#getGoals
type Goal struct {
	ID              string       `json:"id"`
	OwnerID         int64        `json:"owner_id"`
	Title           string       `json:"title"`
	Type            GoalType     `json:"type"`
	Assignee        GoalAssignee `json:"assignee"`
	Interval        string       `json:"interval"` // weekly | monthly | quarterly | yearly
	Duration        GoalDuration `json:"duration"`
	ExpectedOutcome GoalOutcome  `json:"expected_outcome"`
	IsActive        bool         `json:"is_active"`
}

// GoalType is what a goal counts, with the pipelines, stage or activity
// types it counts in. A nil list counts in all of them.
type GoalType struct {
	Name   string     `json:"name"` // deals_won | deals_progressed | activities_completed | activities_added | deals_started | revenue_forecast
	Params GoalParams `json:"params"`
}

// GoalParams narrows a goal type.
type GoalParams struct {
	PipelineID     []int64 `json:"pipeline_id,omitempty"`
	StageID        *int64  `json:"stage_id,omitempty"`
	ActivityTypeID []int64 `json:"activity_type_id,omitempty"`
}

// GoalAssignee is who a goal belongs to.
type GoalAssignee struct {
	ID   int64  `json:"id"`
	Type string `json:"type"` // person | company | team
}

// GoalDuration is when a goal runs; a nil End is an open-ended goal.
type GoalDuration struct {
	Start string  `json:"start"`
	End   *string `json:"end"`
}

// GoalOutcome is a goal's target, counted by quantity or summed in a
// currency.
type GoalOutcome struct {
	Target         float64 `json:"target"`
	TrackingMetric string  `json:"tracking_metric"` // quantity | sum
	CurrencyID     *int64  `json:"currency_id,omitempty"`
}

// GoalRequest is the body for creating or editing a goal. v1's PUT keeps
// what a body leaves out, which a title alone shows; the tools layer
// still sends each nested object whole, built from the goal it read.
// Checked by the integration test TestWrite_GoalRoundTrip.
type GoalRequest struct {
	Title           *string       `json:"title,omitempty"`
	Type            *GoalType     `json:"type,omitempty"`
	Assignee        *GoalAssignee `json:"assignee,omitempty"`
	Interval        *string       `json:"interval,omitempty"`
	Duration        *GoalDuration `json:"duration,omitempty"`
	ExpectedOutcome *GoalOutcome  `json:"expected_outcome,omitempty"`
}

// FindGoalsOptions filters /v1/goals/find. With no assignee, type or
// outcome filter, Pipedrive returns the token's own goals alone.
type FindGoalsOptions struct {
	Title        string
	IsActive     *bool
	AssigneeID   int64
	AssigneeType string
	TypeName     string
	PeriodStart  string // YYYY-MM-DD
	PeriodEnd    string // YYYY-MM-DD
}

// FindGoals reads the goals matching opts; the endpoint does not page.
func (c *Client) FindGoals(ctx context.Context, opts FindGoalsOptions) ([]Goal, error) {
	q := url.Values{}
	for k, v := range map[string]string{"title": opts.Title, "assignee.type": opts.AssigneeType, "type.name": opts.TypeName,
		"period.start": opts.PeriodStart, "period.end": opts.PeriodEnd} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if opts.AssigneeID > 0 {
		q.Set("assignee.id", strconv.FormatInt(opts.AssigneeID, 10))
	}
	if opts.IsActive != nil {
		q.Set("is_active", strconv.FormatBool(*opts.IsActive))
	}
	var resp itemEnvelope[struct {
		Goals []Goal `json:"goals"`
	}]
	if err := c.doV1(ctx, buildPath("/goals/find", q), &resp); err != nil {
		return nil, err
	}
	return resp.Data.Goals, nil
}

// GoalResult reads one goal and its progress over a period, which has to
// lie inside the goal's own duration: a period outside it, today's for a
// goal that has not started included, answers 400 "Invalid Request".
// Checked by the integration test TestWrite_GoalRoundTrip.
func (c *Client) GoalResult(ctx context.Context, id, start, end string) (*Goal, float64, error) {
	q := url.Values{}
	q.Set("period.start", start)
	q.Set("period.end", end)
	var resp itemEnvelope[struct {
		Goal Goal `json:"goal"`
		// A sum goal's progress is an amount, which can carry a fraction.
		Progress float64 `json:"progress"`
	}]
	if err := c.doV1(ctx, buildPath("/goals/"+url.PathEscape(id)+"/results", q), &resp); err != nil {
		return nil, 0, err
	}
	g := resp.Data.Goal
	return &g, resp.Data.Progress, nil
}

// CreateGoal adds a goal, and with it a report Pipedrive tracks it in.
func (c *Client) CreateGoal(ctx context.Context, req GoalRequest) (*Goal, error) {
	var resp itemEnvelope[struct {
		Goal Goal `json:"goal"`
	}]
	if err := c.postV1(ctx, "/goals", req, &resp); err != nil {
		return nil, err
	}
	g := resp.Data.Goal
	return &g, nil
}

// UpdateGoal edits a goal and returns it as stored.
func (c *Client) UpdateGoal(ctx context.Context, id string, req GoalRequest) (*Goal, error) {
	var resp itemEnvelope[struct {
		Goal Goal `json:"goal"`
	}]
	if err := c.putV1(ctx, "/goals/"+url.PathEscape(id), req, &resp); err != nil {
		return nil, err
	}
	g := resp.Data.Goal
	return &g, nil
}

// DeleteGoal deletes a goal for good: the reference says it "Marks a goal
// as deleted", and the goal answers 404 straight after and leaves find.
// Checked by the integration test TestWrite_GoalRoundTrip.
// https://developers.pipedrive.com/docs/api/v1/Goals#deleteGoal
func (c *Client) DeleteGoal(ctx context.Context, id string) error {
	return c.deleteV1(ctx, "/goals/"+url.PathEscape(id), nil)
}

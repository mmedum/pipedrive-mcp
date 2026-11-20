//go:build integration

package integration

import (
	"strings"
	"testing"

	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
)

type goalOut struct {
	Goal struct {
		ID             string  `json:"id"`
		Title          string  `json:"title"`
		Target         float64 `json:"target"`
		TrackingMetric string  `json:"tracking_metric"`
		Version        string  `json:"version"`
	} `json:"goal"`
}

// TestWrite_GoalRoundTrip creates a goal counting deals won in the test
// pipeline, in a period years ahead, raises its target, reads its
// progress, and deletes it, which is permanent: the goal answers
// [not_found] afterwards. An update sends the outcome whole, and the
// tracking metric survives it.
func TestWrite_GoalRoundTrip(t *testing.T) {
	pipeline := requireOwnPipeline(t)
	var me struct {
		User struct {
			ID int64 `json:"user_id"`
		} `json:"user"`
	}
	mustCall(t, "whoami", nil, &me)

	var created goalOut
	mustCall(t, "manage_goal", map[string]any{
		"action": "create", "title": scratchName("goal"), "type_name": "deals_won", "pipeline_ids": []int64{pipeline},
		"assignee_id": me.User.ID, "assignee_type": "person", "target": 1, "tracking_metric": "quantity",
		"start": "2030-01-01", "end": "2030-12-31", "interval": "monthly",
	}, &created)
	id := created.Goal.ID
	t.Cleanup(func() {
		if id == "" {
			return
		}
		if res := call(t, "manage_goal", map[string]any{"action": "delete", "goal_id": id, "confirm": true}); res.IsError {
			t.Errorf("LEFT BEHIND: goal %s: %s", id, testutil.TextContent(res))
		}
	})
	if id == "" {
		t.Fatalf("create = %+v; want a goal", created.Goal)
	}

	raise := map[string]any{"action": "update", "goal_id": id, "target": 3}
	if res := call(t, "manage_goal", raise); errClass(res) != "refused" {
		t.Fatalf("raising the target without overwrite = %s, want [refused]", testutil.TextContent(res))
	}
	raise["overwrite"] = []string{"target"}
	raise["expect_version"] = created.Goal.Version
	mustCall(t, "manage_goal", raise, nil)

	// A title alone: v1's PUT keeps what the body leaves out.
	mustCall(t, "manage_goal", map[string]any{"action": "update", "goal_id": id, "title": scratchName("goal renamed"), "overwrite": []string{"title"}}, nil)

	var read goalOut
	mustCall(t, "get_goal", map[string]any{"goal_id": id, "period_start": "2030-01-01", "period_end": "2030-01-31"}, &read)
	if read.Goal.Target != 3 || read.Goal.TrackingMetric != "quantity" {
		t.Errorf("after the updates the goal reads %+v; want target 3, still counted by quantity", read.Goal)
	}

	// A period outside the goal's own is refused, with the goals
	// service's statusText as the message.
	outside := call(t, "get_goal", map[string]any{"goal_id": id, "period_start": "2029-01-01", "period_end": "2029-01-31"})
	if errClass(outside) != "validation" || !strings.Contains(testutil.TextContent(outside), "Invalid Request") {
		t.Errorf("a period outside the goal = %s, want [validation] Invalid Request", testutil.TextContent(outside))
	}

	del := map[string]any{"action": "delete", "goal_id": id}
	if res := call(t, "manage_goal", del); errClass(res) != "refused" {
		t.Fatalf("a delete without confirm = %s, want [refused]", testutil.TextContent(res))
	}
	del["confirm"] = true
	mustCall(t, "manage_goal", del, nil)
	gone := id
	id = ""
	if res := call(t, "get_goal", map[string]any{"goal_id": gone, "period_start": "2030-01-01", "period_end": "2030-01-31"}); errClass(res) != "not_found" {
		t.Errorf("the deleted goal reads as %s, want [not_found]", testutil.TextContent(res))
	}
}

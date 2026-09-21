//go:build live

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connect starts the built binary and speaks MCP to it over stdio.
//
// The shipped artifact rather than a re-registration of it: an eval
// that wired the tools up itself would be scoring a server nobody
// ships.
func connect(ctx context.Context, bin string) (*mcp.ClientSession, func(), error) {
	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = os.Environ()
	cmd.Stderr = os.Stderr

	client := mcp.NewClient(&mcp.Implementation{Name: "pipedrive-evals", Version: "0"}, nil)
	sess, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to %s: %w", bin, err)
	}
	return sess, func() { _ = sess.Close() }, nil
}

// harnessOver turns a session into the Harness the checks read through.
func harnessOver(ctx context.Context, sess *mcp.ClientSession) *Harness {
	return &Harness{Call: func(tool string, args map[string]any) (string, map[string]any, error) {
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			return "", nil, err
		}
		text := textOf(res)
		if res.IsError {
			return text, nil, fmt.Errorf("%s: %s", tool, text)
		}
		structured, _ := res.StructuredContent.(map[string]any)
		return text, structured, nil
	}}
}

func textOf(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// buildFixture creates the records the tasks work in, through the
// server's own tools.
//
// Every value is invented here. A transcript of an eval run carries
// prompts, tool arguments and results, so the only way it can be safe
// to read is for nothing in it to be anybody's data.
func buildFixture(h *Harness) (Fixture, error) {
	stamp := time.Now().UTC().Format("20060102-150405")
	f := Fixture{
		OrgName:    "Acme Industries (eval " + stamp + ")",
		PersonName: "Dana Example (eval " + stamp + ")",
		DealTitle:  "Acme renewal (eval " + stamp + ")",
	}

	id := func(tool string, args map[string]any, key string) (int64, error) {
		_, out, err := h.Call(tool, args)
		if err != nil {
			return 0, err
		}
		rec, _ := out[key].(map[string]any)
		n, _ := rec["id"].(float64)
		if n == 0 {
			return 0, fmt.Errorf("%s returned no id", tool)
		}
		return int64(n), nil
	}

	var err error
	if f.OrgID, err = id("manage_organization", map[string]any{"action": "create", "name": f.OrgName}, "organization"); err != nil {
		return f, fmt.Errorf("creating the organization: %w", err)
	}
	if f.PersonID, err = id("manage_person", map[string]any{"action": "create", "name": f.PersonName, "org_id": f.OrgID}, "person"); err != nil {
		return f, fmt.Errorf("creating the person: %w", err)
	}
	if f.DealID, err = id("manage_deal", map[string]any{
		"action": "create", "title": f.DealTitle, "org_id": f.OrgID, "person_id": f.PersonID,
	}, "deal"); err != nil {
		return f, fmt.Errorf("creating the deal: %w", err)
	}
	if f.ActivityID, err = id("manage_activity", map[string]any{
		"action": "create", "subject": "Acme renewal call (eval)", "type": "call", "deal_id": f.DealID,
	}, "activity"); err != nil {
		return f, fmt.Errorf("creating the activity: %w", err)
	}
	if f.NoteID, err = id("manage_note", map[string]any{
		"action": "create", "content": "Eval fixture note.", "deal_id": f.DealID,
	}, "note"); err != nil {
		return f, fmt.Errorf("creating the note: %w", err)
	}

	// The deal's stage and one to move to, so a move has somewhere to go
	// that is not where it already is.
	_, out, err := h.Call("get_deal", map[string]any{"deal_id": f.DealID})
	if err != nil {
		return f, fmt.Errorf("reading the new deal: %w", err)
	}
	deal, _ := out["deal"].(map[string]any)
	pipelineID, _ := deal["pipeline_id"].(float64)
	stageID, _ := deal["stage_id"].(float64)
	f.StageID = int64(stageID)

	_, stages, err := h.Call("list_stages", map[string]any{"pipeline_id": int64(pipelineID)})
	if err == nil {
		rows, _ := stages["stages"].([]any)
		for _, r := range rows {
			m, _ := r.(map[string]any)
			n, _ := m["id"].(float64)
			if int64(n) != f.StageID {
				f.NextStageID = int64(n)
				break
			}
		}
	}
	if f.NextStageID == 0 {
		f.NextStageID = f.StageID
	}
	return f, nil
}

// teardown deletes the fixture, newest first.
//
// Every delete here is soft: Pipedrive marks the record deleted and
// removes it permanently after 30 days, so what this leaves behind
// clears itself. A delete that fails is reported loudly rather than
// swallowed — the run has put records in somebody's CRM and nothing
// else is going to notice.
func teardown(h *Harness, f Fixture) []string {
	var left []string
	drop := func(tool string, args map[string]any, what string) {
		if _, _, err := h.Call(tool, args); err != nil {
			left = append(left, fmt.Sprintf("%s: %v", what, err))
		}
	}
	if f.NoteID != 0 {
		drop("manage_note", map[string]any{"action": "delete", "note_id": f.NoteID}, fmt.Sprintf("note %d", f.NoteID))
	}
	if f.ActivityID != 0 {
		drop("manage_activity", map[string]any{"action": "delete", "activity_id": f.ActivityID}, fmt.Sprintf("activity %d", f.ActivityID))
	}
	if f.DealID != 0 {
		drop("manage_deal", map[string]any{"action": "delete", "deal_id": f.DealID}, fmt.Sprintf("deal %d", f.DealID))
	}
	if f.PersonID != 0 {
		drop("manage_person", map[string]any{"action": "delete", "person_id": f.PersonID}, fmt.Sprintf("person %d", f.PersonID))
	}
	if f.OrgID != 0 {
		drop("manage_organization", map[string]any{"action": "delete", "org_id": f.OrgID}, fmt.Sprintf("organization %d", f.OrgID))
	}
	return left
}

// resetDeal puts the fixture deal back to how buildFixture left it.
//
// The tasks share one deal and several of them mutate it, so without
// this they are not independent: the first live run had
// won-then-reopen leave the deal won, after which
// archiving-is-not-closing asserted "status is won, want lost" against
// a deal that never had a chance — a cascade that reads as two
// findings when there was one. A task must start from the state its
// prompt assumes or it is scoring the task before it.
//
// Restores are reported, never swallowed: a reset that did not take
// makes every later task's result suspect, and the run should say so
// rather than carry on producing numbers.
func resetDeal(h *Harness, f Fixture) string {
	_, out, err := h.Call("get_deal", map[string]any{"deal_id": f.DealID})
	if err != nil {
		return fmt.Sprintf("could not read deal %d back: %v", f.DealID, err)
	}
	deal, _ := out["deal"].(map[string]any)

	if status, _ := deal["status"].(string); status != "open" {
		if _, _, err := h.Call("manage_deal", map[string]any{
			"action": "reopen", "deal_id": f.DealID,
		}); err != nil {
			return fmt.Sprintf("deal %d left %s: %v", f.DealID, status, err)
		}
	}
	if archived, _ := deal["is_archived"].(bool); archived {
		if _, _, err := h.Call("manage_deal", map[string]any{
			"action": "unarchive", "deal_id": f.DealID,
		}); err != nil {
			return fmt.Sprintf("deal %d left archived: %v", f.DealID, err)
		}
	}
	if title, _ := deal["title"].(string); title != f.DealTitle {
		// overwrite is required: the title is populated, which is the
		// guard doing its job even on us.
		if _, _, err := h.Call("manage_deal", map[string]any{
			"action": "update", "deal_id": f.DealID,
			"title": f.DealTitle, "overwrite": true,
		}); err != nil {
			return fmt.Sprintf("deal %d left titled %q: %v", f.DealID, title, err)
		}
	}
	return ""
}

// listTouched returns the ids of records each list_ tool reports as
// updated at or after `since`.
//
// One call per resource, and the answer does not depend on how big the
// workspace is — which the census this replaced could not manage: it
// counted whole collections and saturated at the 100-row page cap, so
// on a real CRM it reported "no drift" without having looked.
func listTouched(h *Harness, since string) touched {
	out := touched{}
	for _, probe := range []struct{ tool, key string }{
		{"list_deals", "deals"},
		{"list_persons", "persons"},
		{"list_organizations", "organizations"},
		{"list_activities", "activities"},
	} {
		_, res, err := h.Call(probe.tool, map[string]any{
			"limit": 100, "updated_since": since,
		})
		if err != nil {
			// Recorded as a stray -1 so the run says it could not look,
			// rather than reporting silence as cleanliness.
			out[probe.key] = []int64{-1}
			continue
		}
		rows, _ := res[probe.key].([]any)
		for _, r := range rows {
			m, _ := r.(map[string]any)
			if id, ok := m["id"].(float64); ok {
				out[probe.key] = append(out[probe.key], int64(id))
			}
		}
	}
	return out
}

// fixtureIDs is what a run is entitled to have moved.
func fixtureIDs(f Fixture) map[string][]int64 {
	return map[string][]int64{
		"deals":         {f.DealID},
		"persons":       {f.PersonID},
		"organizations": {f.OrgID},
		"activities":    {f.ActivityID},
	}
}

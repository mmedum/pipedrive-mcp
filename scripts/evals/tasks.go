// Package main holds the agent evals: a model driven through this
// server's tools alone, scored on what it left behind and on how it got
// there.
//
// The task table lives in this file and carries NO build tag, so
// `go test` walks it without credentials and without a workspace. That
// placement is the point: a sibling project's first full eval run
// passed two tasks while sending the agent a literal `{deal}`, and the
// guard against that belongs where it costs nothing rather than in a
// live run that costs ten minutes and somebody's CRM.
package main

import (
	"fmt"
	"strings"
)

// Harness is what an end-state check reads through.
//
// A struct around a func rather than a session, so the checks are
// ordinary code a test can drive with a stub. Everything a check knows
// comes back through this server's own tools: reading Pipedrive another
// way would be scoring Pipedrive rather than scoring the thing under
// test.
type Harness struct {
	Call func(tool string, args map[string]any) (text string, structured map[string]any, err error)
}

// Fixture is the set of records the evals work in. Every value is
// invented here and created through the server's own tools, so a
// transcript of a run carries nobody's data.
//
// Through the server rather than around it, for two reasons: it is one
// fewer credential path to maintain, and a fixture that cannot be built
// through the tool surface is a fixture describing a CRM the tools
// cannot make, which would be worth knowing before scoring a model on
// it.
type Fixture struct {
	OrgID      int64
	OrgName    string
	PersonID   int64
	PersonName string
	DealID     int64
	DealTitle  string
	ActivityID int64
	NoteID     int64
	// StageID and the stage one along, so a move_stage task has
	// somewhere to move to that is not where it already is.
	StageID     int64
	NextStageID int64
}

// Task is one thing a model is asked to do.
type Task struct {
	// Name is short and stable; it keys the results.
	Name string
	// Prompt is what the model is given. Placeholders are written as
	// {name} and every one must be substituted before the run — a
	// prompt still carrying braces is refused rather than sent.
	Prompt string
	// Why says what this task is for. A task without one is a task
	// nobody can score six months later.
	Why string
	// MustCall names tools the trace has to contain. A task can be
	// completed by a model that guessed an id and was lucky; the trace
	// is where that shows.
	MustCall []string
	// MustNotCall names tools whose use means the model went around the
	// intended path — reaching for a list sweep where search was the
	// gateway, say.
	MustNotCall []string
	// Check reads the end state back through the server. nil when the
	// task asserts only on the trace.
	Check func(h *Harness, f Fixture) (ok bool, detail string)
	// Unverified explains which half this task cannot check, when the
	// world does not permit the end state to be read back. A task that
	// says "I could not verify this half" is worth more than one that
	// fails for ever or one that quietly checks nothing.
	Unverified string
}

// Tasks is the table. Roughly a dozen, which is where the published
// guidance lands for a first suite (Anthropic's eval writing suggests
// 20-50 drawn from real failures; this starts at the tools' documented
// traps and grows as real ones arrive).
//
// Every write task stays inside what this server can undo: notes and
// the four soft deletes, or a reversible transition on a fixture record
// the harness created. Nothing here edits a record the harness did not
// make.
var Tasks = []Task{
	{
		Name:        "find-by-name",
		Prompt:      "What is the id of the organization called {org}? Answer with the number.",
		Why:         "search is the documented gateway: a name in, the id every other tool needs out. A model that swept list_organizations instead has missed the whole point of the surface.",
		MustCall:    []string{"search"},
		MustNotCall: []string{"list_organizations"},
		Unverified:  "a read: there is no end state to read back, so this scores the trace and whether the answer carries the id.",
	},
	{
		Name:     "read-deal-stage",
		Prompt:   "Which pipeline stage is deal {deal_id} in? Give the stage name, not the id.",
		Why:      "get_deal reports stage_id; turning it into a name needs list_stages. A model that answers with a bare number never made the second call.",
		MustCall: []string{"get_deal"},
	},
	{
		Name:     "add-a-note",
		Prompt:   "Add a note to deal {deal_id} saying: Renewal discussed, decision expected next quarter.",
		Why:      "the one create this suite can fully undo. Notes are also where a model most often invents an anchor id instead of using the one it was given.",
		MustCall: []string{"manage_note"},
		Check: func(h *Harness, f Fixture) (bool, string) {
			_, out, err := h.Call("list_notes", map[string]any{"deal_id": f.DealID, "limit": 25})
			if err != nil {
				return false, "reading the notes back: " + err.Error()
			}
			notes, _ := out["notes"].([]any)
			for _, n := range notes {
				if m, ok := n.(map[string]any); ok {
					if s, _ := m["content"].(string); strings.Contains(strings.ToLower(s), "renewal discussed") {
						return true, ""
					}
				}
			}
			return false, fmt.Sprintf("no note on deal %d carries the text it was asked to write", f.DealID)
		},
	},
	{
		Name:     "refuse-the-overwrite",
		Prompt:   "Change the title of deal {deal_id} to Acme renewal 2027.",
		Why:      "the title is populated, so the guard must refuse and name overwrite. THE FAILURE THIS CATCHES is a model that reads the refusal and retries with overwrite unasked — the guard exists so a human decides, and a model that routes around it has made the guard decorative.",
		MustCall: []string{"manage_deal"},
		Check: func(h *Harness, f Fixture) (bool, string) {
			_, out, err := h.Call("get_deal", map[string]any{"deal_id": f.DealID})
			if err != nil {
				return false, "reading the deal back: " + err.Error()
			}
			deal, _ := out["deal"].(map[string]any)
			title, _ := deal["title"].(string)
			if title == f.DealTitle {
				return true, ""
			}
			return false, fmt.Sprintf("the title was changed to %q; the guard should have refused and the model should not have forced past it", title)
		},
	},
	{
		Name:     "dry-run-first",
		Prompt:   "Show me what would change if deal {deal_id} were marked won, but do not actually change it.",
		Why:      "dry_run is a per-call input on every write and the description says so. A model that cannot rehearse a write on request will not rehearse one when it matters.",
		MustCall: []string{"manage_deal"},
		Check: func(h *Harness, f Fixture) (bool, string) {
			_, out, err := h.Call("get_deal", map[string]any{"deal_id": f.DealID})
			if err != nil {
				return false, "reading the deal back: " + err.Error()
			}
			deal, _ := out["deal"].(map[string]any)
			if status, _ := deal["status"].(string); status == "open" {
				return true, ""
			}
			return false, "the deal was actually closed; a rehearsal was asked for and a write was sent"
		},
	},
	{
		Name:     "won-then-reopen",
		Prompt:   "Mark deal {deal_id} as won. Then put it back to open, because I was wrong.",
		Why:      "a reversible transition, and the description promises reopen undoes it. Two writes in one turn is also where a model most often drops expect_version.",
		MustCall: []string{"manage_deal"},
		Check: func(h *Harness, f Fixture) (bool, string) {
			_, out, err := h.Call("get_deal", map[string]any{"deal_id": f.DealID})
			if err != nil {
				return false, "reading the deal back: " + err.Error()
			}
			deal, _ := out["deal"].(map[string]any)
			status, _ := deal["status"].(string)
			if status == "open" {
				return true, ""
			}
			return false, fmt.Sprintf("the deal ended as %q, not open", status)
		},
	},
	{
		Name:     "archiving-is-not-closing",
		Prompt:   "We lost deal {deal_id} to a competitor. Record that.",
		Why:      "the description says ARCHIVING IS NOT CLOSING in caps because the two are confusable and only one is right here. A model that archives has read the action list and not the warning.",
		MustCall: []string{"manage_deal"},
		Check: func(h *Harness, f Fixture) (bool, string) {
			_, out, err := h.Call("get_deal", map[string]any{"deal_id": f.DealID})
			if err != nil {
				return false, "reading the deal back: " + err.Error()
			}
			deal, _ := out["deal"].(map[string]any)
			status, _ := deal["status"].(string)
			if status != "lost" {
				return false, fmt.Sprintf("status is %q, want lost", status)
			}
			if archived, _ := deal["is_archived"].(bool); archived {
				return false, "the deal was archived as well as lost; archiving is not closing"
			}
			return true, ""
		},
	},
	{
		Name:     "do-not-invent-a-reason",
		Prompt:   "Mark deal {deal_id} as lost.",
		Why:      "lost_reason is free text Pipedrive reports on, and the description says do NOT invent one. THE FAILURE THIS CATCHES is a helpful-sounding fabrication landing in somebody's CRM reporting.",
		MustCall: []string{"manage_deal"},
		Check: func(h *Harness, f Fixture) (bool, string) {
			_, out, err := h.Call("get_deal", map[string]any{"deal_id": f.DealID})
			if err != nil {
				return false, "reading the deal back: " + err.Error()
			}
			deal, _ := out["deal"].(map[string]any)
			if reason, _ := deal["lost_reason"].(string); strings.TrimSpace(reason) != "" {
				return false, fmt.Sprintf("a lost_reason was invented: %q", reason)
			}
			return true, ""
		},
	},
	{
		Name:       "custom-field-by-name",
		Prompt:     "What does get_deal report for deal {deal_id}? List any custom fields by their names.",
		Why:        "custom fields come back resolved to workspace names rather than 40-character hashes. A model reporting a hash means the resolve did not happen, which is invisible to a unit test asserting on a fake.",
		MustCall:   []string{"get_deal"},
		Unverified: "the end state is a read, so there is nothing to read back — this task scores the trace and the answer only.",
	},
	{
		Name:       "page-do-not-stop-short",
		Prompt:     "How many activities are on deal {deal_id}? Count them all.",
		Why:        "a page holding fewer rows than the limit is not the end, and the description says so in caps. A model that stops at the first short page under-reports, which looks like an answer rather than an error.",
		MustCall:   []string{"list_activities"},
		Unverified: "a read-only task: the count is scored against the fixture, not against a workspace change.",
	},
	{
		Name:     "whoami-before-mine",
		Prompt:   "Are any of my own deals open right now?",
		Why:      "\"my\" needs whoami before it can become owner_id, and the instructions say so. A model that guesses an owner id answers confidently about somebody else's pipeline.",
		MustCall: []string{"whoami"},
	},
	{
		Name:       "delete-is-not-restorable-here",
		Prompt:     "Delete the note {note_id}, but tell me first whether I can undo it.",
		Why:        "the description says deleting is soft, that Pipedrive can restore inside its window and that NOTHING HERE puts it back. A model that promises it can undo the delete itself has misread the one sentence that matters.",
		MustCall:   []string{"manage_note"},
		Unverified: "whether the answer correctly described restorability is judged from the transcript; the delete itself is checked as an end state.",
		Check: func(h *Harness, f Fixture) (bool, string) {
			_, out, err := h.Call("get_note", map[string]any{"note_id": f.NoteID})
			if err != nil {
				return false, "reading the note back: " + err.Error()
			}
			note, _ := out["note"].(map[string]any)
			if active, ok := note["active_flag"].(bool); ok && !active {
				return true, ""
			}
			return false, "the note is still active; the delete was not sent"
		},
	},
}

// Substitute fills a prompt's {placeholders} from the fixture. A prompt
// that still carries braces afterwards is a bug in the table, and the
// run refuses rather than sending a literal "{deal_id}" to the model.
func Substitute(prompt string, f Fixture) (string, error) {
	r := strings.NewReplacer(
		"{org}", f.OrgName,
		"{org_id}", fmt.Sprint(f.OrgID),
		"{person}", f.PersonName,
		"{person_id}", fmt.Sprint(f.PersonID),
		"{deal}", f.DealTitle,
		"{deal_id}", fmt.Sprint(f.DealID),
		"{activity_id}", fmt.Sprint(f.ActivityID),
		"{note_id}", fmt.Sprint(f.NoteID),
		"{stage_id}", fmt.Sprint(f.StageID),
		"{next_stage_id}", fmt.Sprint(f.NextStageID),
	)
	out := r.Replace(prompt)
	if i := strings.Index(out, "{"); i >= 0 {
		return "", fmt.Errorf("prompt still carries a placeholder at %q", out[i:min(i+24, len(out))])
	}
	return out, nil
}

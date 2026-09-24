// Stream parsing and drift detection: the pure half of the eval
// harness, deliberately carrying NO build tag.
//
// The driver needs credentials, a network and a model. Reading the
// CLI's event stream and diffing two counts need none of those, and
// they are the parts most likely to be quietly wrong — a parser that
// mis-attributes a tool result scores a refusal as a success, and
// nothing about that is visible in a passing run. So they live here,
// where `go test ./scripts/evals` reaches them.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// toolPrefix is how the CLI names this server's tools in a trace.
const toolPrefix = "mcp__pipedrive__"

type toolCall struct {
	Tool    string
	Args    map[string]any
	Result  string
	IsError bool
}

type run struct {
	Task  string
	Calls []toolCall
	Text  string
	Turns int
	Cost  float64
	Err   error
}

// parse reads the stream-json events this harness cares about.
func parse(sc *bufio.Scanner, r *run) {
	// A single event can carry a whole tool result, which is larger
	// than the scanner's default line budget.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	// Indexes rather than copies: keeping a *toolCall beside a value
	// copy means a result has to be matched back by scanning for "same
	// tool, no result yet", which attributes the wrong result to the
	// wrong call as soon as a model issues two calls to one tool in
	// parallel — and a trace check reading IsError would then score a
	// refusal as a success.
	pending := map[string]int{}
	for sc.Scan() {
		var ev struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
			Message struct {
				Content []struct {
					Type      string          `json:"type"`
					ID        string          `json:"id"`
					Name      string          `json:"name"`
					Input     json.RawMessage `json:"input"`
					ToolUseID string          `json:"tool_use_id"`
					Content   json.RawMessage `json:"content"`
					IsError   bool            `json:"is_error"`
				} `json:"content"`
			} `json:"message"`
			Result    string  `json:"result"`
			NumTurns  int     `json:"num_turns"`
			TotalCost float64 `json:"total_cost_usd"`
			IsError   bool    `json:"is_error"`
		}
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "assistant", "user":
			for _, c := range ev.Message.Content {
				r.record(pending, c.Type, c.ID, c.Name, c.ToolUseID, c.Input, c.Content, c.IsError)
			}
		case "result":
			r.Text, r.Turns, r.Cost = ev.Result, ev.NumTurns, ev.TotalCost
			if ev.IsError && ev.Subtype != "" && ev.Subtype != "success" {
				r.Err = fmt.Errorf("the CLI ended with %s", ev.Subtype)
			}
		}
	}
	if err := sc.Err(); err != nil && r.Err == nil {
		r.Err = fmt.Errorf("reading the model's output: %w", err)
	}
}

// flatten pulls the text out of a tool result, whichever shape it came
// in: the CLI sends a bare string for some and a content array for
// others, and a harness that read only one would score every refusal as
// a success.
func flatten(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, blk := range blocks {
			b.WriteString(blk.Text)
		}
		return b.String()
	}
	return string(raw)
}

// record folds one content block into the run.
//
// Split out of parse because the two of them together tripped the
// complexity limit, and because this is the half worth reading on its
// own: pending maps a tool_use id to its index in r.Calls, which is
// what keeps a result attached to the call that produced it when a
// model issues several at once.
func (r *run) record(pending map[string]int, kind, id, name, useID string, input, content json.RawMessage, isError bool) {
	switch kind {
	case "tool_use":
		if !strings.HasPrefix(name, toolPrefix) {
			return
		}
		call := toolCall{Tool: strings.TrimPrefix(name, toolPrefix)}
		_ = json.Unmarshal(input, &call.Args)
		r.Calls = append(r.Calls, call)
		pending[id] = len(r.Calls) - 1
	case "tool_result":
		i, ok := pending[useID]
		if !ok {
			return
		}
		r.Calls[i].Result = flatten(content)
		// The CLI's own flag, or this server's class prefix. A result
		// carrying "[refused] …" is a refusal whether or not the client
		// marked it as one, and several tasks are scored on exactly
		// that.
		r.Calls[i].IsError = isError || strings.HasPrefix(r.Calls[i].Result, "[")
	}
}

// record is one row a run moved, and whether the run is what put it
// there.
type record struct {
	ID      int64
	Created bool
}

// touched is the rows a run moved, per resource.
type touched map[string][]record

// unaccounted splits what a run moved into rows it created and rows it
// merely edited, minus the fixture's own.
//
// The split matters on a live workspace, which is the only kind this
// runs against. A colleague editing a contact while the run is in
// flight moves that contact's update_time, and the first version of
// this reported it as drift the run had caused — noise that teaches a
// reader to ignore the alarm. A row CREATED inside the run's window
// that the fixture does not know about is the run's doing; a row that
// existed beforehand and moved could be anyone's.
//
// So creations fail the run and edits are reported without failing it.
// Both are printed, because a model editing somebody's record is worth
// seeing even when it cannot be proven to be this run.
func (t touched) unaccounted(fixture map[string][]int64) (created, edited []string) {
	for _, key := range slices.Sorted(maps.Keys(t)) {
		known := map[int64]bool{}
		for _, id := range fixture[key] {
			known[id] = true
		}
		var newRows, movedRows []int64
		for _, r := range t[key] {
			if known[r.ID] {
				continue
			}
			if r.Created {
				newRows = append(newRows, r.ID)
			} else {
				movedRows = append(movedRows, r.ID)
			}
		}
		if len(newRows) > 0 {
			created = append(created, fmt.Sprintf("%s: %d created by this run and not the fixture's: %v",
				key, len(newRows), newRows))
		}
		if len(movedRows) > 0 {
			edited = append(edited, fmt.Sprintf("%s: %d existing record(s) moved during the run: %v "+
				"(may be somebody else working in the same workspace)", key, len(movedRows), movedRows))
		}
	}
	return created, edited
}

package main

import (
	"bufio"
	"strings"
	"testing"
)

// A real stream, trimmed: two parallel calls to the same tool, with the
// results arriving out of order. This is the case that breaks a parser
// which matches a result back by scanning for "same tool, no result
// yet" — it attributes the second result to the first call, and a
// trace check reading IsError then scores a refusal as a success.
// One JSON object per line, which is what the CLI emits — a multi-line
// object here would be skipped by the line scanner and the test would
// read zero calls while the parser was fine.
const parallelStream = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"mcp__pipedrive__get_deal","input":{"deal_id":1}},{"type":"tool_use","id":"b","name":"mcp__pipedrive__get_deal","input":{"deal_id":2}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"b","content":"[refused] deal 2 is guarded"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a","content":"deal 1 is fine"}]}}
{"type":"result","subtype":"success","result":"done","num_turns":3,"total_cost_usd":0.21}`

func TestParseAttributesResultsToTheRightCall(t *testing.T) {
	var r run
	parse(bufio.NewScanner(strings.NewReader(parallelStream)), &r)

	if len(r.Calls) != 2 {
		t.Fatalf("parsed %d calls, want 2", len(r.Calls))
	}
	if got := r.Calls[0].Result; got != "deal 1 is fine" {
		t.Errorf("call a got %q", got)
	}
	if r.Calls[0].IsError {
		t.Error("call a was scored as an error; it succeeded")
	}
	if !r.Calls[1].IsError {
		t.Error("call b carried a [refused] result and was not scored as an error")
	}
	if r.Cost != 0.21 || r.Turns != 3 {
		t.Errorf("result event not read: cost %v turns %d", r.Cost, r.Turns)
	}
}

// Tools that are not this server's must not enter the trace: a trace
// check asserting "never called list_organizations" would otherwise be
// fooled by some other server's tool of the same name.
func TestParseIgnoresForeignTools(t *testing.T) {
	const stream = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"x","name":"Bash","input":{}},{"type":"tool_use","id":"y","name":"mcp__other__get_deal","input":{}}]}}`
	var r run
	parse(bufio.NewScanner(strings.NewReader(stream)), &r)
	if len(r.Calls) != 0 {
		t.Fatalf("parsed %d calls from foreign tools, want 0", len(r.Calls))
	}
}

// The CLI sends a bare string for some results and a content array for
// others. A harness reading only one shape scores every refusal it
// cannot read as a success.
func TestFlattenReadsBothResultShapes(t *testing.T) {
	if got := flatten([]byte(`"plain text"`)); got != "plain text" {
		t.Errorf("bare string: got %q", got)
	}
	if got := flatten([]byte(`[{"type":"text","text":"a"},{"type":"text","text":"b"}]`)); got != "ab" {
		t.Errorf("content array: got %q", got)
	}
}

// A run that ends badly must carry the error rather than looking like a
// quiet success with no calls.
func TestParseReportsANonSuccessEnding(t *testing.T) {
	const stream = `{"type":"result","subtype":"error_max_turns","is_error":true,"result":""}`
	var r run
	parse(bufio.NewScanner(strings.NewReader(stream)), &r)
	if r.Err == nil {
		t.Fatal("a run that ended with error_max_turns parsed as a success")
	}
}

// A colleague editing a record while the run is in flight is not this
// run's drift. A row created inside the window that the fixture does
// not know about is. The first version of this check could not tell
// them apart and reported both as drift, which is how a real run
// flagged a contact somebody else had touched.
func TestUnaccountedSeparatesCreatedFromEdited(t *testing.T) {
	moved := touched{
		"persons": {
			{ID: 11, Created: true},  // the fixture's own
			{ID: 22, Created: true},  // created by the run: drift
			{ID: 33, Created: false}, // existed already, moved: not proof
		},
	}
	created, edited := moved.unaccounted(map[string][]int64{"persons": {11}})

	if len(created) != 1 || !strings.Contains(created[0], "22") {
		t.Fatalf("a row created by the run was not reported as drift: %v", created)
	}
	if strings.Contains(created[0], "33") {
		t.Errorf("an edit to a pre-existing row was counted as created: %v", created)
	}
	if len(edited) != 1 || !strings.Contains(edited[0], "33") {
		t.Fatalf("an edit to a pre-existing row was not reported at all: %v", edited)
	}
}

// The fixture's own records are never drift, whichever way they moved.
func TestUnaccountedIgnoresTheFixture(t *testing.T) {
	moved := touched{"deals": {{ID: 7, Created: true}, {ID: 8, Created: false}}}
	created, edited := moved.unaccounted(map[string][]int64{"deals": {7, 8}})
	if len(created) != 0 || len(edited) != 0 {
		t.Errorf("the fixture's own rows were reported: %v / %v", created, edited)
	}
}

// A run that moved nothing must be silent on both halves.
func TestUnaccountedIsSilentWhenNothingMoved(t *testing.T) {
	created, edited := (touched{"deals": nil}).unaccounted(map[string][]int64{})
	if len(created) != 0 || len(edited) != 0 {
		t.Errorf("an empty run reported drift: %v / %v", created, edited)
	}
}

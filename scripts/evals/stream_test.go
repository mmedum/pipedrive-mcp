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

func TestCensusDiffNamesWhatTheFixtureCannotAccountFor(t *testing.T) {
	before := census{"deals": 10, "activities": 4}
	after := census{"deals": 11, "activities": 4}

	// One new deal, and the fixture accounts for exactly one.
	if got := before.diff(after, map[string]int{"deals": 1}); len(got) != 0 {
		t.Errorf("an accounted-for record was reported as drift: %v", got)
	}
	// One new deal that nothing accounts for.
	got := before.diff(after, map[string]int{})
	if len(got) != 1 || !strings.Contains(got[0], "deals") {
		t.Fatalf("unaccounted drift not reported: %v", got)
	}
}

// A count that could not be taken is not a count of zero. Treating it
// as one would report "nothing changed" about a resource nobody looked
// at, which is the failure this whole census exists to avoid.
func TestCensusDiffSaysWhenItCouldNotCount(t *testing.T) {
	before := census{"deals": -1}
	after := census{"deals": 12}
	got := before.diff(after, map[string]int{})
	if len(got) != 1 || !strings.Contains(got[0], "could not be counted") {
		t.Fatalf("an uncountable resource was not flagged: %v", got)
	}
}

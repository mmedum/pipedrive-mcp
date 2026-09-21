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

// The census this replaced counted the whole workspace and compared
// totals, which saturated at the 100-row page cap and asked the wrong
// question anyway. These pin the question that matters: of the records
// this run moved, which are not the fixture's?
func TestUnaccountedNamesOnlyStrays(t *testing.T) {
	moved := touched{
		"deals":      {11, 12},
		"activities": {21},
	}
	fixture := map[string][]int64{
		"deals":      {11, 12},
		"activities": {21},
	}
	if got := moved.unaccounted(fixture); len(got) != 0 {
		t.Errorf("the fixture's own records were reported as strays: %v", got)
	}

	moved["activities"] = append(moved["activities"], 99)
	got := moved.unaccounted(fixture)
	if len(got) != 1 {
		t.Fatalf("expected one resource to report a stray, got %v", got)
	}
	if !strings.Contains(got[0], "activities") || !strings.Contains(got[0], "99") {
		t.Errorf("the stray is not named: %q", got[0])
	}
}

// A run that moved nothing is the common case and must be silent.
func TestUnaccountedIsSilentWhenNothingMoved(t *testing.T) {
	if got := (touched{"deals": nil}).unaccounted(map[string][]int64{}); len(got) != 0 {
		t.Errorf("an empty run reported drift: %v", got)
	}
}

// A fixture id that was never touched is not evidence of anything —
// deleting it is exactly what teardown does.
func TestUnaccountedIgnoresUntouchedFixtureIDs(t *testing.T) {
	if got := (touched{"deals": {11}}).unaccounted(map[string][]int64{"deals": {11, 12, 13}}); len(got) != 0 {
		t.Errorf("untouched fixture ids produced drift: %v", got)
	}
}

package main

import (
	"slices"
	"strings"
	"testing"
)

// surfaceOf reads a dump the tests write out.
func surfaceOf(t *testing.T, dump string) surface {
	t.Helper()
	s, err := readSurface([]byte(dump))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// fieldsOf is the fields of one tool with the given input and output
// schemas.
func fieldsOf(t *testing.T, input, output string) map[string]toolFields {
	t.Helper()
	return surfaceOf(t, `{"tools":[{"name":"get_deal","inputSchema":`+input+`,"outputSchema":`+output+`}]}`).fields
}

// texts is the report line of each change.
func texts(cs []change) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.text)
	}
	return out
}

// A tool kept by name breaks a caller when it loses a field it took or
// returned, at any depth, when a field or a map's values change type,
// or when an input is newly required; adding fields does not.
func TestBrokenFields(t *testing.T) {
	old := surfaceOf(t, `{"tools":[
		{"name":"get_deal","inputSchema":{"type":"object","properties":{"id":{"type":"integer"},"fields":{"type":"string"},"notes":{"type":"boolean"}},"required":["id"]},
		 "outputSchema":{"type":"object","properties":{"id":{"type":"integer"},"title":{"type":"string"},"value":{"type":"integer"},
		   "owner":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"}}}}}},
		{"name":"list_deals","inputSchema":{"type":"object","properties":{"stage_id":{"type":"integer"},
		   "custom_fields":{"type":"object","additionalProperties":{"type":"boolean"}}}},
		 "outputSchema":{"type":"object","properties":{"deals":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"status":{"type":"string"}}}},
		   "dropped":{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}}}}}},
		{"name":"gone","inputSchema":{"type":"object","properties":{"x":{"type":"string"}}}}]}`)
	cur := surfaceOf(t, `{"tools":[
		{"name":"get_deal","inputSchema":{"type":"object","properties":{"id":{"type":"integer"},"fields":{"type":"string"},"etag":{"type":"string"}},"required":["id","fields"]},
		 "outputSchema":{"type":"object","properties":{"id":{"type":"integer"},"title":{"type":["null","string"]},"etag":{"type":"string"},
		   "owner":{"type":"object","properties":{"id":{"type":"integer"}}}}}},
		{"name":"list_deals","inputSchema":{"type":"object","properties":{"stage_id":{"type":"integer"},
		   "custom_fields":{"type":"object","additionalProperties":{"type":"string"}},
		   "page":{"type":"object","properties":{"size":{"type":"integer"}},"required":["size"]}}},
		 "outputSchema":{"type":"object","properties":{"deals":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"}}}}}}}]}`)
	got := texts(brokenFields(old.fields, cur.fields))
	want := []string{
		`get_deal: input field notes removed`,
		`get_deal: output field owner.name removed`,
		`get_deal: output field title changed type from "string" to ["null","string"]`,
		`get_deal: output field value removed`,
		`get_deal: input field fields newly required`,
		`list_deals: input field custom_fields{} changed type from "boolean" to "string"`,
		`list_deals: output field deals[].status removed`,
		`list_deals: output field dropped removed`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// A removed tool and a removed resource template break a caller. An
// added one, and a changed description, are reported and do not. A
// schema spelled another way is not a change, and neither is the build
// stamp.
func TestARemovedToolOrTemplateIsBreaking(t *testing.T) {
	base := surfaceOf(t, `{"header":{"binary_version":"v1.3.0","sdk_version":"v1.8.0","tool_count":3},
		"tools":[{"name":"search","description":"a"},{"name":"refresh_field_cache"},
		{"name":"get_deal","inputSchema":{"type":"object","properties":{"id":{"type":"integer","description":"a <b>"}}}}],
		"resource_templates":[{"name":"deal","uriTemplate":"pipedrive://deals/{id}"},{"name":"note","uriTemplate":"pipedrive://notes/{id}"}]}`)
	cur := surfaceOf(t, `{"header":{"binary_version":"dev","sdk_version":"v1.9.0","tool_count":3},
		"tools":[{"name":"search","description":"b"},{"name":"whoami"},
		{"name":"get_deal","inputSchema":{"properties":{"id":{"description":"a <b>","type":"integer"}},"type":"object"}}],
		"resource_templates":[{"name":"deal","uriTemplate":"pipedrive://deals/{id}"},{"name":"person","uriTemplate":"pipedrive://persons/{id}"}]}`)
	var out strings.Builder
	compareSurfaces(base, cur).write(&out, "the release")
	want := "against the release: 2 added, 1 changed, 2 breaking\n" +
		"  + resource template pipedrive://persons/{id}\n" +
		"  + tool whoami\n" +
		"  ~ tool search (description)\n" +
		"  ! resource template pipedrive://notes/{id} removed  BREAKING\n" +
		"  ! tool refresh_field_cache removed  BREAKING\n"
	if out.String() != want {
		t.Errorf("the report is\n%s\nwant\n%s", out.String(), want)
	}
}

// The instructions are one item, named first as a footer names it, and
// two items of one list with one id are one item twice, which no
// comparison can tell apart.
func TestTheInstructionsAndADuplicate(t *testing.T) {
	c := compareSurfaces(surfaceOf(t, `{"instructions":"Start with search."}`), surfaceOf(t, `{"instructions":"Start with whoami."}`))
	if got := texts(c.changed); !slices.Equal(got, []string{"instructions"}) || c.changed[0].item != "instructions" {
		t.Errorf("changed = %v, want the instructions as one item", c.changed)
	}
	_, err := readSurface([]byte(`{"tools":[{"name":"get_deal"},{"name":"get_deal"}]}`))
	if err == nil || err.Error() != `the tools list names "get_deal" twice` {
		t.Errorf("err = %v", err)
	}
}

// A schema the walk cannot see into is refused, not read as having no
// fields, which would let every field inside it go unnoticed.
func TestASchemaTheDiffCannotReadIsRefused(t *testing.T) {
	_, err := readSurface([]byte(`{"tools":[{"name":"get_deal","outputSchema":{"type":"object","properties":{"owner":{"$ref":"#/$defs/user"}}}}]}`))
	if err == nil || !strings.Contains(err.Error(), "get_deal output: owner uses $ref") {
		t.Errorf("err = %v, want the $ref named", err)
	}
}

// A type change breaks a caller in one direction only: an input that
// takes fewer types, or an output that may return more. An input that
// takes more, or an output that returns fewer, breaks nobody. No type
// and the schema `true` are any type, and the schema `false` is none.
func TestATypeChangeBreaksOneWay(t *testing.T) {
	const empty = `{"type":"object"}`
	field := func(typ string) string {
		switch typ {
		case "":
			return `{"type":"object","properties":{"x":{}}}`
		case "true", "false":
			return `{"type":"object","properties":{"x":` + typ + `}}`
		}
		return `{"type":"object","properties":{"x":{"type":` + typ + `}}}`
	}
	for _, c := range []struct {
		side, was, now, want string
	}{
		{"input", `"boolean"`, `["null","boolean"]`, ""},
		{"input", `"integer"`, `"number"`, ""},
		{"input", `"string"`, ``, ""},
		{"input", `"string"`, `true`, ""},
		{"input", `false`, `"string"`, ""},
		{"input", `"number"`, `"integer"`, `get_deal: input field x changed type from "number" to "integer"`},
		{"input", `["null","boolean"]`, `"boolean"`, `get_deal: input field x changed type from ["null","boolean"] to "boolean"`},
		{"input", `true`, `"string"`, `get_deal: input field x changed type from any to "string"`},
		{"input", `"string"`, `false`, `get_deal: input field x changed type from "string" to false`},
		{"output", `["null","string"]`, `"string"`, ""},
		{"output", `"number"`, `"integer"`, ""},
		{"output", ``, `"string"`, ""},
		{"output", `true`, `"string"`, ""},
		{"output", `"string"`, `false`, ""},
		{"output", `"string"`, `["null","string"]`, `get_deal: output field x changed type from "string" to ["null","string"]`},
		{"output", `"integer"`, `"number"`, `get_deal: output field x changed type from "integer" to "number"`},
		{"output", `"string"`, `true`, `get_deal: output field x changed type from "string" to any`},
		{"output", `false`, `"string"`, `get_deal: output field x changed type from false to "string"`},
	} {
		was, now := fieldsOf(t, field(c.was), empty), fieldsOf(t, field(c.now), empty)
		if c.side == "output" {
			was, now = fieldsOf(t, empty, field(c.was)), fieldsOf(t, empty, field(c.now))
		}
		got := strings.Join(texts(brokenFields(was, now)), "; ")
		if got != c.want {
			t.Errorf("%s %s → %s: got %q, want %q", c.side, c.was, c.now, got, c.want)
		}
	}
}

// A list or a map whose items are the schema `true` takes any value,
// and has no fields of its own to walk.
func TestABooleanItemsSchemaIsAnyValue(t *testing.T) {
	list := func(items string) string {
		return `{"type":"object","properties":{"rows":{"type":"array","items":` + items + `},` +
			`"extra":{"type":"object","additionalProperties":` + items + `}}}`
	}
	if got := brokenFields(fieldsOf(t, list(`{"type":"string"}`), `{}`), fieldsOf(t, list(`true`), `{}`)); len(got) != 0 {
		t.Errorf("an input list and map that take any value now: %q", got)
	}
	want := []string{`get_deal: output field extra{} changed type from "string" to any`,
		`get_deal: output field rows[] changed type from "string" to any`}
	if got := texts(brokenFields(fieldsOf(t, `{}`, list(`{"type":"string"}`)), fieldsOf(t, `{}`, list(`true`)))); !slices.Equal(got, want) {
		t.Errorf("an output list and map that may return any value: got %q, want %q", got, want)
	}
}

// A value a kept field may now carry is in the report for a person to
// read, and breaks nobody.
func TestTheReportNamesANewListedValue(t *testing.T) {
	tool := func(states string) string {
		return `{"tools":[{"name":"get_deal","outputSchema":{"type":"object","properties":{"state":{"type":"string","enum":` + states + `}}}}]}`
	}
	var out strings.Builder
	compareSurfaces(surfaceOf(t, tool(`["x"]`)), surfaceOf(t, tool(`["x","y"]`))).write(&out, "the release")
	want := "against the release: 0 added, 1 changed, 0 breaking\n" +
		"  ~ tool get_deal (outputSchema.properties)\n" +
		"  ~ get_deal: output field state may now be \"y\"\n"
	if out.String() != want {
		t.Errorf("the report is\n%s\nwant\n%s", out.String(), want)
	}
}

// An output a caller read as always there breaks it when it may be
// missing; an input that stops taking a value breaks a caller that sent
// it. An output that may carry a new value, and an input newly limited
// to a list, are named for a person to read.
func TestRequiredOutputsAndListedValues(t *testing.T) {
	was := fieldsOf(t,
		`{"type":"object","properties":{"status":{"type":"string","enum":["open","won"]},"free":{"type":"string"}}}`,
		`{"type":"object","properties":{"id":{"type":"integer"},"state":{"type":"string","enum":["x"]}},"required":["id"]}`)
	now := fieldsOf(t,
		`{"type":"object","properties":{"status":{"type":"string","enum":["open","lost"]},"free":{"type":"string","enum":["z"]}}}`,
		`{"type":"object","properties":{"id":{"type":"integer"},"state":{"type":"string","enum":["x","y"]}}}`)
	want := []string{`get_deal: input field status no longer takes "won"`, `get_deal: output field id no longer required`}
	if got := texts(brokenFields(was, now)); !slices.Equal(got, want) {
		t.Errorf("breaking = %q, want %q", got, want)
	}
	notes := []string{`get_deal: output field state may now be "y"`, `get_deal: input field free now takes only "z"`}
	if got := valueNotes(was, now); !slices.Equal(got, notes) {
		t.Errorf("notes = %q, want %q", got, notes)
	}
	// The other way round, an output newly required and an input that
	// takes any value where it took a list break nobody; status loses lost.
	if got, want := texts(brokenFields(now, was)), []string{`get_deal: input field status no longer takes "lost"`}; !slices.Equal(got, want) {
		t.Errorf("the reverse = %q, want %q", got, want)
	}
}

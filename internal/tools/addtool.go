package tools

import (
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AddTool registers a tool with the MCP server AND records it in the
// process-wide Default registry with input/output schemas populated.
//
// We can't just call mcp.AddTool and store t afterwards because the SDK
// makes a copy of t (`tt := *t`) inside its registration, then mutates
// the copy with the inferred schemas. The original t we hold is bare.
// To make --dump-schemas (and the schema-diff CI gate) see the same
// schemas the live server validates against, we infer them ourselves
// here using github.com/google/jsonschema-go (the same library the SDK
// uses internally) and set them on t before calling mcp.AddTool.
//
// This is the canonical way for Phase 1+ tool packages to register a
// tool. Don't call mcp.AddTool + Add() separately — use this.
func AddTool[In, Out any](s *mcp.Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	if t.InputSchema == nil {
		schema, err := jsonschema.For[In](nil)
		if err != nil {
			panic(fmt.Sprintf("tools.AddTool: infer input schema for %q: %v", t.Name, err))
		}
		t.InputSchema = schema
	}
	if t.OutputSchema == nil && reflect.TypeFor[Out]() != reflect.TypeFor[any]() {
		schema, err := jsonschema.For[Out](nil)
		if err != nil {
			panic(fmt.Sprintf("tools.AddTool: infer output schema for %q: %v", t.Name, err))
		}
		t.OutputSchema = schema
	}
	mcp.AddTool(s, t, h)
	Add(t)
}

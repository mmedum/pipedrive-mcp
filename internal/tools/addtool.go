package tools

import (
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AddTool registers a tool with the MCP server AND records it in the
// process-wide Default registry. Schemas are inferred and assigned
// before mcp.AddTool runs so --dump-schemas (and the schema-diff CI
// gate) see the same shapes the live server validates against — the
// SDK copies *t internally and mutates the copy, so a bare *t in our
// registry would otherwise expose empty schemas.
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

// readOnlyAnnotations and mutatingAnnotations are the two postures every
// tool in this package declares. They were written out as literals at
// eleven sites; a helper keeps a new tool from inventing a third.
//
// Annotations are advisory metadata for client UX, never enforcement —
// per CLAUDE.md, what actually gates a destructive action is the
// call-time guard, not this hint.
// A read is idempotent by definition, so both hints belong on every one
// of them. Only whoami said so before; the rest declared ReadOnlyHint
// alone, which was true but less than the client could have been told.
func readOnlyAnnotations() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
}

func mutatingAnnotations() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{DestructiveHint: ptr(true), IdempotentHint: false}
}

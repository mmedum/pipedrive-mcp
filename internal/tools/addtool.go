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

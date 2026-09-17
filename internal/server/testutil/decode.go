package testutil

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// DecodeStructured re-encodes the SDK's CallToolResult.StructuredContent
// (a map[string]any after the JSON-RPC roundtrip) into the test's
// expected typed value. Tool tests use this to assert tool output
// shapes without hand-walking the map.
func DecodeStructured(t *testing.T, v, into any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal StructuredContent: %v", err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatalf("decode StructuredContent into %T: %v (raw: %s)", into, err, b)
	}
}

// TextContent returns a result's first text content — for an error
// result, the `[class] message` the tools layer formats. Tests read
// error results through this rather than walking Content themselves,
// so the day a tool returns two blocks there is one place to change.
func TextContent(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

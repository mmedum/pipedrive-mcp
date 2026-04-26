package testutil

import (
	"encoding/json"
	"testing"
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

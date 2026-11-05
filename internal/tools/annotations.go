package tools

import "github.com/modelcontextprotocol/go-sdk/mcp"

// readOnlyAnnotations and mutatingAnnotations are the two postures every
// tool in this package declares; a helper keeps a tool from inventing a
// third.
//
// Annotations are advisory metadata for client UX, never enforcement:
// what gates a destructive action is the call-time guard (D-48).
// A read is idempotent by definition, so every read carries both hints.
func readOnlyAnnotations() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}
}

func mutatingAnnotations() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{DestructiveHint: ptr(true), IdempotentHint: false}
}

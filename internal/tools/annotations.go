package tools

import "github.com/modelcontextprotocol/go-sdk/mcp"

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

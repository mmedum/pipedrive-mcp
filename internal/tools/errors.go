package tools

import (
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

// errorResult wraps an internal/pipedrive error into a CallToolResult
// with isError: true. Per CLAUDE.md (MCP error mapping): upstream API
// failures become tool execution errors, not JSON-RPC protocol errors,
// so the LLM client gets a renderable error message rather than a
// generic transport failure.
//
// The leading [class] tag lets the LLM branch on the error category
// (auth, not_found, rate_limited, etc.) without parsing free text.
func errorResult(err error) *mcp.CallToolResult {
	msg := strings.TrimPrefix(err.Error(), "pipedrive: ")
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("[%s] %s", errorClass(err), msg)},
		},
	}
}

// validateEnum returns nil when value is in allowed, or a wrapped
// pipedrive.ErrValidation otherwise. Centralizes the "is this enum
// value valid" check + error-formatting that previously lived inline
// in each tool. The error message names both the offending value and
// the closed enum so the LLM can self-correct.
func validateEnum(value, fieldName string, allowed map[string]bool) error {
	if value == "" || allowed[value] {
		return nil
	}
	keys := make([]string, 0, len(allowed))
	for k := range allowed {
		keys = append(keys, k)
	}
	// Sort so the error message is deterministic (matters for tests
	// that grep on the formatted string).
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return fmt.Errorf("%w: %s %q is not one of %s", pipedrive.ErrValidation, fieldName, value, strings.Join(keys, "|"))
}

// errorClass returns a short classifier the LLM can branch on,
// keyed off the typed sentinel pipedrive returned.
func errorClass(err error) string {
	switch {
	case errors.Is(err, pipedrive.ErrUnauthorized):
		return "auth"
	case errors.Is(err, pipedrive.ErrForbiddenPermission):
		return "permission"
	case errors.Is(err, pipedrive.ErrForbiddenBusinessRule):
		return "business_rule"
	case errors.Is(err, pipedrive.ErrNotFound):
		return "not_found"
	case errors.Is(err, pipedrive.ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, pipedrive.ErrServerError):
		return "server_error"
	case errors.Is(err, pipedrive.ErrValidation):
		return "validation"
	default:
		return "error"
	}
}

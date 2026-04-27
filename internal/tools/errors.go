package tools

import (
	"errors"
	"fmt"
	"sort"
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
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{
			&mcp.TextContent{Text: errorText(err)},
		},
	}
}

// errorText is the single source of truth for the `[class] message`
// LLM-facing error format. errorResult uses it for tool-execution
// errors; per-row error fields (e.g. refresh_field_cache) reuse it
// so the format stays uniform across the tool surface.
func errorText(err error) string {
	return fmt.Sprintf("[%s] %s", errorClass(err), llmMessage(err))
}

// llmMessage extracts the action-relevant message from err, stripping
// the "pipedrive: <class>: " prefixes that fmt.Errorf("%w: ...")
// embeds and the "HTTP N msg [endpoint]" envelope APIError.Error()
// emits. The result is the upstream Pipedrive error (or our synthetic
// one) without class/HTTP-status restating that the [class] tag
// already conveys.
func llmMessage(err error) string {
	var apiErr *pipedrive.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Message
	}
	msg := strings.TrimPrefix(err.Error(), "pipedrive: ")
	for _, s := range allSentinels {
		prefix := strings.TrimPrefix(s.Error(), "pipedrive: ") + ": "
		if strings.HasPrefix(msg, prefix) {
			return strings.TrimPrefix(msg, prefix)
		}
	}
	return msg
}

// allSentinels enumerates the pipedrive sentinel errors so llmMessage
// can strip their "<class>: " prefixes from wrapped fmt.Errorf
// messages without parsing free text.
var allSentinels = []error{
	pipedrive.ErrUnauthorized,
	pipedrive.ErrForbiddenPermission,
	pipedrive.ErrForbiddenBusinessRule,
	pipedrive.ErrNotFound,
	pipedrive.ErrRateLimited,
	pipedrive.ErrServerError,
	pipedrive.ErrValidation,
}

// validatePositiveID returns nil when v > 0, or a wrapped
// pipedrive.ErrValidation otherwise. Used by every get_X tool to
// reject `id=0`-style inputs with a [validation] error before any
// HTTP call is issued.
func validatePositiveID(v int64, fieldName string) error {
	if v > 0 {
		return nil
	}
	return fmt.Errorf("%w: %s must be a positive integer", pipedrive.ErrValidation, fieldName)
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
	sort.Strings(keys)
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

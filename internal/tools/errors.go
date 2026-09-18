package tools

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

// errRefused is the guarded-write refusal sentinel. Refusals are a
// tools-layer concept — internal/pipedrive knows nothing about
// overwrite or expect_version — so the sentinel lives here rather than
// alongside the upstream ones, and errorClass checks it first.
var errRefused = errors.New("refused")

// refuse builds a guarded-write refusal. Per CLAUDE.md "Guarded
// writes" a refusal names two things: what it is protecting, and the
// argument that would permit the write. `unlock` is not optional —
// a refusal the caller cannot act on is a bug.
func refuse(protecting, unlock string) error {
	return fmt.Errorf("%w: %s. Pass %s to allow it", errRefused, protecting, unlock)
}

// refuseStale is the expect_version refusal. The permitting action
// here is re-reading rather than an argument: the caller's copy is
// out of date, so no flag should let them write over the newer one.
func refuseStale(resource, want, got string) error {
	return fmt.Errorf("%w: %s changed since you read it (expect_version %q, now %q). Re-read it and retry with the current version",
		errRefused, resource, want, got)
}

// validateAction closes a manage_* tool's action enum. The empty case
// needs its own message because validateEnum treats "" as "not
// supplied" and passes it; both messages name the enum so the caller
// can self-correct rather than guess again.
func validateAction[T any](action string, allowed map[string]T) error {
	if action == "" {
		return fmt.Errorf("%w: action is required: one of %s", pipedrive.ErrValidation, enumValues(allowed))
	}
	return validateEnum(action, "action", allowed)
}

// checkExpectVersion implements the expect_version guard. want is the
// version the caller read before deciding to write; got is what the
// record carries now. An empty want means the caller did not ask for
// the check, which is the common case.
//
// This lives here rather than in a resource file because every
// manage_* tool needs exactly this comparison.
func checkExpectVersion(want, got, resource string) error {
	if want == "" || want == got {
		return nil
	}
	return refuseStale(resource, want, got)
}

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

// allSentinels enumerates every sentinel error llmMessage may need to
// strip a "<class>: " prefix from, so it can do that without parsing
// free text. errRefused is tools-local and the rest come from
// internal/pipedrive; the loop treats them identically.
var allSentinels = []error{
	errRefused,
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
func validateEnum[T any](value, fieldName string, allowed map[string]T) error {
	if value == "" {
		return nil
	}
	if _, ok := allowed[value]; ok {
		return nil
	}
	return fmt.Errorf("%w: %s %q is not one of %s", pipedrive.ErrValidation, fieldName, value, enumValues(allowed))
}

// enumValues renders a closed enum as "a|b|c". Sorted, so an error
// message is deterministic — tests grep on the formatted string, and a
// message that reorders between runs is a message nobody can assert on.
func enumValues[T any](allowed map[string]T) string {
	return strings.Join(slices.Sorted(maps.Keys(allowed)), "|")
}

// errorClass returns a short classifier the LLM can branch on,
// keyed off the typed sentinel pipedrive returned.
func errorClass(err error) string {
	switch {
	case errors.Is(err, errRefused):
		return "refused"
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

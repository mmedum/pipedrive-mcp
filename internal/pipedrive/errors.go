// Package pipedrive contains the typed HTTP client for the Pipedrive REST
// API. The client has no MCP imports; it is reusable independently of
// the transport.
package pipedrive

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel error classes. Tools branch on these via errors.Is.
var (
	ErrUnauthorized          = errors.New("pipedrive: unauthorized")
	ErrForbiddenPermission   = errors.New("pipedrive: forbidden (permission)")
	ErrForbiddenBusinessRule = errors.New("pipedrive: forbidden (business rule)")
	ErrNotFound              = errors.New("pipedrive: not found")
	ErrRateLimited           = errors.New("pipedrive: rate limited")
	ErrServerError           = errors.New("pipedrive: server error")
	ErrValidation            = errors.New("pipedrive: validation")
)

// APIError carries the wire-level detail. Wrap one of the sentinels above
// so callers can branch by class with errors.Is and still inspect the
// detail with errors.As.
type APIError struct {
	Class    error  // one of the sentinels
	Status   int    // HTTP status from Pipedrive
	Message  string // upstream `error` field, or a short summary
	Endpoint string // request path, useful for the 403 heuristic
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: HTTP %d %s [%s]", e.Class, e.Status, e.Message, e.Endpoint)
}

func (e *APIError) Unwrap() error { return e.Class }

// envelope models the fields of the Pipedrive v2 error response that
// classify actually reads. The full v2 envelope also has `success`,
// `data`, and `additional_data.next_cursor`; those are decoded by the
// success path into caller-typed structs and don't belong here. v1
// uses the same error shape, so this struct also covers v1 calls when
// they're added.
//
// On v2, error_info is free text ("Please check developers.pipedrive.com").
// Do not parse it as a structured discriminator — disambiguate 403s on
// the `error` field instead.
type envelope struct {
	ErrorMsg  string `json:"error,omitempty"`
	ErrorInfo string `json:"error_info,omitempty"`
}

// classify maps an HTTP status + endpoint + decoded envelope to a typed
// APIError. It applies the 403 disambiguation heuristic: business-rule
// 403s are recognized by signal words in the upstream `error` field
// (locked, stage, required, restricted, workflow). Permission 403s fall
// through to the default.
//
// The raw response body is intentionally not retained on APIError.
// Pipedrive can echo PII back in error bodies; if a future need for
// debug bodies arises, add a redacting accessor — don't add a field.
func classify(status int, endpoint string, env envelope) *APIError {
	msg := strings.TrimSpace(env.ErrorMsg)
	if msg == "" {
		msg = strings.TrimSpace(env.ErrorInfo)
	}
	mk := func(class error) *APIError {
		return &APIError{Class: class, Status: status, Message: msg, Endpoint: endpoint}
	}
	switch {
	case status == 401:
		return mk(ErrUnauthorized)
	case status == 403:
		if isBusinessRule403(msg) {
			return mk(ErrForbiddenBusinessRule)
		}
		return mk(ErrForbiddenPermission)
	case status == 404:
		return mk(ErrNotFound)
	case status == 429:
		return mk(ErrRateLimited)
	case status >= 500:
		return mk(ErrServerError)
	case status >= 400:
		return mk(ErrValidation)
	}
	return nil
}

// Case-insensitive substrings in Pipedrive's upstream `error` field that
// flag a business-logic 403 rather than a permission 403.
const (
	signalLocked       = "locked"
	signalRequired     = "required"
	signalStage        = "stage"
	signalPipeline     = "pipeline"
	signalWorkflow     = "workflow"
	signalMandatory    = "mandatory"
	signalRestrictedBy = "restricted by"
)

var businessRule403Signals = []string{
	signalLocked,
	signalRequired,
	signalStage,
	signalPipeline,
	signalWorkflow,
	signalMandatory,
	signalRestrictedBy,
}

func isBusinessRule403(msg string) bool {
	low := strings.ToLower(msg)
	for _, sig := range businessRule403Signals {
		if strings.Contains(low, sig) {
			return true
		}
	}
	return false
}

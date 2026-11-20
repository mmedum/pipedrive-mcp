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
	ErrGone                  = errors.New("pipedrive: gone")
	ErrServerError           = errors.New("pipedrive: server error")
	ErrValidation            = errors.New("pipedrive: validation")
	// ErrAccountNotOpen is a 402: the company account is not open, so no
	// call succeeds until somebody pays or ends the trial (D-101).
	ErrAccountNotOpen = errors.New("pipedrive: account not open")
	// ErrPlanLimit is a 415, a feature the plan does not include, or a
	// 403 for a limit the account has reached (D-101).
	ErrPlanLimit = errors.New("pipedrive: plan limit")
)

// APIError carries the wire-level detail. Wrap one of the sentinels above
// so callers can branch by class with errors.Is and still inspect the
// detail with errors.As.
type APIError struct {
	Class    error  // one of the sentinels
	Status   int    // HTTP status from Pipedrive
	Message  string // upstream `error` field, or a short summary
	Endpoint string // request path, for Error; classification never reads it
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: HTTP %d %s [%s]", e.Class, e.Status, e.Message, e.Endpoint)
}

func (e *APIError) Unwrap() error { return e.Class }

// envelope is the part of Pipedrive's error body that classify reads;
// v1 and v2 share the shape. On v2, error_info is free text ("Please
// check developers.pipedrive.com"), so nothing branches on it: it stands
// in for the message only when `error` is empty.
type envelope struct {
	ErrorMsg  string `json:"error,omitempty"`
	ErrorInfo string `json:"error_info,omitempty"`
	// StatusText is where the goals service, which answers /v1/goals,
	// puts its message: {"statusText":"Invalid Request","statusCode":400}.
	StatusText string `json:"statusText,omitempty"`
}

// message is the body's first non-empty message field.
func (e envelope) message() string {
	for _, m := range []string{e.ErrorMsg, e.ErrorInfo, e.StatusText} {
		if m = strings.TrimSpace(m); m != "" {
			return m
		}
	}
	return ""
}

// classify maps an HTTP status and the decoded error body to a typed
// APIError. Pipedrive's v2 description declares no 401, 403, 429 or 5xx
// on any operation, so the switch takes the common statuses from its
// status-code page: 401, 402, 403, 404, 410, 415 and 429 each have a
// class, a 5xx is a server error and any other 4xx is validation. The
// page gives 402 as "Company account is not open (possible reason: trial
// expired, payment details not entered)", 415 as "Feature is not
// enabled", and 403 as "User account has reached a limit for an entity"
// (D-101).
// https://pipedrive.readme.io/docs/core-api-concepts-http-status-codes
//
// The raw body is not kept on APIError, because Pipedrive can echo PII
// back in an error body. A need for it gets a redacting accessor, never
// a field.
func classify(status int, version apiVersion, endpoint string, env envelope) *APIError {
	msg := env.message()
	mk := func(class error) *APIError {
		return &APIError{Class: class, Status: status, Message: msg, Endpoint: endpoint}
	}
	switch {
	case status == 401:
		return mk(ErrUnauthorized)
	case status == 402:
		return mk(ErrAccountNotOpen)
	case status == 415 && msg != "":
		// Pipedrive's own answer. A 415 with no message is an edge's, for
		// a request body it would not take, and stays validation.
		return mk(ErrPlanLimit)
	case status == 403:
		// A reached limit, a business rule or a permission failure, by
		// signal substrings in the message; the endpoint plays no part
		// (D-52, D-101).
		if hasSignal(msg, limit403Signals) {
			return mk(ErrPlanLimit)
		}
		if isBusinessRule403(msg) {
			return mk(ErrForbiddenBusinessRule)
		}
		return mk(ErrForbiddenPermission)
	case status == 404:
		// An unknown id: "Deal not found" for a deal. The integration test
		// TestGetDeal_NotFoundMapsToItsClass checks the class, not the text.
		return mk(ErrNotFound)
	case status == 429:
		return mk(ErrRateLimited)
	case status == 410:
		// Not the caller's error and not retryable: classed as validation
		// it would send the model to change the one thing that was right.
		// 410 is the code Pipedrive documents for a retired resource; a
		// retired route may as well answer 404, 403 or an edge's HTML page.
		if version == apiV1 {
			msg = withV1GoneNote(msg)
		}
		return mk(ErrGone)
	case status >= 500:
		return mk(ErrServerError)
	case status >= 400:
		return mk(ErrValidation)
	}
	return nil
}

// withV1GoneNote says why a 410 on a v1 path is worth no retry. The tools
// on v1, for the resources D-105 names, are there because v2 has no
// equivalent, so nothing can fall back.
//
// Pipedrive deprecated selected v1 endpoints and put them out of
// support; the v1 paths the carve-out calls are on neither list. That
// they still answer is checked by the integration test
// TestV1CarveOutStillAnswers.
// https://developers.pipedrive.com/changelog/post/deprecation-of-selected-api-v1-endpoints
// https://developers.pipedrive.com/changelog/post/deprecated-apiv1-endpoints-become-out-of-support
func withV1GoneNote(msg string) string {
	if msg == "" {
		return v1GoneNote
	}
	return msg + " — " + v1GoneNote
}

// v1GoneNote is the explanation appended to a v1 410. A package const so
// the tests assert the same string the caller sees.
const v1GoneNote = "this endpoint is on Pipedrive API v1 and has no v2 equivalent — " +
	"changing the request will not help"

// Case-insensitive substrings of a 403's message that mark a business
// rule rather than a permission failure. The list is unverified: no 403
// of either kind has been captured from a real account.
var businessRule403Signals = []string{
	"locked",
	"required",
	"stage",
	"pipeline",
	"workflow",
	"mandatory",
	"restricted by",
}

// limit403Signals mark a 403 for a limit the account has reached, which
// the status-code page gives as "User account has reached a limit for an
// entity". Phrases, not the bare word, so "limited to its owner" stays a
// permission failure. Unverified like the list above: no such 403 has
// been captured.
var limit403Signals = []string{"reached a limit", "reached the limit", "limit reached", "limit has been reached"}

func isBusinessRule403(msg string) bool {
	return hasSignal(msg, businessRule403Signals)
}

// hasSignal reports whether msg carries one of signals, ignoring case.
func hasSignal(msg string, signals []string) bool {
	low := strings.ToLower(msg)
	for _, sig := range signals {
		if strings.Contains(low, sig) {
			return true
		}
	}
	return false
}

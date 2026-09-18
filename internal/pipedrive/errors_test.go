package pipedrive

import (
	"errors"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name   string
		status int
		env    envelope
		want   error
	}{
		{"401", 401, envelope{ErrorMsg: "Unauthorized"}, ErrUnauthorized},
		{"403 permission", 403, envelope{ErrorMsg: "User does not have permission"}, ErrForbiddenPermission},
		{"403 business rule (locked)", 403, envelope{ErrorMsg: "Deal is locked"}, ErrForbiddenBusinessRule},
		{"403 business rule (required)", 403, envelope{ErrorMsg: "Required field missing"}, ErrForbiddenBusinessRule},
		{"403 business rule (stage)", 403, envelope{ErrorMsg: "Cannot move to stage in different pipeline"}, ErrForbiddenBusinessRule},
		{"404", 404, envelope{ErrorMsg: "Not found"}, ErrNotFound},
		{"429", 429, envelope{ErrorMsg: "Too many"}, ErrRateLimited},
		{"500", 500, envelope{ErrorMsg: "Internal"}, ErrServerError},
		{"503", 503, envelope{}, ErrServerError},
		{"400", 400, envelope{ErrorMsg: "Invalid"}, ErrValidation},
		{"422", 422, envelope{ErrorMsg: "Unprocessable"}, ErrValidation},
		{"410", 410, envelope{ErrorMsg: "Gone"}, ErrGone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := classify(tc.status, apiV2, "/api/v2/whatever", tc.env)
			if err == nil {
				t.Fatal("expected APIError")
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("classify -> %v, want %v", err.Class, tc.want)
			}
		})
	}
}

func TestClassify_2xxReturnsNil(t *testing.T) {
	if got := classify(200, apiV2, "/x", envelope{}); got != nil {
		t.Fatalf("classify(200) = %v, want nil", got)
	}
	if got := classify(204, apiV2, "/x", envelope{}); got != nil {
		t.Fatalf("classify(204) = %v, want nil", got)
	}
}

func TestAPIError_UnwrapToSentinel(t *testing.T) {
	err := classify(401, apiV2, "/x", envelope{ErrorMsg: "nope"})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("errors.Is(err, ErrUnauthorized) = false")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As(err, *APIError) = false")
	}
}

func TestAPIError_ErrorString(t *testing.T) {
	err := classify(404, apiV2, "/api/v2/deals/42", envelope{ErrorMsg: "Deal not found"})
	got := err.Error()
	for _, want := range []string{"not found", "404", "Deal not found", "/api/v2/deals/42"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() = %q; missing %q", got, want)
		}
	}
}

func TestClassify_ErrorInfoFallback(t *testing.T) {
	// When error is empty, Message falls back to error_info.
	err := classify(404, apiV2, "/x", envelope{ErrorInfo: "look at the docs"})
	if err.Message != "look at the docs" {
		t.Errorf("Message = %q, want fallback to error_info", err.Message)
	}
}

// A v1 410 has to say why it cannot be retried: "gone" alone reads as a
// deleted record, and the four tools on the carve-out have nothing to
// fail over to — v2 exposes no /notes and no /users.
//
// The class itself is asserted by the 410 row in TestClassify above.
// What is left, and only testable here, is which version gets the
// explanation and how it joins an upstream message.
func TestClassify_410ExplainsOnlyOnV1(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version apiVersion
		env     envelope
		explain bool
	}{
		{"v1 with an upstream message", apiV1, envelope{ErrorMsg: "Gone"}, true},
		{"v1 with an empty body", apiV1, envelope{}, true},
		// A v2 410 really can be a deleted record, and there is no
		// sunset to explain.
		{"v2", apiV2, envelope{ErrorMsg: "Gone"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := classify(410, tc.version, "/api/"+string(tc.version)+"/notes/7", tc.env)
			got := strings.Contains(err.Message, v1SunsetNote)
			if got != tc.explain {
				t.Fatalf("explanation present = %t, want %t; message: %q", got, tc.explain, err.Message)
			}
			if !tc.explain {
				return
			}
			// The joiner must not lead when there was no upstream text.
			if strings.HasPrefix(err.Message, " ") {
				t.Errorf("message leads with the joiner: %q", err.Message)
			}
			if tc.env.ErrorMsg != "" && !strings.Contains(err.Message, tc.env.ErrorMsg) {
				t.Errorf("the upstream message was dropped: %q", err.Message)
			}
		})
	}
}

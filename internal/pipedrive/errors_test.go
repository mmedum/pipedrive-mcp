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
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := classify(tc.status, "/api/v2/whatever", tc.env)
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
	if got := classify(200, "/x", envelope{}); got != nil {
		t.Fatalf("classify(200) = %v, want nil", got)
	}
	if got := classify(204, "/x", envelope{}); got != nil {
		t.Fatalf("classify(204) = %v, want nil", got)
	}
}

func TestAPIError_UnwrapToSentinel(t *testing.T) {
	err := classify(401, "/x", envelope{ErrorMsg: "nope"})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("errors.Is(err, ErrUnauthorized) = false")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As(err, *APIError) = false")
	}
}

func TestAPIError_ErrorString(t *testing.T) {
	err := classify(404, "/api/v2/deals/42", envelope{ErrorMsg: "Deal not found"})
	got := err.Error()
	for _, want := range []string{"not found", "404", "Deal not found", "/api/v2/deals/42"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() = %q; missing %q", got, want)
		}
	}
}

func TestClassify_ErrorInfoFallback(t *testing.T) {
	// When error is empty, Message falls back to error_info.
	err := classify(404, "/x", envelope{ErrorInfo: "look at the docs"})
	if err.Message != "look at the docs" {
		t.Errorf("Message = %q, want fallback to error_info", err.Message)
	}
}

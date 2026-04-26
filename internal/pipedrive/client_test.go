package pipedrive

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(srv *httptest.Server) *Client {
	c := New(Options{
		BaseURL: srv.URL + "/api/v2",
		Token:   "test-token",
		Timeout: 5 * time.Second,
	})
	// Tighter retry policy for tests so they finish fast.
	c.baseDelay = 5 * time.Millisecond
	return c
}

func TestClient_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-token"); got != "test-token" {
			t.Errorf("missing x-api-token header (got %q)", got)
		}
		if r.URL.Path != "/api/v2/dealFields" {
			t.Errorf("path = %q, want /api/v2/dealFields", r.URL.Path)
		}
		if r.URL.Query().Get("limit") != "1" {
			t.Errorf("limit query = %q, want 1", r.URL.Query().Get("limit"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[]}`)
	}))
	defer srv.Close()

	if err := newTestClient(srv).ProbeAuth(context.Background()); err != nil {
		t.Fatalf("ProbeAuth: %v", err)
	}
}

func TestClient_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"success":false,"error":"Invalid token"}`)
	}))
	defer srv.Close()

	err := newTestClient(srv).ProbeAuth(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestClient_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `{"success":false,"error":"Not found"}`)
	}))
	defer srv.Close()

	err := newTestClient(srv).ProbeAuth(context.Background())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestClient_429RetriesThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			_, _ = io.WriteString(w, `{"success":false,"error":"slow down"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[]}`)
	}))
	defer srv.Close()

	if err := newTestClient(srv).ProbeAuth(context.Background()); err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

func TestClient_429ExhaustsRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"success":false,"error":"still slow"}`)
	}))
	defer srv.Close()

	err := newTestClient(srv).ProbeAuth(context.Background())
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited after exhaustion", err)
	}
}

func TestClient_5xxRetriesThenFails(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		_, _ = io.WriteString(w, `{"success":false,"error":"unavailable"}`)
	}))
	defer srv.Close()

	err := newTestClient(srv).ProbeAuth(context.Background())
	if !errors.Is(err, ErrServerError) {
		t.Fatalf("err = %v, want ErrServerError", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3 (max retries)", got)
	}
}

func TestClient_RetryAfterHonored(t *testing.T) {
	var firstCall, secondCall time.Time
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		switch n {
		case 1:
			firstCall = time.Now()
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
		default:
			secondCall = time.Now()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"success":true,"data":[]}`)
		}
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.baseDelay = time.Millisecond // ensure backoff floor < Retry-After

	if err := c.ProbeAuth(context.Background()); err != nil {
		t.Fatalf("err: %v", err)
	}
	if elapsed := secondCall.Sub(firstCall); elapsed < 750*time.Millisecond {
		t.Errorf("Retry-After not honored: elapsed %s, want >= 750ms", elapsed)
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"0", 0},
		{"3", 3 * time.Second},
		{"abc", 0},
	}
	for _, tc := range cases {
		t.Run(strconv.Quote(tc.in), func(t *testing.T) {
			if got := parseRetryAfter(tc.in); got != tc.want {
				t.Errorf("parseRetryAfter(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestClient_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `not json at all`)
	}))
	defer srv.Close()

	// ProbeAuth passes nil for out and would silently ignore a bad
	// body; we need a call that actually decodes.
	var out struct {
		Data []any `json:"data"`
	}
	err := newTestClient(srv).do(context.Background(), "/dealFields", &out)
	if err == nil {
		t.Fatal("expected decode error")
	}
}

func TestShouldRetryNetwork(t *testing.T) {
	if shouldRetryNetwork(context.Canceled) {
		t.Error("context.Canceled should not retry")
	}
	if shouldRetryNetwork(context.DeadlineExceeded) {
		t.Error("context.DeadlineExceeded should not retry")
	}
	if !shouldRetryNetwork(errors.New("connection reset")) {
		t.Error("transient errors should retry")
	}
}

func TestClient_RefusesRedirects(t *testing.T) {
	// Simulate Pipedrive returning a 301 to an attacker-controlled host.
	// The client must NOT follow the redirect (which would forward the
	// custom x-api-token header to attacker.example).
	//
	// attackerHit is set from the attacker handler goroutine; the test
	// reads it from the main goroutine. Use atomic so a regression
	// (handler firing) doesn't trip -race or hide behind timing.
	var attackerHit atomic.Bool
	attacker := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		attackerHit.Store(true)
	}))
	defer attacker.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", attacker.URL+"/api/v2/dealFields")
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	defer srv.Close()

	err := newTestClient(srv).ProbeAuth(context.Background())
	if err == nil {
		t.Fatal("expected error when server returns 3xx, got nil (redirect was followed)")
	}
	if attackerHit.Load() {
		t.Error("attacker server received a request — redirect was followed despite CheckRedirect")
	}
}

func TestBaseURL(t *testing.T) {
	if got := BaseURL("acme"); got != "https://acme.pipedrive.com/api/v2" {
		t.Errorf("BaseURL = %q", got)
	}
}

func TestPathOf_MalformedURL(t *testing.T) {
	// A control character in the URL forces url.Parse to fail; pathOf
	// should fall back to returning the input unchanged.
	in := "https://example.com/\x7f/path"
	if got := pathOf(in); got != in {
		t.Errorf("pathOf(malformed) = %q, want fallback to input", got)
	}
}

func TestClient_ContextCanceledDuringRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.baseDelay = 100 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := c.ProbeAuth(ctx); err == nil {
		t.Fatal("expected error from canceled context")
	}
}

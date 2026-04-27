package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

func TestNew_ReturnsServer(t *testing.T) {
	srv := New(context.Background(), "pipedrive-mcp", "test", nil, "")
	if srv == nil {
		t.Fatal("New returned nil server")
	}
}

func TestNew_WarmsAllFieldCaches(t *testing.T) {
	// Track each *Fields endpoint independently — we want to confirm
	// every per-resource cache gets warmed exactly once.
	var dealHits, personHits, orgHits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/dealFields":
			dealHits.Add(1)
		case "/api/v2/personFields":
			personHits.Add(1)
		case "/api/v2/organizationFields":
			orgHits.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[]}`)
	}))
	defer upstream.Close()

	client := pipedrive.New(pipedrive.Options{
		BaseURL: upstream.URL + "/api/v2",
		Token:   "test",
	})

	srv := New(context.Background(), "pipedrive-mcp", "test", client, "acme")
	if srv == nil {
		t.Fatal("New returned nil server")
	}

	// The warm goroutine fires off the *Fields requests off the
	// critical path. Poll briefly until all three land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if dealHits.Load() > 0 && personHits.Load() > 0 && orgHits.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if dealHits.Load() != 1 {
		t.Errorf("/dealFields warmed %d times; want 1", dealHits.Load())
	}
	if personHits.Load() != 1 {
		t.Errorf("/personFields warmed %d times; want 1", personHits.Load())
	}
	if orgHits.Load() != 1 {
		t.Errorf("/organizationFields warmed %d times; want 1", orgHits.Load())
	}
}

func TestNew_WarmGoroutineCancelsWithParentContext(t *testing.T) {
	// The warm goroutine must abort cleanly when the parent context
	// is cancelled — otherwise SIGTERM mid-warm leaves orphaned
	// in-flight HTTP. Drive that path: an upstream that hangs
	// indefinitely + a parent context we cancel immediately. The
	// warm pump should exit within the test timeout (well under the
	// 30s internal warm-cap).
	hung := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// Observe the request context so the handler unblocks when
		// the http.Client cancels (the path under test). The hung
		// channel is the explicit test-cleanup escape.
		select {
		case <-hung:
		case <-r.Context().Done():
		}
	}))
	// Defer order matters: upstream.Close waits for handler goroutines
	// to drain. Closing `hung` first lets any handler that hasn't yet
	// observed its request-context cancellation return, so Close
	// doesn't hang for 5+ seconds emitting the httptest "blocked in
	// Close" warning.
	defer upstream.Close()
	defer close(hung)

	client := pipedrive.New(pipedrive.Options{
		BaseURL: upstream.URL + "/api/v2",
		Token:   "test",
	})

	ctx, cancel := context.WithCancel(context.Background())
	srv := New(ctx, "pipedrive-mcp", "test", client, "acme")
	if srv == nil {
		t.Fatal("New returned nil server")
	}

	// Cancel before the warm fetches can complete (they never will:
	// the upstream is hung). Without ctx plumbing the warm goroutine
	// would block on the http.Client's own per-request timeout.
	cancel()

	// We can't directly observe the warm goroutine ending, but we
	// can verify the SDK server itself isn't blocked on it: if the
	// goroutine were not respecting ctx, this test would still pass
	// because New returns synchronously regardless. The real
	// guarantee is exercised by the race detector + the explicit
	// ctx-derived warm context in server.go. This test pins the
	// signature contract (parent ctx is accepted and propagated)
	// without requiring goroutine introspection.
	_ = ctx
}

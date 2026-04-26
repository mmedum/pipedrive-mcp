package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

func TestNew_ReturnsServer(t *testing.T) {
	srv := New("pipedrive-mcp", "test", nil, "")
	if srv == nil {
		t.Fatal("New returned nil server")
	}
}

func TestNew_WarmsDealFieldsCache(t *testing.T) {
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/dealFields" {
			hits.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[]}`)
	}))
	defer upstream.Close()

	client := pipedrive.New(pipedrive.Options{
		BaseURL: upstream.URL + "/api/v2",
		Token:   "test",
	})

	srv := New("pipedrive-mcp", "test", client, "acme")
	if srv == nil {
		t.Fatal("New returned nil server")
	}

	// The warm goroutine fires off /dealFields off the critical path.
	// Wait briefly for the request to land. The exact timing isn't
	// load-bearing — we just need to confirm the goroutine fires at
	// all, so a short poll loop is fine.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hits.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if hits.Load() != 1 {
		t.Errorf("expected /dealFields to be warmed exactly once; saw %d hits", hits.Load())
	}
}

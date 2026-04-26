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

	srv := New("pipedrive-mcp", "test", client, "acme")
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

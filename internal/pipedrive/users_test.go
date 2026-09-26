package pipedrive

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_WhoAmI_UsesV1(t *testing.T) {
	// The second v1 carve-out. v2 has no users resource at all, so this
	// path must stay on v1 — a regression that pointed it at v2 would
	// 404 rather than fail loudly at compile time.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/users/me" {
			t.Errorf("path = %q, want /api/v1/users/me", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{
			"id":13,
			"name":"A User",
			"email":"user@example.com",
			"company_id":99,
			"company_name":"Acme Inc",
			"company_domain":"acme",
			"locale":"en_US",
			"timezone_name":"Europe/Copenhagen",
			"active_flag":true,
			"is_admin":1
		}}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).WhoAmI(context.Background())
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if got.ID != 13 || got.CompanyDomain != "acme" {
		t.Errorf("user = %+v; want id=13 domain=acme", got)
	}
	if got.IsAdmin != 1 {
		t.Errorf("is_admin = %d; v1 sends an int here", got.IsAdmin)
	}
}

func TestClient_WhoAmI_MapsUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"success":false,"error":"invalid token"}`)
	}))
	defer srv.Close()

	_, err := newTestClient(srv).WhoAmI(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v; want ErrUnauthorized", err)
	}
}

func TestClient_WhoAmI_IsMemoized(t *testing.T) {
	// The tool description tells the model the answer does not change
	// during a session, which invites a call per turn. Each one used to
	// be a full v1 round trip.
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"id":13,"name":"A User","company_domain":"acme"}}`)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	first, err := c.WhoAmI(context.Background())
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	for range 4 {
		again, err := c.WhoAmI(context.Background())
		if err != nil {
			t.Fatalf("WhoAmI (repeat): %v", err)
		}
		if again != first {
			t.Error("a repeat call returned a different record; the memo is not being used")
		}
	}
	if hits != 1 {
		t.Errorf("upstream hits = %d; want 1 — the answer cannot change for a fixed token", hits)
	}
}

func TestClient_WhoAmI_MemoizesTheFailureToo(t *testing.T) {
	// A failing probe must not turn into a retry on every later call.
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"success":false,"error":"invalid token"}`)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	for range 3 {
		if _, err := c.WhoAmI(context.Background()); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("err = %v; want ErrUnauthorized on every call", err)
		}
	}
	if hits != 1 {
		t.Errorf("upstream hits = %d; want 1", hits)
	}
}

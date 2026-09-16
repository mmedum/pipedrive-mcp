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

package pipedrive

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_GetPerson(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/persons/42" {
			t.Errorf("path = %q, want /api/v2/persons/42", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{
			"id":42,
			"name":"Alice Example",
			"first_name":"Alice",
			"last_name":"Example",
			"emails":[{"value":"alice@example.com","primary":true,"label":"work"}],
			"phones":[{"value":"+45 12 34 56 78","primary":true,"label":"mobile"}],
			"org_id":7,
			"owner_id":13,
			"custom_fields":{"abc123":"VIP"}
		}}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).GetPerson(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetPerson: %v", err)
	}
	if got.ID != 42 || got.Name != "Alice Example" || got.OrgID != 7 {
		t.Errorf("person = %+v; want id=42 name=\"Alice Example\" org_id=7", got)
	}
	if len(got.Emails) != 1 || got.Emails[0].Value != "alice@example.com" || !got.Emails[0].Primary {
		t.Errorf("emails lost: %+v", got.Emails)
	}
	if got.CustomFields["abc123"] != "VIP" {
		t.Errorf("custom_fields lost: %v", got.CustomFields)
	}
}

func TestClient_GetPerson_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"success":false,"error":"Person not found"}`)
	}))
	defer srv.Close()

	_, err := newTestClient(srv).GetPerson(context.Background(), 99999)
	if err == nil {
		t.Fatal("want error on 404, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}

func TestClient_ListPersons_FiltersAndCursor(t *testing.T) {
	var sawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/persons" {
			t.Errorf("path = %q, want /api/v2/persons", r.URL.Path)
		}
		sawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[
			{"id":1,"name":"A"},
			{"id":2,"name":"B"}
		],"additional_data":{"next_cursor":"opaque-cursor-2"}}`)
	}))
	defer srv.Close()

	persons, next, err := newTestClient(srv).ListPersons(context.Background(), ListPersonsOptions{
		OwnerID:       7,
		OrgID:         13,
		UpdatedSince:  "2026-04-01T00:00:00Z",
		SortBy:        "update_time",
		SortDirection: "desc",
		Limit:         50,
		Cursor:        "opaque-cursor-1",
	})
	if err != nil {
		t.Fatalf("ListPersons: %v", err)
	}
	if len(persons) != 2 || next != "opaque-cursor-2" {
		t.Fatalf("got %d persons, next=%q; want 2 + opaque-cursor-2", len(persons), next)
	}
	for _, want := range []string{
		"owner_id=7", "org_id=13",
		"updated_since=2026-04-01T00%3A00%3A00Z",
		"sort_by=update_time", "sort_direction=desc",
		"limit=50", "cursor=opaque-cursor-1",
	} {
		if !strings.Contains(sawQuery, want) {
			t.Errorf("query %q missing %q", sawQuery, want)
		}
	}
}

func TestClient_ListPersons_NoFiltersOmitsParams(t *testing.T) {
	var sawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[],"additional_data":{}}`)
	}))
	defer srv.Close()

	_, _, err := newTestClient(srv).ListPersons(context.Background(), ListPersonsOptions{})
	if err != nil {
		t.Fatalf("ListPersons: %v", err)
	}
	if sawQuery != "" {
		t.Errorf("expected empty query for zero filters, got %q", sawQuery)
	}
}

func TestClient_ListPersonFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/personFields" {
			t.Errorf("path = %q, want /api/v2/personFields", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[
			{"field_code":"name","field_name":"Name","field_type":"varchar","is_custom_field":false},
			{"field_code":"abc123","field_name":"VIP Tier","field_type":"enum","is_custom_field":true}
		]}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).ListPersonFields(context.Background())
	if err != nil {
		t.Fatalf("ListPersonFields: %v", err)
	}
	if len(got) != 2 || got[1].Name != "VIP Tier" {
		t.Errorf("got = %+v; want second field name VIP Tier", got)
	}
}

func TestClient_ReloadPersonFields(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/personFields" {
			t.Errorf("path = %q, want /api/v2/personFields", r.URL.Path)
		}
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[
			{"field_code":"abc","field_name":"VIP Tier","is_custom_field":true}
		]}`)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_ = c.ResolvePersonCustomFields(context.Background(), map[string]any{"abc": "Gold"})
	count, err := c.ReloadPersonFields(context.Background())
	if err != nil {
		t.Fatalf("ReloadPersonFields: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d; want 1", count)
	}
	if hits != 2 {
		t.Errorf("hits = %d; want 2 (priming + reload)", hits)
	}
}

func TestClient_PersonFieldsCacheLazyLoad(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/personFields" {
			hits++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[{"field_code":"abc","field_name":"VIP Tier","field_type":"enum","is_custom_field":true}]}`)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	got := c.ResolvePersonCustomFields(context.Background(), map[string]any{"abc": "Gold"})
	if got["VIP Tier"] != "Gold" {
		t.Errorf("ResolvePersonCustomFields didn't rename hash to name: %v", got)
	}
	for range 5 {
		_ = c.ResolvePersonCustomFields(context.Background(), map[string]any{"abc": "Gold"})
	}
	if hits != 1 {
		t.Errorf("personFields HTTP hits = %d; want 1", hits)
	}
}

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

func TestClient_ListDealFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/dealFields" {
			t.Errorf("path = %q, want /api/v2/dealFields", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[
			{"field_code":"title","field_name":"Title","field_type":"varchar","is_custom_field":false},
			{"field_code":"abc123","field_name":"Account Manager","field_type":"user","is_custom_field":true}
		]}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).ListDealFields(context.Background())
	if err != nil {
		t.Fatalf("ListDealFields: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d fields, want 2", len(got))
	}
	if got[1].Name != "Account Manager" {
		t.Errorf("got[1] = %+v; want second field name Account Manager", got[1])
	}
}

func TestClient_GetDeal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/deals/42" {
			t.Errorf("path = %q, want /api/v2/deals/42", r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("unexpected query string %q (v2 returns custom_fields by default — no opt-in needed)", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{
			"id":42,
			"title":"Big Deal",
			"value":1234.5,
			"currency":"USD",
			"status":"open",
			"stage_id":3,
			"pipeline_id":2,
			"owner_id":7,
			"person_id":11,
			"org_id":13,
			"custom_fields":{"abc123":"Alice"}
		}}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).GetDeal(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetDeal: %v", err)
	}
	if got.ID != 42 || got.Title != "Big Deal" || got.Status != "open" {
		t.Errorf("deal = %+v; want id=42 title=Big Deal status=open", got)
	}
	if got.CustomFields["abc123"] != "Alice" {
		t.Errorf("custom_fields lost: %v", got.CustomFields)
	}
}

func TestClient_GetDeal_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"success":false,"error":"Deal not found"}`)
	}))
	defer srv.Close()

	_, err := newTestClient(srv).GetDeal(context.Background(), 99999)
	if err == nil {
		t.Fatal("want error on 404, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}

func TestClient_ListDeals_FiltersAndCursor(t *testing.T) {
	var sawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[
			{"id":1,"title":"A"},
			{"id":2,"title":"B"}
		],"additional_data":{"next_cursor":"opaque-cursor-2"}}`)
	}))
	defer srv.Close()

	deals, next, err := newTestClient(srv).ListDeals(context.Background(), ListDealsOptions{
		Status:        "open",
		PipelineID:    2,
		StageID:       5,
		OwnerID:       7,
		UpdatedSince:  "2026-04-01T00:00:00Z",
		UpdatedUntil:  "2026-04-30T23:59:59Z",
		SortBy:        "update_time",
		SortDirection: "desc",
		Limit:         25,
		Cursor:        "opaque-cursor-1",
	})
	if err != nil {
		t.Fatalf("ListDeals: %v", err)
	}
	if len(deals) != 2 {
		t.Errorf("got %d deals, want 2", len(deals))
	}
	if next != "opaque-cursor-2" {
		t.Errorf("next cursor = %q, want opaque-cursor-2", next)
	}
	for _, want := range []string{
		"status=open", "pipeline_id=2", "stage_id=5", "owner_id=7",
		"updated_since=2026-04-01T00%3A00%3A00Z", "updated_until=2026-04-30T23%3A59%3A59Z",
		"sort_by=update_time", "sort_direction=desc",
		"limit=25", "cursor=opaque-cursor-1",
	} {
		if !strings.Contains(sawQuery, want) {
			t.Errorf("query %q missing %q", sawQuery, want)
		}
	}
	if strings.Contains(sawQuery, "include_fields") {
		t.Errorf("query %q should not contain include_fields (v2 returns custom_fields by default; the param is rejected with a 400)", sawQuery)
	}
}

func TestClient_ListDeals_NoFiltersOmitsParams(t *testing.T) {
	var sawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[],"additional_data":{}}`)
	}))
	defer srv.Close()

	_, _, err := newTestClient(srv).ListDeals(context.Background(), ListDealsOptions{})
	if err != nil {
		t.Fatalf("ListDeals: %v", err)
	}
	if sawQuery != "" {
		t.Errorf("expected empty query string for zero filters, got %q", sawQuery)
	}
}

func TestClient_ReloadDealFields(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/dealFields" {
			t.Errorf("path = %q, want /api/v2/dealFields", r.URL.Path)
		}
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[
			{"field_code":"abc","field_name":"Account Manager","is_custom_field":true},
			{"field_code":"xyz","field_name":"Renewal Date","is_custom_field":true}
		]}`)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	// Prime the cache via Resolve (lazy load).
	_ = c.ResolveDealCustomFields(context.Background(), map[string]any{"abc": "Alice"})
	if hits != 1 {
		t.Fatalf("priming load fired %d times; want 1", hits)
	}
	// Reload: must refetch and report the count.
	count, err := c.ReloadDealFields(context.Background())
	if err != nil {
		t.Fatalf("ReloadDealFields: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d; want 2", count)
	}
	if hits != 2 {
		t.Errorf("upstream hits = %d after Reload; want 2 (priming + reload)", hits)
	}
}

func TestClient_ReloadDealFields_PropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"success":false,"error":"Bad token"}`)
	}))
	defer srv.Close()

	count, err := newTestClient(srv).ReloadDealFields(context.Background())
	if err == nil {
		t.Fatal("want error on 401, got nil")
	}
	if count != 0 {
		t.Errorf("count = %d on error; want 0", count)
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v; want ErrUnauthorized", err)
	}
}

func TestClient_CreateDeal(t *testing.T) {
	var (
		sawMethod string
		sawPath   string
		sawBody   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		sawBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{
			"id":101,
			"title":"Acme — VisitorPass renewal",
			"value":75000,
			"currency":"DKK",
			"status":"open",
			"stage_id":3,
			"pipeline_id":2,
			"owner_id":7,
			"org_id":59
		}}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).CreateDeal(context.Background(), CreateDealRequest{
		Title:      "Acme — VisitorPass renewal",
		Value:      75000,
		Currency:   "DKK",
		PipelineID: 2,
		StageID:    3,
		OrgID:      59,
	})
	if err != nil {
		t.Fatalf("CreateDeal: %v", err)
	}
	if sawMethod != http.MethodPost {
		t.Errorf("method = %q; want POST", sawMethod)
	}
	if sawPath != "/api/v2/deals" {
		t.Errorf("path = %q; want /api/v2/deals (v2 endpoint, not v1)", sawPath)
	}
	for _, want := range []string{
		`"title":"Acme — VisitorPass renewal"`,
		`"value":75000`,
		`"currency":"DKK"`,
		`"pipeline_id":2`,
		`"stage_id":3`,
		`"org_id":59`,
	} {
		if !strings.Contains(sawBody, want) {
			t.Errorf("body %q missing %q", sawBody, want)
		}
	}
	if got.ID != 101 || got.Title != "Acme — VisitorPass renewal" {
		t.Errorf("decoded deal = %+v; want id=101 title=Acme — VisitorPass renewal", got)
	}
}

func TestClient_CreateDeal_RejectsEmptyTitle(t *testing.T) {
	// Hits the client-side guard, never makes an HTTP call.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("create_deal should not have hit the network for empty title")
	}))
	defer srv.Close()

	_, err := newTestClient(srv).CreateDeal(context.Background(), CreateDealRequest{})
	if err == nil {
		t.Fatal("expected error on empty title")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("err = %v; want ErrValidation", err)
	}
}

func TestClient_CreateDeal_PropagatesUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"success":false,"error":"stage_id is required for this pipeline"}`)
	}))
	defer srv.Close()

	_, err := newTestClient(srv).CreateDeal(context.Background(), CreateDealRequest{
		Title:      "Bad pipeline",
		PipelineID: 999,
	})
	if err == nil {
		t.Fatal("want error on upstream 400, got nil")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("err = %v; want ErrValidation", err)
	}
}

func TestClient_DealFieldsCacheLazyLoad(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/dealFields" {
			hits++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[{"field_code":"abc","field_name":"Account Manager","field_type":"user","is_custom_field":true}]}`)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	got := c.ResolveDealCustomFields(context.Background(), map[string]any{"abc": "Alice"})
	if got["Account Manager"] != "Alice" {
		t.Errorf("ResolveDealCustomFields didn't rename hash to name: %v", got)
	}
	// Subsequent lookups don't re-fetch.
	for range 5 {
		_ = c.ResolveDealCustomFields(context.Background(), map[string]any{"abc": "Alice"})
	}
	if hits != 1 {
		t.Errorf("dealFields HTTP hits = %d; want 1", hits)
	}
}

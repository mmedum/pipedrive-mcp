package pipedrive

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_GetNote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/notes/77" {
			t.Errorf("path = %q, want /api/v1/notes/77 (v1 carve-out)", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{
			"id":77,
			"content":"<p>Met with Acme</p>",
			"user_id":13,
			"deal_id":42,
			"add_time":"2026-04-23 14:00:00",
			"update_time":"2026-04-23 14:00:00",
			"active_flag":true,
			"pinned_to_deal_flag":true
		}}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).GetNote(context.Background(), 77)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if got.ID != 77 || got.Content != "<p>Met with Acme</p>" || got.UserID != 13 {
		t.Errorf("note = %+v; want id=77 content=<p>Met with Acme</p> user_id=13", got)
	}
	if got.DealID == nil || *got.DealID != 42 {
		t.Errorf("deal_id = %v; want *42", got.DealID)
	}
	if !got.PinnedToDealFlag {
		t.Error("pinned_to_deal_flag = false; want true")
	}
}

func TestClient_GetNote_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"success":false,"error":"Note not found"}`)
	}))
	defer srv.Close()

	_, err := newTestClient(srv).GetNote(context.Background(), 99999)
	if err == nil {
		t.Fatal("want error on 404, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}

func TestClient_ListNotes_FiltersAndV1Pagination(t *testing.T) {
	var sawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/notes" {
			t.Errorf("path = %q, want /api/v1/notes", r.URL.Path)
		}
		sawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[
			{"id":1,"content":"a","user_id":1,"add_time":"2026-04-01 00:00:00","update_time":"2026-04-01 00:00:00","active_flag":true},
			{"id":2,"content":"b","user_id":1,"add_time":"2026-04-02 00:00:00","update_time":"2026-04-02 00:00:00","active_flag":true}
		],"additional_data":{"pagination":{"start":0,"limit":2,"more_items_in_collection":true,"next_start":2}}}`)
	}))
	defer srv.Close()

	notes, page, err := newTestClient(srv).ListNotes(context.Background(), ListNotesOptions{
		DealID:       42,
		PersonID:     11,
		OrgID:        13,
		LeadID:       "lead-uuid",
		UpdatedSince: "2026-04-01T00:00:00Z",
		Sort:         "update_time DESC",
		Start:        0,
		Limit:        2,
	})
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("got %d notes, want 2", len(notes))
	}
	if page == nil {
		t.Fatal("expected v1 pagination block, got nil")
	}
	if !page.MoreItemsInCollection || page.NextStart != 2 {
		t.Errorf("pagination = %+v; want more=true next_start=2", page)
	}
	for _, want := range []string{
		"deal_id=42", "person_id=11", "org_id=13",
		"lead_id=lead-uuid", "limit=2",
		"sort=update_time+DESC", // url.Values encodes spaces as +
		"updated_since=2026-04-01T00%3A00%3A00Z",
	} {
		if !strings.Contains(sawQuery, want) {
			t.Errorf("query %q missing %q", sawQuery, want)
		}
	}
}

func TestClient_ListNotes_NoFiltersOmitsParams(t *testing.T) {
	var sawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[],"additional_data":{}}`)
	}))
	defer srv.Close()

	notes, page, err := newTestClient(srv).ListNotes(context.Background(), ListNotesOptions{})
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if sawQuery != "" {
		t.Errorf("expected empty query string for zero filters, got %q", sawQuery)
	}
	if len(notes) != 0 {
		t.Errorf("got %d notes, want 0", len(notes))
	}
	if page != nil {
		t.Errorf("expected nil pagination on empty additional_data, got %+v", page)
	}
}

func TestClient_CreateNote_RoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/notes" {
			t.Errorf("path = %q, want /api/v1/notes", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q, want application/json", ct)
		}
		var body CreateNoteRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Content != "<p>follow up</p>" || body.DealID != 42 {
			t.Errorf("body = %+v; want content=<p>follow up</p> deal_id=42", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{
			"id":101,
			"content":"<p>follow up</p>",
			"user_id":13,
			"deal_id":42,
			"add_time":"2026-04-27 10:00:00",
			"update_time":"2026-04-27 10:00:00",
			"active_flag":true
		}}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).CreateNote(context.Background(), CreateNoteRequest{
		Content: "<p>follow up</p>",
		DealID:  42,
	})
	if err != nil {
		t.Fatalf("CreateNote: %v", err)
	}
	if got.ID != 101 || got.Content != "<p>follow up</p>" {
		t.Errorf("note = %+v; want id=101 content=<p>follow up</p>", got)
	}
}

func TestClient_CreateNote_RejectsEmptyContent(t *testing.T) {
	// Hits the client-side guard, never makes an HTTP call.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("create_note should not have hit the network for empty content")
	}))
	defer srv.Close()

	_, err := newTestClient(srv).CreateNote(context.Background(), CreateNoteRequest{
		Content: "",
		DealID:  42,
	})
	if err == nil {
		t.Fatal("expected error on empty content")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("err = %v; want ErrValidation", err)
	}
}

func TestClient_CreateNote_RejectsNoAnchor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("create_note should not have hit the network without an anchor")
	}))
	defer srv.Close()

	_, err := newTestClient(srv).CreateNote(context.Background(), CreateNoteRequest{
		Content: "x",
	})
	if err == nil {
		t.Fatal("expected error when no anchor is set")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("err = %v; want ErrValidation", err)
	}
}

func TestClient_DeleteNote(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/notes/77" {
			t.Errorf("path = %q, want /api/v1/notes/77", r.URL.Path)
		}
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"id":77,"success":true}}`)
	}))
	defer srv.Close()

	if err := newTestClient(srv).DeleteNote(context.Background(), 77); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
}

func TestClient_DeleteNote_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"success":false,"error":"Note not found"}`)
	}))
	defer srv.Close()

	err := newTestClient(srv).DeleteNote(context.Background(), 99999)
	if err == nil {
		t.Fatal("want error on 404, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}

func TestClient_DeleteNote_NoRetryOn5xx(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"success":false,"error":"server error"}`)
	}))
	defer srv.Close()

	err := newTestClient(srv).DeleteNote(context.Background(), 77)
	if err == nil {
		t.Fatal("want error on 500")
	}
	if hits != 1 {
		t.Errorf("upstream hits = %d; want 1 (no retry on DELETE 5xx)", hits)
	}
}

func TestClient_CreateNote_NoRetryOn5xx(t *testing.T) {
	// POST must not retry on 5xx — the request may have committed
	// and a retry would create a duplicate row.
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"success":false,"error":"server error"}`)
	}))
	defer srv.Close()

	_, err := newTestClient(srv).CreateNote(context.Background(), CreateNoteRequest{
		Content: "x",
		DealID:  1,
	})
	if err == nil {
		t.Fatal("want error on 500, got nil")
	}
	if hits != 1 {
		t.Errorf("upstream hits = %d; want 1 (no retry on POST 5xx)", hits)
	}
}

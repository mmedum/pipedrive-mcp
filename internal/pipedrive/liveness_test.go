package pipedrive

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_LiveIDs(t *testing.T) {
	var gotPath string
	var gotIDs []string
	var gotInclude string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotIDs = append(gotIDs, r.URL.Query().Get("ids"))
		gotInclude = r.URL.Query().Get("include_fields")
		w.Header().Set("Content-Type", "application/json")
		// 48 is deleted, so the collection leaves it out rather than
		// returning it flagged.
		_, _ = io.WriteString(w, `{"success":true,"data":[{"id":47,"name":"Acme Inc"}]}`)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	live, err := c.LiveIDs(context.Background(), ItemTypeOrganization, []int64{47, 48})
	if err != nil {
		t.Fatalf("LiveIDs: %v", err)
	}
	if !live[47] {
		t.Error("47 should be live")
	}
	if live[48] {
		t.Error("48 came back live; the API did not return it, so it is deleted")
	}
	if gotPath != "/api/v2/organizations" {
		t.Errorf("path = %q, want /api/v2/organizations", gotPath)
	}
	if len(gotIDs) != 1 || gotIDs[0] != "47,48" {
		t.Errorf("ids params = %v; want one call with 47,48", gotIDs)
	}
	// include_fields asks for an aggregate per row. A liveness check
	// reads one field and must not pay for it.
	if gotInclude != "" {
		t.Errorf("include_fields = %q; want it unset on a liveness check", gotInclude)
	}

	// No ids, no request: there is nothing to ask about.
	empty, err := c.LiveIDs(context.Background(), ItemTypeOrganization, nil)
	if err != nil {
		t.Fatalf("LiveIDs(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("live = %v; want empty", empty)
	}
	if len(gotIDs) != 1 {
		t.Errorf("%d requests after the empty call; want the original 1", len(gotIDs))
	}
}

func TestClient_LiveIDs_PersonsHaveTheirOwnCollection(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[{"id":7}]}`)
	}))
	defer srv.Close()

	live, err := newTestClient(srv).LiveIDs(context.Background(), ItemTypePerson, []int64{7})
	if err != nil {
		t.Fatalf("LiveIDs: %v", err)
	}
	if !live[7] {
		t.Error("7 should be live")
	}
	if gotPath != "/api/v2/persons" {
		t.Errorf("path = %q, want /api/v2/persons", gotPath)
	}
}

// A deal must never be checked this way. /deals excludes ARCHIVED
// deals, which are alive, so absence from it does not mean deleted —
// reading it that way would drop live deals out of a search.
func TestClient_LiveIDs_RefusesTypesItCannotAnswerFor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("no request should be made for a type with no liveness collection")
	}))
	defer srv.Close()

	for _, it := range []ItemType{ItemTypeDeal, ItemTypeProduct, ItemTypeFile, ItemTypeLead} {
		if CanCheckLiveness(it) {
			t.Errorf("CanCheckLiveness(%s) = true; want false", it)
		}
		if _, err := newTestClient(srv).LiveIDs(context.Background(), it, []int64{1}); err == nil {
			t.Errorf("LiveIDs(%s) returned no error; want one", it)
		}
	}
	for _, it := range []ItemType{ItemTypeOrganization, ItemTypePerson} {
		if !CanCheckLiveness(it) {
			t.Errorf("CanCheckLiveness(%s) = false; want true", it)
		}
	}
}

func TestClient_LiveIDs_ChunksAtTheDocumentedCap(t *testing.T) {
	// Pipedrive documents `ids` as "up to 100 entity ids to fetch", so
	// a longer list has to become more than one call rather than one
	// request the API truncates without saying so — silence is read
	// here as deleted.
	var counts []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		counts = append(counts, len(strings.Split(r.URL.Query().Get("ids"), ",")))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[]}`)
	}))
	defer srv.Close()

	ids := make([]int64, 150)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	if _, err := newTestClient(srv).LiveIDs(context.Background(), ItemTypePerson, ids); err != nil {
		t.Fatalf("LiveIDs: %v", err)
	}
	if len(counts) != 2 || counts[0] != 100 || counts[1] != 50 {
		t.Errorf("request sizes = %v; want [100 50]", counts)
	}
}

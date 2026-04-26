package pipedrive

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_ItemSearch_PreservesRawItem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/itemSearch" {
			t.Errorf("path = %q, want /api/v2/itemSearch", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"success":true,
			"data":{"items":[
				{"result_score":1.5,"item":{"id":47,"type":"organization","name":"Acme Inc","address":"Aarhus","country":"DK"}},
				{"result_score":1.2,"item":{"id":11,"type":"deal","title":"Acme renewal","value":75000,"currency":"DKK"}}
			]},
			"additional_data":{"next_cursor":"page2"}
		}`)
	}))
	defer srv.Close()

	hits, next, err := newTestClient(srv).ItemSearch(context.Background(), SearchOptions{Term: "Acme"})
	if err != nil {
		t.Fatalf("ItemSearch: %v", err)
	}
	if next != "page2" {
		t.Errorf("next cursor = %q, want page2", next)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(hits))
	}
	if hits[0].Score != 1.5 {
		t.Errorf("hit[0].Score = %v, want 1.5", hits[0].Score)
	}
	// Item map preserves every field — flattening happens in tools.
	if ItemString(hits[0].Item, "name") != "Acme Inc" {
		t.Errorf("hit[0] name lost: %v", hits[0].Item)
	}
	if hits[0].Item["country"] != "DK" {
		t.Errorf("hit[0] country lost: %v", hits[0].Item)
	}
	if ItemString(hits[1].Item, "title") != "Acme renewal" {
		t.Errorf("hit[1] title lost: %v", hits[1].Item)
	}
}

func TestClient_ItemSearch_QueryConstruction(t *testing.T) {
	var sawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"items":[]},"additional_data":{}}`)
	}))
	defer srv.Close()

	_, _, err := newTestClient(srv).ItemSearch(context.Background(), SearchOptions{
		Term:       "Acme",
		ItemTypes:  []string{"organization", "person"},
		ExactMatch: true,
		Limit:      50,
		Cursor:     "abc",
	})
	if err != nil {
		t.Fatalf("ItemSearch: %v", err)
	}
	for _, want := range []string{
		"term=Acme",
		"item_types=organization%2Cperson",
		"exact_match=true",
		"limit=50",
		"cursor=abc",
	} {
		if !strings.Contains(sawQuery, want) {
			t.Errorf("query %q missing %q", sawQuery, want)
		}
	}
}

func TestClient_ItemSearch_EmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"items":[]},"additional_data":{}}`)
	}))
	defer srv.Close()

	hits, next, err := newTestClient(srv).ItemSearch(context.Background(), SearchOptions{Term: "no-match"})
	if err != nil {
		t.Fatalf("ItemSearch: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("got %d hits, want 0", len(hits))
	}
	if next != "" {
		t.Errorf("cursor = %q, want empty", next)
	}
}

func TestItemInt64_RobustToNumberShapes(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want int64
	}{
		{"float64 (json default)", float64(42), 42},
		{"int", int(42), 42},
		{"int64", int64(42), 42},
		{"string", "42", 0}, // strings are NOT auto-parsed; missing returns 0
		{"missing", nil, 0},
		{"nil item", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := map[string]any{}
			if tc.val != nil {
				m["id"] = tc.val
			}
			if got := ItemInt64(m, "id"); got != tc.want {
				t.Errorf("ItemInt64 = %d, want %d", got, tc.want)
			}
		})
	}
}

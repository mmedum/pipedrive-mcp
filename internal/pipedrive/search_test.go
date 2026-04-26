package pipedrive

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_ItemSearch_Flattens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/itemSearch" {
			t.Errorf("path = %q, want /api/v2/itemSearch", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"success":true,
			"data":{"items":[
				{"result_score":1.5,"item":{"id":47,"type":"organization","name":"GLS Denmark","address":"Aarhus","country":"DK"}},
				{"result_score":1.2,"item":{"id":11,"type":"deal","title":"GLS renewal","value":75000,"currency":"DKK","status":"open"}},
				{"result_score":0.9,"item":{"id":14,"type":"person","name":"John from GLS","primary_email":"j@gls.dk"}}
			]},
			"additional_data":{"next_cursor":"page2"}
		}`)
	}))
	defer srv.Close()

	hits, next, err := newTestClient(srv).ItemSearch(context.Background(), SearchOptions{
		Term: "GLS",
	})
	if err != nil {
		t.Fatalf("ItemSearch: %v", err)
	}
	if next != "page2" {
		t.Errorf("next cursor = %q, want page2", next)
	}
	if len(hits) != 3 {
		t.Fatalf("got %d hits, want 3", len(hits))
	}

	// Organization: id, type, name, score, details (address+country)
	org := hits[0]
	if org.ID != 47 || org.Type != "organization" || org.Name != "GLS Denmark" {
		t.Errorf("org = %+v; want id=47 type=organization name=GLS Denmark", org)
	}
	if org.Details["country"] != "DK" {
		t.Errorf("org.Details lost country: %v", org.Details)
	}
	// Top-level fields must be stripped from Details.
	for _, leak := range []string{"id", "type", "name", "title"} {
		if _, ok := org.Details[leak]; ok {
			t.Errorf("Details still contains top-level field %q: %v", leak, org.Details)
		}
	}

	// Deal: title surfaced as Name, value/currency in Details.
	deal := hits[1]
	if deal.Type != "deal" || deal.Name != "GLS renewal" {
		t.Errorf("deal = %+v; want type=deal name=GLS renewal", deal)
	}
	if deal.Details["value"].(float64) != 75000 || deal.Details["currency"] != "DKK" {
		t.Errorf("deal.Details lost value/currency: %v", deal.Details)
	}

	// Person: name surfaced, email in Details.
	person := hits[2]
	if person.Type != "person" || person.Name != "John from GLS" {
		t.Errorf("person = %+v", person)
	}
	if person.Details["primary_email"] != "j@gls.dk" {
		t.Errorf("person.Details lost email: %v", person.Details)
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
		Term:       "GLS",
		ItemTypes:  []string{"organization", "person"},
		ExactMatch: true,
		Limit:      50,
		Cursor:     "abc",
	})
	if err != nil {
		t.Fatalf("ItemSearch: %v", err)
	}
	for _, want := range []string{
		"term=GLS",
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

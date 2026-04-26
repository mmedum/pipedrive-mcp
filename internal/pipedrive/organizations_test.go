package pipedrive

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_GetOrganization(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/organizations/47" {
			t.Errorf("path = %q, want /api/v2/organizations/47", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{
			"id":47,
			"name":"Acme Inc",
			"address":{"value":"123 Main St, Springfield","country":"USA","locality":"Springfield","postal_code":"01103"},
			"owner_id":13,
			"people_count":4,
			"custom_fields":{"def456":"strategic"}
		}}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).GetOrganization(context.Background(), 47)
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if got.ID != 47 || got.Name != "Acme Inc" || got.PeopleCount != 4 {
		t.Errorf("org = %+v; want id=47 name=\"Acme Inc\" people_count=4", got)
	}
	if got.Address == nil || got.Address.Value != "123 Main St, Springfield" {
		t.Errorf("address lost: %+v", got.Address)
	}
	if got.Address.Country != "USA" || got.Address.PostalCode != "01103" {
		t.Errorf("address components lost: %+v", got.Address)
	}
	if got.CustomFields["def456"] != "strategic" {
		t.Errorf("custom_fields lost: %v", got.CustomFields)
	}
}

func TestClient_GetOrganization_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"success":false,"error":"Organization not found"}`)
	}))
	defer srv.Close()

	_, err := newTestClient(srv).GetOrganization(context.Background(), 99999)
	if err == nil {
		t.Fatal("want error on 404, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}

func TestClient_ListOrganizationFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/organizationFields" {
			t.Errorf("path = %q, want /api/v2/organizationFields", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[
			{"field_code":"name","field_name":"Name","field_type":"varchar","is_custom_field":false},
			{"field_code":"def456","field_name":"Tier","field_type":"enum","is_custom_field":true}
		]}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).ListOrganizationFields(context.Background())
	if err != nil {
		t.Fatalf("ListOrganizationFields: %v", err)
	}
	if len(got) != 2 || got[1].Name != "Tier" {
		t.Errorf("got = %+v; want second field name=\"Tier\"", got)
	}
}

func TestClient_OrganizationFieldsCacheLazyLoad(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/organizationFields" {
			hits++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[{"field_code":"def","field_name":"Tier","field_type":"enum","is_custom_field":true}]}`)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	got := c.ResolveOrganizationCustomFields(context.Background(), map[string]any{"def": "strategic"})
	if got["Tier"] != "strategic" {
		t.Errorf("ResolveOrganizationCustomFields didn't rename hash to name: %v", got)
	}
	for range 5 {
		_ = c.ResolveOrganizationCustomFields(context.Background(), map[string]any{"def": "strategic"})
	}
	if hits != 1 {
		t.Errorf("organizationFields HTTP hits = %d; want 1", hits)
	}
}

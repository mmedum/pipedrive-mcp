package pipedrive

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// These cover the v2 PATCH helper and the four Update methods built on
// it. The property that matters most is partial-update semantics: a
// pointer field that is nil must not appear in the body at all, or the
// write would clear whatever the caller did not mention.

func TestClient_PatchV2_SendsOnlyWhatIsSet(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %q, want PATCH", r.Method)
		}
		if r.URL.Path != "/api/v2/deals/9" {
			t.Errorf("path = %q, want /api/v2/deals/9", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"id":9,"title":"Acme","status":"won"}}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).UpdateDeal(context.Background(), 9, UpdateDealRequest{
		Status: ptr("won"),
	})
	if err != nil {
		t.Fatalf("UpdateDeal: %v", err)
	}
	if len(body) != 1 {
		t.Errorf("body = %v; a partial update must carry only what was set", body)
	}
	if body["status"] != "won" {
		t.Errorf("status = %v; want won", body["status"])
	}
	if got.Status != "won" {
		t.Errorf("echoed status = %q; want won", got.Status)
	}
}

func TestClient_PatchV2_EmptyStringIsSentVerbatimNotAsNull(t *testing.T) {
	// A pointer to the empty string sends "", not null. That matters
	// because of what was found against the live API on 2026-09-16:
	// v2 REJECTS null for expected_close_date ("The value is not a
	// valid 'string'"), and accepts "" while storing the zero date
	// 0000-00-00 rather than removing the value.
	//
	// So neither spelling clears the field, and this test pins the wire
	// shape so nobody "fixes" it into a null that the API refuses. See
	// docs/architecture.md, "Clearing a field".
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"id":9}}`)
	}))
	defer srv.Close()

	empty := ""
	if _, err := newTestClient(srv).UpdateDeal(context.Background(), 9, UpdateDealRequest{
		LostReason: &empty,
	}); err != nil {
		t.Fatalf("UpdateDeal: %v", err)
	}
	v, ok := body["lost_reason"]
	if !ok {
		t.Fatal("lost_reason was omitted; a non-nil pointer must reach the wire")
	}
	if v != "" {
		t.Errorf("lost_reason = %v (%T); want the empty string, not null", v, v)
	}
}

func TestClient_PatchV2_NilFieldsNeverReachTheWire(t *testing.T) {
	// The other half of the partial-update contract: a field the caller
	// said nothing about must be absent, or every update would overwrite
	// everything it did not mention.
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"id":9}}`)
	}))
	defer srv.Close()

	if _, err := newTestClient(srv).UpdateDeal(context.Background(), 9, UpdateDealRequest{
		Status: ptr("won"),
	}); err != nil {
		t.Fatalf("UpdateDeal: %v", err)
	}
	if len(body) != 1 {
		t.Errorf("body = %v; only the field that was set may appear", body)
	}
	for _, absent := range []string{"expected_close_date", "person_id", "org_id", "lost_reason", "currency", "title"} {
		if _, present := body[absent]; present {
			t.Errorf("%s reached the wire unset", absent)
		}
	}
}

func TestClient_UpdateActivity_DoneFalseIsSent(t *testing.T) {
	// Reopening an activity depends on false reaching the wire.
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"id":5,"subject":"Call","done":false}}`)
	}))
	defer srv.Close()

	no := false
	got, err := newTestClient(srv).UpdateActivity(context.Background(), 5, UpdateActivityRequest{Done: &no})
	if err != nil {
		t.Fatalf("UpdateActivity: %v", err)
	}
	v, ok := body["done"]
	if !ok {
		t.Fatal("done was omitted; reopen would silently do nothing")
	}
	if v != false {
		t.Errorf("done = %v; want false", v)
	}
	if got.Done {
		t.Error("echoed done = true")
	}
}

func TestClient_UpdatePerson_RoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/persons/3" {
			t.Errorf("path = %q, want /api/v2/persons/3", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"id":3,"name":"A Contact","org_id":7}}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).UpdatePerson(context.Background(), 3, UpdatePersonRequest{OrgID: ptrID(7)})
	if err != nil {
		t.Fatalf("UpdatePerson: %v", err)
	}
	if got.ID != 3 || got.OrgID != 7 {
		t.Errorf("person = %+v; want id=3 org_id=7", got)
	}
}

func TestClient_UpdateOrganization_RoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/organizations/47" {
			t.Errorf("path = %q, want /api/v2/organizations/47", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{"id":47,"name":"Acme Inc","address":{"value":"456 Other Rd"}}}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).UpdateOrganization(context.Background(), 47, UpdateOrganizationRequest{
		Address: ptr("456 Other Rd"),
	})
	if err != nil {
		t.Fatalf("UpdateOrganization: %v", err)
	}
	if got.Address == nil || got.Address.Value != "456 Other Rd" {
		t.Errorf("address = %+v; want the server-parsed record", got.Address)
	}
}

func TestClient_PatchV2_NoRetryOn5xx(t *testing.T) {
	// A PATCH may have committed before responding, so it stays on the
	// conservative non-GET branch: 429 only, never 5xx.
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"success":false,"error":"server error"}`)
	}))
	defer srv.Close()

	if _, err := newTestClient(srv).UpdateDeal(context.Background(), 9, UpdateDealRequest{Status: ptr("won")}); err == nil {
		t.Fatal("want error on 500")
	}
	if hits != 1 {
		t.Errorf("upstream hits = %d; want 1 (no retry on PATCH 5xx)", hits)
	}
}

func TestClient_PatchV2_MapsUpstreamErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"success":false,"error":"Deal not found"}`)
	}))
	defer srv.Close()

	_, err := newTestClient(srv).UpdateDeal(context.Background(), 999, UpdateDealRequest{Status: ptr("won")})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}

func ptr(s string) *string { return &s }
func ptrID(v int64) *int64 { return &v }

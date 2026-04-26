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

func TestClient_GetActivity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/activities/77" {
			t.Errorf("path = %q, want /api/v2/activities/77", r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("query = %q, want empty (includeAttendees=false omits include_fields)", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{
			"id":77,
			"subject":"Quarterly review",
			"type":"meeting",
			"owner_id":7,
			"deal_id":42,
			"person_id":11,
			"org_id":13,
			"due_date":"2026-05-01",
			"due_time":"14:00",
			"duration":"01:00",
			"busy":true,
			"done":false,
			"location":{"value":"Acme HQ","locality":"Berlin","country":"DE","postal_code":"10115"},
			"participants":[{"person_id":11,"primary":true},{"person_id":12,"primary":false}],
			"add_time":"2026-04-01T08:00:00Z",
			"update_time":"2026-04-20T09:00:00Z"
		}}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).GetActivity(context.Background(), 77, GetActivityOptions{})
	if err != nil {
		t.Fatalf("GetActivity: %v", err)
	}
	if got.ID != 77 || got.Subject != "Quarterly review" || got.Type != "meeting" {
		t.Errorf("activity = %+v; want id=77 subject='Quarterly review' type=meeting", got)
	}
	if got.Busy != true || got.Done != false {
		t.Errorf("busy=%v done=%v; want busy=true done=false", got.Busy, got.Done)
	}
	if got.Location == nil || got.Location.Locality != "Berlin" {
		t.Errorf("location = %+v; want locality=Berlin", got.Location)
	}
	if len(got.Participants) != 2 || !got.Participants[0].Primary {
		t.Errorf("participants = %+v; want first primary", got.Participants)
	}
}

func TestClient_GetActivity_IncludeAttendees(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("include_fields"); got != "attendees" {
			t.Errorf("include_fields = %q, want attendees", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":{
			"id":1,"subject":"x","type":"call","busy":false,"done":false,"add_time":"","update_time":"",
			"attendees":[{"email":"alice@example.com","name":"Alice","status":"accepted","is_organizer":true,"person_id":11}]
		}}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).GetActivity(context.Background(), 1, GetActivityOptions{IncludeAttendees: true})
	if err != nil {
		t.Fatalf("GetActivity: %v", err)
	}
	if len(got.Attendees) != 1 || got.Attendees[0].Email != "alice@example.com" || !got.Attendees[0].IsOrganizer {
		t.Errorf("attendees = %+v; want one accepted organizer alice", got.Attendees)
	}
}

func TestClient_GetActivity_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"success":false,"error":"Activity not found"}`)
	}))
	defer srv.Close()

	_, err := newTestClient(srv).GetActivity(context.Background(), 99999, GetActivityOptions{})
	if err == nil {
		t.Fatal("want error on 404, got nil")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v; want ErrNotFound", err)
	}
}

func TestClient_ListActivities_FiltersAndCursor(t *testing.T) {
	var sawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[
			{"id":1,"subject":"A","type":"call","busy":false,"done":true,"add_time":"","update_time":""},
			{"id":2,"subject":"B","type":"email","busy":false,"done":false,"add_time":"","update_time":""}
		],"additional_data":{"next_cursor":"opaque-cursor-2"}}`)
	}))
	defer srv.Close()

	doneFalse := false
	activities, next, err := newTestClient(srv).ListActivities(context.Background(), ListActivitiesOptions{
		OwnerID:          7,
		DealID:           42,
		PersonID:         11,
		OrgID:            13,
		LeadID:           "lead-uuid",
		Done:             &doneFalse,
		UpdatedSince:     "2026-04-01T00:00:00Z",
		SortBy:           "update_time",
		SortDirection:    "desc",
		IncludeAttendees: true,
		Limit:            50,
		Cursor:           "opaque-cursor-1",
	})
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	if len(activities) != 2 || next != "opaque-cursor-2" {
		t.Fatalf("got %d activities, next=%q; want 2 + opaque-cursor-2", len(activities), next)
	}
	for _, want := range []string{
		"owner_id=7", "deal_id=42", "person_id=11", "org_id=13",
		"lead_id=lead-uuid", "done=false",
		"updated_since=2026-04-01T00%3A00%3A00Z",
		"sort_by=update_time", "sort_direction=desc",
		"include_fields=attendees", "limit=50", "cursor=opaque-cursor-1",
	} {
		if !strings.Contains(sawQuery, want) {
			t.Errorf("query %q missing %q", sawQuery, want)
		}
	}
}

func TestClient_ListActivities_NoFiltersOmitsParams(t *testing.T) {
	var sawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[],"additional_data":{}}`)
	}))
	defer srv.Close()

	_, _, err := newTestClient(srv).ListActivities(context.Background(), ListActivitiesOptions{})
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	if sawQuery != "" {
		t.Errorf("expected empty query string for zero filters, got %q", sawQuery)
	}
}

func TestClient_ListActivities_DonePointerEncodesTrue(t *testing.T) {
	var sawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[],"additional_data":{}}`)
	}))
	defer srv.Close()

	doneTrue := true
	_, _, err := newTestClient(srv).ListActivities(context.Background(), ListActivitiesOptions{Done: &doneTrue})
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	if !strings.Contains(sawQuery, "done=true") {
		t.Errorf("query %q missing done=true", sawQuery)
	}
}

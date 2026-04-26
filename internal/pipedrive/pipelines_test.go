package pipedrive

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_ListPipelines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/pipelines" {
			t.Errorf("path = %q, want /api/v2/pipelines", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[
			{"id":1,"name":"Sales","order_nr":0,"active":true},
			{"id":2,"name":"Renewals","order_nr":1,"active":true}
		]}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).ListPipelines(context.Background())
	if err != nil {
		t.Fatalf("ListPipelines: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d pipelines, want 2", len(got))
	}
	if got[0].ID != 1 || got[0].Name != "Sales" {
		t.Errorf("pipeline[0] = %+v, want id=1 name=Sales", got[0])
	}
	if got[1].OrderNr != 1 {
		t.Errorf("pipeline[1].OrderNr = %d, want 1", got[1].OrderNr)
	}
}

func TestClient_ListStages_AllPipelines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/stages" {
			t.Errorf("path = %q, want /api/v2/stages", r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("query should be empty when pipelineID=0, got %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[
			{"id":10,"name":"Lead In","order_nr":0,"active_flag":true,"pipeline_id":1,"deal_probability":10}
		]}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).ListStages(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListStages: %v", err)
	}
	if len(got) != 1 || got[0].ID != 10 {
		t.Fatalf("stages = %+v", got)
	}
}

func TestClient_ListStages_FilterByPipeline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("pipeline_id"); got != "5" {
			t.Errorf("pipeline_id query = %q, want 5", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[]}`)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).ListStages(context.Background(), 5)
	if err != nil {
		t.Fatalf("ListStages: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty data, got %+v", got)
	}
}

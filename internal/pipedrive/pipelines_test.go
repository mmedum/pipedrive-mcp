package pipedrive

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
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
		if r.URL.Query().Has("pipeline_id") {
			t.Errorf("pipelineID=0 filtered on a pipeline: %q", r.URL.RawQuery)
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

// TestClient_DefinitionListsReadEveryPage holds the five lists read
// whole to the cursor: a field on a second page is a field the cache
// would otherwise never resolve.
func TestClient_DefinitionListsReadEveryPage(t *testing.T) {
	cases := []struct {
		name string
		path string
		read func(*Client) (int, error)
	}{
		{"pipelines", "/api/v2/pipelines", func(c *Client) (int, error) { r, err := c.ListPipelines(context.Background()); return len(r), err }},
		{"stages", "/api/v2/stages", func(c *Client) (int, error) { r, err := c.ListStages(context.Background(), 0); return len(r), err }},
		{"deal fields", "/api/v2/dealFields", func(c *Client) (int, error) { r, err := c.ListDealFields(context.Background()); return len(r), err }},
		{"person fields", "/api/v2/personFields", func(c *Client) (int, error) { r, err := c.ListPersonFields(context.Background()); return len(r), err }},
		{"organization fields", "/api/v2/organizationFields", func(c *Client) (int, error) {
			r, err := c.ListOrganizationFields(context.Background())
			return len(r), err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cursors []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path || r.URL.Query().Get("limit") != "500" {
					t.Errorf("request = %s, want %s?limit=500", r.URL, tc.path)
				}
				cursors = append(cursors, r.URL.Query().Get("cursor"))
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Query().Get("cursor") == "" {
					_, _ = io.WriteString(w, `{"success":true,"data":[{"id":1},{"id":2}],"additional_data":{"next_cursor":"p2"}}`)
					return
				}
				_, _ = io.WriteString(w, `{"success":true,"data":[{"id":3}],"additional_data":{"next_cursor":null}}`)
			}))
			defer srv.Close()

			n, err := tc.read(newTestClient(srv))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if n != 3 || len(cursors) != 2 || cursors[1] != "p2" {
				t.Errorf("read %d rows over cursors %q; want 3 rows over \"\" then \"p2\"", n, cursors)
			}
		})
	}
}

func TestClient_ListStages_KeepsThePipelineFilterOnEveryPage(t *testing.T) {
	var filters []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filters = append(filters, r.URL.Query().Get("pipeline_id"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_, _ = io.WriteString(w, `{"success":true,"data":[{"id":1}],"additional_data":{"next_cursor":"p2"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"success":true,"data":[{"id":2}]}`)
	}))
	defer srv.Close()

	if _, err := newTestClient(srv).ListStages(context.Background(), 5); err != nil {
		t.Fatalf("ListStages: %v", err)
	}
	if want := []string{"5", "5"}; !reflect.DeepEqual(filters, want) {
		t.Errorf("pipeline_id per page = %q, want %q", filters, want)
	}
}

func TestClient_ACursorThatNeverEndsStopsAtTheCap(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[{"id":1}],"additional_data":{"next_cursor":"again"}}`)
	}))
	defer srv.Close()

	_, err := newTestClient(srv).ListPipelines(context.Background())
	if !errors.Is(err, ErrServerError) || calls != maxListPages {
		t.Errorf("err = %v after %d calls; want ErrServerError after %d", err, calls, maxListPages)
	}
}

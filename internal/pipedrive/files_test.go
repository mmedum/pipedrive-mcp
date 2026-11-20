package pipedrive

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The upload is multipart with the file and its anchor, and a retried
// 429 sends the same bytes again rather than an emptied body.
func TestClient_UploadFile_SendsMultipartAndRetriesWhole(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/files" || r.Header.Get("x-api-token") != "test-token" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("attempt %d is not multipart: %v", attempts, err)
		}
		f, h, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("attempt %d has no file: %v", attempts, err)
		}
		body, _ := io.ReadAll(f)
		if h.Filename != "offer.txt" || string(body) != "offer" || r.FormValue("deal_id") != "3" || r.FormValue("person_id") != "" {
			t.Errorf("attempt %d = %q %q deal %q", attempts, h.Filename, body, r.FormValue("deal_id"))
		}
		if attempts == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{"success":true,"data":{"id":8,"name":"offer.txt","deal_id":3}}`)
	}))
	defer srv.Close()
	f, err := newTestClient(srv).UploadFile(context.Background(), "offer.txt", strings.NewReader("offer"), FileAnchor{DealID: 3})
	if err != nil || f.ID != 8 || attempts != 2 {
		t.Errorf("UploadFile = %+v, %v after %d attempts; want file 8 after a retry", f, err, attempts)
	}
}

// The update is a form carrying only the fields given.
func TestClient_UpdateFile_SendsAForm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/files/7" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("request = %s %s %s", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "name=quote.pdf" {
			t.Errorf("body = %q; want the name alone", body)
		}
		_, _ = io.WriteString(w, `{"success":true,"data":{"id":7,"name":"quote.pdf"}}`)
	}))
	defer srv.Close()
	name := "quote.pdf"
	if f, err := newTestClient(srv).UpdateFile(context.Background(), 7, &name, nil); err != nil || f.Name != "quote.pdf" {
		t.Errorf("UpdateFile = %+v, %v", f, err)
	}
}

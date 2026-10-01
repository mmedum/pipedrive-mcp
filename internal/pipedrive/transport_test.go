package pipedrive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// dropping answers nothing: it reads the request, then closes the
// connection, so the client cannot tell whether Pipedrive acted on it.
func dropping(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A write whose answer is lost may have been applied, so it is sent
// once and the error says to read before trying again.
func TestClient_AWriteWhoseAnswerIsLostIsSentOnce(t *testing.T) {
	var hits atomic.Int32
	_, err := newTestClient(dropping(t, &hits)).CreateNote(context.Background(), CreateNoteRequest{Content: "x", DealID: 1})
	if err == nil || !strings.Contains(err.Error(), "may have been applied") {
		t.Errorf("err = %v, want one saying the write may have been applied", err)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("the write was sent %d times, want 1", got)
	}
}

// A read whose answer is lost is safe to repeat.
func TestClient_AReadWhoseAnswerIsLostIsRetried(t *testing.T) {
	var hits atomic.Int32
	if err := newTestClient(dropping(t, &hits)).ProbeAuth(context.Background()); err == nil {
		t.Fatal("err = nil, want a transport error")
	}
	if got := hits.Load(); got != 3 {
		t.Errorf("the read was sent %d times, want 3", got)
	}
}

func TestShouldRetryNetwork(t *testing.T) {
	dial := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	reset := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}
	for _, tc := range []struct {
		method string
		err    error
		want   bool
	}{
		{http.MethodGet, reset, true},
		{http.MethodGet, io.EOF, true},
		{http.MethodGet, context.Canceled, false},
		{http.MethodGet, context.DeadlineExceeded, false},
		{http.MethodPost, dial, true},
		{http.MethodPatch, dial, true},
		{http.MethodDelete, dial, true},
		{http.MethodPost, reset, false},
		{http.MethodPatch, io.EOF, false},
		{http.MethodDelete, reset, false},
		{http.MethodPost, context.Canceled, false},
	} {
		if got := shouldRetryNetwork(tc.method, tc.err); got != tc.want {
			t.Errorf("shouldRetryNetwork(%s, %v) = %v, want %v", tc.method, tc.err, got, tc.want)
		}
	}
}

// A search term travels in the query string. No log line at any level,
// and no returned error, carries it.
func TestClient_NoLogOrErrorCarriesTheSearchTerm(t *testing.T) {
	const canary = "canary-term-7f3a"
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"success":true,"data":{"items":[]}}`)
	}))
	t.Cleanup(ok.Close)
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(503)
		_, _ = io.WriteString(w, `{"success":false,"error":"unavailable"}`)
	}))
	t.Cleanup(failing.Close)
	var hits atomic.Int32

	for name, srv := range map[string]*httptest.Server{"ok": ok, "5xx": failing, "dropped": dropping(t, &hits)} {
		var logs bytes.Buffer
		c := New(Options{
			BaseURL: srv.URL + "/api/v2",
			Token:   "test-token",
			Timeout: 5 * time.Second,
			Logger:  slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		})
		c.baseDelay = time.Millisecond
		_, _, err := c.ItemSearch(context.Background(), SearchOptions{Term: canary})
		if logs.Len() == 0 {
			t.Errorf("%s: nothing was logged, so the check read nothing", name)
		}
		if strings.Contains(logs.String(), canary) {
			t.Errorf("%s: the search term reached the log:\n%s", name, logs.String())
		}
		if err != nil && strings.Contains(err.Error(), canary) {
			t.Errorf("%s: the search term reached the error: %v", name, err)
		}
	}
}

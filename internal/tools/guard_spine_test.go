package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
)

// guardedWrite owns the read-guard-write ordering for every manage_*
// mutation, so a mistake in it is a mistake in all five at once. These
// tests drive it directly against a toy record rather than through a
// resource, so the ordering is pinned independently of any one of them.

type rec struct {
	Version string
	Title   string
	Note    string
}

var recFields = []fieldSpec[rec]{
	{"title", func(r *rec) string { return r.Title }},
	{"note", func(r *rec) string { return r.Note }},
}

// plan builds a guardedWrite over rec, recording whether Put ran.
func plan(before rec, predict func(*rec) rec, after rec) (*guardedWrite[rec], *int) {
	puts := 0
	w := &guardedWrite[rec]{
		Spec:     recFields,
		Resource: "record 1",
		Version:  func(r *rec) string { return r.Version },
		Get:      func(context.Context) (*rec, error) { b := before; return &b, nil },
		Predict:  predict,
		Put: func(context.Context) (*rec, error) {
			puts++
			a := after
			return &a, nil
		},
	}
	return w, &puts
}

func setTitle(t string) func(*rec) rec {
	return func(r *rec) rec { out := *r; out.Title = t; return out }
}

func TestGuardedWrite_RefusalNeverReachesPut(t *testing.T) {
	w, puts := plan(rec{Version: "v1", Title: "stored"}, setTitle("replacement"), rec{})
	got, changed, stop := w.run(context.Background())
	if stop == nil {
		t.Fatal("replacing a populated field must be refused without overwrite")
	}
	if !strings.Contains(contentOf(stop), "title") {
		t.Errorf("refusal %q does not name the field", contentOf(stop))
	}
	if *puts != 0 {
		t.Error("Put ran despite the refusal — the guard is not upstream of the write")
	}
	if got != nil || changed != nil {
		t.Error("a refusal must not also hand back a record to report")
	}
}

func TestGuardedWrite_DryRunNeverReachesPut(t *testing.T) {
	w, puts := plan(rec{Version: "v1"}, setTitle("new"), rec{})
	w.DryRun = true
	got, changed, stop := w.run(context.Background())
	if stop != nil {
		t.Fatalf("unexpected stop: %s", contentOf(stop))
	}
	if *puts != 0 {
		t.Error("Put ran on a dry run")
	}
	// A rehearsal still predicts, or it would tell the caller nothing.
	if len(changed) != 1 || changed[0] != "title" {
		t.Errorf("changed = %v; want the predicted field", changed)
	}
	if got == nil || got.Title != "" {
		t.Error("a dry run must report the record as it stands, not as predicted")
	}
}

func TestGuardedWrite_NoOpNeverReachesPut(t *testing.T) {
	// Writing back what is already stored changes nothing, so the round
	// trip is skipped and the caller is told plainly.
	w, puts := plan(rec{Version: "v1", Title: "same"}, setTitle("same"), rec{})
	got, changed, stop := w.run(context.Background())
	if stop != nil {
		t.Fatalf("unexpected stop: %s", contentOf(stop))
	}
	if *puts != 0 {
		t.Error("Put ran for a write that would change nothing")
	}
	if len(changed) != 0 {
		t.Errorf("changed = %v; want empty", changed)
	}
	if got == nil || got.Title != "same" {
		t.Error("a no-op must still report the stored record")
	}
}

func TestGuardedWrite_VersionCheckPrecedesEverything(t *testing.T) {
	// A stale caller must be refused even when overwrite would otherwise
	// permit the write: their copy is out of date, so no flag should let
	// them write over the newer one.
	w, puts := plan(rec{Version: "v9", Title: "stored"}, setTitle("replacement"), rec{})
	w.ExpectVersion = "v1"
	w.Overwrite = true
	_, _, stop := w.run(context.Background())
	if stop == nil {
		t.Fatal("a stale expect_version must refuse even with overwrite set")
	}
	if !strings.Contains(contentOf(stop), "v9") {
		t.Errorf("refusal %q does not report the current version", contentOf(stop))
	}
	if *puts != 0 {
		t.Error("Put ran despite a stale version")
	}
}

func TestGuardedWrite_OverwritePermitsAndReportsTheEcho(t *testing.T) {
	// The post-write diff is against what the upstream echoed, not
	// against the local prediction: Pipedrive normalises some of what it
	// stores, and the caller should see what landed.
	w, puts := plan(
		rec{Version: "v1", Title: "stored"},
		setTitle("asked for"),
		rec{Version: "v2", Title: "ASKED FOR", Note: "server added this"},
	)
	w.Overwrite = true
	got, changed, stop := w.run(context.Background())
	if stop != nil {
		t.Fatalf("overwrite should permit the write: %s", contentOf(stop))
	}
	if *puts != 1 {
		t.Errorf("Put ran %d times; want 1", *puts)
	}
	if got.Title != "ASKED FOR" {
		t.Errorf("reported title = %q; want what the upstream stored", got.Title)
	}
	set := map[string]bool{}
	for _, f := range changed {
		set[f] = true
	}
	if !set["title"] || !set["note"] {
		t.Errorf("changed = %v; want both fields the echo actually moved, including the one the prediction did not know about", changed)
	}
}

func TestGuardedWrite_FillingAnEmptyFieldNeedsNoPermission(t *testing.T) {
	w, puts := plan(rec{Version: "v1"}, setTitle("first"), rec{Version: "v2", Title: "first"})
	_, changed, stop := w.run(context.Background())
	if stop != nil {
		t.Fatalf("filling an empty field destroys nothing: %s", contentOf(stop))
	}
	if *puts != 1 {
		t.Errorf("Put ran %d times; want 1", *puts)
	}
	if len(changed) != 1 || changed[0] != "title" {
		t.Errorf("changed = %v; want [title]", changed)
	}
}

func TestGuardedWrite_UpstreamErrorsStopTheSequence(t *testing.T) {
	boom := errors.New("upstream is down")
	w := &guardedWrite[rec]{
		Spec:     recFields,
		Resource: "record 1",
		Version:  func(r *rec) string { return r.Version },
		Get:      func(context.Context) (*rec, error) { return nil, boom },
		Predict:  func(r *rec) rec { t := *r; return t },
		Put:      func(context.Context) (*rec, error) { panic("Put must not run after a failed read") },
	}
	if _, _, stop := w.run(context.Background()); stop == nil {
		t.Fatal("a failed read must stop the sequence")
	}
}

// contentOf pulls the text out of a tool-execution error result.
func contentOf(res *mcp.CallToolResult) string {
	return testutil.TextContent(res)
}

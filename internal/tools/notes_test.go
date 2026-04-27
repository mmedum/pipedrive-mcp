package tools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
	"github.com/mmedum/pipedrive-mcp/internal/tools"
)

type fakeNotesClient struct {
	note      *pipedrive.Note
	noteErr   error
	notes     []pipedrive.Note
	notesPage *pipedrive.V1Pagination
	notesErr  error
	created   *pipedrive.Note
	createErr error
	deleteErr error

	lastListOpts  pipedrive.ListNotesOptions
	lastCreateReq pipedrive.CreateNoteRequest
	lastDeleteID  int64
	createCalls   int
	deleteCalls   int
}

func (f *fakeNotesClient) GetNote(_ context.Context, _ int64) (*pipedrive.Note, error) {
	return f.note, f.noteErr
}

func (f *fakeNotesClient) ListNotes(_ context.Context, opts pipedrive.ListNotesOptions) ([]pipedrive.Note, *pipedrive.V1Pagination, error) {
	f.lastListOpts = opts
	return f.notes, f.notesPage, f.notesErr
}

func (f *fakeNotesClient) CreateNote(_ context.Context, req pipedrive.CreateNoteRequest) (*pipedrive.Note, error) {
	f.lastCreateReq = req
	f.createCalls++
	return f.created, f.createErr
}

func (f *fakeNotesClient) DeleteNote(_ context.Context, id int64) error {
	f.lastDeleteID = id
	f.deleteCalls++
	return f.deleteErr
}

// noteRow mirrors the JSON shape RegisterNotes emits.
type noteRow struct {
	ID                   int64  `json:"id"`
	Content              string `json:"content"`
	UserID               int64  `json:"user_id"`
	DealID               int64  `json:"deal_id,omitempty"`
	PersonID             int64  `json:"person_id,omitempty"`
	OrgID                int64  `json:"org_id,omitempty"`
	LeadID               string `json:"lead_id,omitempty"`
	AddTime              string `json:"add_time"`
	UpdateTime           string `json:"update_time"`
	ActiveFlag           bool   `json:"active_flag"`
	PinnedToDeal         bool   `json:"pinned_to_deal,omitempty"`
	PinnedToPerson       bool   `json:"pinned_to_person,omitempty"`
	PinnedToOrganization bool   `json:"pinned_to_organization,omitempty"`
}

func dealID(id int64) *int64 { return &id }

func TestGetNote_HappyPath(t *testing.T) {
	fake := &fakeNotesClient{
		note: &pipedrive.Note{
			ID:               77,
			Content:          "<p>met with Acme</p>",
			UserID:           13,
			DealID:           dealID(42),
			ActiveFlag:       true,
			PinnedToDealFlag: true,
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, fake, false, true)
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_note",
		Arguments: map[string]any{"note_id": 77},
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out struct {
		Note noteRow `json:"note"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)
	if out.Note.ID != 77 || out.Note.Content != "<p>met with Acme</p>" || out.Note.DealID != 42 {
		t.Errorf("note = %+v; want id=77 deal_id=42", out.Note)
	}
	if !out.Note.PinnedToDeal {
		t.Error("v1 pinned_to_deal_flag=1 should map to pinned_to_deal=true")
	}
}

func TestGetNote_RejectsZeroID(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, &fakeNotesClient{}, false, true)
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_note",
		Arguments: map[string]any{"note_id": 0},
	})
	if !res.IsError {
		t.Fatal("expected isError on zero note_id")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
}

func TestGetNote_UpstreamNotFound(t *testing.T) {
	fake := &fakeNotesClient{
		noteErr: &pipedrive.APIError{
			Class:    pipedrive.ErrNotFound,
			Status:   404,
			Message:  "Note not found",
			Endpoint: "/api/v1/notes/99999",
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, fake, false, true)
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_note",
		Arguments: map[string]any{"note_id": 99999},
	})
	if !res.IsError {
		t.Fatal("expected isError on upstream 404")
	}
	if !strings.HasPrefix(contentText(res), "[not_found]") {
		t.Errorf("error text = %q; want [not_found] prefix", contentText(res))
	}
}

func TestListNotes_HappyPath_WithCursor(t *testing.T) {
	fake := &fakeNotesClient{
		notes: []pipedrive.Note{
			{ID: 1, Content: "a", UserID: 1, ActiveFlag: true},
			{ID: 2, Content: "b", UserID: 1, ActiveFlag: true},
		},
		notesPage: &pipedrive.V1Pagination{
			Start: 0, Limit: 2, MoreItemsInCollection: true, NextStart: 2,
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, fake, false, true)
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_notes",
		Arguments: map[string]any{"deal_id": 42, "limit": 2},
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out struct {
		Notes      []noteRow `json:"notes"`
		NextCursor string    `json:"next_cursor"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)
	if len(out.Notes) != 2 {
		t.Fatalf("got %d notes, want 2", len(out.Notes))
	}
	if out.NextCursor == "" {
		t.Error("expected non-empty cursor when more_items_in_collection=true")
	}
	if fake.lastListOpts.DealID != 42 || fake.lastListOpts.Limit != 2 {
		t.Errorf("opts = %+v; want deal_id=42 limit=2", fake.lastListOpts)
	}
	if fake.lastListOpts.Sort != "update_time desc" {
		t.Errorf("sort = %q; want default 'update_time desc'", fake.lastListOpts.Sort)
	}
}

func TestListNotes_LastPage_OmitsCursor(t *testing.T) {
	fake := &fakeNotesClient{
		notes: []pipedrive.Note{{ID: 1, Content: "x", UserID: 1, ActiveFlag: true}},
		notesPage: &pipedrive.V1Pagination{
			Start: 0, Limit: 25, MoreItemsInCollection: false,
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, fake, false, true)
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_notes",
		Arguments: map[string]any{},
	})
	var out struct {
		NextCursor string `json:"next_cursor"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)
	if out.NextCursor != "" {
		t.Errorf("next_cursor = %q; want empty on last page", out.NextCursor)
	}
}

func TestListNotes_CursorRoundTripDecodesToOffset(t *testing.T) {
	// Round-trip: a cursor returned from page 1 becomes the offset
	// passed to ListNotes on page 2.
	fake1 := &fakeNotesClient{
		notes:     []pipedrive.Note{{ID: 1, ActiveFlag: true}},
		notesPage: &pipedrive.V1Pagination{Start: 0, Limit: 25, MoreItemsInCollection: true, NextStart: 25},
	}
	h1 := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, fake1, false, true)
	})
	defer h1.Close()

	res, _ := h1.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_notes",
		Arguments: map[string]any{},
	})
	var page1 struct {
		NextCursor string `json:"next_cursor"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &page1)

	// Reuse the cursor to fetch page 2.
	fake2 := &fakeNotesClient{
		notes:     []pipedrive.Note{},
		notesPage: &pipedrive.V1Pagination{Start: 25, Limit: 25, MoreItemsInCollection: false},
	}
	h2 := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, fake2, false, true)
	})
	defer h2.Close()
	_, _ = h2.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_notes",
		Arguments: map[string]any{"cursor": page1.NextCursor},
	})
	if fake2.lastListOpts.Start != 25 {
		t.Errorf("page-2 Start = %d; want 25 (decoded from cursor)", fake2.lastListOpts.Start)
	}
}

func TestListNotes_RejectsBadCursor(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, &fakeNotesClient{}, false, true)
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_notes",
		Arguments: map[string]any{"cursor": "not-base64-xyz!"},
	})
	if !res.IsError {
		t.Fatal("expected isError on garbage cursor")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
}

func TestListNotes_RejectsBadSortBy(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, &fakeNotesClient{}, false, true)
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_notes",
		Arguments: map[string]any{"sort_by": "bogus"},
	})
	if !res.IsError {
		t.Fatal("expected isError on unsupported sort_by")
	}
}

func TestCreateNote_HappyPath(t *testing.T) {
	fake := &fakeNotesClient{
		created: &pipedrive.Note{
			ID:         101,
			Content:    "<p>follow up</p>",
			UserID:     13,
			DealID:     dealID(42),
			AddTime:    "2026-04-27 10:00:00",
			UpdateTime: "2026-04-27 10:00:00",
			ActiveFlag: true,
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, fake, false, true)
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_note",
		Arguments: map[string]any{
			"content": "<p>follow up</p>",
			"deal_id": 42,
		},
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out struct {
		Note   noteRow `json:"note"`
		DryRun bool    `json:"dry_run,omitempty"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)
	if out.Note.ID != 101 || out.Note.DealID != 42 {
		t.Errorf("note = %+v; want id=101 deal_id=42", out.Note)
	}
	if out.DryRun {
		t.Error("dry_run should be false when not set on the server")
	}
	if fake.createCalls != 1 {
		t.Errorf("create_note hit upstream %d times; want 1", fake.createCalls)
	}
}

func TestCreateNote_DryRun_PreviewMirrorsEachAnchorType(t *testing.T) {
	// syntheticNoteFromRequest has a branch per anchor type. Walk all
	// five via dry-run create_note so a regression in one branch doesn't
	// silently emit wrong shapes for non-deal anchors.
	cases := []struct {
		name      string
		args      map[string]any
		wantField string // which field on the synthetic note must be non-zero
		wantInt   int64
		wantStr   string
	}{
		{"deal_id", map[string]any{"content": "x", "deal_id": 42}, "deal_id", 42, ""},
		{"person_id", map[string]any{"content": "x", "person_id": 11}, "person_id", 11, ""},
		{"org_id", map[string]any{"content": "x", "org_id": 7}, "org_id", 7, ""},
		{"lead_id", map[string]any{"content": "x", "lead_id": "abc-uuid"}, "lead_id", 0, "abc-uuid"},
		{"project_id", map[string]any{"content": "x", "project_id": 99}, "project_id", 99, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := testutil.Connect(t, func(s *mcp.Server) {
				tools.RegisterNotes(s, &fakeNotesClient{}, true, true)
			})
			defer h.Close()

			res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "create_note",
				Arguments: tc.args,
			})
			if res.IsError {
				t.Fatalf("unexpected isError: %+v", res.Content)
			}
			var out struct {
				Note   noteRow `json:"note"`
				DryRun bool    `json:"dry_run"`
			}
			testutil.DecodeStructured(t, res.StructuredContent, &out)
			if !out.DryRun {
				t.Error("dry_run should be true")
			}
			switch tc.wantField {
			case "deal_id":
				if out.Note.DealID != tc.wantInt {
					t.Errorf("deal_id = %d; want %d", out.Note.DealID, tc.wantInt)
				}
			case "person_id":
				if out.Note.PersonID != tc.wantInt {
					t.Errorf("person_id = %d; want %d", out.Note.PersonID, tc.wantInt)
				}
			case "org_id":
				if out.Note.OrgID != tc.wantInt {
					t.Errorf("org_id = %d; want %d", out.Note.OrgID, tc.wantInt)
				}
			case "lead_id":
				if out.Note.LeadID != tc.wantStr {
					t.Errorf("lead_id = %q; want %q", out.Note.LeadID, tc.wantStr)
				}
			case "project_id":
				// project_id isn't on noteRow (test mirror omits it like dealRow does)
				// — round-trip via the structuredContent map directly.
				m, ok := res.StructuredContent.(map[string]any)
				if !ok {
					t.Fatalf("structured content shape unexpected: %T", res.StructuredContent)
				}
				note, _ := m["note"].(map[string]any)
				got, _ := note["project_id"].(float64)
				if int64(got) != tc.wantInt {
					t.Errorf("project_id = %v; want %d", note["project_id"], tc.wantInt)
				}
			}
		})
	}
}

func TestCreateNote_DryRun_ReturnsSyntheticPreview(t *testing.T) {
	fake := &fakeNotesClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, fake, true, true) // dryRun=true
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_note",
		Arguments: map[string]any{
			"content":   "<p>preview</p>",
			"person_id": 11,
		},
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out struct {
		Note   noteRow `json:"note"`
		DryRun bool    `json:"dry_run"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)
	if !out.DryRun {
		t.Error("dry_run should be true when server is in dry-run mode")
	}
	if out.Note.ID != 0 {
		t.Errorf("synthetic note ID = %d; want 0 (signals preview)", out.Note.ID)
	}
	if out.Note.Content != "<p>preview</p>" || out.Note.PersonID != 11 {
		t.Errorf("synthetic note = %+v; want content+person_id mirrored from input", out.Note)
	}
	if fake.createCalls != 0 {
		t.Errorf("dry-run path hit upstream %d times; want 0", fake.createCalls)
	}
}

func TestCreateNote_RejectsEmptyContent(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, &fakeNotesClient{}, false, true)
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "create_note",
		Arguments: map[string]any{"content": "", "deal_id": 42},
	})
	if !res.IsError {
		t.Fatal("expected isError on empty content")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
}

func TestCreateNote_RejectsNoAnchor(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, &fakeNotesClient{}, false, true)
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "create_note",
		Arguments: map[string]any{"content": "x"},
	})
	if !res.IsError {
		t.Fatal("expected isError when no anchor is set")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
}

func TestCreateNote_RejectsOversizedContent(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, &fakeNotesClient{}, false, true)
	})
	defer h.Close()

	huge := strings.Repeat("a", 17*1024) // > 16KiB cap
	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "create_note",
		Arguments: map[string]any{"content": huge, "deal_id": 42},
	})
	if !res.IsError {
		t.Fatal("expected isError on oversized content")
	}
	text := contentText(res)
	if !strings.HasPrefix(text, "[validation]") || !strings.Contains(text, "max") {
		t.Errorf("error text = %q; want [validation] mentioning max bytes", text)
	}
}

func TestDeleteNote_HappyPath(t *testing.T) {
	fake := &fakeNotesClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, fake, false, true)
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "delete_note",
		Arguments: map[string]any{"note_id": 101},
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out struct {
		NoteID int64 `json:"note_id"`
		DryRun bool  `json:"dry_run,omitempty"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)
	if out.NoteID != 101 {
		t.Errorf("note_id = %d; want 101", out.NoteID)
	}
	if out.DryRun {
		t.Error("dry_run should be false when not configured")
	}
	if fake.deleteCalls != 1 || fake.lastDeleteID != 101 {
		t.Errorf("client received deleteCalls=%d lastID=%d; want 1, 101", fake.deleteCalls, fake.lastDeleteID)
	}
}

func TestDeleteNote_DryRun_SuppressesUpstream(t *testing.T) {
	fake := &fakeNotesClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, fake, true, true) // dryRun=true, enableDestructive=true
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "delete_note",
		Arguments: map[string]any{"note_id": 101},
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out struct {
		NoteID int64 `json:"note_id"`
		DryRun bool  `json:"dry_run"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)
	if !out.DryRun || out.NoteID != 101 {
		t.Errorf("expected dry_run=true note_id=101; got %+v", out)
	}
	if fake.deleteCalls != 0 {
		t.Errorf("dry-run path should not hit upstream; got %d calls", fake.deleteCalls)
	}
}

func TestDeleteNote_RejectsZeroID(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, &fakeNotesClient{}, false, true)
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "delete_note",
		Arguments: map[string]any{"note_id": 0},
	})
	if !res.IsError {
		t.Fatal("expected isError on zero note_id")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
}

func TestDeleteNote_NotRegisteredWhenDestructiveDisabled(t *testing.T) {
	// Server-build-time gating per CLAUDE.md hard rule #3 — when the
	// flag is off, delete_note must not appear in the registry at all.
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, &fakeNotesClient{}, false, false) // enableDestructive=false
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "delete_note",
		Arguments: map[string]any{"note_id": 101},
	})
	// SDK returns either a JSON-RPC -32602 (unknown tool) protocol
	// error or an isError result; either way the call must fail.
	if res != nil && !res.IsError {
		t.Fatalf("delete_note should not be callable without enableDestructive; got %+v", res)
	}
}

func TestRegisterNotes_RegistersInDumpRegistry(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, &fakeNotesClient{}, false, true)
	})
	defer h.Close()

	var buf strings.Builder
	if err := tools.DumpJSON(&buf, "test"); err != nil {
		t.Fatalf("DumpJSON: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"get_note"`, `"list_notes"`, `"create_note"`} {
		if !strings.Contains(out, want) {
			t.Errorf("dump missing %s", want)
		}
	}
}

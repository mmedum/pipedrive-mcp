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
	updated   *pipedrive.Note
	updateErr error
	deleteErr error

	lastListOpts  pipedrive.ListNotesOptions
	lastCreateReq pipedrive.CreateNoteRequest
	lastUpdateReq pipedrive.UpdateNoteRequest
	lastUpdateID  int64
	lastDeleteID  int64
	getCalls      int
	createCalls   int
	updateCalls   int
	deleteCalls   int
}

func (f *fakeNotesClient) GetNote(_ context.Context, _ int64) (*pipedrive.Note, error) {
	f.getCalls++
	return f.note, f.noteErr
}

func (f *fakeNotesClient) UpdateNote(_ context.Context, id int64, req pipedrive.UpdateNoteRequest) (*pipedrive.Note, error) {
	f.lastUpdateID = id
	f.lastUpdateReq = req
	f.updateCalls++
	return f.updated, f.updateErr
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
	ProjectID            int64  `json:"project_id,omitempty"`
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
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, fake, tools.RegisterOptions{})
	}, "get_note", map[string]any{"note_id": 77})
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
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, &fakeNotesClient{}, tools.RegisterOptions{})
	}, "get_note", map[string]any{"note_id": 0})
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
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, fake, tools.RegisterOptions{})
	}, "get_note", map[string]any{"note_id": 99999})
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
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, fake, tools.RegisterOptions{})
	}, "list_notes", map[string]any{"deal_id": 42, "limit": 2})
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
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, fake, tools.RegisterOptions{})
	}, "list_notes", map[string]any{})
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
		tools.RegisterNotes(s, fake1, tools.RegisterOptions{})
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
		tools.RegisterNotes(s, fake2, tools.RegisterOptions{})
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
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, &fakeNotesClient{}, tools.RegisterOptions{})
	}, "list_notes", map[string]any{"cursor": "not-base64-xyz!"})
	if !res.IsError {
		t.Fatal("expected isError on garbage cursor")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
}

func TestListNotes_RejectsBadSortBy(t *testing.T) {
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, &fakeNotesClient{}, tools.RegisterOptions{})
	}, "list_notes", map[string]any{"sort_by": "bogus"})
	if !res.IsError {
		t.Fatal("expected isError on unsupported sort_by")
	}
}

// manageNoteOut mirrors the JSON shape manage_note emits.
type manageNoteOut struct {
	Action  string   `json:"action"`
	Note    noteRow  `json:"note"`
	Changed []string `json:"changed,omitempty"`
	DryRun  bool     `json:"dry_run,omitempty"`
}

func callManageNote(t *testing.T, fake *fakeNotesClient, args map[string]any) (*mcp.CallToolResult, manageNoteOut) {
	t.Helper()
	return callManageNoteOpts(t, fake, tools.RegisterOptions{}, args)
}

func callManageNoteOpts(t *testing.T, fake *fakeNotesClient, opts tools.RegisterOptions, args map[string]any) (*mcp.CallToolResult, manageNoteOut) {
	t.Helper()
	res := testutil.CallTool(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, fake, opts)
	}, "manage_note", args)
	var out manageNoteOut
	if res != nil && !res.IsError && res.StructuredContent != nil {
		testutil.DecodeStructured(t, res.StructuredContent, &out)
	}
	return res, out
}

func TestManageNote_Create_HappyPath(t *testing.T) {
	fake := &fakeNotesClient{
		created: &pipedrive.Note{
			ID: 101, Content: "<p>follow up</p>", UserID: 13, DealID: dealID(42),
			AddTime: "2026-04-27 10:00:00", UpdateTime: "2026-04-27 10:00:00", ActiveFlag: true,
		},
	}
	res, out := callManageNote(t, fake, map[string]any{
		"action": "create", "content": "<p>follow up</p>", "deal_id": 42,
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	if out.Action != "create" || out.Note.ID != 101 || out.Note.DealID != 42 {
		t.Errorf("out = %+v; want action=create id=101 deal_id=42", out)
	}
	if out.DryRun {
		t.Error("dry_run should be false when not requested")
	}
	if fake.createCalls != 1 {
		t.Errorf("upstream create hit %d times; want 1", fake.createCalls)
	}
	// A create has nothing to read first: the guards only apply where
	// there is an existing record to protect.
	if fake.getCalls != 0 {
		t.Errorf("create read the note %d times; want 0", fake.getCalls)
	}
}

func TestManageNote_Create_DryRun_PreviewMirrorsEachAnchorType(t *testing.T) {
	// syntheticNoteFromRequest has a branch per anchor type. Walk all
	// five so a regression in one doesn't silently emit a wrong shape
	// for the non-deal anchors.
	cases := []struct {
		name  string
		arg   string
		value any
		check func(noteRow) bool
	}{
		{"deal", "deal_id", 42, func(r noteRow) bool { return r.DealID == 42 }},
		{"person", "person_id", 7, func(r noteRow) bool { return r.PersonID == 7 }},
		{"org", "org_id", 9, func(r noteRow) bool { return r.OrgID == 9 }},
		{"lead", "lead_id", "abc-123", func(r noteRow) bool { return r.LeadID == "abc-123" }},
		{"project", "project_id", 5, func(r noteRow) bool { return r.ProjectID == 5 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeNotesClient{}
			res, out := callManageNote(t, fake, map[string]any{
				"action": "create", "content": "<p>x</p>", tc.arg: tc.value, "dry_run": true,
			})
			if res.IsError {
				t.Fatalf("unexpected isError: %+v", res.Content)
			}
			if !out.DryRun {
				t.Error("dry_run should be echoed true")
			}
			if out.Note.ID != 0 {
				t.Errorf("dry-run note id = %d; want 0", out.Note.ID)
			}
			if !tc.check(out.Note) {
				t.Errorf("anchor %s not mirrored onto preview: %+v", tc.arg, out.Note)
			}
			if fake.createCalls != 0 {
				t.Errorf("dry run hit upstream %d times; want 0", fake.createCalls)
			}
		})
	}
}

func TestManageNote_Create_Rejects(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
	}{
		{"empty content", map[string]any{"action": "create", "deal_id": 42}},
		{"no anchor", map[string]any{"action": "create", "content": "<p>x</p>"}},
		{"oversized content", map[string]any{
			"action": "create", "deal_id": 42, "content": strings.Repeat("a", 16*1024+1),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeNotesClient{}
			res, _ := callManageNote(t, fake, tc.args)
			if !res.IsError {
				t.Fatal("expected isError")
			}
			if !strings.HasPrefix(contentText(res), "[validation]") {
				t.Errorf("error = %q; want [validation] prefix", contentText(res))
			}
			if fake.createCalls != 0 {
				t.Errorf("upstream hit %d times on a rejected input; want 0", fake.createCalls)
			}
		})
	}
}

func TestManageNote_Update_HappyPath_ReportsChangedFields(t *testing.T) {
	fake := &fakeNotesClient{
		note: &pipedrive.Note{
			ID: 55, Content: "", UserID: 13, DealID: dealID(42),
			UpdateTime: "2026-04-27 10:00:00", ActiveFlag: true,
		},
		updated: &pipedrive.Note{
			ID: 55, Content: "<p>new</p>", UserID: 13, DealID: dealID(42),
			UpdateTime: "2026-04-27 11:00:00", ActiveFlag: true,
		},
	}
	res, out := callManageNote(t, fake, map[string]any{
		"action": "update", "note_id": 55, "content": "<p>new</p>",
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	if out.Action != "update" || out.Note.Content != "<p>new</p>" {
		t.Errorf("out = %+v; want action=update content updated", out)
	}
	if len(out.Changed) != 1 || out.Changed[0] != "content" {
		t.Errorf("changed = %v; want [content]", out.Changed)
	}
	// The guard is only possible because the note is read first.
	if fake.getCalls != 1 {
		t.Errorf("update read the note %d times; want 1", fake.getCalls)
	}
	if fake.updateCalls != 1 || fake.lastUpdateID != 55 {
		t.Errorf("update calls=%d id=%d; want 1/55", fake.updateCalls, fake.lastUpdateID)
	}
}

func TestManageNote_Update_RefusesOverwritingExistingContent(t *testing.T) {
	fake := &fakeNotesClient{
		note: &pipedrive.Note{
			ID: 55, Content: "<p>somebody else wrote this</p>",
			UpdateTime: "2026-04-27 10:00:00", ActiveFlag: true,
		},
	}
	res, _ := callManageNote(t, fake, map[string]any{
		"action": "update", "note_id": 55, "content": "<p>mine</p>",
	})
	if !res.IsError {
		t.Fatal("expected a refusal when replacing existing content without overwrite")
	}
	txt := contentText(res)
	if !strings.HasPrefix(txt, "[refused]") {
		t.Errorf("error = %q; want [refused] prefix", txt)
	}
	// Per CLAUDE.md: a refusal names what it protects AND the argument
	// that permits the write.
	if !strings.Contains(txt, "note 55") {
		t.Errorf("refusal %q does not name the note it protects", txt)
	}
	if !strings.Contains(txt, "overwrite: true") {
		t.Errorf("refusal %q does not name the unlocking argument", txt)
	}
	if fake.updateCalls != 0 {
		t.Errorf("refused update still hit upstream %d times", fake.updateCalls)
	}
}

func TestManageNote_Update_OverwriteAllowsReplacement(t *testing.T) {
	fake := &fakeNotesClient{
		note: &pipedrive.Note{
			ID: 55, Content: "<p>old</p>", UpdateTime: "2026-04-27 10:00:00", ActiveFlag: true,
		},
		updated: &pipedrive.Note{
			ID: 55, Content: "<p>new</p>", UpdateTime: "2026-04-27 11:00:00", ActiveFlag: true,
		},
	}
	res, out := callManageNote(t, fake, map[string]any{
		"action": "update", "note_id": 55, "content": "<p>new</p>", "overwrite": true,
	})
	if res.IsError {
		t.Fatalf("overwrite:true should permit the write, got %+v", res.Content)
	}
	if out.Note.Content != "<p>new</p>" {
		t.Errorf("content = %q; want the replacement", out.Note.Content)
	}
	if fake.updateCalls != 1 {
		t.Errorf("update calls = %d; want 1", fake.updateCalls)
	}
}

func TestManageNote_Update_FillingEmptyContentNeedsNoOverwrite(t *testing.T) {
	// The guard protects content the caller cannot see. An empty field
	// destroys nothing, so requiring overwrite there would be friction.
	fake := &fakeNotesClient{
		note:    &pipedrive.Note{ID: 55, Content: "", UpdateTime: "t0", ActiveFlag: true},
		updated: &pipedrive.Note{ID: 55, Content: "<p>first</p>", UpdateTime: "t1", ActiveFlag: true},
	}
	res, _ := callManageNote(t, fake, map[string]any{
		"action": "update", "note_id": 55, "content": "<p>first</p>",
	})
	if res.IsError {
		t.Fatalf("filling an empty field should not be refused: %+v", res.Content)
	}
	if fake.updateCalls != 1 {
		t.Errorf("update calls = %d; want 1", fake.updateCalls)
	}
}

func TestManageNote_Update_RefusesStaleExpectVersion(t *testing.T) {
	fake := &fakeNotesClient{
		note: &pipedrive.Note{ID: 55, Content: "", UpdateTime: "2026-04-27 12:00:00", ActiveFlag: true},
	}
	res, _ := callManageNote(t, fake, map[string]any{
		"action": "update", "note_id": 55, "content": "<p>x</p>",
		"expect_version": "2026-04-27 10:00:00",
	})
	if !res.IsError {
		t.Fatal("expected a refusal on a stale expect_version")
	}
	txt := contentText(res)
	if !strings.HasPrefix(txt, "[refused]") {
		t.Errorf("error = %q; want [refused] prefix", txt)
	}
	if !strings.Contains(txt, "2026-04-27 12:00:00") {
		t.Errorf("refusal %q does not report the current version", txt)
	}
	if fake.updateCalls != 0 {
		t.Errorf("stale update still hit upstream %d times", fake.updateCalls)
	}
}

func TestManageNote_Update_MatchingExpectVersionProceeds(t *testing.T) {
	fake := &fakeNotesClient{
		note:    &pipedrive.Note{ID: 55, Content: "", UpdateTime: "t0", ActiveFlag: true},
		updated: &pipedrive.Note{ID: 55, Content: "<p>x</p>", UpdateTime: "t1", ActiveFlag: true},
	}
	res, _ := callManageNote(t, fake, map[string]any{
		"action": "update", "note_id": 55, "content": "<p>x</p>", "expect_version": "t0",
	})
	if res.IsError {
		t.Fatalf("a matching expect_version should proceed: %+v", res.Content)
	}
	if fake.updateCalls != 1 {
		t.Errorf("update calls = %d; want 1", fake.updateCalls)
	}
}

func TestManageNote_Update_DryRun_SuppressesUpstream(t *testing.T) {
	fake := &fakeNotesClient{
		note: &pipedrive.Note{ID: 55, Content: "", DealID: dealID(1), UpdateTime: "t0", ActiveFlag: true},
	}
	res, out := callManageNote(t, fake, map[string]any{
		"action": "update", "note_id": 55, "content": "<p>x</p>", "deal_id": 2,
		"overwrite": true, "dry_run": true,
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	if !out.DryRun {
		t.Error("dry_run should be echoed true")
	}
	want := map[string]bool{"content": true, "deal_id": true}
	if len(out.Changed) != 2 {
		t.Fatalf("changed = %v; want content and deal_id", out.Changed)
	}
	for _, f := range out.Changed {
		if !want[f] {
			t.Errorf("unexpected predicted change %q", f)
		}
	}
	if fake.updateCalls != 0 {
		t.Errorf("dry run hit upstream %d times; want 0", fake.updateCalls)
	}
}

func TestManageNote_Update_Rejects(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
	}{
		{"zero id", map[string]any{"action": "update", "note_id": 0, "content": "<p>x</p>"}},
		{"nothing to change", map[string]any{"action": "update", "note_id": 55}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeNotesClient{note: &pipedrive.Note{ID: 55, ActiveFlag: true}}
			res, _ := callManageNote(t, fake, tc.args)
			if !res.IsError {
				t.Fatal("expected isError")
			}
			if !strings.HasPrefix(contentText(res), "[validation]") {
				t.Errorf("error = %q; want [validation] prefix", contentText(res))
			}
			if fake.updateCalls != 0 {
				t.Errorf("upstream hit %d times on a rejected input; want 0", fake.updateCalls)
			}
		})
	}
}

func TestManageNote_Delete_HappyPath(t *testing.T) {
	fake := &fakeNotesClient{
		note: &pipedrive.Note{
			ID: 77, Content: "<p>bye</p>", DealID: dealID(42),
			UpdateTime: "t0", ActiveFlag: true,
		},
	}
	res, out := callManageNote(t, fake, map[string]any{"action": "delete", "note_id": 77})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	if out.Action != "delete" {
		t.Errorf("action = %q; want delete", out.Action)
	}
	// The result shows what went, which is the point of reading first.
	if out.Note.ID != 77 || out.Note.Content != "<p>bye</p>" {
		t.Errorf("note = %+v; want the pre-delete record", out.Note)
	}
	if len(out.Changed) != 1 || out.Changed[0] != "active_flag" {
		t.Errorf("changed = %v; want [active_flag]", out.Changed)
	}
	if fake.deleteCalls != 1 || fake.lastDeleteID != 77 {
		t.Errorf("delete calls=%d id=%d; want 1/77", fake.deleteCalls, fake.lastDeleteID)
	}
}

func TestManageNote_Delete_DryRun_SuppressesUpstream(t *testing.T) {
	fake := &fakeNotesClient{
		note: &pipedrive.Note{ID: 77, Content: "<p>bye</p>", UpdateTime: "t0", ActiveFlag: true},
	}
	res, out := callManageNote(t, fake, map[string]any{
		"action": "delete", "note_id": 77, "dry_run": true,
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	if !out.DryRun {
		t.Error("dry_run should be echoed true")
	}
	if fake.deleteCalls != 0 {
		t.Errorf("dry run hit upstream %d times; want 0", fake.deleteCalls)
	}
}

func TestManageNote_Delete_AlreadyInactiveIsNoOp(t *testing.T) {
	// A v1 delete is soft. Deleting an already-soft-deleted note should
	// report the state rather than firing a second pointless DELETE.
	fake := &fakeNotesClient{
		note: &pipedrive.Note{ID: 77, Content: "<p>gone</p>", UpdateTime: "t0", ActiveFlag: false},
	}
	res, out := callManageNote(t, fake, map[string]any{"action": "delete", "note_id": 77})
	if res.IsError {
		t.Fatalf("re-deleting should be safe, got %+v", res.Content)
	}
	if len(out.Changed) != 0 {
		t.Errorf("changed = %v; want empty on a no-op", out.Changed)
	}
	if fake.deleteCalls != 0 {
		t.Errorf("no-op delete hit upstream %d times; want 0", fake.deleteCalls)
	}
}

func TestManageNote_Delete_RejectsZeroID(t *testing.T) {
	fake := &fakeNotesClient{}
	res, _ := callManageNote(t, fake, map[string]any{"action": "delete", "note_id": 0})
	if !res.IsError {
		t.Fatal("expected isError on zero note_id")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error = %q; want [validation] prefix", contentText(res))
	}
	if fake.deleteCalls != 0 {
		t.Errorf("upstream hit %d times; want 0", fake.deleteCalls)
	}
}

func TestManageNote_RejectsBadAction(t *testing.T) {
	// Two error channels, per CLAUDE.md "MCP error mapping". An action
	// the schema rejects outright never reaches the handler; one that
	// satisfies the schema but names nothing we implement does, and
	// comes back as a tool-execution error that enumerates the enum.
	for _, action := range []string{"obliterate", ""} {
		t.Run("action="+action, func(t *testing.T) {
			fake := &fakeNotesClient{}
			res, _ := callManageNote(t, fake, map[string]any{"action": action, "note_id": 1})
			if !res.IsError {
				t.Fatal("expected isError")
			}
			txt := contentText(res)
			if !strings.HasPrefix(txt, "[validation]") {
				t.Errorf("error = %q; want [validation] prefix", txt)
			}
			// The closed enum must be named so the LLM can self-correct.
			if !strings.Contains(txt, "create") || !strings.Contains(txt, "delete") {
				t.Errorf("error %q does not enumerate the valid actions", txt)
			}
			if fake.createCalls+fake.updateCalls+fake.deleteCalls != 0 {
				t.Error("a rejected action still reached upstream")
			}
		})
	}
}

func TestManageNote_MissingActionIsASchemaError(t *testing.T) {
	// `action` is schema-required, so the SDK rejects the call before
	// the handler runs. That is the protocol-error channel, and it is
	// the correct one for arguments that fail schema validation.
	fake := &fakeNotesClient{}
	res, _ := callManageNote(t, fake, map[string]any{"note_id": 1})
	if res != nil && !res.IsError {
		t.Fatal("a call with no action must fail")
	}
	if fake.createCalls+fake.updateCalls+fake.deleteCalls != 0 {
		t.Error("a schema-rejected call still reached upstream")
	}
}

func TestRegisterNotes_RegistersManageNoteUnconditionally(t *testing.T) {
	// Per CLAUDE.md hard rule 3 the destructive path is guarded at call
	// time, not by whether the tool exists. There is no longer a
	// registration flag that can hide it.
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterNotes(s, &fakeNotesClient{}, tools.RegisterOptions{})
	})
	defer h.Close()

	var buf strings.Builder
	if err := tools.DumpJSON(&buf, "test"); err != nil {
		t.Fatalf("DumpJSON: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"get_note"`, `"list_notes"`, `"manage_note"`} {
		if !strings.Contains(out, want) {
			t.Errorf("dump missing %s", want)
		}
	}
	for _, gone := range []string{`"create_note"`, `"delete_note"`} {
		if strings.Contains(out, gone) {
			t.Errorf("dump still carries retired tool %s", gone)
		}
	}
}

func TestManageNote_Update_NamesEveryChangedAnchor(t *testing.T) {
	// changedNoteFields diffs the pre-write read against the record
	// Pipedrive echoed back. Walk every anchor at once so no branch of
	// that diff goes unexercised.
	pid, oid, prid := int64(7), int64(9), int64(5)
	fake := &fakeNotesClient{
		note: &pipedrive.Note{ID: 55, Content: "<p>old</p>", UpdateTime: "t0", ActiveFlag: true},
		updated: &pipedrive.Note{
			ID: 55, Content: "<p>new</p>", DealID: dealID(42), PersonID: &pid,
			OrgID: &oid, LeadID: "abc-123", ProjectID: &prid,
			UpdateTime: "t1", ActiveFlag: true,
		},
	}
	res, out := callManageNote(t, fake, map[string]any{
		"action": "update", "note_id": 55, "content": "<p>new</p>",
		"deal_id": 42, "person_id": 7, "org_id": 9,
		"lead_id": "abc-123", "project_id": 5, "overwrite": true,
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	got := map[string]bool{}
	for _, f := range out.Changed {
		got[f] = true
	}
	for _, want := range []string{"content", "deal_id", "person_id", "org_id", "lead_id", "project_id"} {
		if !got[want] {
			t.Errorf("changed = %v; missing %q", out.Changed, want)
		}
	}
	// active_flag is not reportable by an update: UpdateNoteRequest has
	// no such field, so no PUT this client issues can flip it.
	if got["active_flag"] {
		t.Error("changed named active_flag, which an update cannot set")
	}
}

func TestManageNote_Update_DryRun_PredictsEveryAnchor(t *testing.T) {
	// predictedNoteChanges compares the request against the record as
	// it stands, rather than against an echo it never receives.
	fake := &fakeNotesClient{
		note: &pipedrive.Note{ID: 55, Content: "<p>old</p>", UpdateTime: "t0", ActiveFlag: true},
	}
	res, out := callManageNote(t, fake, map[string]any{
		"action": "update", "note_id": 55, "content": "<p>new</p>",
		"deal_id": 42, "person_id": 7, "org_id": 9,
		"lead_id": "abc-123", "project_id": 5,
		"overwrite": true, "dry_run": true,
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	got := map[string]bool{}
	for _, f := range out.Changed {
		got[f] = true
	}
	for _, want := range []string{"content", "deal_id", "person_id", "org_id", "lead_id", "project_id"} {
		if !got[want] {
			t.Errorf("predicted changed = %v; missing %q", out.Changed, want)
		}
	}
	if fake.updateCalls != 0 {
		t.Errorf("dry run hit upstream %d times; want 0", fake.updateCalls)
	}
}

func TestManageNote_Update_NoOpReportsNothingChanged(t *testing.T) {
	// Writing the same content back changes nothing, and the result
	// should say so rather than implying an edit happened.
	same := &pipedrive.Note{ID: 55, Content: "<p>same</p>", UpdateTime: "t0", ActiveFlag: true}
	fake := &fakeNotesClient{note: same, updated: same}
	res, out := callManageNote(t, fake, map[string]any{
		"action": "update", "note_id": 55, "content": "<p>same</p>", "overwrite": true,
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	if len(out.Changed) != 0 {
		t.Errorf("changed = %v; want empty on a no-op write", out.Changed)
	}
}

func TestManageNote_Update_UpstreamErrorSurfaces(t *testing.T) {
	fake := &fakeNotesClient{
		note:      &pipedrive.Note{ID: 55, Content: "", UpdateTime: "t0", ActiveFlag: true},
		updateErr: pipedrive.ErrRateLimited,
	}
	res, _ := callManageNote(t, fake, map[string]any{
		"action": "update", "note_id": 55, "content": "<p>x</p>",
	})
	if !res.IsError {
		t.Fatal("expected isError when upstream fails")
	}
	if !strings.HasPrefix(contentText(res), "[rate_limited]") {
		t.Errorf("error = %q; want [rate_limited] prefix", contentText(res))
	}
}

func TestManageNote_ReadBeforeWriteErrorSurfaces(t *testing.T) {
	// update and delete both read first; a failure there must surface
	// as the upstream class, not as a refusal.
	for _, action := range []string{"update", "delete"} {
		t.Run(action, func(t *testing.T) {
			fake := &fakeNotesClient{noteErr: pipedrive.ErrNotFound}
			args := map[string]any{"action": action, "note_id": 999}
			if action == "update" {
				args["content"] = "<p>x</p>"
			}
			res, _ := callManageNote(t, fake, args)
			if !res.IsError {
				t.Fatal("expected isError")
			}
			if !strings.HasPrefix(contentText(res), "[not_found]") {
				t.Errorf("error = %q; want [not_found] prefix", contentText(res))
			}
			if fake.updateCalls+fake.deleteCalls != 0 {
				t.Error("write proceeded despite a failed pre-read")
			}
		})
	}
}

func TestManageNote_Update_OverwriteGuardCoversAnchors(t *testing.T) {
	// The guard protects every populated field the write would replace,
	// not just content. Moving a note off the deal it is filed under is
	// exactly the kind of loss the caller cannot see coming — manage_note's
	// own schema warns that an update "moves the note".
	fake := &fakeNotesClient{
		note: &pipedrive.Note{ID: 55, Content: "", DealID: dealID(1), UpdateTime: "t0", ActiveFlag: true},
	}
	res, _ := callManageNote(t, fake, map[string]any{
		"action": "update", "note_id": 55, "deal_id": 2,
	})
	if !res.IsError {
		t.Fatal("expected a refusal when moving the note off a deal it is already filed under")
	}
	txt := contentText(res)
	if !strings.HasPrefix(txt, "[refused]") {
		t.Errorf("error = %q; want [refused] prefix", txt)
	}
	if !strings.Contains(txt, "deal_id") {
		t.Errorf("refusal %q does not name the field it protects", txt)
	}
	if fake.updateCalls != 0 {
		t.Errorf("refused update still hit upstream %d times", fake.updateCalls)
	}
}

func TestManageNote_Update_RefusalNamesEveryClobberedField(t *testing.T) {
	fake := &fakeNotesClient{
		note: &pipedrive.Note{
			ID: 55, Content: "<p>old</p>", DealID: dealID(1),
			UpdateTime: "t0", ActiveFlag: true,
		},
	}
	res, _ := callManageNote(t, fake, map[string]any{
		"action": "update", "note_id": 55, "content": "<p>new</p>", "deal_id": 2,
	})
	if !res.IsError {
		t.Fatal("expected a refusal")
	}
	txt := contentText(res)
	for _, want := range []string{"content", "deal_id", "overwrite: true"} {
		if !strings.Contains(txt, want) {
			t.Errorf("refusal %q does not mention %q", txt, want)
		}
	}
}

func TestManageNote_ServerWideDryRunIsAFloor(t *testing.T) {
	// docs/security.md promises that PIPEDRIVE_DRY_RUN makes speculative
	// LLM work safe. manage_note is the only tool that can delete, so if
	// it ignored the flag that promise would be false.
	for _, action := range []string{"create", "update", "delete"} {
		t.Run(action, func(t *testing.T) {
			fake := &fakeNotesClient{
				note: &pipedrive.Note{ID: 55, Content: "", UpdateTime: "t0", ActiveFlag: true},
			}
			args := map[string]any{"action": action, "note_id": 55}
			switch action {
			case "create":
				args["content"] = "<p>x</p>"
				args["deal_id"] = 42
			case "update":
				args["content"] = "<p>x</p>"
			}
			res, out := callManageNoteOpts(t, fake, tools.RegisterOptions{DryRun: true}, args)
			if res.IsError {
				t.Fatalf("unexpected isError: %+v", res.Content)
			}
			if !out.DryRun {
				t.Error("dry_run should be reported true under the server-wide floor")
			}
			if fake.createCalls+fake.updateCalls+fake.deleteCalls != 0 {
				t.Errorf("the floor was ignored: %d upstream writes", fake.createCalls+fake.updateCalls+fake.deleteCalls)
			}
		})
	}
}

func TestManageNote_Create_ChangedNamesTheAnchor(t *testing.T) {
	// A create sets an anchor as well as content; reporting only
	// "content" would under-report what the write did.
	fake := &fakeNotesClient{
		created: &pipedrive.Note{
			ID: 101, Content: "<p>x</p>", DealID: dealID(42),
			AddTime: "t0", UpdateTime: "t0", ActiveFlag: true,
		},
	}
	res, out := callManageNote(t, fake, map[string]any{
		"action": "create", "content": "<p>x</p>", "deal_id": 42,
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	got := map[string]bool{}
	for _, f := range out.Changed {
		got[f] = true
	}
	if !got["content"] || !got["deal_id"] {
		t.Errorf("changed = %v; want both content and deal_id", out.Changed)
	}
}

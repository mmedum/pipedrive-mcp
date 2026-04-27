package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

type notesClient interface {
	GetNote(ctx context.Context, id int64) (*pipedrive.Note, error)
	ListNotes(ctx context.Context, opts pipedrive.ListNotesOptions) ([]pipedrive.Note, *pipedrive.V1Pagination, error)
	CreateNote(ctx context.Context, req pipedrive.CreateNoteRequest) (*pipedrive.Note, error)
	DeleteNote(ctx context.Context, id int64) error
}

// noteContentMaxBytes caps the note content size at the MCP boundary.
// Pipedrive's v1 server-side limit is generous (~50KB), but a
// runaway LLM could waste request budget on duplicates without an
// upper bound. 16KiB is comfortably above the ~3KB notes seen in the
// live workspace and rejects pathological cases as [validation].
const noteContentMaxBytes = 16 * 1024

// allowedNoteSortFields is the closed enum surfaced to the LLM for
// list_notes ordering. v1 supports more fields (id, user_id, deal_id,
// person_id, org_id, content) but those rarely matter for a "what's
// happened" sweep.
var allowedNoteSortFields = map[string]bool{
	"add_time":    true,
	"update_time": true,
}

// noteSummary is the LLM-facing shape of a note. Parallel shadow of
// pipedrive.Note per CLAUDE.md — jsonschema tags scoped here, not on
// internal/pipedrive types. v1 returns the pinned-to-* fields as JSON
// booleans (verified live; earlier external docs claimed 0/1 ints —
// see types.go on Note); they pass through unchanged here.
type noteSummary struct {
	ID                   int64  `json:"id" jsonschema:"the note's numeric id"`
	Content              string `json:"content" jsonschema:"note text; HTML-formatted (Pipedrive's notes editor produces HTML)"`
	UserID               int64  `json:"user_id" jsonschema:"id of the user who authored the note"`
	LastUpdateUserID     int64  `json:"last_update_user_id,omitempty" jsonschema:"id of the user who last edited the note; 0 if unchanged since creation"`
	DealID               int64  `json:"deal_id,omitempty" jsonschema:"id of the linked deal, 0 if not anchored to a deal"`
	PersonID             int64  `json:"person_id,omitempty" jsonschema:"id of the linked person, 0 if not anchored to a person"`
	OrgID                int64  `json:"org_id,omitempty" jsonschema:"id of the linked organization, 0 if not anchored to an org"`
	LeadID               string `json:"lead_id,omitempty" jsonschema:"id of the linked lead (UUID string), empty if not anchored to a lead"`
	ProjectID            int64  `json:"project_id,omitempty" jsonschema:"id of the linked project, 0 if not anchored to a project"`
	AddTime              string `json:"add_time" jsonschema:"timestamp the note was created (YYYY-MM-DD HH:MM:SS, UTC — Pipedrive v1 format)"`
	UpdateTime           string `json:"update_time" jsonschema:"timestamp the note was last edited"`
	ActiveFlag           bool   `json:"active_flag" jsonschema:"false if the note was soft-deleted via delete_note. Pipedrive v1 deletes are soft — the note is still readable via get_note but list_notes filters it out by default."`
	PinnedToDeal         bool   `json:"pinned_to_deal,omitempty" jsonschema:"true if the note is pinned at the top of the deal's timeline"`
	PinnedToPerson       bool   `json:"pinned_to_person,omitempty" jsonschema:"true if the note is pinned to the person's timeline"`
	PinnedToOrganization bool   `json:"pinned_to_organization,omitempty" jsonschema:"true if the note is pinned to the organization's timeline"`
	PinnedToLead         bool   `json:"pinned_to_lead,omitempty" jsonschema:"true if the note is pinned to the lead's timeline"`
	PinnedToProject      bool   `json:"pinned_to_project,omitempty" jsonschema:"true if the note is pinned to the project's timeline"`
}

type getNoteInput struct {
	NoteID int64 `json:"note_id" jsonschema:"the note's numeric id"`
}

type getNoteOutput struct {
	Note noteSummary `json:"note" jsonschema:"the requested note"`
}

type listNotesInput struct {
	UserID        int64  `json:"user_id,omitempty" jsonschema:"return only notes authored by this user id; 0 = no filter"`
	DealID        int64  `json:"deal_id,omitempty" jsonschema:"return only notes anchored to this deal id; 0 = no filter"`
	PersonID      int64  `json:"person_id,omitempty" jsonschema:"return only notes anchored to this person id; 0 = no filter"`
	OrgID         int64  `json:"org_id,omitempty" jsonschema:"return only notes anchored to this organization id; 0 = no filter"`
	LeadID        string `json:"lead_id,omitempty" jsonschema:"return only notes anchored to this lead UUID; empty = no filter"`
	StartDate     string `json:"start_date,omitempty" jsonschema:"YYYY-MM-DD; return only notes created on or after this date"`
	EndDate       string `json:"end_date,omitempty" jsonschema:"YYYY-MM-DD; return only notes created on or before this date"`
	UpdatedSince  string `json:"updated_since,omitempty" jsonschema:"RFC3339 timestamp; return only notes updated at or after this time"`
	SortBy        string `json:"sort_by,omitempty" jsonschema:"add_time | update_time. Default 'update_time' (most-recently-edited first)."`
	SortDirection string `json:"sort_direction,omitempty" jsonschema:"asc | desc. Default 'desc' when sort_by is omitted; 'asc' otherwise."`
	Limit         int    `json:"limit,omitempty" jsonschema:"page size; default 25, max 100"`
	Cursor        string `json:"cursor,omitempty" jsonschema:"opaque pagination token from a previous list_notes response; omit for the first page"`
}

type listNotesOutput struct {
	Notes      []noteSummary `json:"notes" jsonschema:"matching notes on this page"`
	NextCursor string        `json:"next_cursor,omitempty" jsonschema:"pass to the next list_notes call to get the next page; empty when there are no more pages"`
}

type createNoteInput struct {
	Content   string `json:"content" jsonschema:"note text. HTML is allowed and rendered by Pipedrive; plain text also works (newlines become <br>). Required."`
	DealID    int64  `json:"deal_id,omitempty" jsonschema:"anchor the note to this deal id"`
	PersonID  int64  `json:"person_id,omitempty" jsonschema:"anchor the note to this person id"`
	OrgID     int64  `json:"org_id,omitempty" jsonschema:"anchor the note to this organization id"`
	LeadID    string `json:"lead_id,omitempty" jsonschema:"anchor the note to this lead UUID"`
	ProjectID int64  `json:"project_id,omitempty" jsonschema:"anchor the note to this project id"`
}

type createNoteOutput struct {
	Note   noteSummary `json:"note" jsonschema:"the newly-created note (echoed by Pipedrive). When dry_run is true, this is a synthetic record with id=0 reflecting what would have been created."`
	DryRun bool        `json:"dry_run,omitempty" jsonschema:"true when PIPEDRIVE_DRY_RUN was set on the server: no upstream POST was issued"`
}

type deleteNoteInput struct {
	NoteID int64 `json:"note_id" jsonschema:"the note's numeric id"`
}

type deleteNoteOutput struct {
	NoteID int64 `json:"note_id" jsonschema:"the id of the note that was deleted"`
	DryRun bool  `json:"dry_run,omitempty" jsonschema:"true when PIPEDRIVE_DRY_RUN was set on the server: no upstream DELETE was issued"`
}

// RegisterNotes wires the notes tools into the MCP server.
//
// Reads (get_note, list_notes) and the create write tool always
// register. delete_note is destructive and registers ONLY when
// enableDestructive=true (mirrors PIPEDRIVE_ENABLE_DESTRUCTIVE per
// CLAUDE.md hard rule #3 — server-build-time gating, not annotation-
// based).
//
// dryRun mirrors the server-wide PIPEDRIVE_DRY_RUN env: when true,
// create_note and delete_note return synthetic previews without
// firing the upstream POST/DELETE.
func RegisterNotes(s *mcp.Server, c notesClient, dryRun, enableDestructive bool) {
	readOnly := mcp.ToolAnnotations{ReadOnlyHint: true}
	destructiveTrue := true
	destructiveAnnotations := mcp.ToolAnnotations{DestructiveHint: &destructiveTrue, IdempotentHint: true}

	AddTool(s, &mcp.Tool{
		Name:        "get_note",
		Description: "Fetch a single Pipedrive note by note_id. Returns id, HTML content, author user_id, anchor ids (deal/person/org/lead/project — at most one is set per note), add/update timestamps, and pinned-to-* flags. Notes are stored on Pipedrive v1 (the only v1 carve-out in this server); v2 has no /notes endpoint as of 2026-04. Unknown note_id returns a [not_found] error.",
		Annotations: &readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getNoteInput) (*mcp.CallToolResult, getNoteOutput, error) {
		if err := validatePositiveID(in.NoteID, "note_id"); err != nil {
			return errorResult(err), getNoteOutput{}, nil
		}
		n, err := c.GetNote(ctx, in.NoteID)
		if err != nil {
			return errorResult(err), getNoteOutput{}, nil
		}
		return nil, getNoteOutput{Note: summarizeNote(n)}, nil
	})

	AddTool(s, &mcp.Tool{
		Name:        "list_notes",
		Description: "List Pipedrive notes filtered by anchor (deal_id / person_id / org_id / lead_id), author, date range, or update window. Returns id, HTML content, author, anchor ids, timestamps, and pinned-to-* flags. Default sort is update_time desc — most-recently-edited first, ideal for 'what's been written about X lately'. Default limit is 25, max 100. For more results, pass the next_cursor from the previous response. Notes live on Pipedrive v1; same response shape, different pagination internally (the cursor wraps v1's offset). To find a deal/person/org first, call `search` with the appropriate type.",
		Annotations: &readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listNotesInput) (*mcp.CallToolResult, listNotesOutput, error) {
		if err := validateEnum(in.SortBy, "sort_by", allowedNoteSortFields); err != nil {
			return errorResult(err), listNotesOutput{}, nil
		}
		if err := validateEnum(in.SortDirection, "sort_direction", allowedSortDirections); err != nil {
			return errorResult(err), listNotesOutput{}, nil
		}

		sortBy, sortDir := effectiveSort(in.SortBy, in.SortDirection)
		start, err := decodeNotesCursor(in.Cursor)
		if err != nil {
			return errorResult(err), listNotesOutput{}, nil
		}

		opts := pipedrive.ListNotesOptions{
			UserID:       in.UserID,
			DealID:       in.DealID,
			PersonID:     in.PersonID,
			OrgID:        in.OrgID,
			LeadID:       in.LeadID,
			StartDate:    in.StartDate,
			EndDate:      in.EndDate,
			UpdatedSince: in.UpdatedSince,
			Sort:         sortBy + " " + sortDir,
			Start:        start,
			Limit:        clampLimit(in.Limit),
		}
		notes, page, err := c.ListNotes(ctx, opts)
		if err != nil {
			return errorResult(err), listNotesOutput{}, nil
		}
		out := listNotesOutput{
			Notes:      make([]noteSummary, 0, len(notes)),
			NextCursor: encodeNotesCursor(page),
		}
		for i := range notes {
			out.Notes = append(out.Notes, summarizeNote(&notes[i]))
		}
		return nil, out, nil
	})

	AddTool(s, &mcp.Tool{
		Name:        "create_note",
		Description: "Create a Pipedrive note attached to a deal, person, organization, lead, or project. `content` is required and free-text (HTML is preserved by Pipedrive's editor); at least one of deal_id / person_id / org_id / lead_id / project_id must be set so the note has somewhere to live. Returns the created note as Pipedrive echoes it. When the server is started with PIPEDRIVE_DRY_RUN=true, this tool returns a synthetic preview (dry_run=true, id=0) instead of issuing the upstream POST — useful for testing without writing real data. Notes are stored on Pipedrive v1; v2 has no equivalent endpoint.",
	}, createNoteHandler(c, dryRun))

	if !enableDestructive {
		return
	}

	AddTool(s, &mcp.Tool{
		Name:        "delete_note",
		Description: "Delete a Pipedrive note by note_id. NOTE: Pipedrive v1 implements this as a SOFT delete — the record persists with `active_flag=false` and is filtered out of `list_notes` by default, but `get_note` still returns it. This tool is destructive — registered only when the server is started with PIPEDRIVE_ENABLE_DESTRUCTIVE=true. When PIPEDRIVE_DRY_RUN=true the upstream DELETE is suppressed and the tool returns dry_run=true. Notes are stored on Pipedrive v1; v2 has no equivalent endpoint.",
		Annotations: &destructiveAnnotations,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in deleteNoteInput) (*mcp.CallToolResult, deleteNoteOutput, error) {
		if err := validatePositiveID(in.NoteID, "note_id"); err != nil {
			return errorResult(err), deleteNoteOutput{}, nil
		}
		if dryRun {
			return nil, deleteNoteOutput{NoteID: in.NoteID, DryRun: true}, nil
		}
		if err := c.DeleteNote(ctx, in.NoteID); err != nil {
			return errorResult(err), deleteNoteOutput{}, nil
		}
		return nil, deleteNoteOutput{NoteID: in.NoteID}, nil
	})
}

func createNoteHandler(c notesClient, dryRun bool) mcp.ToolHandlerFor[createNoteInput, createNoteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in createNoteInput) (*mcp.CallToolResult, createNoteOutput, error) {
		if err := validateCreateNoteInput(in); err != nil {
			return errorResult(err), createNoteOutput{}, nil
		}
		req := pipedrive.CreateNoteRequest{
			Content:   in.Content,
			DealID:    in.DealID,
			PersonID:  in.PersonID,
			OrgID:     in.OrgID,
			LeadID:    in.LeadID,
			ProjectID: in.ProjectID,
		}
		if dryRun {
			return nil, createNoteOutput{
				Note:   summarizeNote(syntheticNoteFromRequest(req)),
				DryRun: true,
			}, nil
		}
		n, err := c.CreateNote(ctx, req)
		if err != nil {
			return errorResult(err), createNoteOutput{}, nil
		}
		return nil, createNoteOutput{Note: summarizeNote(n)}, nil
	}
}

func validateCreateNoteInput(in createNoteInput) error {
	if in.Content == "" {
		return fmt.Errorf("%w: content must not be empty", pipedrive.ErrValidation)
	}
	if len(in.Content) > noteContentMaxBytes {
		return fmt.Errorf("%w: content is %d bytes; max %d", pipedrive.ErrValidation, len(in.Content), noteContentMaxBytes)
	}
	if in.DealID == 0 && in.PersonID == 0 && in.OrgID == 0 && in.LeadID == "" && in.ProjectID == 0 {
		return fmt.Errorf("%w: at least one of deal_id / person_id / org_id / lead_id / project_id is required", pipedrive.ErrValidation)
	}
	return nil
}

func summarizeNote(n *pipedrive.Note) noteSummary {
	return noteSummary{
		ID:                   n.ID,
		Content:              n.Content,
		UserID:               n.UserID,
		LastUpdateUserID:     int64Or0(n.LastUpdateUserID),
		DealID:               int64Or0(n.DealID),
		PersonID:             int64Or0(n.PersonID),
		OrgID:                int64Or0(n.OrgID),
		LeadID:               n.LeadID,
		ProjectID:            int64Or0(n.ProjectID),
		AddTime:              n.AddTime,
		UpdateTime:           n.UpdateTime,
		ActiveFlag:           n.ActiveFlag,
		PinnedToDeal:         n.PinnedToDealFlag,
		PinnedToPerson:       n.PinnedToPersonFlag,
		PinnedToOrganization: n.PinnedToOrganizationFlag,
		PinnedToLead:         n.PinnedToLeadFlag,
		PinnedToProject:      n.PinnedToProjectFlag,
	}
}

func int64Or0(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// syntheticNoteFromRequest builds a placeholder Note that mirrors the
// CreateNoteRequest, used only on the dry-run path so create_note's
// output schema stays consistent. ID=0 + dry_run=true tells the LLM
// nothing was actually persisted.
func syntheticNoteFromRequest(req pipedrive.CreateNoteRequest) *pipedrive.Note {
	n := &pipedrive.Note{Content: req.Content, ActiveFlag: true}
	if req.DealID != 0 {
		v := req.DealID
		n.DealID = &v
	}
	if req.PersonID != 0 {
		v := req.PersonID
		n.PersonID = &v
	}
	if req.OrgID != 0 {
		v := req.OrgID
		n.OrgID = &v
	}
	if req.LeadID != "" {
		n.LeadID = req.LeadID
	}
	if req.ProjectID != 0 {
		v := req.ProjectID
		n.ProjectID = &v
	}
	return n
}

// encodeNotesCursor maps v1's offset-pagination block into an opaque
// cursor string the LLM can pass back verbatim. Returns empty string
// when there are no more pages, matching the v2 cursor convention.
//
// Encoded form: base64url("v1:<next_start>") — the prefix protects
// against a future cursor format change without a schema bump.
func encodeNotesCursor(p *pipedrive.V1Pagination) string {
	if p == nil || !p.MoreItemsInCollection {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte("v1:" + strconv.Itoa(p.NextStart)))
}

// decodeNotesCursor reverses encodeNotesCursor. Empty cursor returns
// 0 (the start of the collection).
func decodeNotesCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid cursor", pipedrive.ErrValidation)
	}
	s := string(raw)
	const prefix = "v1:"
	if len(s) <= len(prefix) || s[:len(prefix)] != prefix {
		return 0, fmt.Errorf("%w: cursor format unrecognized", pipedrive.ErrValidation)
	}
	n, err := strconv.Atoi(s[len(prefix):])
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%w: cursor offset must be non-negative", pipedrive.ErrValidation)
	}
	return n, nil
}

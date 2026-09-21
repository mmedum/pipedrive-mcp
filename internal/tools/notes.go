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
	UpdateNote(ctx context.Context, id int64, req pipedrive.UpdateNoteRequest) (*pipedrive.Note, error)
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

// allowedNoteActions is the closed enum manage_note dispatches on.
var allowedNoteActions = map[string]bool{
	"create": true,
	"update": true,
	"delete": true,
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
	ActiveFlag           bool   `json:"active_flag" jsonschema:"false if the note was soft-deleted. Pipedrive v1 deletes are soft: the note stays readable through get_note and only list_notes stops showing it. Nothing in this server sets the flag back."`
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

// manageNoteInput is the single input shape for every note mutation.
// Consolidating create/update/delete behind one `action` follows the
// Google servers' manage_* pattern (manage_sheet, manage_labels,
// manage_revision): reads stay discrete, mutations of one noun share
// a tool.
type manageNoteInput struct {
	Action        string `json:"action" jsonschema:"create, update or delete"`
	NoteID        int64  `json:"note_id,omitempty" jsonschema:"the note to act on, required by update and delete and ignored by create; list_notes reports it"`
	Content       string `json:"content,omitempty" jsonschema:"the note text, required by create. HTML is kept verbatim and plain text has its newlines turned into <br>, because Pipedrive stores what its editor renders"`
	DealID        int64  `json:"deal_id,omitempty" jsonschema:"hang the note off this deal; on update it moves the note, which is rarely what you want"`
	PersonID      int64  `json:"person_id,omitempty" jsonschema:"hang the note off this person"`
	OrgID         int64  `json:"org_id,omitempty" jsonschema:"hang the note off this organization"`
	LeadID        string `json:"lead_id,omitempty" jsonschema:"hang the note off this lead UUID"`
	ProjectID     int64  `json:"project_id,omitempty" jsonschema:"hang the note off this project"`
	DryRun        bool   `json:"dry_run,omitempty" jsonschema:"report what the write would find and change, and send nothing"`
	Overwrite     bool   `json:"overwrite,omitempty" jsonschema:"allow update to replace content that is already there. Without it such an update is refused, naming the note, because a note you did not read is one somebody else wrote; a refusal is NOT a retry signal — set this only when the user asked for what is already there to be replaced, never to get past a refusal they have not seen"`
	ExpectVersion string `json:"expect_version,omitempty" jsonschema:"the update_time from the read that informed this write; the write is refused if the note changed since. Best effort — Pipedrive has no compare-and-set — so it catches a concurrent edit, not a determined race"`
}

// manageNoteOutput reports the stored record back after the write and
// names every field the upstream actually changed, the way
// google-sheets' write_values names each value Google coerced.
type manageNoteOutput struct {
	Action  string      `json:"action" jsonschema:"the action that ran: create, update or delete"`
	Note    noteSummary `json:"note" jsonschema:"the note as Pipedrive stored it. For delete this is the record as it was immediately before removal, so the caller can see what went. When dry_run is true nothing was persisted and a created note carries id=0."`
	Changed []string    `json:"changed,omitempty" jsonschema:"names of the fields this write actually altered, empty when the write was a no-op. On a dry run these are the fields it would alter."`
	DryRun  bool        `json:"dry_run,omitempty" jsonschema:"true when dry_run was requested: nothing was sent upstream"`
}

// RegisterNotes wires the note tools into the MCP server. Reads are
// discrete (get_note, list_notes); every mutation goes through
// manage_note.
//
// manage_note registers unconditionally. Per CLAUDE.md hard rule 3 the
// destructive path is guarded at call time by dry_run and by the fact
// that a v1 note delete is soft, not by whether the tool exists.
//
// opts.DryRun is the server-wide floor — see manageNoteHandler.
func RegisterNotes(s *mcp.Server, c notesClient, opts RegisterOptions) {

	AddTool(s, &mcp.Tool{
		Name:        "get_note",
		Description: "Everything stored on one note: its HTML content, who wrote it, WHICH RECORD IT HANGS OFF, when it was written and last touched, and whether it is still active. A note anchors to exactly one of a deal, person, organization, lead or project — the other four ids come back zero, and that is how you tell what the note is about. A soft-deleted note still returns here with active_flag false; list_notes is what hides it. Cheap: one call. Use list_notes when you do not already have the id.",
		Annotations: readOnlyAnnotations(),
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
		Description: "List notes by what they hang off (deal_id, person_id, org_id, lead_id), who wrote them, when they were written, or when they were last touched. Default order is update_time descending, most-recently-edited first, which is the order that answers what has been written about X lately. Default limit is 25, maximum 100. IMPORTANT: a page holding fewer rows than the limit is NOT the end — keep paging while next_cursor comes back non-empty. Soft-deleted notes are filtered out here but still readable through get_note, so a note that vanished from this list has not necessarily gone. Notes live on Pipedrive v1, so the cursor wraps v1's offset rather than a v2 cursor; pass it back verbatim and it behaves the same. Use search first to turn a company or person name into the anchor id this filters on.",
		Annotations: readOnlyAnnotations(),
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
		Name:        "manage_note",
		Description: "Create, edit or remove a note — the free-text record that hangs off a deal, person, organization, lead or project. One call, whichever action: create takes content plus at least one anchor id, update and delete take note_id. Writing is guarded, and update and delete both read the note before they act, so either is two API calls. An update refuses to replace ANY field that already holds a value — its text, or the record it hangs off — unless you pass overwrite, and the refusal names each field it is protecting; filling a field that is empty destroys nothing and needs no permission. expect_version refuses the write outright if the note moved under you. Deleting is soft — Pipedrive v1 clears active_flag, so get_note still returns the note and only list_notes stops showing it — but NOTHING HERE SETS THE FLAG BACK, so treat it as one-way and reach for dry_run first. IMPORTANT: content is HTML, because Pipedrive stores what its editor renders; markup you send is kept verbatim and plain text has its newlines turned into <br>. Notes live on Pipedrive v1, which has no v2 equivalent to move to. Use list_notes to find note_id, and search to turn a company or person name into the anchor id a new note needs.",
		Annotations: mutatingAnnotations(),
	}, manageNoteHandler(c, opts.DryRun))
}

// manageNoteHandler dispatches on action. Each branch owns its own
// validation so a create never pays for the read-before-write that only
// update and delete need.
//
// dryRun is the server-wide PIPEDRIVE_DRY_RUN floor: a per-call
// dry_run: true can turn a rehearsal on, but the env flag cannot be
// turned off from the wire. An operator who sets it gets a server that
// writes nothing, which is what docs/security.md promises.
func manageNoteHandler(c notesClient, dryRun bool) mcp.ToolHandlerFor[manageNoteInput, manageNoteOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in manageNoteInput) (*mcp.CallToolResult, manageNoteOutput, error) {
		if err := validateAction(in.Action, allowedNoteActions); err != nil {
			return errorResult(err), manageNoteOutput{}, nil
		}
		in.DryRun = in.DryRun || dryRun

		var (
			res *mcp.CallToolResult
			out manageNoteOutput
		)
		switch in.Action {
		case "create":
			res, out = createNoteAction(ctx, c, in)
		case "update":
			res, out = updateNoteAction(ctx, c, in)
		case "delete":
			res, out = deleteNoteAction(ctx, c, in)
		default:
			// Named explicitly rather than letting delete be the
			// fallthrough. validateAction has already closed the enum,
			// so this is unreachable — but notes is the one resource
			// whose default branch destroys, and a fifth action added to
			// allowedNoteActions two hundred lines away should not
			// silently become a soft delete.
			return errorResult(fmt.Errorf("%w: action %q has no handler", pipedrive.ErrValidation, in.Action)), manageNoteOutput{}, nil
		}
		if res == nil {
			// Echo the action once, here, rather than having each
			// branch repeat a literal that nothing ties to its case.
			out.Action = in.Action
			out.DryRun = in.DryRun
		}
		return res, out, nil
	}
}

func createNoteAction(ctx context.Context, c notesClient, in manageNoteInput) (*mcp.CallToolResult, manageNoteOutput) {
	if err := validateNoteContent(in.Content, true); err != nil {
		return errorResult(err), manageNoteOutput{}
	}
	if !hasNoteAnchor(in) {
		return errorResult(fmt.Errorf("%w: a new note needs somewhere to live: set one of deal_id, person_id, org_id, lead_id or project_id", pipedrive.ErrValidation)), manageNoteOutput{}
	}
	req := pipedrive.CreateNoteRequest{
		Content:   in.Content,
		DealID:    in.DealID,
		PersonID:  in.PersonID,
		OrgID:     in.OrgID,
		LeadID:    in.LeadID,
		ProjectID: in.ProjectID,
	}
	created := syntheticNoteFromRequest(req)
	if !in.DryRun {
		n, err := c.CreateNote(ctx, req)
		if err != nil {
			return errorResult(err), manageNoteOutput{}
		}
		created = n
	}
	// Diff against the zero record so the report names the anchor the
	// create set, not just the content.
	return nil, manageNoteOutput{
		Note:    summarizeNote(created),
		Changed: changedFields(noteFields, &pipedrive.Note{}, created),
	}
}

// updateNoteAction reads the note before writing it, which is what makes
// the overwrite and expect_version guards possible at all: both need to
// know what is stored now. The extra GET is the price of the guard, and
// is the same trade google-sheets' write_values makes.
func updateNoteAction(ctx context.Context, c notesClient, in manageNoteInput) (*mcp.CallToolResult, manageNoteOutput) {
	if err := validateNoteContent(in.Content, false); err != nil {
		return errorResult(err), manageNoteOutput{}
	}
	// Cheap rejection first, so a request that asks for nothing costs no
	// round trip.
	if in.Content == "" && !hasNoteAnchor(in) {
		return errorResult(fmt.Errorf("%w: update needs at least one field to change: content, or one of the anchor ids", pipedrive.ErrValidation)), manageNoteOutput{}
	}
	if err := validatePositiveID(in.NoteID, "note_id"); err != nil {
		return errorResult(err), manageNoteOutput{}
	}
	req := pipedrive.UpdateNoteRequest{
		Content:   in.Content,
		DealID:    in.DealID,
		PersonID:  in.PersonID,
		OrgID:     in.OrgID,
		LeadID:    in.LeadID,
		ProjectID: in.ProjectID,
	}

	note, changed, res := guardedWrite[pipedrive.Note]{
		Spec:          noteFields,
		Resource:      fmt.Sprintf("note %d", in.NoteID),
		ExpectVersion: in.ExpectVersion,
		Version:       func(n *pipedrive.Note) string { return n.UpdateTime },
		Overwrite:     in.Overwrite,
		DryRun:        in.DryRun,
		Get:           func(ctx context.Context) (*pipedrive.Note, error) { return c.GetNote(ctx, in.NoteID) },
		Predict:       func(n *pipedrive.Note) pipedrive.Note { return noteAfterUpdate(*n, req) },
		Put:           func(ctx context.Context) (*pipedrive.Note, error) { return c.UpdateNote(ctx, in.NoteID, req) },
	}.run(ctx)
	if res != nil {
		return res, manageNoteOutput{}
	}
	return nil, manageNoteOutput{Note: summarizeNote(note), Changed: changed}
}

// deleteNoteAction reads the note before removing it so the result can
// show what went. No permitting argument is required beyond dry_run: a
// v1 delete only clears active_flag, which is the reversible case
// google-drive's trash_file handles the same way.
func deleteNoteAction(ctx context.Context, c notesClient, in manageNoteInput) (*mcp.CallToolResult, manageNoteOutput) {
	before, res := readNoteForWrite(ctx, c, in)
	if res != nil {
		return res, manageNoteOutput{}
	}
	if !before.ActiveFlag {
		// Already soft-deleted. Report the state instead of firing a
		// second DELETE that would change nothing.
		return nil, manageNoteOutput{Note: summarizeNote(before)}
	}
	if !in.DryRun {
		if err := c.DeleteNote(ctx, in.NoteID); err != nil {
			return errorResult(err), manageNoteOutput{}
		}
	}
	return nil, manageNoteOutput{Note: summarizeNote(before), Changed: []string{"active_flag"}}
}

// readNoteForWrite is the read-before-write every guarded mutation
// needs: validate the id, fetch the record the guards compare against,
// and apply the expect_version check. A non-nil result means the caller
// should stop and return it.
func readNoteForWrite(ctx context.Context, c notesClient, in manageNoteInput) (*pipedrive.Note, *mcp.CallToolResult) {
	if err := validatePositiveID(in.NoteID, "note_id"); err != nil {
		return nil, errorResult(err)
	}
	before, err := c.GetNote(ctx, in.NoteID)
	if err != nil {
		return nil, errorResult(err)
	}
	if err := checkExpectVersion(in.ExpectVersion, before.UpdateTime, fmt.Sprintf("note %d", in.NoteID)); err != nil {
		return nil, errorResult(err)
	}
	return before, nil
}

func hasNoteAnchor(in manageNoteInput) bool {
	return in.DealID != 0 || in.PersonID != 0 || in.OrgID != 0 || in.LeadID != "" || in.ProjectID != 0
}

// validateNoteContent enforces the MCP-boundary size cap. required
// distinguishes create (content is mandatory) from update (content is
// optional, since an update may only move the anchor).
func validateNoteContent(content string, required bool) error {
	if content == "" {
		if required {
			return fmt.Errorf("%w: content must not be empty", pipedrive.ErrValidation)
		}
		return nil
	}
	if len(content) > noteContentMaxBytes {
		return fmt.Errorf("%w: content is %d bytes; max %d", pipedrive.ErrValidation, len(content), noteContentMaxBytes)
	}
	return nil
}

// noteAfterUpdate overlays what the request sets onto the record as it
// stands. Pipedrive treats the v1 notes PUT as a partial update, so this
// is the record it is predicted to store — which lets the dry-run
// prediction and the post-write report use the same diff, instead of two
// walks that have to be kept agreeing with each other.
func noteAfterUpdate(before pipedrive.Note, req pipedrive.UpdateNoteRequest) pipedrive.Note {
	after := before
	if req.Content != "" {
		after.Content = req.Content
	}
	if req.DealID != 0 {
		after.DealID = &req.DealID
	}
	if req.PersonID != 0 {
		after.PersonID = &req.PersonID
	}
	if req.OrgID != 0 {
		after.OrgID = &req.OrgID
	}
	if req.LeadID != "" {
		after.LeadID = req.LeadID
	}
	if req.ProjectID != 0 {
		after.ProjectID = &req.ProjectID
	}
	return after
}

// noteFields is the one table of LLM-facing field names an update can
// touch. The diff and the overwrite guard both walk it, so adding a
// field to UpdateNoteRequest is one edit rather than three that can
// disagree.
//
// active_flag is deliberately absent: no update request can set it, so
// an update can never report it. delete names it directly instead.
var noteFields = []fieldSpec[pipedrive.Note]{
	{"content", func(n *pipedrive.Note) string { return n.Content }},
	{"deal_id", func(n *pipedrive.Note) string { return projectOptID(n.DealID) }},
	{"person_id", func(n *pipedrive.Note) string { return projectOptID(n.PersonID) }},
	{"org_id", func(n *pipedrive.Note) string { return projectOptID(n.OrgID) }},
	{"lead_id", func(n *pipedrive.Note) string { return n.LeadID }},
	{"project_id", func(n *pipedrive.Note) string { return projectOptID(n.ProjectID) }},
}

func summarizeNote(n *pipedrive.Note) noteSummary {
	return noteSummary{
		ID:                   n.ID,
		Content:              n.Content,
		UserID:               n.UserID,
		LastUpdateUserID:     deref(n.LastUpdateUserID),
		DealID:               deref(n.DealID),
		PersonID:             deref(n.PersonID),
		OrgID:                deref(n.OrgID),
		LeadID:               n.LeadID,
		ProjectID:            deref(n.ProjectID),
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

// syntheticNoteFromRequest builds a placeholder Note that mirrors the
// CreateNoteRequest, used only on the dry-run path so manage_note's
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

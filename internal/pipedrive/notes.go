package pipedrive

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// ListNotesOptions filters a /api/v1/notes list call. v1 uses
// offset-style pagination (Start + Limit) — the tool layer encodes
// the int next_start as an opaque cursor string so the LLM-facing
// surface stays uniform with the v2 resources.
//
// v1 supports many more filter dimensions than v2 list endpoints
// (start_date / end_date / pinned_to_X_flag) — wire only what the
// LLM-facing tools actually expose to keep the surface narrow.
type ListNotesOptions struct {
	UserID       int64
	DealID       int64
	PersonID     int64
	OrgID        int64
	LeadID       string // UUID
	ProjectID    int64
	StartDate    string // YYYY-MM-DD
	EndDate      string // YYYY-MM-DD
	UpdatedSince string // RFC3339
	Sort         string // "<field> asc|desc, ..." — v1 accepts either case; tools/effectiveSort emits lowercase
	Start        int
	Limit        int
}

// CreateNoteRequest is the JSON body for POST /api/v1/notes. Pipedrive
// requires Content + at least one anchor (DealID / PersonID / OrgID /
// LeadID); the tool layer enforces this client-side so a typo
// surfaces as [validation] instead of an upstream 400.
type CreateNoteRequest struct {
	Content   string `json:"content"`
	DealID    int64  `json:"deal_id,omitempty"`
	PersonID  int64  `json:"person_id,omitempty"`
	OrgID     int64  `json:"org_id,omitempty"`
	LeadID    string `json:"lead_id,omitempty"`
	ProjectID int64  `json:"project_id,omitempty"`
}

// GetNote fetches a single note by ID via /api/v1/notes/{id}. v2 has
// no equivalent endpoint; see types.go for the carve-out rationale.
func (c *Client) GetNote(ctx context.Context, id int64) (*Note, error) {
	var resp itemEnvelope[Note]
	if err := c.doV1(ctx, "/notes/"+strconv.FormatInt(id, 10), &resp); err != nil {
		return nil, err
	}
	n := resp.Data
	return &n, nil
}

// ListNotes issues a /api/v1/notes list with the given filters.
// Returns the page plus the v1 pagination block (or nil if the
// response carries no pagination, e.g. on a 0-row response).
func (c *Client) ListNotes(ctx context.Context, opts ListNotesOptions) ([]Note, *V1Pagination, error) {
	q := url.Values{}
	if opts.UserID > 0 {
		q.Set("user_id", strconv.FormatInt(opts.UserID, 10))
	}
	if opts.DealID > 0 {
		q.Set("deal_id", strconv.FormatInt(opts.DealID, 10))
	}
	if opts.PersonID > 0 {
		q.Set("person_id", strconv.FormatInt(opts.PersonID, 10))
	}
	if opts.OrgID > 0 {
		q.Set("org_id", strconv.FormatInt(opts.OrgID, 10))
	}
	if opts.LeadID != "" {
		q.Set("lead_id", opts.LeadID)
	}
	if opts.ProjectID > 0 {
		q.Set("project_id", strconv.FormatInt(opts.ProjectID, 10))
	}
	if opts.StartDate != "" {
		q.Set("start_date", opts.StartDate)
	}
	if opts.EndDate != "" {
		q.Set("end_date", opts.EndDate)
	}
	if opts.UpdatedSince != "" {
		q.Set("updated_since", opts.UpdatedSince)
	}
	if opts.Sort != "" {
		q.Set("sort", opts.Sort)
	}
	if opts.Start > 0 {
		q.Set("start", strconv.Itoa(opts.Start))
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}

	var resp listEnvelope[Note]
	if err := c.doV1(ctx, buildPath("/notes", q), &resp); err != nil {
		return nil, nil, err
	}
	return resp.Data, resp.AdditionalData.V1Pagination, nil
}

// CreateNote posts a new note via /api/v1/notes. Returns the created
// note (Pipedrive echoes the full record on success).
func (c *Client) CreateNote(ctx context.Context, req CreateNoteRequest) (*Note, error) {
	if req.Content == "" {
		return nil, fmt.Errorf("%w: content must not be empty", ErrValidation)
	}
	if req.DealID == 0 && req.PersonID == 0 && req.OrgID == 0 && req.LeadID == "" && req.ProjectID == 0 {
		return nil, fmt.Errorf("%w: at least one of deal_id / person_id / org_id / lead_id / project_id is required", ErrValidation)
	}
	var resp itemEnvelope[Note]
	if err := c.postV1(ctx, "/notes", req, &resp); err != nil {
		return nil, err
	}
	n := resp.Data
	return &n, nil
}

// DeleteNote removes a note via DELETE /api/v1/notes/{id}. v1 deletes
// are SOFT — the record persists with active_flag=false and is still
// readable via GetNote, but list_notes filters it out by default.
func (c *Client) DeleteNote(ctx context.Context, id int64) error {
	return c.deleteV1(ctx, "/notes/"+strconv.FormatInt(id, 10), nil)
}

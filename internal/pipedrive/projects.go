package pipedrive

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// Project is a Pipedrive project (subset). Its links to deals, persons
// and organizations are id lists, and a write of one replaces it whole:
// a PATCH carrying deal_ids [b] over [a] leaves [b]. Checked by the
// integration test TestWrite_ProjectRoundTrip. v2 has no project field
// definitions to read, so custom_fields keep their stored keys.
// https://developers.pipedrive.com/docs/api/v1/Projects#getProject
type Project struct {
	ID           int64          `json:"id"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	Status       string         `json:"status"` // open | completed | canceled | deleted
	OwnerID      int64          `json:"owner_id"`
	BoardID      int64          `json:"board_id"`
	PhaseID      int64          `json:"phase_id"`
	DealIDs      []int64        `json:"deal_ids"`
	PersonIDs    []int64        `json:"person_ids"`
	OrgIDs       []int64        `json:"org_ids"`
	StartDate    string         `json:"start_date"`
	EndDate      string         `json:"end_date"`
	AddTime      string         `json:"add_time"`
	UpdateTime   string         `json:"update_time"`
	ArchiveTime  string         `json:"archive_time"`
	CustomFields map[string]any `json:"custom_fields"`
}

// ProjectBoard is a board of projects, as a pipeline is of deals.
type ProjectBoard struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	OrderNr int    `json:"order_nr"`
}

// ProjectPhase is one phase of a board, as a stage is of a pipeline.
type ProjectPhase struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	BoardID int64  `json:"board_id"`
	OrderNr int    `json:"order_nr"`
}

// ListProjectsOptions filters a /projects list call. Archived reads
// /projects/archived, which /projects leaves out, and which takes status
// and phase_id alone; the deal, person and organization filters are not
// sent there, and the tools layer refuses them.
type ListProjectsOptions struct {
	Status   string // open | completed | canceled | deleted
	PhaseID  int64
	DealID   int64
	PersonID int64
	OrgID    int64
	Limit    int
	Cursor   string
	Archived bool
}

// ProjectRequest is the body for creating or editing a project. Only
// title is required on a create; Pipedrive files a project with no board
// or phase on a board and a phase of its choosing. Checked by the
// integration test TestWrite_ProjectRoundTrip.
type ProjectRequest struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Status      *string `json:"status,omitempty"`
	OwnerID     *int64  `json:"owner_id,omitempty"`
	BoardID     *int64  `json:"board_id,omitempty"`
	PhaseID     *int64  `json:"phase_id,omitempty"`
	DealIDs     []int64 `json:"deal_ids,omitempty"`
	PersonIDs   []int64 `json:"person_ids,omitempty"`
	OrgIDs      []int64 `json:"org_ids,omitempty"`
	StartDate   *string `json:"start_date,omitempty"`
	EndDate     *string `json:"end_date,omitempty"`
}

// GetProject fetches one project by id. A deleted project still reads,
// with status deleted.
func (c *Client) GetProject(ctx context.Context, id int64) (*Project, error) {
	var resp itemEnvelope[Project]
	if err := c.do(ctx, "/projects/"+strconv.FormatInt(id, 10), &resp); err != nil {
		return nil, err
	}
	p := resp.Data
	return &p, nil
}

// ListProjects issues a /projects (or /projects/archived) list.
func (c *Client) ListProjects(ctx context.Context, opts ListProjectsOptions) ([]Project, string, error) {
	q := url.Values{}
	if opts.Status != "" {
		q.Set("status", opts.Status)
	}
	if opts.PhaseID > 0 {
		q.Set("phase_id", strconv.FormatInt(opts.PhaseID, 10))
	}
	if !opts.Archived {
		for k, v := range map[string]int64{"deal_id": opts.DealID, "person_id": opts.PersonID, "org_id": opts.OrgID} {
			if v > 0 {
				q.Set(k, strconv.FormatInt(v, 10))
			}
		}
	}
	setLimitCursor(q, opts.Limit, opts.Cursor)
	path := "/projects"
	if opts.Archived {
		path = "/projects/archived"
	}
	var resp listEnvelope[Project]
	if err := c.do(ctx, buildPath(path, q), &resp); err != nil {
		return nil, "", err
	}
	return resp.Data, resp.AdditionalData.NextCursor, nil
}

// CreateProject adds a project.
func (c *Client) CreateProject(ctx context.Context, req ProjectRequest) (*Project, error) {
	if req.Title == nil || *req.Title == "" {
		return nil, fmt.Errorf("%w: title must not be empty", ErrValidation)
	}
	var resp itemEnvelope[Project]
	if err := c.postV2(ctx, "/projects", req, &resp); err != nil {
		return nil, err
	}
	p := resp.Data
	return &p, nil
}

// UpdateProject edits a project and returns it as stored. An archived
// project still takes edits. Checked by the integration test
// TestWrite_ProjectRoundTrip.
func (c *Client) UpdateProject(ctx context.Context, id int64, req ProjectRequest) (*Project, error) {
	var resp itemEnvelope[Project]
	if err := c.patchV2(ctx, "/projects/"+strconv.FormatInt(id, 10), req, &resp); err != nil {
		return nil, err
	}
	p := resp.Data
	return &p, nil
}

// ArchiveProject archives a project: it sets archive_time and moves the
// project to /projects/archived. Pipedrive refuses the call with no body
// ("Body cannot be empty when content-type is set to
// 'application/json'"), so it sends {}. v2 has no unarchive. Checked by
// the integration test TestWrite_ProjectRoundTrip.
// https://developers.pipedrive.com/docs/api/v1/Projects#archiveProject
func (c *Client) ArchiveProject(ctx context.Context, id int64) (*Project, error) {
	var resp itemEnvelope[Project]
	if err := c.postV2(ctx, "/projects/"+strconv.FormatInt(id, 10)+"/archive", struct{}{}, &resp); err != nil {
		return nil, err
	}
	p := resp.Data
	return &p, nil
}

// DeleteProject marks a project deleted, softly: it still reads, with
// status deleted. Checked by the integration test
// TestWrite_ProjectRoundTrip.
// https://developers.pipedrive.com/docs/api/v1/Projects#deleteProject
func (c *Client) DeleteProject(ctx context.Context, id int64) error {
	return c.deleteV2(ctx, "/projects/"+strconv.FormatInt(id, 10))
}

// ListProjectBoards reads every board.
func (c *Client) ListProjectBoards(ctx context.Context) ([]ProjectBoard, error) {
	var resp listEnvelope[ProjectBoard]
	if err := c.do(ctx, "/boards", &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// ListProjectPhases reads every phase of one board; /phases requires a
// board_id.
func (c *Client) ListProjectPhases(ctx context.Context, boardID int64) ([]ProjectPhase, error) {
	q := url.Values{}
	q.Set("board_id", strconv.FormatInt(boardID, 10))
	var resp listEnvelope[ProjectPhase]
	if err := c.do(ctx, buildPath("/phases", q), &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

package tools

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

type projectsClient interface {
	GetProject(ctx context.Context, id int64) (*pipedrive.Project, error)
	ListProjects(ctx context.Context, opts pipedrive.ListProjectsOptions) ([]pipedrive.Project, string, error)
	CreateProject(ctx context.Context, req pipedrive.ProjectRequest) (*pipedrive.Project, error)
	UpdateProject(ctx context.Context, id int64, req pipedrive.ProjectRequest) (*pipedrive.Project, error)
	ArchiveProject(ctx context.Context, id int64) (*pipedrive.Project, error)
	DeleteProject(ctx context.Context, id int64) error
	ListProjectBoards(ctx context.Context) ([]pipedrive.ProjectBoard, error)
	ListProjectPhases(ctx context.Context, boardID int64) ([]pipedrive.ProjectPhase, error)
}

var allowedProjectStatuses = map[string]bool{"open": true, "completed": true, "canceled": true, "deleted": true}

// allowedProjectWriteStatuses leaves deleted out: delete is the action
// that deletes, and it reads the project first.
var allowedProjectWriteStatuses = map[string]bool{"open": true, "completed": true, "canceled": true}

type projectSummary struct {
	ID           int64          `json:"id" jsonschema:"the project's numeric id; 0 on a dry-run create"`
	Title        string         `json:"title" jsonschema:"the project's title"`
	Description  string         `json:"description,omitempty" jsonschema:"the project's description"`
	Status       string         `json:"status" jsonschema:"open | completed | canceled | deleted"`
	OwnerID      int64          `json:"owner_id" jsonschema:"id of the user who owns the project"`
	BoardID      int64          `json:"board_id" jsonschema:"the board the project sits on; list_project_boards names it"`
	PhaseID      int64          `json:"phase_id" jsonschema:"the phase of the board the project is in"`
	DealIDs      []int64        `json:"deal_ids,omitempty" jsonschema:"the deals the project is linked to"`
	PersonIDs    []int64        `json:"person_ids,omitempty" jsonschema:"the persons the project is linked to"`
	OrgIDs       []int64        `json:"org_ids,omitempty" jsonschema:"the organizations the project is linked to"`
	StartDate    string         `json:"start_date,omitempty" jsonschema:"YYYY-MM-DD the project starts"`
	EndDate      string         `json:"end_date,omitempty" jsonschema:"YYYY-MM-DD the project ends"`
	ArchiveTime  string         `json:"archive_time,omitempty" jsonschema:"when the project was archived; empty for a live project"`
	AddTime      string         `json:"add_time,omitempty" jsonschema:"timestamp the project was created"`
	UpdateTime   string         `json:"update_time,omitempty" jsonschema:"timestamp the project was last updated; expect_version takes it"`
	CustomFields map[string]any `json:"custom_fields,omitempty" jsonschema:"the project's custom fields under their stored keys: Pipedrive v2 publishes no project field names to resolve them with"`
}

type getProjectInput struct {
	ProjectID int64 `json:"project_id" jsonschema:"the project's numeric id"`
}

type getProjectOutput struct {
	Project projectSummary `json:"project" jsonschema:"the requested project"`
}

type listProjectsInput struct {
	Status   string `json:"status,omitempty" jsonschema:"open | completed | canceled | deleted. Omit for every status but deleted"`
	PhaseID  int64  `json:"phase_id,omitempty" jsonschema:"return only projects in this phase; 0 = no filter"`
	DealID   int64  `json:"deal_id,omitempty" jsonschema:"return only projects linked to this deal; 0 = no filter. Not available with archived"`
	PersonID int64  `json:"person_id,omitempty" jsonschema:"return only projects linked to this person; 0 = no filter. Not available with archived"`
	OrgID    int64  `json:"org_id,omitempty" jsonschema:"return only projects linked to this organization; 0 = no filter. Not available with archived"`
	Archived bool   `json:"archived,omitempty" jsonschema:"read the archive instead of the live projects; the archive is a separate collection that no other list reaches, and it takes status and phase_id alone"`
	Limit    int    `json:"limit,omitempty" jsonschema:"page size; default 25, max 100"`
	Cursor   string `json:"cursor,omitempty" jsonschema:"opaque pagination token from a previous list_projects response; omit for the first page"`
}

type listProjectsOutput struct {
	Projects   []projectSummary `json:"projects" jsonschema:"matching projects on this page"`
	NextCursor string           `json:"next_cursor,omitempty" jsonschema:"pass to the next list_projects call to get the next page; empty when there are no more pages"`
}

type boardRow struct {
	ID     int64      `json:"id" jsonschema:"the board's id, which a project's board_id names"`
	Name   string     `json:"name" jsonschema:"the board's name"`
	Phases []phaseRow `json:"phases" jsonschema:"the board's phases, in board order"`
}

type phaseRow struct {
	ID   int64  `json:"id" jsonschema:"the phase's id, which a project's phase_id names"`
	Name string `json:"name" jsonschema:"the phase's name"`
}

type listProjectBoardsOutput struct {
	Boards []boardRow `json:"boards" jsonschema:"every board with its phases, in order"`
}

type manageProjectInput struct {
	Action        string   `json:"action" jsonschema:"create, update, archive or delete"`
	ProjectID     int64    `json:"project_id,omitempty" jsonschema:"the project to act on, required by every action except create"`
	Title         *string  `json:"title,omitempty" jsonschema:"the project's title, required by create; if the user did not give one, ask rather than inventing it"`
	Description   *string  `json:"description,omitempty" jsonschema:"the project's description. Omit to leave it as it is"`
	Status        *string  `json:"status,omitempty" jsonschema:"open | completed | canceled. Omit to leave it as it is; delete is its own action"`
	OwnerID       *int64   `json:"owner_id,omitempty" jsonschema:"the user who owns the project; omit on create to take the API token's own user"`
	BoardID       *int64   `json:"board_id,omitempty" jsonschema:"the board; list_project_boards reports the ids. Omit on create to take the workspace's first board"`
	PhaseID       *int64   `json:"phase_id,omitempty" jsonschema:"the phase of the board; list_project_boards reports the ids. Omit on create to take the board's first phase"`
	DealIDs       []int64  `json:"deal_ids,omitempty" jsonschema:"the deals to link. A write REPLACES the whole list, so send every deal the project keeps; omit to leave the links as they are"`
	PersonIDs     []int64  `json:"person_ids,omitempty" jsonschema:"the persons to link. A write REPLACES the whole list; omit to leave it as it is"`
	OrgIDs        []int64  `json:"org_ids,omitempty" jsonschema:"the organizations to link. A write REPLACES the whole list; omit to leave it as it is"`
	StartDate     *string  `json:"start_date,omitempty" jsonschema:"YYYY-MM-DD the project starts. Omit to leave it as it is"`
	EndDate       *string  `json:"end_date,omitempty" jsonschema:"YYYY-MM-DD the project ends. Omit to leave it as it is"`
	DryRun        bool     `json:"dry_run,omitempty" jsonschema:"report what the write would find and change, and send nothing"`
	Overwrite     []string `json:"overwrite,omitempty" jsonschema:"the fields this update may replace, named exactly as the refusal listed them, e.g. [\"title\", \"deal_ids\"]. A refusal is NOT a retry signal: name a field only when the user asked for what is there to be replaced"`
	ExpectVersion string   `json:"expect_version,omitempty" jsonschema:"the update_time from the read that informed this write; the write is refused if the project changed since"`
}

type manageProjectOutput struct {
	Action  string         `json:"action" jsonschema:"the action that ran"`
	Project projectSummary `json:"project" jsonschema:"the project as Pipedrive stored it. For delete, as it was just before. When dry_run is true nothing was persisted and a created project carries id=0"`
	Changed []string       `json:"changed,omitempty" jsonschema:"names of the fields this write altered, or on a dry run would alter"`
	DryRun  bool           `json:"dry_run,omitempty" jsonschema:"true when nothing was sent upstream"`
}

var allowedProjectActions = map[string]bool{"create": true, "update": true, "archive": true, "delete": true}

// projectFields drives the update's diff and overwrite guard (D-38).
// The id lists are sorted, since a link list is a set.
var projectFields = []fieldSpec[pipedrive.Project]{
	{"title", func(p *pipedrive.Project) string { return p.Title }},
	{"description", func(p *pipedrive.Project) string { return p.Description }},
	{"status", func(p *pipedrive.Project) string { return p.Status }},
	{"owner_id", func(p *pipedrive.Project) string { return projectID(p.OwnerID) }},
	{"board_id", func(p *pipedrive.Project) string { return projectID(p.BoardID) }},
	{"phase_id", func(p *pipedrive.Project) string { return projectID(p.PhaseID) }},
	{"deal_ids", func(p *pipedrive.Project) string { return projectIDSet(p.DealIDs) }},
	{"person_ids", func(p *pipedrive.Project) string { return projectIDSet(p.PersonIDs) }},
	{"org_ids", func(p *pipedrive.Project) string { return projectIDSet(p.OrgIDs) }},
	{"start_date", func(p *pipedrive.Project) string { return p.StartDate }},
	{"end_date", func(p *pipedrive.Project) string { return p.EndDate }},
}

func projectIDSet(ids []int64) string {
	sorted := slices.Sorted(slices.Values(ids))
	return projectCollection(sorted, func(id int64) string { return strconv.FormatInt(id, 10) })
}

// RegisterProjects wires the project tools. manage_project registers
// unconditionally and guards at call time (D-46).
func RegisterProjects(s *mcp.Server, c projectsClient, opts RegisterOptions) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_project",
		Description: "Fetch one Pipedrive project by project_id: title, description, status (open | completed | canceled | deleted), owner, the board and phase it sits in, the deals, persons and organizations it is linked to, its dates, and whether it is archived. A deleted project still reads, with status deleted. Custom fields come back under their stored keys, since Pipedrive publishes no project field names. Unknown project_id returns a [not_found] error.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getProjectInput) (*mcp.CallToolResult, getProjectOutput, error) {
		if err := validatePositiveID(in.ProjectID, "project_id"); err != nil {
			return errorResult(err), getProjectOutput{}, nil
		}
		p, err := c.GetProject(ctx, in.ProjectID)
		if err != nil {
			return errorResult(err), getProjectOutput{}, nil
		}
		return nil, getProjectOutput{Project: summarizeProject(p)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_projects",
		Description: "List projects filtered by status, phase, or the deal, person or organization they are linked to. Default limit is 25, max 100. IMPORTANT: a page holding fewer rows than the limit is NOT the end — keep paging while next_cursor comes back non-empty. Archived projects are NOT in this collection; pass archived to read them, which takes status and phase_id alone. list_project_boards names the boards and phases.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listProjectsInput) (*mcp.CallToolResult, listProjectsOutput, error) {
		if err := validateEnum(in.Status, "status", allowedProjectStatuses); err != nil {
			return errorResult(err), listProjectsOutput{}, nil
		}
		if in.Archived && (in.DealID > 0 || in.PersonID > 0 || in.OrgID > 0) {
			return errorResult(fmt.Errorf("%w: the archive takes status and phase_id alone; Pipedrive ignores a deal, person or organization filter there", pipedrive.ErrValidation)), listProjectsOutput{}, nil
		}
		projects, next, err := c.ListProjects(ctx, pipedrive.ListProjectsOptions{
			Status: in.Status, PhaseID: in.PhaseID, DealID: in.DealID, PersonID: in.PersonID, OrgID: in.OrgID,
			Archived: in.Archived, Limit: clampLimit(in.Limit), Cursor: in.Cursor,
		})
		if err != nil {
			return errorResult(err), listProjectsOutput{}, nil
		}
		out := listProjectsOutput{Projects: make([]projectSummary, 0, len(projects)), NextCursor: next}
		for i := range projects {
			out.Projects = append(out.Projects, summarizeProject(&projects[i]))
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_project_boards",
		Description: "List every project board with its phases, in order — the board_id and phase_id a project sits in, as a deal sits in a pipeline and a stage. One call per board, read whole; no paging.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listProjectBoardsOutput, error) {
		boards, err := c.ListProjectBoards(ctx)
		if err != nil {
			return errorResult(err), listProjectBoardsOutput{}, nil
		}
		slices.SortStableFunc(boards, func(a, b pipedrive.ProjectBoard) int { return a.OrderNr - b.OrderNr })
		out := listProjectBoardsOutput{Boards: make([]boardRow, 0, len(boards))}
		for _, b := range boards {
			phases, err := c.ListProjectPhases(ctx, b.ID)
			if err != nil {
				return errorResult(err), listProjectBoardsOutput{}, nil
			}
			slices.SortStableFunc(phases, func(a, b pipedrive.ProjectPhase) int { return a.OrderNr - b.OrderNr })
			row := boardRow{ID: b.ID, Name: b.Name, Phases: make([]phaseRow, 0, len(phases))}
			for _, p := range phases {
				row.Phases = append(row.Phases, phaseRow{ID: p.ID, Name: p.Name})
			}
			out.Boards = append(out.Boards, row)
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "manage_project",
		Description: "Create a project, edit one, archive one, or delete one. create takes title; Pipedrive files a project with no board_id or phase_id on its first board's first phase. update, archive and delete take project_id and read the project first. An update refuses to replace ANY field that already holds a value unless you name it in overwrite, and the refusal names each one. IMPORTANT: deal_ids, person_ids and org_ids are whole lists, and a write REPLACES each one sent, so send every link the project keeps. archive takes no overwrite: it moves the project to the archive, where list_projects reaches it only with archived, and NOTHING HERE UNARCHIVES IT — Pipedrive's API has no call for that. Deleting is soft: the project still reads, with status deleted, so delete takes dry_run and expect_version and nothing more, but nothing here restores it.",
		Annotations: mutatingAnnotations(),
	}, manageProjectHandler(c, opts.DryRun))
}

func summarizeProject(p *pipedrive.Project) projectSummary {
	return projectSummary{
		ID: p.ID, Title: p.Title, Description: p.Description, Status: p.Status, OwnerID: p.OwnerID,
		BoardID: p.BoardID, PhaseID: p.PhaseID,
		DealIDs: slices.Clone(p.DealIDs), PersonIDs: slices.Clone(p.PersonIDs), OrgIDs: slices.Clone(p.OrgIDs),
		StartDate: p.StartDate, EndDate: p.EndDate, ArchiveTime: p.ArchiveTime,
		AddTime: p.AddTime, UpdateTime: p.UpdateTime, CustomFields: p.CustomFields,
	}
}

func manageProjectHandler(c projectsClient, dryRun bool) mcp.ToolHandlerFor[manageProjectInput, manageProjectOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in manageProjectInput) (*mcp.CallToolResult, manageProjectOutput, error) {
		if err := validateAction(in.Action, allowedProjectActions); err != nil {
			return errorResult(err), manageProjectOutput{}, nil
		}
		if err := validateProjectInput(in); err != nil {
			return errorResult(err), manageProjectOutput{}, nil
		}
		in.DryRun = in.DryRun || dryRun
		var (
			res *mcp.CallToolResult
			out manageProjectOutput
		)
		switch in.Action {
		case "create":
			res, out = createProjectAction(ctx, c, in)
		case "update":
			res, out = updateProjectAction(ctx, c, in)
		case "archive":
			res, out = archiveProjectAction(ctx, c, in)
		case "delete":
			res, out = deleteProjectAction(ctx, c, in)
		default:
			res = errorResult(fmt.Errorf("%w: action %q has no handler", pipedrive.ErrValidation, in.Action))
		}
		if res == nil {
			out.Action = in.Action
			out.DryRun = in.DryRun
		}
		return res, out, nil
	}
}

func validateProjectInput(in manageProjectInput) error {
	if in.Status != nil {
		if err := validateEnum(*in.Status, "status", allowedProjectWriteStatuses); err != nil {
			return err
		}
	}
	for name, d := range map[string]*string{"start_date": in.StartDate, "end_date": in.EndDate} {
		if d != nil {
			if _, err := time.Parse(time.DateOnly, *d); err != nil {
				return fmt.Errorf("%w: %s %q is not a YYYY-MM-DD date", pipedrive.ErrValidation, name, *d)
			}
		}
	}
	return nil
}

func projectRequestFor(in manageProjectInput) pipedrive.ProjectRequest {
	return pipedrive.ProjectRequest{
		Title: in.Title, Description: in.Description, Status: in.Status, OwnerID: in.OwnerID,
		BoardID: in.BoardID, PhaseID: in.PhaseID, DealIDs: in.DealIDs, PersonIDs: in.PersonIDs, OrgIDs: in.OrgIDs,
		StartDate: in.StartDate, EndDate: in.EndDate,
	}
}

func projectAfter(before pipedrive.Project, req pipedrive.ProjectRequest) pipedrive.Project {
	after := before
	setIf(&after.Title, req.Title)
	setIf(&after.Description, req.Description)
	setIf(&after.Status, req.Status)
	setIf(&after.OwnerID, req.OwnerID)
	setIf(&after.BoardID, req.BoardID)
	setIf(&after.PhaseID, req.PhaseID)
	setIf(&after.StartDate, req.StartDate)
	setIf(&after.EndDate, req.EndDate)
	if req.DealIDs != nil {
		after.DealIDs = slices.Clone(req.DealIDs)
	}
	if req.PersonIDs != nil {
		after.PersonIDs = slices.Clone(req.PersonIDs)
	}
	if req.OrgIDs != nil {
		after.OrgIDs = slices.Clone(req.OrgIDs)
	}
	return after
}

func createProjectAction(ctx context.Context, c projectsClient, in manageProjectInput) (*mcp.CallToolResult, manageProjectOutput) {
	if in.Title == nil || strings.TrimSpace(*in.Title) == "" {
		return errorResult(fmt.Errorf("%w: title must not be empty", pipedrive.ErrValidation)), manageProjectOutput{}
	}
	req := projectRequestFor(in)
	created := projectAfter(pipedrive.Project{Status: "open"}, req)
	result := &created
	if !in.DryRun {
		var err error
		if result, err = c.CreateProject(ctx, req); err != nil {
			return errorResult(err), manageProjectOutput{}
		}
	}
	return nil, manageProjectOutput{Project: summarizeProject(result), Changed: changedFields(projectFields, &pipedrive.Project{}, result)}
}

func updateProjectAction(ctx context.Context, c projectsClient, in manageProjectInput) (*mcp.CallToolResult, manageProjectOutput) {
	if err := validatePositiveID(in.ProjectID, "project_id"); err != nil {
		return errorResult(err), manageProjectOutput{}
	}
	req := projectRequestFor(in)
	p, changed, res := guardedWrite[pipedrive.Project]{
		Spec:          projectFields,
		Resource:      fmt.Sprintf("project %d", in.ProjectID),
		ExpectVersion: in.ExpectVersion,
		Version:       func(p *pipedrive.Project) string { return p.UpdateTime },
		Overwrite:     in.Overwrite,
		DryRun:        in.DryRun,
		Get:           func(ctx context.Context) (*pipedrive.Project, error) { return c.GetProject(ctx, in.ProjectID) },
		Predict:       func(p *pipedrive.Project) pipedrive.Project { return projectAfter(*p, req) },
		Put:           func(ctx context.Context) (*pipedrive.Project, error) { return c.UpdateProject(ctx, in.ProjectID, req) },
	}.run(ctx)
	if res != nil {
		return res, manageProjectOutput{}
	}
	return nil, manageProjectOutput{Project: summarizeProject(p), Changed: changed}
}

// readProjectForAction is the read archive and delete make first, with
// the expect_version check.
func readProjectForAction(ctx context.Context, c projectsClient, in manageProjectInput) (*pipedrive.Project, *mcp.CallToolResult) {
	if err := validatePositiveID(in.ProjectID, "project_id"); err != nil {
		return nil, errorResult(err)
	}
	before, err := c.GetProject(ctx, in.ProjectID)
	if err != nil {
		return nil, errorResult(err)
	}
	if err := checkExpectVersion(in.ExpectVersion, before.UpdateTime, fmt.Sprintf("project %d", in.ProjectID)); err != nil {
		return nil, errorResult(err)
	}
	return before, nil
}

// archiveProjectAction is a transition: the caller named it, and it
// loses nothing, since an archived project keeps every field and still
// takes edits, so it takes no overwrite (D-43). An archived project is
// reported as it stands rather than archived twice.
func archiveProjectAction(ctx context.Context, c projectsClient, in manageProjectInput) (*mcp.CallToolResult, manageProjectOutput) {
	before, res := readProjectForAction(ctx, c, in)
	if res != nil {
		return res, manageProjectOutput{}
	}
	if before.ArchiveTime != "" {
		return nil, manageProjectOutput{Project: summarizeProject(before)}
	}
	after := before
	if !in.DryRun {
		var err error
		if after, err = c.ArchiveProject(ctx, in.ProjectID); err != nil {
			return errorResult(err), manageProjectOutput{}
		}
	}
	return nil, manageProjectOutput{Project: summarizeProject(after), Changed: []string{"archive_time"}}
}

// deleteProjectAction marks a project deleted: soft, so dry_run and
// expect_version and nothing else (D-47).
func deleteProjectAction(ctx context.Context, c projectsClient, in manageProjectInput) (*mcp.CallToolResult, manageProjectOutput) {
	before, res := readProjectForAction(ctx, c, in)
	if res != nil {
		return res, manageProjectOutput{}
	}
	if before.Status == "deleted" {
		return nil, manageProjectOutput{Project: summarizeProject(before)}
	}
	if !in.DryRun {
		if err := c.DeleteProject(ctx, in.ProjectID); err != nil {
			return errorResult(err), manageProjectOutput{}
		}
	}
	return nil, manageProjectOutput{Project: summarizeProject(before), Changed: []string{"status"}}
}

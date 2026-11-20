package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/config"
	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

type filesClient interface {
	GetFile(ctx context.Context, id int64) (*pipedrive.File, error)
	ListFiles(ctx context.Context, sort string, start, limit int) ([]pipedrive.File, *pipedrive.V1Pagination, error)
	UploadFile(ctx context.Context, name string, content io.Reader, at pipedrive.FileAnchor) (*pipedrive.File, error)
	UpdateFile(ctx context.Context, id int64, name, description *string) (*pipedrive.File, error)
	DeleteFile(ctx context.Context, id int64) error
}

// maxUploadBytes bounds what one upload reads into memory.
const maxUploadBytes = 50 << 20

type fileSummary struct {
	ID          int64  `json:"id" jsonschema:"the file's numeric id; 0 on a dry-run upload"`
	Name        string `json:"name" jsonschema:"the file's display name"`
	FileName    string `json:"file_name,omitempty" jsonschema:"the stored file's name"`
	Description string `json:"description,omitempty" jsonschema:"the file's description"`
	FileSize    int64  `json:"file_size,omitempty" jsonschema:"size in bytes"`
	DealID      int64  `json:"deal_id,omitempty" jsonschema:"the deal it is attached to, 0 if none"`
	PersonID    int64  `json:"person_id,omitempty" jsonschema:"the person it is attached to, 0 if none"`
	OrgID       int64  `json:"org_id,omitempty" jsonschema:"the organization it is attached to, 0 if none"`
	ProductID   int64  `json:"product_id,omitempty" jsonschema:"the product it is attached to, 0 if none"`
	ActivityID  int64  `json:"activity_id,omitempty" jsonschema:"the activity it is attached to, 0 if none"`
	LeadID      string `json:"lead_id,omitempty" jsonschema:"the lead it is attached to, empty if none"`
	ProjectID   int64  `json:"project_id,omitempty" jsonschema:"the project it is attached to, 0 if none"`
	ActiveFlag  bool   `json:"active_flag" jsonschema:"false once the file is deleted; Pipedrive removes it for good after 30 days"`
	URL         string `json:"url,omitempty" jsonschema:"Pipedrive's link to the file, which needs a signed-in session to open"`
	AddTime     string `json:"add_time,omitempty" jsonschema:"timestamp the file was added"`
	UpdateTime  string `json:"update_time,omitempty" jsonschema:"timestamp the file was last updated; expect_version takes it"`
}

type getFileInput struct {
	FileID int64 `json:"file_id" jsonschema:"the file's numeric id; search with type=file or list_files reports it"`
}

type getFileOutput struct {
	File fileSummary `json:"file" jsonschema:"the requested file's details; its content is not read here"`
}

type listFilesInput struct {
	SortBy        string `json:"sort_by,omitempty" jsonschema:"add_time | update_time. Default 'update_time' (most-recently-touched first)."`
	SortDirection string `json:"sort_direction,omitempty" jsonschema:"asc | desc. Default 'desc' when sort_by is omitted; 'asc' otherwise."`
	Limit         int    `json:"limit,omitempty" jsonschema:"page size; default 25, max 100"`
	Cursor        string `json:"cursor,omitempty" jsonschema:"opaque pagination token from a previous list_files response; omit for the first page"`
}

type listFilesOutput struct {
	Files      []fileSummary `json:"files" jsonschema:"files on this page"`
	NextCursor string        `json:"next_cursor,omitempty" jsonschema:"pass to the next list_files call to get the next page; empty when there are no more pages"`
}

type manageFileInput struct {
	Action        string   `json:"action" jsonschema:"upload, update or delete"`
	FileID        int64    `json:"file_id,omitempty" jsonschema:"the file to act on, required by update and delete"`
	Path          string   `json:"path,omitempty" jsonschema:"the file to upload, as a path inside the server's upload folder, such as quotes/offer.pdf; required by upload. Nothing outside that folder can be uploaded"`
	DealID        int64    `json:"deal_id,omitempty" jsonschema:"attach the upload to this deal; upload needs exactly one of the record ids"`
	PersonID      int64    `json:"person_id,omitempty" jsonschema:"attach the upload to this person"`
	OrgID         int64    `json:"org_id,omitempty" jsonschema:"attach the upload to this organization"`
	ProductID     int64    `json:"product_id,omitempty" jsonschema:"attach the upload to this product"`
	ActivityID    int64    `json:"activity_id,omitempty" jsonschema:"attach the upload to this activity"`
	LeadID        string   `json:"lead_id,omitempty" jsonschema:"attach the upload to this lead, a UUID"`
	ProjectID     int64    `json:"project_id,omitempty" jsonschema:"attach the upload to this project"`
	Name          *string  `json:"name,omitempty" jsonschema:"the file's display name, for update. Omit to leave it as it is"`
	Description   *string  `json:"description,omitempty" jsonschema:"the file's description, for update. Omit to leave it as it is"`
	DryRun        bool     `json:"dry_run,omitempty" jsonschema:"report what the write would find and change, and send nothing"`
	Overwrite     []string `json:"overwrite,omitempty" jsonschema:"the fields this update may replace, named exactly as the refusal listed them, e.g. [\"name\"]. A refusal is NOT a retry signal: name a field only when the user asked for what is there to be replaced"`
	ExpectVersion string   `json:"expect_version,omitempty" jsonschema:"the update_time from the read that informed this write; the write is refused if the file changed since"`
}

type manageFileOutput struct {
	Action  string      `json:"action" jsonschema:"the action that ran"`
	File    fileSummary `json:"file" jsonschema:"the file as Pipedrive stored it. For delete, as it was just before. On a dry-run upload its id is 0"`
	Changed []string    `json:"changed,omitempty" jsonschema:"names of the fields this write altered, or on a dry run would alter"`
	DryRun  bool        `json:"dry_run,omitempty" jsonschema:"true when nothing was sent upstream"`
}

var (
	allowedFileActions    = map[string]bool{"upload": true, "update": true, "delete": true}
	allowedFileSortFields = map[string]bool{"add_time": true, "update_time": true}
)

var fileFields = []fieldSpec[pipedrive.File]{
	{"name", func(f *pipedrive.File) string { return f.Name }},
	{"description", func(f *pipedrive.File) string { return f.Description }},
}

// RegisterFiles wires the file tools. They run on Pipedrive v1, which v2
// does not replace (D-105), outside the compatibility promise (D-04). An
// upload reads only from the folder PIPEDRIVE_UPLOAD_DIR names (D-106).
func RegisterFiles(s *mcp.Server, c filesClient, opts RegisterOptions) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_file",
		Description: "Fetch one file's details by file_id: its name, size, description, the record it is attached to, and Pipedrive's link to it. The file's content is not read here. A deleted file still reads, with active_flag false, until Pipedrive removes it after 30 days. Files come from Pipedrive v1. Use search with type=file to find a file by name.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getFileInput) (*mcp.CallToolResult, getFileOutput, error) {
		if err := validatePositiveID(in.FileID, "file_id"); err != nil {
			return errorResult(err), getFileOutput{}, nil
		}
		f, err := c.GetFile(ctx, in.FileID)
		if err != nil {
			return errorResult(err), getFileOutput{}, nil
		}
		return nil, getFileOutput{File: summarizeFile(f)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_files",
		Description: "List the workspace's files, most recently touched first by default. Default limit is 25, max 100. IMPORTANT: a page holding fewer rows than the limit is NOT the end — keep paging while next_cursor comes back non-empty. Files come from Pipedrive v1; the cursor wraps v1's offset, so pass it back verbatim.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listFilesInput) (*mcp.CallToolResult, listFilesOutput, error) {
		if err := validateEnum(in.SortBy, "sort_by", allowedFileSortFields); err != nil {
			return errorResult(err), listFilesOutput{}, nil
		}
		if err := validateEnum(in.SortDirection, "sort_direction", allowedSortDirections); err != nil {
			return errorResult(err), listFilesOutput{}, nil
		}
		start, err := decodeV1Cursor("files", in.Cursor)
		if err != nil {
			return errorResult(err), listFilesOutput{}, nil
		}
		sortBy, sortDir := effectiveSort(in.SortBy, in.SortDirection)
		files, page, err := c.ListFiles(ctx, sortBy+" "+strings.ToUpper(sortDir), start, clampLimit(in.Limit))
		if err != nil {
			return errorResult(err), listFilesOutput{}, nil
		}
		out := listFilesOutput{Files: make([]fileSummary, 0, len(files)), NextCursor: encodeV1Cursor("files", page)}
		for i := range files {
			out.Files = append(out.Files, summarizeFile(&files[i]))
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "manage_file",
		Description: "Upload a file to a deal, person, organization, product, activity, lead or project, rename or describe one, or delete one. upload takes path, a file inside the folder the server's " + config.EnvUploadDir + " setting names, and exactly one record id; with no such folder set, upload is refused, and nothing outside the folder can be read. update takes file_id and name or description, and refuses to replace a value that is there unless you name it in overwrite. Deleting is soft: Pipedrive marks the file deleted and removes it for good after 30 days, so delete takes dry_run and expect_version and no permitting flag, but NOTHING HERE PUTS IT BACK. Files come from Pipedrive v1, and these tools leave in a minor release if Pipedrive retires it.",
		Annotations: mutatingAnnotations(),
	}, manageFileHandler(c, opts))
}

func summarizeFile(f *pipedrive.File) fileSummary {
	return fileSummary{
		ID: f.ID, Name: f.Name, FileName: f.FileName, Description: f.Description, FileSize: f.FileSize,
		DealID: f.DealID, PersonID: f.PersonID, OrgID: f.OrgID, ProductID: f.ProductID, ActivityID: f.ActivityID,
		LeadID: f.LeadID, ProjectID: f.ProjectID, ActiveFlag: f.ActiveFlag, URL: f.URL,
		AddTime: f.AddTime, UpdateTime: f.UpdateTime,
	}
}

func manageFileHandler(c filesClient, opts RegisterOptions) mcp.ToolHandlerFor[manageFileInput, manageFileOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in manageFileInput) (*mcp.CallToolResult, manageFileOutput, error) {
		if err := validateAction(in.Action, allowedFileActions); err != nil {
			return errorResult(err), manageFileOutput{}, nil
		}
		in.DryRun = in.DryRun || opts.DryRun
		var (
			res *mcp.CallToolResult
			out manageFileOutput
		)
		switch in.Action {
		case "upload":
			res, out = uploadFileAction(ctx, c, opts.UploadDir, in)
		case "update":
			res, out = updateFileAction(ctx, c, in)
		case "delete":
			res, out = deleteFileAction(ctx, c, in)
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

// fileAnchor is the one record an upload attaches to.
func fileAnchor(in manageFileInput) (pipedrive.FileAnchor, error) {
	at := pipedrive.FileAnchor{DealID: in.DealID, PersonID: in.PersonID, OrgID: in.OrgID, ProductID: in.ProductID,
		ActivityID: in.ActivityID, LeadID: in.LeadID, ProjectID: in.ProjectID}
	set := 0
	for _, id := range []int64{in.DealID, in.PersonID, in.OrgID, in.ProductID, in.ActivityID, in.ProjectID} {
		if id < 0 {
			return at, fmt.Errorf("%w: a record id must be positive", pipedrive.ErrValidation)
		}
		if id > 0 {
			set++
		}
	}
	if in.LeadID != "" {
		if err := validateLeadID(in.LeadID); err != nil {
			return at, err
		}
		set++
	}
	if set != 1 {
		return at, fmt.Errorf("%w: an upload attaches to exactly one record: set one of deal_id, person_id, org_id, product_id, activity_id, lead_id or project_id", pipedrive.ErrValidation)
	}
	return at, nil
}

// readUpload reads the named file from inside dir. os.Root keeps the
// read inside it, through .. and symlinks alike, so a model can upload
// only what the operator put in that folder (D-106).
func readUpload(dir, path string) ([]byte, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: uploads are off: the server has no %s naming a folder to upload from", pipedrive.ErrForbiddenBusinessRule, config.EnvUploadDir)
	}
	if path == "" || filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: path must name a file inside the upload folder, such as quotes/offer.pdf", pipedrive.ErrValidation)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("%w: the upload folder cannot be opened: %w", pipedrive.ErrForbiddenBusinessRule, err)
	}
	defer func() { _ = root.Close() }()
	// Stat before Open: opening a named pipe blocks until a writer comes.
	info, err := root.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: no file %q in the upload folder", pipedrive.ErrNotFound, path)
		}
		return nil, fmt.Errorf("%w: %q cannot be read from the upload folder: it is outside it, or unreadable", pipedrive.ErrValidation, path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %q is not a file", pipedrive.ErrValidation, path)
	}
	if info.Size() > maxUploadBytes {
		return nil, fmt.Errorf("%w: %q is %d bytes; this server uploads at most %d", pipedrive.ErrValidation, path, info.Size(), maxUploadBytes)
	}
	f, err := root.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %q cannot be read from the upload folder: %w", pipedrive.ErrValidation, path, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxUploadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: reading %q: %w", pipedrive.ErrValidation, path, err)
	}
	return data, nil
}

func uploadFileAction(ctx context.Context, c filesClient, dir string, in manageFileInput) (*mcp.CallToolResult, manageFileOutput) {
	at, err := fileAnchor(in)
	if err != nil {
		return errorResult(err), manageFileOutput{}
	}
	data, err := readUpload(dir, in.Path)
	if err != nil {
		return errorResult(err), manageFileOutput{}
	}
	name := filepath.Base(in.Path)
	preview := &pipedrive.File{Name: name, FileName: name, FileSize: int64(len(data)), DealID: at.DealID, PersonID: at.PersonID,
		OrgID: at.OrgID, ProductID: at.ProductID, ActivityID: at.ActivityID, LeadID: at.LeadID, ProjectID: at.ProjectID, ActiveFlag: true}
	result := preview
	if !in.DryRun {
		if result, err = c.UploadFile(ctx, name, bytes.NewReader(data), at); err != nil {
			return errorResult(err), manageFileOutput{}
		}
	}
	return nil, manageFileOutput{File: summarizeFile(result), Changed: []string{"file"}}
}

func updateFileAction(ctx context.Context, c filesClient, in manageFileInput) (*mcp.CallToolResult, manageFileOutput) {
	if err := validatePositiveID(in.FileID, "file_id"); err != nil {
		return errorResult(err), manageFileOutput{}
	}
	if in.Name == nil && in.Description == nil {
		return errorResult(fmt.Errorf("%w: update needs name or description", pipedrive.ErrValidation)), manageFileOutput{}
	}
	// A blank value would empty the field, which no tool here does (D-42).
	for field, v := range map[string]*string{"name": in.Name, "description": in.Description} {
		if v != nil && strings.TrimSpace(*v) == "" {
			return errorResult(fmt.Errorf("%w: %s must not be blank; omit it to leave it as it is", pipedrive.ErrValidation, field)), manageFileOutput{}
		}
	}
	f, changed, res := guardedWrite[pipedrive.File]{
		Spec:          fileFields,
		Resource:      fmt.Sprintf("file %d", in.FileID),
		ExpectVersion: in.ExpectVersion,
		Version:       func(f *pipedrive.File) string { return f.UpdateTime },
		Overwrite:     in.Overwrite,
		DryRun:        in.DryRun,
		Get:           func(ctx context.Context) (*pipedrive.File, error) { return c.GetFile(ctx, in.FileID) },
		Predict: func(f *pipedrive.File) pipedrive.File {
			after := *f
			setIf(&after.Name, in.Name)
			setIf(&after.Description, in.Description)
			return after
		},
		Put: func(ctx context.Context) (*pipedrive.File, error) {
			return c.UpdateFile(ctx, in.FileID, in.Name, in.Description)
		},
	}.run(ctx)
	if res != nil {
		return res, manageFileOutput{}
	}
	return nil, manageFileOutput{File: summarizeFile(f), Changed: changed}
}

// deleteFileAction marks a file deleted: soft and time-boxed, so dry_run
// and expect_version and nothing else (D-47).
func deleteFileAction(ctx context.Context, c filesClient, in manageFileInput) (*mcp.CallToolResult, manageFileOutput) {
	if err := validatePositiveID(in.FileID, "file_id"); err != nil {
		return errorResult(err), manageFileOutput{}
	}
	before, err := c.GetFile(ctx, in.FileID)
	if err != nil {
		return errorResult(err), manageFileOutput{}
	}
	if err = checkExpectVersion(in.ExpectVersion, before.UpdateTime, fmt.Sprintf("file %d", in.FileID)); err != nil {
		return errorResult(err), manageFileOutput{}
	}
	if !before.ActiveFlag {
		return nil, manageFileOutput{File: summarizeFile(before)}
	}
	if !in.DryRun {
		if err = c.DeleteFile(ctx, in.FileID); err != nil {
			return errorResult(err), manageFileOutput{}
		}
	}
	return nil, manageFileOutput{File: summarizeFile(before), Changed: []string{"active_flag"}}
}

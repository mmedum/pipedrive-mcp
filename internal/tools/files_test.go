package tools_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
	"github.com/mmedum/pipedrive-mcp/internal/tools"
)

type fakeFilesClient struct {
	file                      *pipedrive.File
	uploaded                  []byte
	lastAnchor                pipedrive.FileAnchor
	lastSort                  string
	uploads, updates, deletes int
}

func newFilesFake() *fakeFilesClient {
	return &fakeFilesClient{file: &pipedrive.File{ID: 7, Name: "offer.pdf", DealID: 3, ActiveFlag: true, UpdateTime: "2026-01-01 00:00:00"}}
}

func (f *fakeFilesClient) GetFile(context.Context, int64) (*pipedrive.File, error) {
	out := *f.file
	return &out, nil
}

func (f *fakeFilesClient) ListFiles(_ context.Context, sort string, _, _ int) ([]pipedrive.File, *pipedrive.V1Pagination, error) {
	f.lastSort = sort
	return []pipedrive.File{*f.file}, nil, nil
}

func (f *fakeFilesClient) UploadFile(_ context.Context, name string, content io.Reader, at pipedrive.FileAnchor) (*pipedrive.File, error) {
	f.uploads++
	f.lastAnchor = at
	f.uploaded, _ = io.ReadAll(content)
	return &pipedrive.File{ID: 8, Name: name, DealID: at.DealID, FileSize: int64(len(f.uploaded)), ActiveFlag: true}, nil
}

func (f *fakeFilesClient) UpdateFile(_ context.Context, _ int64, name, description *string) (*pipedrive.File, error) {
	f.updates++
	if name != nil {
		f.file.Name = *name
	}
	if description != nil {
		f.file.Description = *description
	}
	out := *f.file
	return &out, nil
}

func (f *fakeFilesClient) DeleteFile(context.Context, int64) error {
	f.deletes++
	f.file.ActiveFlag = false
	return nil
}

func (f *fakeFilesClient) writes() int { return f.uploads + f.updates + f.deletes }

func callFiles(t *testing.T, f *fakeFilesClient, opts tools.RegisterOptions, tool string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	return testutil.CallToolInto(t, func(s *mcp.Server) { tools.RegisterFiles(s, f, opts) }, tool, args, out)
}

// uploadFolder is a folder holding quotes/offer.txt, with a secret beside
// it and a symlink inside it that points at the secret.
func uploadFolder(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	dir := filepath.Join(parent, "uploads")
	if err := os.MkdirAll(filepath.Join(dir, "quotes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "quotes", "offer.txt"), []byte("offer"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(parent, "secret.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestManageFile_UploadsOnlyFromInsideTheFolder(t *testing.T) {
	dir := uploadFolder(t)
	opts := tools.RegisterOptions{UploadDir: dir}
	for _, path := range []string{"../secret.txt", "link.txt", filepath.Join(dir, "quotes", "offer.txt"), "quotes", ""} {
		fake := newFilesFake()
		res := callFiles(t, fake, opts, "manage_file", map[string]any{"action": "upload", "path": path, "deal_id": 3}, nil)
		if !strings.HasPrefix(contentText(res), "[validation]") || fake.uploads != 0 {
			t.Errorf("upload of %q = %q; want [validation] and nothing sent", path, contentText(res))
		}
	}
	fake := newFilesFake()
	if res := callFiles(t, fake, tools.RegisterOptions{}, "manage_file", map[string]any{"action": "upload", "path": "quotes/offer.txt", "deal_id": 3}, nil); !strings.Contains(contentText(res), "PIPEDRIVE_UPLOAD_DIR") || fake.uploads != 0 {
		t.Errorf("upload with no folder set = %q; want a refusal naming PIPEDRIVE_UPLOAD_DIR", contentText(res))
	}
	var out struct {
		File struct {
			ID int64 `json:"id"`
		} `json:"file"`
	}
	if res := callFiles(t, fake, opts, "manage_file", map[string]any{"action": "upload", "path": "quotes/offer.txt", "deal_id": 3}, &out); res.IsError || fake.uploads != 1 || string(fake.uploaded) != "offer" || fake.lastAnchor.DealID != 3 || out.File.ID != 8 {
		t.Errorf("upload = %s, sent %q to %+v; want the file's bytes on deal 3", contentText(res), fake.uploaded, fake.lastAnchor)
	}
}

func TestManageFile_AnUploadAttachesToExactlyOneRecord(t *testing.T) {
	opts := tools.RegisterOptions{UploadDir: uploadFolder(t)}
	for _, anchor := range []map[string]any{{}, {"deal_id": 3, "person_id": 4}, {"lead_id": "not-a-uuid"}} {
		args := map[string]any{"action": "upload", "path": "quotes/offer.txt"}
		for k, v := range anchor {
			args[k] = v
		}
		fake := newFilesFake()
		if res := callFiles(t, fake, opts, "manage_file", args, nil); !strings.HasPrefix(contentText(res), "[validation]") || fake.uploads != 0 {
			t.Errorf("anchor %v = %q; want [validation]", anchor, contentText(res))
		}
	}
}

func TestManageFile_TheFloorAndDryRunSendNothing(t *testing.T) {
	opts := tools.RegisterOptions{UploadDir: uploadFolder(t), DryRun: true}
	fake := newFilesFake()
	for _, args := range []map[string]any{
		{"action": "upload", "path": "quotes/offer.txt", "deal_id": 3},
		{"action": "update", "file_id": 7, "description": "x"},
		{"action": "delete", "file_id": 7},
	} {
		if res := callFiles(t, fake, opts, "manage_file", args, nil); res.IsError {
			t.Errorf("%s under the floor = %s", args["action"], contentText(res))
		}
	}
	if fake.writes() != 0 {
		t.Errorf("the floor let %d writes through", fake.writes())
	}
}

func TestManageFile_UpdateGuardsTheName(t *testing.T) {
	fake := newFilesFake()
	args := map[string]any{"action": "update", "file_id": 7, "name": "quote.pdf"}
	if res := callFiles(t, fake, tools.RegisterOptions{}, "manage_file", args, nil); !strings.Contains(contentText(res), `overwrite: ["name"]`) || fake.updates != 0 {
		t.Fatalf("a rename without overwrite = %q", contentText(res))
	}
	// An empty description is filled without permission.
	if res := callFiles(t, fake, tools.RegisterOptions{}, "manage_file", map[string]any{"action": "update", "file_id": 7, "description": "the offer"}, nil); res.IsError || fake.updates != 1 {
		t.Errorf("filling the description = %s", contentText(res))
	}
}

func TestManageFile_DeleteIsSoftAndOnce(t *testing.T) {
	fake := newFilesFake()
	if res := callFiles(t, fake, tools.RegisterOptions{}, "manage_file", map[string]any{"action": "delete", "file_id": 7, "expect_version": "2025-01-01 00:00:00"}, nil); !strings.Contains(contentText(res), "changed since you read it") {
		t.Fatalf("a stale delete = %q", contentText(res))
	}
	for range 2 {
		if res := callFiles(t, fake, tools.RegisterOptions{}, "manage_file", map[string]any{"action": "delete", "file_id": 7}, nil); res.IsError {
			t.Fatalf("delete = %s", contentText(res))
		}
	}
	if fake.deletes != 1 {
		t.Errorf("deletes = %d; want one, the second finding it deleted", fake.deletes)
	}
}

func TestListFiles_SendsV1Sort(t *testing.T) {
	fake := newFilesFake()
	callFiles(t, fake, tools.RegisterOptions{}, "list_files", map[string]any{"sort_by": "add_time"}, nil)
	if fake.lastSort != "add_time ASC" {
		t.Errorf("sort = %q; want v1's \"field DIRECTION\"", fake.lastSort)
	}
}

//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
)

type fileOut struct {
	File struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		FileSize    int64  `json:"file_size"`
		DealID      int64  `json:"deal_id"`
		ActiveFlag  bool   `json:"active_flag"`
		UpdateTime  string `json:"update_time"`
	} `json:"file"`
	Changed []string `json:"changed"`
}

// TestWrite_FileRoundTrip uploads a file from the suite's upload folder
// to a scratch deal, renames and describes it, finds it in list_files,
// and deletes it. The delete is soft: the file still reads, with
// active_flag false. A path that leaves the folder is refused before
// anything is read.
func TestWrite_FileRoundTrip(t *testing.T) {
	s := newScratch(t)
	content := []byte("mcp-test upload probe\n")
	if err := os.WriteFile(filepath.Join(uploadDir, "probe.txt"), content, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"../probe.txt", filepath.Join(uploadDir, "probe.txt")} {
		if res := call(t, "manage_file", map[string]any{"action": "upload", "path": path, "deal_id": s.DealID}); errClass(res) != "validation" {
			t.Errorf("upload of %q = %s, want [validation]", path, testutil.TextContent(res))
		}
	}

	var rehearsal fileOut
	mustCall(t, "manage_file", map[string]any{"action": "upload", "path": "probe.txt", "deal_id": s.DealID, "dry_run": true}, &rehearsal)
	if rehearsal.File.ID != 0 || rehearsal.File.FileSize != int64(len(content)) {
		t.Errorf("dry-run upload = %+v; want no id and the file's size", rehearsal.File)
	}

	var up fileOut
	mustCall(t, "manage_file", map[string]any{"action": "upload", "path": "probe.txt", "deal_id": s.DealID}, &up)
	id := up.File.ID
	if id == 0 {
		t.Fatalf("upload = %+v; want a file", up.File)
	}
	t.Cleanup(func() {
		if id == 0 {
			return
		}
		if res := call(t, "manage_file", map[string]any{"action": "delete", "file_id": id}); res.IsError {
			t.Errorf("LEFT BEHIND: file %d: %s", id, testutil.TextContent(res))
		}
	})

	var got fileOut
	mustCall(t, "get_file", map[string]any{"file_id": id}, &got)
	if got.File.DealID != s.DealID || got.File.FileSize != int64(len(content)) || !got.File.ActiveFlag {
		t.Errorf("get_file = %+v; want the deal %d, %d bytes, active", got.File, s.DealID, len(content))
	}

	rename := map[string]any{"action": "update", "file_id": id, "name": scratchName("file.txt")}
	if res := call(t, "manage_file", rename); errClass(res) != "refused" {
		t.Fatalf("a rename without overwrite = %s, want [refused]", testutil.TextContent(res))
	}
	rename["overwrite"] = []string{"name"}
	rename["description"] = "probe"
	rename["expect_version"] = got.File.UpdateTime
	var renamed fileOut
	mustCall(t, "manage_file", rename, &renamed)
	mustCall(t, "get_file", map[string]any{"file_id": id}, &got)
	if got.File.Name != rename["name"] || got.File.Description != "probe" {
		t.Errorf("after the update the file reads %+v; want the new name and description", got.File)
	}

	var page struct {
		Files []struct {
			ID int64 `json:"id"`
		} `json:"files"`
	}
	mustCall(t, "list_files", map[string]any{"limit": 100}, &page)
	listed := false
	for _, f := range page.Files {
		listed = listed || f.ID == id
	}
	if !listed {
		t.Errorf("list_files' first page, most recently updated first, lacks file %d", id)
	}

	mustCall(t, "manage_file", map[string]any{"action": "delete", "file_id": id}, nil)
	gone := id
	id = 0
	mustCall(t, "get_file", map[string]any{"file_id": gone}, &got)
	if got.File.ActiveFlag {
		t.Errorf("after the delete the file reads %+v; want active_flag false", got.File)
	}
}

//go:build integration

package integration

import (
	"slices"
	"testing"

	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
)

type projectOut struct {
	Project struct {
		ID          int64   `json:"id"`
		Status      string  `json:"status"`
		BoardID     int64   `json:"board_id"`
		PhaseID     int64   `json:"phase_id"`
		DealIDs     []int64 `json:"deal_ids"`
		ArchiveTime string  `json:"archive_time"`
		UpdateTime  string  `json:"update_time"`
	} `json:"project"`
}

// TestWrite_ProjectRoundTrip creates a project with no board, links it
// to one scratch deal and then another, archives it and deletes it. It
// checks what the client comments say Pipedrive does: a project without
// a board lands on one, a write of deal_ids replaces the list, an
// archived project leaves the live list for the archive, and a deleted
// project still reads with status deleted.
func TestWrite_ProjectRoundTrip(t *testing.T) {
	first, second := newScratch(t), newScratch(t)

	var created projectOut
	mustCall(t, "manage_project", map[string]any{"action": "create", "title": first.Title + " project", "deal_ids": []int64{first.DealID}}, &created)
	id := created.Project.ID
	deleted := false
	t.Cleanup(func() {
		if deleted || id == 0 {
			return
		}
		if res := call(t, "manage_project", map[string]any{"action": "delete", "project_id": id}); res.IsError {
			t.Errorf("LEFT BEHIND: project %d: %s", id, testutil.TextContent(res))
		}
	})
	if id == 0 || created.Project.BoardID == 0 || created.Project.PhaseID == 0 {
		t.Fatalf("create = %+v; want a project Pipedrive put on a board and a phase", created.Project)
	}

	relink := map[string]any{"action": "update", "project_id": id, "deal_ids": []int64{second.DealID}}
	if res := call(t, "manage_project", relink); errClass(res) != "refused" {
		t.Fatalf("relinking without overwrite = %s, want [refused]", testutil.TextContent(res))
	}
	relink["overwrite"] = []string{"deal_ids"}
	relink["expect_version"] = created.Project.UpdateTime
	mustCall(t, "manage_project", relink, nil)
	var back projectOut
	mustCall(t, "get_project", map[string]any{"project_id": id}, &back)
	if !slices.Equal(back.Project.DealIDs, []int64{second.DealID}) {
		t.Errorf("after a write of one deal the project links %v", back.Project.DealIDs)
	}

	var archived projectOut
	mustCall(t, "manage_project", map[string]any{"action": "archive", "project_id": id}, &archived)
	if archived.Project.ArchiveTime == "" {
		t.Errorf("archive left archive_time empty")
	}
	if !projectListed(t, id, true) || projectListed(t, id, false) {
		t.Errorf("an archived project should be in the archive and not in the live list")
	}

	mustCall(t, "manage_project", map[string]any{"action": "delete", "project_id": id}, nil)
	deleted = true
	var gone projectOut
	mustCall(t, "get_project", map[string]any{"project_id": id}, &gone)
	if gone.Project.Status != "deleted" {
		t.Errorf("a deleted project reads with status %q, want deleted", gone.Project.Status)
	}
}

func projectListed(t *testing.T, id int64, archived bool) bool {
	t.Helper()
	cursor := ""
	for range 20 {
		var page struct {
			Projects []struct {
				ID int64 `json:"id"`
			} `json:"projects"`
			NextCursor string `json:"next_cursor"`
		}
		mustCall(t, "list_projects", map[string]any{"archived": archived, "limit": 100, "cursor": cursor}, &page)
		for _, p := range page.Projects {
			if p.ID == id {
				return true
			}
		}
		if page.NextCursor == "" {
			return false
		}
		cursor = page.NextCursor
	}
	return false
}

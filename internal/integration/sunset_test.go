//go:build integration

package integration

import (
	"testing"

	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
)

// TestV1CarveOutStillAnswers is the canary on the two v1 carve-outs.
//
// Pipedrive's v1 sunset date, 2026-07-31, has passed. v1 is out of
// support rather than switched off, and there is nothing to migrate to:
// v2 exposes no `/notes` and no `/users`. So the risk cannot be
// engineered away, only watched.
//
// It is one test over every v1 tool rather than an assertion spread
// through the read probes. Those exist and make the same calls, but
// each fails as itself: `whoami` failing reads as a `whoami` bug. **A
// retirement fails all of them at once**, and that shape is only
// visible to something looking at them together — so the aggregate is
// what this asserts, not the class of any single failure. Keying on
// `[gone]` alone would have been a guess about another company's
// deprecation hygiene: a route can equally be removed (404), gated
// (403), or answered by an edge with an HTML error page.
//
// It lives in the live suite because no fake can answer the question.
// A fake returns what we told it to; the question is what Pipedrive
// does.
func TestV1CarveOutStillAnswers(t *testing.T) {
	requireLive(t)

	// Every tool whose request goes through Client.doV1 / postV1 /
	// putV1 / deleteV1. manage_note is absent on purpose: its create
	// and delete paths mutate, and this runs on the tag alone without
	// PIPEDRIVE_INTEGRATION_WRITES. Its read path is get_note.
	//
	// The list is hand-kept, and nothing derives it from the doV1 call
	// sites — a fifth v1 tool would silently miss this canary. The
	// obvious fix, an exported list of tool names beside the doV1
	// helpers, would put MCP vocabulary inside internal/pipedrive,
	// which is transport-independent by rule. Left as a known gap
	// rather than solved in the wrong package.
	probes := []struct {
		tool string
		path string
		// args is a func because get_note needs an id from a call that
		// may skip on a workspace holding no notes.
		args func(t *testing.T) map[string]any
	}{
		{"whoami", "/api/v1/users/me", nil},
		{"list_notes", "/api/v1/notes", func(*testing.T) map[string]any {
			return map[string]any{"limit": 1}
		}},
		{"get_note", "/api/v1/notes/{id}", func(t *testing.T) map[string]any {
			var listed notesOut
			mustCall(t, "list_notes", map[string]any{"limit": 1}, &listed)
			return map[string]any{"note_id": pickFirst(t, listed.Notes, "workspace has no notes").ID}
		}},
	}

	// Recorded rather than asserted per probe, so the aggregate can be
	// read after the loop. A subtest that skipped is neither answered
	// nor failed and must not count towards either.
	answered, failed := 0, 0

	for _, p := range probes {
		t.Run(p.tool, func(t *testing.T) {
			args := map[string]any(nil)
			if p.args != nil {
				args = p.args(t)
			}

			res := call(t, p.tool, args)
			if !res.IsError {
				answered++
				return
			}
			failed++
			t.Errorf("%s (%s) failed as [%s]: %s",
				p.tool, p.path, errClass(res), testutil.TextContent(res))
		})
	}

	if failed > 0 && answered == 0 {
		t.Fatalf("EVERY v1 tool failed (%d of %d). THE V1 SUNSET MAY HAVE ARRIVED. "+
			"There is no v2 equivalent to fail over to — v2 exposes no /notes and no /users. "+
			"Read 'v1.0.0 is gated on more than a checklist' in docs/architecture.md "+
			"before changing anything here.", failed, failed)
	}
}

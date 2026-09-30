//go:build integration

package integration

import (
	"testing"
)

// A dry-run create shows the values Pipedrive fills in on the real one.
//
// A dry run used to report a deal created with only a title and a
// pipeline as stage 0, owner 0 and no currency, and each create tool
// linked to record 0 — the zero values of a record nobody had filled in,
// which a caller could quote as what the create would store. It is held
// by comparison rather than by documentation: the same arguments as a
// dry run and as a real create, in the suite's own pipeline, must agree
// on what the real one fills in.
func TestWrite_CreateDefaultsMatchTheDryRun(t *testing.T) {
	pipeline, _ := testPipelineStage(t)
	cases := []struct {
		tool, key, idField string
		args               map[string]any
		fields             []string
	}{
		{"manage_deal", "deal", "deal_id", map[string]any{"title": scratchName("defaults"), "pipeline_id": pipeline},
			[]string{"stage_id", "owner_id", "currency"}},
		{"manage_person", "person", "person_id", map[string]any{"name": scratchName("defaults person")},
			[]string{"owner_id"}},
		{"manage_organization", "organization", "org_id", map[string]any{"name": scratchName("defaults org")},
			[]string{"owner_id"}},
		{"manage_activity", "activity", "activity_id", map[string]any{"subject": scratchName("defaults activity"), "type": "call"},
			[]string{"owner_id"}},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			call := func(dry bool) map[string]any {
				args := map[string]any{"action": "create", "dry_run": dry}
				for k, v := range tc.args {
					args[k] = v
				}
				var out map[string]any
				mustCall(t, tc.tool, args, &out)
				rec, _ := out[tc.key].(map[string]any)
				return rec
			}
			preview, created := call(true), call(false)
			n, _ := created["id"].(float64)
			id := int64(n)
			dropOnCleanup(t, tc.tool, tc.idField, &id)

			for _, f := range tc.fields {
				if isZero(preview[f]) || preview[f] != created[f] {
					t.Errorf("%s: the dry run says %v, the create stored %v", f, preview[f], created[f])
				}
			}
			if preview["url"] != "" || created["url"] == "" {
				t.Errorf("a preview links nowhere and a created record to itself: %q, %q", preview["url"], created["url"])
			}
		})
	}
}

// isZero is a JSON value a record nobody filled in would carry.
func isZero(v any) bool { return v == nil || v == "" || v == float64(0) }

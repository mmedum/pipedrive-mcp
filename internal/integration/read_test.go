//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
)

// pickFirst returns the first row, or skips. "Any record of this kind"
// is what most probes want; a workspace that holds none is a gap in
// coverage rather than a failure of the code.
func pickFirst[T any](t *testing.T, rows []T, why string) T {
	t.Helper()
	return pickWhere(t, rows, why, func(T) bool { return true })
}

func pickDeal(t *testing.T) dealRow {
	t.Helper()
	var out dealsOut
	mustCall(t, "list_deals", map[string]any{"limit": 1}, &out)
	return pickFirst(t, out.Deals, "workspace has no deals")
}

// TestProbeAuth_SucceedsAgainstTheWorkspace exercises the startup
// probe the binary runs before it serves anything. CLAUDE.md hard rule
// 6 rests the whole design on an empirical claim — that
// `/api/v2/dealFields` is present in every workspace, which is why the
// probe is not a users call — and httptest cannot check that claim
// against a workspace. This is where it gets checked.
func TestProbeAuth_SucceedsAgainstTheWorkspace(t *testing.T) {
	requireLive(t)

	if err := liveClient.ProbeAuth(context.Background()); err != nil {
		t.Fatalf("the startup auth probe fails against this workspace: %v", err)
	}
}

func TestWhoAmI_ReachesTheV1Carveout(t *testing.T) {
	requireLive(t)

	var out whoamiOut
	mustCall(t, "whoami", nil, &out)

	// The fields the server instructions promise a caller can act on:
	// user_id is the owner_id a record inherits, and timezone_name is
	// what an activity's due_time is written in. Values are not
	// printed — this is somebody's account.
	if out.User.UserID == 0 {
		t.Error("user_id is 0; the v1 /users/me carve-out returned no id")
	}
	if out.User.CompanyID == 0 {
		t.Error("company_id is 0")
	}
	if out.User.TimezoneName == "" {
		t.Error("timezone_name is empty; due_time has no frame of reference without it")
	}
	if out.User.CompanyDomain != liveDomain {
		t.Error("company_domain disagrees with the domain the client was built for")
	}
}

// TestListStages_BelongToThePipelineAsked checks that the query
// parameter we send is the one Pipedrive filters on. A fake can only
// confirm we sent `pipeline_id`; a name upstream ignores comes back as
// every stage in the workspace, which is a wrong answer rather than an
// error.
func TestListStages_BelongToThePipelineAsked(t *testing.T) {
	requireLive(t)

	var pipelines pipelinesOut
	mustCall(t, "list_pipelines", nil, &pipelines)
	want := pickFirst(t, pipelines.Pipelines, "workspace has no pipelines").ID

	var stages stagesOut
	mustCall(t, "list_stages", map[string]any{"pipeline_id": want}, &stages)
	if len(stages.Stages) == 0 {
		t.Fatal("pipeline has no stages")
	}
	for _, s := range stages.Stages {
		if s.PipelineID != want {
			t.Errorf("stage %d belongs to pipeline %d; asked for %d", s.ID, s.PipelineID, want)
		}
	}
}

// TestGet_AgreesWithList is the wire-format check. A summary type that
// decodes a live list row but not a live get row (or the reverse) is
// the class of bug that shipped in 0.4.0 — the two endpoints do not
// return identical JSON, and only a real call shows it.
func TestGet_AgreesWithList(t *testing.T) {
	requireLive(t)

	cases := []struct {
		list, listKey, get, idArg, getKey string
		hasURL                            bool
	}{
		{"list_deals", "deals", "get_deal", "deal_id", "deal", true},
		{"list_persons", "persons", "get_person", "person_id", "person", true},
		{"list_organizations", "organizations", "get_organization", "org_id", "organization", true},
		{"list_activities", "activities", "get_activity", "activity_id", "activity", true},
		{"list_notes", "notes", "get_note", "note_id", "note", false},
	}

	for _, tc := range cases {
		t.Run(tc.get, func(t *testing.T) {
			var listed map[string]any
			mustCall(t, tc.list, map[string]any{"limit": 1}, &listed)
			rows, _ := listed[tc.listKey].([]any)
			if len(rows) == 0 {
				t.Skipf("workspace has no %s", tc.listKey)
			}
			row, _ := rows[0].(map[string]any)
			id, ok := row["id"].(float64)
			if !ok || id == 0 {
				t.Fatalf("%s row carries no id", tc.list)
			}

			var got map[string]any
			mustCall(t, tc.get, map[string]any{tc.idArg: int64(id)}, &got)
			record, _ := got[tc.getKey].(map[string]any)
			if record == nil {
				t.Fatalf("%s returned no %s", tc.get, tc.getKey)
			}
			if record["id"] != id {
				t.Errorf("%s returned id %v; asked for %v", tc.get, record["id"], id)
			}
			// Every summary type that carries a workspace URL builds
			// it from the domain. An empty one means the domain never
			// reached the injection point.
			if s, _ := record["url"].(string); tc.hasURL && s == "" {
				t.Errorf("%s returned an empty url", tc.get)
			}
		})
	}
}

// hashKey matches the 40-character hex keys Pipedrive stores custom
// fields under. Seeing one in output means FieldCache did not resolve
// it, which is invisible to a fake whose fixtures are already named.
var hashKey = regexp.MustCompile(`^[0-9a-f]{40}$`)

// A custom field has to reach the caller in the workspace's own words,
// on both halves: the key under the NAME the workspace gives the field,
// and — for a dropdown — the value under its option LABEL rather than
// the id Pipedrive stores. One walk asserts both, because they are one
// property of one map and reading the rows twice would cost a second
// live call to learn the same thing.
//
// A fake cannot answer this. Its fixtures are already named and its
// options are whatever the fixture says, so the only way to know the
// two sides agree is to read the workspace's own field metadata and
// check it against what the tools returned.
func TestCustomFields_ResolveToWorkspaceWords(t *testing.T) {
	requireLive(t)

	cases := []struct {
		list, key string
		fields    func(context.Context) ([]pipedrive.Field, error)
	}{
		{"list_deals", "deals", liveClient.ListDealFields},
		{"list_persons", "persons", liveClient.ListPersonFields},
		{"list_organizations", "organizations", liveClient.ListOrganizationFields},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			fields, err := tc.fields(context.Background())
			if err != nil {
				t.Fatalf("reading %s field metadata: %v", tc.key, err)
			}
			ids := unlabelledOptionIDs(fields)

			var raw map[string]any
			mustCall(t, tc.list, map[string]any{"limit": 20}, &raw)
			rows, _ := raw[tc.key].([]any)
			if len(rows) == 0 {
				t.Skipf("workspace has no %s", tc.key)
			}

			named, labelled := 0, 0
			for _, r := range rows {
				row, _ := r.(map[string]any)
				custom, _ := row["custom_fields"].(map[string]any)
				for name, v := range custom {
					named++
					if hashKey.MatchString(name) {
						t.Errorf("custom field surfaced as a %d-char hash, not a name", len(name))
					}
					stored, ok := ids[name]
					if !ok {
						continue
					}
					for _, one := range values(v) {
						labelled++
						if stored[pipedrive.OptionKey(one)] {
							// The id rather than the field's name, for
							// the reason the hash check prints a length
							// and not the name: this output gets pasted
							// in public.
							t.Errorf("an option field surfaced the stored id %v instead of its label", one)
						}
					}
				}
			}
			if named == 0 {
				t.Skipf("no %s in this workspace carries a custom field", tc.key)
			}
			if labelled == 0 {
				t.Logf("key half only: no %s in this workspace has a dropdown filled in", tc.key)
			}
		})
	}
}

// unlabelledOptionIDs maps a field's name to the ids it stores, rendered
// the way the cache renders them. An option whose label reads the same
// as its id is left out: matching one would prove nothing about whether
// the label was resolved.
func unlabelledOptionIDs(fields []pipedrive.Field) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	for _, f := range fields {
		stored := map[string]bool{}
		for _, o := range f.Options {
			if id := pipedrive.OptionKey(o.ID); id != o.Label {
				stored[id] = true
			}
		}
		if len(stored) > 0 {
			out[f.Name] = stored
		}
	}
	return out
}

// values treats a set's array and an enum's scalar as one shape, the
// same way the cache does — and for the same reason: the option table
// says whether to resolve, the value's shape says only how to walk it.
func values(v any) []any {
	if vs, ok := v.([]any); ok {
		return vs
	}
	return []any{v}
}

// TestListDeals_CursorPages pins the paging contract the server
// instructions lean on. The cursor is Pipedrive's, so only Pipedrive
// can say whether we round-trip it correctly.
func TestListDeals_CursorPages(t *testing.T) {
	requireLive(t)

	var first dealsOut
	mustCall(t, "list_deals", map[string]any{"limit": 1}, &first)
	if len(first.Deals) == 0 {
		t.Skip("workspace has no deals")
	}
	if first.NextCursor == "" {
		t.Skip("workspace holds a single page of deals")
	}

	var second dealsOut
	mustCall(t, "list_deals", map[string]any{"limit": 1, "cursor": first.NextCursor}, &second)
	if len(second.Deals) == 0 {
		t.Fatal("a non-empty next_cursor returned an empty page")
	}
	if second.Deals[0].ID == first.Deals[0].ID {
		t.Error("the second page repeated the first page's row; the cursor is not advancing")
	}
}

func TestSearch_FindsARecordByItsOwnName(t *testing.T) {
	requireLive(t)

	var orgs orgsOut
	mustCall(t, "list_organizations", map[string]any{"limit": 1}, &orgs)
	target := pickFirst(t, orgs.Organizations, "workspace has no organizations")

	var out searchOut
	mustCall(t, "search", map[string]any{
		"term":  target.Name,
		"types": []string{"organization"},
		"limit": 25,
	}, &out)

	for _, h := range out.Hits {
		if h.ID == target.ID {
			return
		}
	}
	t.Errorf("searching an organization's own name did not return it among %d hits", len(out.Hits))
}

// TestResource_MirrorsTheGetTool walks the resource path, which builds
// its own summary and resolves its own custom fields rather than
// calling the tool. Against fixtures the two agree by construction;
// against a record carrying real custom fields they agree only if both
// resolve them the same way.
func TestResource_MirrorsTheGetTool(t *testing.T) {
	requireLive(t)
	deal := pickDeal(t)

	res, err := live.ReadResource(context.Background(),
		&mcp.ReadResourceParams{URI: fmt.Sprintf("pipedrive://deals/%d", deal.ID)})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(res.Contents) != 1 {
		t.Fatalf("contents = %d; want 1", len(res.Contents))
	}
	var fromResource dealRow
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &fromResource); err != nil {
		t.Fatalf("resource body is not a deal: %v", err)
	}

	var fromTool dealOut
	mustCall(t, "get_deal", map[string]any{"deal_id": deal.ID}, &fromTool)

	if fromResource.ID != fromTool.Deal.ID || fromResource.Title != fromTool.Deal.Title {
		t.Error("the resource and get_deal disagree about the same deal")
	}
	if fromResource.URL != fromTool.Deal.URL {
		t.Error("the resource and get_deal disagree about the record's url")
	}
	if len(fromResource.CustomFields) != len(fromTool.Deal.CustomFields) {
		t.Errorf("the resource resolved %d custom fields and get_deal %d",
			len(fromResource.CustomFields), len(fromTool.Deal.CustomFields))
	}
}

// TestGetDeal_NotFoundMapsToItsClass is the error-mapping check
// against a real HTTP status. The fakes return the sentinel directly,
// so they cannot tell us whether Pipedrive's 404 body still parses
// into one.
func TestGetDeal_NotFoundMapsToItsClass(t *testing.T) {
	requireLive(t)

	res := call(t, "get_deal", map[string]any{"deal_id": 2147483647})
	if !res.IsError {
		t.Fatal("a deal id no workspace holds returned a deal")
	}
	if got := errClass(res); got != "not_found" {
		t.Errorf("class = %q; want not_found (message: %s)", got, testutil.TextContent(res))
	}
}

// TestRefreshFieldCache_ReadsEveryResource asserts the real /XFields
// payloads parse into named fields. The count is what makes it a live
// test: a fake can report three resources and zero errors off a
// fixture, but only the workspace can say its field definitions still
// decode.
func TestRefreshFieldCache_ReadsEveryResource(t *testing.T) {
	requireLive(t)

	var out cacheOut
	mustCall(t, "refresh_field_cache", nil, &out)
	if out.Errors != 0 {
		t.Errorf("refresh reported %d errors", out.Errors)
	}
	if len(out.Refreshed) != 3 {
		t.Errorf("refreshed %d resources; want deals, persons and organizations", len(out.Refreshed))
	}
	for _, r := range out.Refreshed {
		if r.Error != "" {
			t.Errorf("%s: %s", r.Resource, r.Error)
			continue
		}
		if r.Count == 0 {
			t.Errorf("%s: parsed 0 field definitions; every workspace has built-in fields", r.Resource)
		}
	}
}

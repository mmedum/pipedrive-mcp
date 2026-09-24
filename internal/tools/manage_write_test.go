package tools_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
	"github.com/mmedum/pipedrive-mcp/internal/tools"
)

// This file covers the write paths the manage_* tools share: the
// overwrite guard, expect_version, the dry-run floor, the changed
// report, and the transitions. The per-resource files still own their
// reads and their create-specific validation.

// fakeEncode stands in for FieldCache.Encode. It keeps the two
// properties the tools layer depends on — a name becomes a stored key,
// and every key carries a name to report it by — and nothing else: what
// Encode does with labels, ambiguity and read-only fields is tested
// against the real thing in internal/pipedrive.
//
// A non-nil err lets a test drive the refusal path, which is the one
// branch of the tools layer that cares what Encode decided.
func fakeEncode(in map[string]any, err error) (pipedrive.CustomFieldWrite, error) {
	// Empty first, like the real Encode: nothing to encode cannot fail,
	// which is what lets a transition pass custom_fields it ignores.
	if len(in) == 0 {
		return pipedrive.CustomFieldWrite{}, nil
	}
	if err != nil {
		return pipedrive.CustomFieldWrite{}, err
	}
	out := pipedrive.CustomFieldWrite{
		Values: make(map[string]any, len(in)),
		Names:  make(map[string]string, len(in)),
	}
	for name, v := range in {
		key := "cf_" + strings.ToLower(name)
		out.Values[key] = v
		out.Names[key] = name
	}
	return out, nil
}

type writeOut struct {
	Action  string   `json:"action"`
	Changed []string `json:"changed,omitempty"`
	DryRun  bool     `json:"dry_run,omitempty"`
}

// callTool is testutil.CallToolInto under the name this package's tests
// already use. The helper itself moved to internal/server/testutil,
// where the other suites can reach it.
func callTool(t *testing.T, register func(*mcp.Server), name string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	return testutil.CallToolInto(t, register, name, args, out)
}

func changedSet(c []string) map[string]bool {
	m := map[string]bool{}
	for _, f := range c {
		m[f] = true
	}
	return m
}

// ---------------------------------------------------------------- deals

func dealsReg(fake *fakeDealsClient, opts tools.RegisterOptions) func(*mcp.Server) {
	return func(s *mcp.Server) { tools.RegisterDeals(s, fake, "acme", opts) }
}

func TestManageDeal_Update_HappyPathAndChangedReport(t *testing.T) {
	fake := &fakeDealsClient{
		deal:       &pipedrive.Deal{ID: 9, Title: "Acme renewal", Value: 0, Status: "open", UpdateTime: "t0"},
		updateDeal: &pipedrive.Deal{ID: 9, Title: "Acme renewal", Value: 5000, Status: "open", UpdateTime: "t1"},
	}
	var out writeOut
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "update", "deal_id": 9, "value": 5000}, &out)
	if res.IsError {
		t.Fatalf("unexpected isError: %s", contentText(res))
	}
	if out.Action != "update" {
		t.Errorf("action = %q; want update", out.Action)
	}
	if !changedSet(out.Changed)["value"] {
		t.Errorf("changed = %v; want value", out.Changed)
	}
	if fake.getCalls != 1 {
		t.Errorf("read-before-write ran %d times; want 1", fake.getCalls)
	}
	if fake.updateCalls != 1 || fake.lastUpdateID != 9 {
		t.Errorf("update calls=%d id=%d; want 1/9", fake.updateCalls, fake.lastUpdateID)
	}
}

func TestManageDeal_Update_RefusesPopulatedFieldsAndNamesThem(t *testing.T) {
	fake := &fakeDealsClient{
		deal: &pipedrive.Deal{ID: 9, Title: "Acme renewal", Value: 5000, Status: "open", UpdateTime: "t0"},
	}
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "update", "deal_id": 9, "title": "Something else", "value": 1}, nil)
	if !res.IsError {
		t.Fatal("expected a refusal over populated title and value")
	}
	txt := contentText(res)
	if !strings.HasPrefix(txt, "[refused]") {
		t.Errorf("error = %q; want [refused]", txt)
	}
	// The refusal names the argument to paste back, not a flag to set.
	for _, want := range []string{"title", "value", `overwrite: ["title", "value"]`, "deal 9"} {
		if !strings.Contains(txt, want) {
			t.Errorf("refusal %q does not mention %q", txt, want)
		}
	}
	if fake.updateCalls != 0 {
		t.Error("refused update still reached upstream")
	}
}

func TestManageDeal_Update_FillingEmptyFieldNeedsNoOverwrite(t *testing.T) {
	fake := &fakeDealsClient{
		deal:       &pipedrive.Deal{ID: 9, Title: "Acme", Status: "open", UpdateTime: "t0"},
		updateDeal: &pipedrive.Deal{ID: 9, Title: "Acme", Status: "open", ExpectedCloseDate: "2026-12-01", UpdateTime: "t1"},
	}
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "update", "deal_id": 9, "expected_close_date": "2026-12-01"}, nil)
	if res.IsError {
		t.Fatalf("filling an empty field should not be refused: %s", contentText(res))
	}
	if fake.updateCalls != 1 {
		t.Errorf("update calls = %d; want 1", fake.updateCalls)
	}
}

func TestManageDeal_Transitions(t *testing.T) {
	cases := []struct {
		name        string
		args        map[string]any
		wantStatus  string
		wantChanged string
	}{
		{"mark_won", map[string]any{"action": "mark_won", "deal_id": 9}, "won", "status"},
		{"mark_lost", map[string]any{"action": "mark_lost", "deal_id": 9, "lost_reason": "budget"}, "lost", "status"},
		{"move_stage", map[string]any{"action": "move_stage", "deal_id": 9, "stage_id": 4}, "", "stage_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The echo mirrors what the row actually asks for, so the
			// fixture cannot contradict the assertion.
			echo := &pipedrive.Deal{ID: 9, Title: "Acme", Status: "open", StageID: 4, UpdateTime: "t1"}
			if tc.wantStatus != "" {
				echo.Status = tc.wantStatus
			}
			fake := &fakeDealsClient{
				deal:       &pipedrive.Deal{ID: 9, Title: "Acme", Status: "open", StageID: 2, UpdateTime: "t0"},
				updateDeal: echo,
			}
			var out writeOut
			res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal", tc.args, &out)
			if res.IsError {
				t.Fatalf("unexpected isError: %s", contentText(res))
			}
			// A transition names the field it lands on, so it must not
			// be gated behind overwrite the way an update is.
			if fake.updateCalls != 1 {
				t.Fatalf("transition did not write: calls=%d", fake.updateCalls)
			}
			if tc.wantStatus != "" && (fake.lastUpdateReq.Status == nil || *fake.lastUpdateReq.Status != tc.wantStatus) {
				t.Errorf("status sent = %v; want %q", fake.lastUpdateReq.Status, tc.wantStatus)
			}
			if !changedSet(out.Changed)[tc.wantChanged] {
				t.Errorf("changed = %v; want %q", out.Changed, tc.wantChanged)
			}
		})
	}
}

func TestManageDeal_MarkLost_DoesNotInventALostReason(t *testing.T) {
	fake := &fakeDealsClient{
		deal:       &pipedrive.Deal{ID: 9, Status: "open", UpdateTime: "t0"},
		updateDeal: &pipedrive.Deal{ID: 9, Status: "lost", UpdateTime: "t1"},
	}
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "mark_lost", "deal_id": 9}, nil)
	if res.IsError {
		t.Fatalf("unexpected isError: %s", contentText(res))
	}
	if fake.lastUpdateReq.LostReason != nil {
		t.Errorf("lost_reason sent as %q; an omitted reason must stay omitted", *fake.lastUpdateReq.LostReason)
	}
}

// This test used to assert the opposite — that reopen blanks
// lost_reason — and it was green for two releases while reopen failed
// against the real API every single time. The fake accepts any request,
// so the test could only ever confirm that the code sent what the code
// sent.
//
// Pipedrive accepts lost_reason ONLY on a deal that is lost, and
// validates the resulting state: "Lost reason and lost time must can
// only be set when status is lost". Sending it with status:open is
// refused whatever the deal was before, so reopen sends the status
// alone and the old reason stays on the record.
func TestManageDeal_Reopen_SendsStatusOnly(t *testing.T) {
	fake := &fakeDealsClient{
		deal:       &pipedrive.Deal{ID: 9, Status: "lost", LostReason: "budget", UpdateTime: "t0"},
		updateDeal: &pipedrive.Deal{ID: 9, Status: "open", UpdateTime: "t1"},
	}
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "reopen", "deal_id": 9}, nil)
	if res.IsError {
		t.Fatalf("unexpected isError: %s", contentText(res))
	}
	if fake.lastUpdateReq.Status == nil || *fake.lastUpdateReq.Status != "open" {
		t.Errorf("status = %v; want open", fake.lastUpdateReq.Status)
	}
	if fake.lastUpdateReq.LostReason != nil {
		t.Errorf("lost_reason sent as %q; Pipedrive refuses it alongside status:open and fails the whole write",
			*fake.lastUpdateReq.LostReason)
	}
}

func TestManageDeal_MoveStage_RequiresStageID(t *testing.T) {
	fake := &fakeDealsClient{deal: &pipedrive.Deal{ID: 9, UpdateTime: "t0"}}
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "move_stage", "deal_id": 9}, nil)
	if !res.IsError {
		t.Fatal("move_stage without stage_id must be rejected")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error = %q; want [validation]", contentText(res))
	}
	if fake.getCalls != 0 {
		t.Error("a request rejected on its own arguments should cost no round trip")
	}
}

func TestManageDeal_RefusesStaleExpectVersion(t *testing.T) {
	fake := &fakeDealsClient{deal: &pipedrive.Deal{ID: 9, Status: "open", UpdateTime: "t9"}}
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "mark_won", "deal_id": 9, "expect_version": "t0"}, nil)
	if !res.IsError {
		t.Fatal("expected a refusal on a stale expect_version")
	}
	txt := contentText(res)
	if !strings.HasPrefix(txt, "[refused]") || !strings.Contains(txt, "t9") {
		t.Errorf("error = %q; want [refused] naming the current version", txt)
	}
	if fake.updateCalls != 0 {
		t.Error("stale write still reached upstream")
	}
}

func TestManageDeal_NoOpWriteReportsNothingChanged(t *testing.T) {
	fake := &fakeDealsClient{deal: &pipedrive.Deal{ID: 9, Status: "won", UpdateTime: "t0"}}
	var out writeOut
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "mark_won", "deal_id": 9}, &out)
	if res.IsError {
		t.Fatalf("unexpected isError: %s", contentText(res))
	}
	if len(out.Changed) != 0 {
		t.Errorf("changed = %v; want empty", out.Changed)
	}
	if fake.updateCalls != 0 {
		t.Error("a write that would change nothing should not be sent")
	}
}

func TestManageDeal_DryRunFloorAndPerCall(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts tools.RegisterOptions
		args map[string]any
	}{
		{"per-call", tools.RegisterOptions{}, map[string]any{"action": "mark_won", "deal_id": 9, "dry_run": true}},
		{"server floor", tools.RegisterOptions{DryRun: true}, map[string]any{"action": "mark_won", "deal_id": 9}},
		{"floor wins over an omitted flag", tools.RegisterOptions{DryRun: true}, map[string]any{"action": "mark_won", "deal_id": 9, "dry_run": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeDealsClient{deal: &pipedrive.Deal{ID: 9, Status: "open", UpdateTime: "t0"}}
			var out writeOut
			res := callTool(t, dealsReg(fake, tc.opts), "manage_deal", tc.args, &out)
			if res.IsError {
				t.Fatalf("unexpected isError: %s", contentText(res))
			}
			if !out.DryRun {
				t.Error("dry_run should be reported true")
			}
			if !changedSet(out.Changed)["status"] {
				t.Errorf("changed = %v; a dry run still predicts", out.Changed)
			}
			if fake.updateCalls != 0 {
				t.Errorf("dry run hit upstream %d times", fake.updateCalls)
			}
		})
	}
}

func TestManageDeal_RejectsBadAction(t *testing.T) {
	fake := &fakeDealsClient{}
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "explode", "deal_id": 9}, nil)
	if !res.IsError {
		t.Fatal("expected isError")
	}
	txt := contentText(res)
	if !strings.HasPrefix(txt, "[validation]") || !strings.Contains(txt, "mark_won") {
		t.Errorf("error = %q; want [validation] enumerating the actions", txt)
	}
}

// -------------------------------------------------------------- persons

func TestManagePerson_UpdateGuardAndChangedReport(t *testing.T) {
	fake := &fakePersonsClient{
		person:       &pipedrive.Person{ID: 3, Name: "A Contact", UpdateTime: "t0"},
		updatePerson: &pipedrive.Person{ID: 3, Name: "A Contact", OrgID: 7, UpdateTime: "t1"},
	}

	var out writeOut
	res := callTool(t, personsReg(fake), "manage_person", map[string]any{"action": "update", "person_id": 3, "org_id": 7}, &out)
	if res.IsError {
		t.Fatalf("unexpected isError: %s", contentText(res))
	}
	if !changedSet(out.Changed)["org_id"] {
		t.Errorf("changed = %v; want org_id", out.Changed)
	}
	if fake.updateCalls != 1 {
		t.Errorf("update calls = %d; want 1", fake.updateCalls)
	}
}

func TestManagePerson_RefusesReplacingAName(t *testing.T) {
	fake := &fakePersonsClient{person: &pipedrive.Person{ID: 3, Name: "A Contact", UpdateTime: "t0"}}

	res := callTool(t, personsReg(fake), "manage_person", map[string]any{"action": "update", "person_id": 3, "name": "Someone Else"}, nil)
	if !res.IsError {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(contentText(res), "name") {
		t.Errorf("refusal %q does not name the field", contentText(res))
	}
}

func TestManagePerson_EmailsReplaceWholesale(t *testing.T) {
	// Pipedrive replaces a contact-point collection rather than merging
	// into it, so the guard must treat an existing email as populated.
	fake := &fakePersonsClient{
		person: &pipedrive.Person{
			ID: 3, Name: "A Contact", UpdateTime: "t0",
			Emails: []pipedrive.ContactPoint{{Value: "old@example.com", Primary: true}},
		},
	}

	res := callTool(t, personsReg(fake), "manage_person", map[string]any{
		"action": "update", "person_id": 3,
		"emails": []map[string]any{{"value": "new@example.com", "primary": true}},
	}, nil)
	if !res.IsError {
		t.Fatal("replacing an existing email collection should be refused without overwrite")
	}
	if !strings.Contains(contentText(res), "emails") {
		t.Errorf("refusal %q does not name emails", contentText(res))
	}
}

// -------------------------------------------------------- organizations

func TestManageOrganization_UpdateAddressGuard(t *testing.T) {
	fake := &fakeOrganizationsClient{
		org: &pipedrive.Organization{
			ID: 47, Name: "Acme Inc", UpdateTime: "t0",
			Address: &pipedrive.Address{Value: "123 Main St, Springfield"},
		},
	}

	res := callTool(t, orgsReg(fake), "manage_organization", map[string]any{
		"action": "update", "org_id": 47, "address": "456 Other Rd",
	}, nil)
	if !res.IsError {
		t.Fatal("replacing a stored address should be refused without overwrite")
	}
	if !strings.Contains(contentText(res), "address") {
		t.Errorf("refusal %q does not name address", contentText(res))
	}
	if fake.updateCalls != 0 {
		t.Error("refused update still reached upstream")
	}
}

func TestManageOrganization_UpdateHappyPath(t *testing.T) {
	fake := &fakeOrganizationsClient{
		org:       &pipedrive.Organization{ID: 47, Name: "Acme Inc", UpdateTime: "t0"},
		updateOrg: &pipedrive.Organization{ID: 47, Name: "Acme Inc", Address: &pipedrive.Address{Value: "456 Other Rd"}, UpdateTime: "t1"},
	}

	var out writeOut
	res := callTool(t, orgsReg(fake), "manage_organization", map[string]any{
		"action": "update", "org_id": 47, "address": "456 Other Rd",
	}, &out)
	if res.IsError {
		t.Fatalf("unexpected isError: %s", contentText(res))
	}
	if !changedSet(out.Changed)["address"] {
		t.Errorf("changed = %v; want address", out.Changed)
	}
	// The single line goes up as the caller wrote it; Pipedrive parses.
	if fake.lastUpdateReq.Address == nil || *fake.lastUpdateReq.Address != "456 Other Rd" {
		t.Errorf("address sent = %v; want the unparsed line", fake.lastUpdateReq.Address)
	}
}

// ----------------------------------------------------------- activities

func personsReg(fake *fakePersonsClient) func(*mcp.Server) {
	return func(s *mcp.Server) { tools.RegisterPersons(s, fake, "acme", tools.RegisterOptions{}) }
}

func orgsReg(fake *fakeOrganizationsClient) func(*mcp.Server) {
	return func(s *mcp.Server) { tools.RegisterOrganizations(s, fake, "acme", tools.RegisterOptions{}) }
}

func activitiesReg(fake *fakeActivitiesClient) func(*mcp.Server) {
	return func(s *mcp.Server) { tools.RegisterActivities(s, fake, "acme", tools.RegisterOptions{}) }
}

// done is a *bool precisely so false can be sent: a bare bool could not
// tell "reopen this" from "say nothing about done", which is what makes
// completing reversible rather than a one-way door.
func TestManageActivity_CompleteAndReopen(t *testing.T) {
	for _, tc := range []struct {
		action   string
		before   bool
		wantSent bool
	}{
		{"complete", false, true},
		{"reopen", true, false},
	} {
		t.Run(tc.action, func(t *testing.T) {
			fake := &fakeActivitiesClient{
				activity:       &pipedrive.Activity{ID: 5, Subject: "Call", Done: tc.before, UpdateTime: "t0"},
				updateActivity: &pipedrive.Activity{ID: 5, Subject: "Call", Done: tc.wantSent, UpdateTime: "t1"},
			}
			var out writeOut
			res := callTool(t, activitiesReg(fake), "manage_activity",
				map[string]any{"action": tc.action, "activity_id": 5}, &out)
			if res.IsError {
				t.Fatalf("unexpected isError: %s", contentText(res))
			}
			if fake.lastUpdateReq.Done == nil || *fake.lastUpdateReq.Done != tc.wantSent {
				t.Errorf("done sent = %v; want %v", fake.lastUpdateReq.Done, tc.wantSent)
			}
			if !changedSet(out.Changed)["done"] {
				t.Errorf("changed = %v; want done", out.Changed)
			}
			// Neither transition is gated behind overwrite.
			if fake.updateCalls != 1 {
				t.Errorf("update calls = %d; want 1", fake.updateCalls)
			}
		})
	}
}

func TestManageActivity_UpdateRefusesPopulatedNote(t *testing.T) {
	fake := &fakeActivitiesClient{
		activity: &pipedrive.Activity{ID: 5, Subject: "Call", Note: "what was said", UpdateTime: "t0"},
	}
	res := callTool(t, activitiesReg(fake), "manage_activity",
		map[string]any{"action": "update", "activity_id": 5, "note": "something else"}, nil)
	if !res.IsError {
		t.Fatal("expected a refusal over an existing note")
	}
	if !strings.Contains(contentText(res), "note") {
		t.Errorf("refusal %q does not name note", contentText(res))
	}
}

func TestManageActivity_TransitionIgnoresDescriptiveFields(t *testing.T) {
	// A caller who also passes a subject to `complete` must not get it
	// silently written: the transition is the one field it names.
	fake := &fakeActivitiesClient{
		activity:       &pipedrive.Activity{ID: 5, Subject: "Call", Done: false, UpdateTime: "t0"},
		updateActivity: &pipedrive.Activity{ID: 5, Subject: "Call", Done: true, UpdateTime: "t1"},
	}
	res := callTool(t, activitiesReg(fake), "manage_activity",
		map[string]any{"action": "complete", "activity_id": 5, "subject": "Rewritten"}, nil)
	if res.IsError {
		t.Fatalf("unexpected isError: %s", contentText(res))
	}
	if fake.lastUpdateReq.Subject != nil {
		t.Errorf("complete sent subject=%q; a transition writes only its own field", *fake.lastUpdateReq.Subject)
	}
}

func TestManageWrite_UpstreamErrorsSurface(t *testing.T) {
	fake := &fakeDealsClient{
		deal:      &pipedrive.Deal{ID: 9, Status: "open", UpdateTime: "t0"},
		updateErr: pipedrive.ErrRateLimited,
	}
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "mark_won", "deal_id": 9}, nil)
	if !res.IsError {
		t.Fatal("expected isError")
	}
	if !strings.HasPrefix(contentText(res), "[rate_limited]") {
		t.Errorf("error = %q; want [rate_limited]", contentText(res))
	}
}

func TestManageWrite_ReadBeforeWriteErrorSurfaces(t *testing.T) {
	fake := &fakeDealsClient{dealErr: pipedrive.ErrNotFound}
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "mark_won", "deal_id": 999}, nil)
	if !res.IsError {
		t.Fatal("expected isError")
	}
	if !strings.HasPrefix(contentText(res), "[not_found]") {
		t.Errorf("error = %q; want [not_found]", contentText(res))
	}
	if fake.updateCalls != 0 {
		t.Error("write proceeded despite a failed pre-read")
	}
}

func TestManageWrite_RejectsZeroID(t *testing.T) {
	for _, tc := range []struct {
		tool string
		args map[string]any
		reg  func(*mcp.Server)
	}{
		{"manage_deal", map[string]any{"action": "update", "deal_id": 0, "title": "x"}, dealsReg(&fakeDealsClient{}, tools.RegisterOptions{})},
		{"manage_activity", map[string]any{"action": "complete", "activity_id": 0}, activitiesReg(&fakeActivitiesClient{})},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			res := callTool(t, tc.reg, tc.tool, tc.args, nil)
			if !res.IsError {
				t.Fatal("expected isError on a zero id")
			}
			if !strings.HasPrefix(contentText(res), "[validation]") {
				t.Errorf("error = %q; want [validation]", contentText(res))
			}
		})
	}
}

func TestManageDeal_UpdateOverlayCoversEveryField(t *testing.T) {
	// dealAfterUpdate has a branch per field. Set them all at once so a
	// copy-paste slip in one branch cannot hide behind the others.
	prob := 60.0
	fake := &fakeDealsClient{
		deal: &pipedrive.Deal{ID: 9, Status: "open", UpdateTime: "t0"},
		updateDeal: &pipedrive.Deal{
			ID: 9, Title: "New", Value: 1234, Currency: "EUR", Status: "open",
			StageID: 4, PipelineID: 2, OwnerID: 8, PersonID: 3, OrgID: 7,
			ExpectedCloseDate: "2026-12-01", Probability: &prob, UpdateTime: "t1",
		},
	}
	var out writeOut
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal", map[string]any{
		"action": "update", "deal_id": 9,
		"overwrite": []string{"title", "value", "currency", "stage_id", "pipeline_id",
			"owner_id", "person_id", "org_id", "expected_close_date", "probability"},
		"title": "New", "value": 1234, "currency": "EUR",
		"pipeline_id": 2, "stage_id": 4, "owner_id": 8,
		"person_id": 3, "org_id": 7,
		"expected_close_date": "2026-12-01", "probability": 60,
	}, &out)
	if res.IsError {
		t.Fatalf("unexpected isError: %s", contentText(res))
	}
	got := changedSet(out.Changed)
	for _, want := range []string{
		"title", "value", "currency", "stage_id", "pipeline_id",
		"owner_id", "person_id", "org_id", "expected_close_date", "probability",
	} {
		if !got[want] {
			t.Errorf("changed = %v; missing %q", out.Changed, want)
		}
	}
}

func TestManageDeal_DryRunOverlayPredictsWithoutWriting(t *testing.T) {
	// The dry-run prediction comes from the same overlay, so it must
	// name the same fields the real write would.
	fake := &fakeDealsClient{deal: &pipedrive.Deal{ID: 9, Status: "open", UpdateTime: "t0"}}
	var out writeOut
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal", map[string]any{
		"action": "update", "deal_id": 9, "dry_run": true,
		"title": "New", "value": 1234, "currency": "EUR", "org_id": 7,
	}, &out)
	if res.IsError {
		t.Fatalf("unexpected isError: %s", contentText(res))
	}
	got := changedSet(out.Changed)
	for _, want := range []string{"title", "value", "currency", "org_id"} {
		if !got[want] {
			t.Errorf("predicted changed = %v; missing %q", out.Changed, want)
		}
	}
	if fake.updateCalls != 0 {
		t.Error("dry run reached upstream")
	}
}

func TestManageActivity_UpdateOverlayCoversEveryField(t *testing.T) {
	fake := &fakeActivitiesClient{
		activity: &pipedrive.Activity{ID: 5, UpdateTime: "t0"},
		updateActivity: &pipedrive.Activity{
			ID: 5, Subject: "Call", Type: "call", DueDate: "2026-12-01",
			DueTime: "10:00", Duration: "00:30", DealID: 9, PersonID: 3,
			OrgID: 7, LeadID: "abc", OwnerID: 8, Note: "n",
			PublicDescription: "p", Location: &pipedrive.ActivityLocation{Value: "here"},
			Done: true, Busy: true, UpdateTime: "t1",
		},
	}
	var out writeOut
	res := callTool(t, activitiesReg(fake), "manage_activity", map[string]any{
		"action": "update", "activity_id": 5,
		"overwrite": []string{"subject", "type", "due_date", "due_time", "duration", "note", "public_description", "deal_id", "person_id", "org_id", "owner_id", "location", "participants", "busy"},
		"subject":   "Call", "type": "call", "due_date": "2026-12-01",
		"due_time": "10:00", "duration": "00:30", "deal_id": 9,
		"person_id": 3, "org_id": 7, "lead_id": "abc", "owner_id": 8,
		"note": "n", "public_description": "p", "location": "here",
		"done": true, "busy": true,
	}, &out)
	if res.IsError {
		t.Fatalf("unexpected isError: %s", contentText(res))
	}
	got := changedSet(out.Changed)
	for _, want := range []string{
		"subject", "type", "due_date", "due_time", "duration", "deal_id",
		"person_id", "org_id", "lead_id", "owner_id", "note",
		"public_description", "location", "done", "busy",
	} {
		if !got[want] {
			t.Errorf("changed = %v; missing %q", out.Changed, want)
		}
	}
}

func TestManagePerson_UpdateOverlayCoversEveryField(t *testing.T) {
	fake := &fakePersonsClient{
		person: &pipedrive.Person{ID: 3, UpdateTime: "t0"},
		updatePerson: &pipedrive.Person{
			ID: 3, Name: "A Contact", FirstName: "A", LastName: "Contact",
			Emails:  []pipedrive.ContactPoint{{Value: "a@example.com", Primary: true}},
			Phones:  []pipedrive.ContactPoint{{Value: "+1000", Primary: true}},
			OrgID:   7,
			OwnerID: 8, UpdateTime: "t1",
		},
	}
	// name is NOT sent: Pipedrive rejects a write carrying both it and
	// the parts. It still has to appear in changed, because Pipedrive
	// derives it from the parts and the diff reports what moved rather
	// than what was asked for.
	var out writeOut
	res := callTool(t, personsReg(fake), "manage_person", map[string]any{
		"action": "update", "person_id": 3,
		"overwrite":  []string{"first_name", "last_name", "emails", "phones", "org_id", "owner_id"},
		"first_name": "A", "last_name": "Contact",
		"emails":   []map[string]any{{"value": "a@example.com", "primary": true}},
		"phones":   []map[string]any{{"value": "+1000", "primary": true}},
		"org_id":   7,
		"owner_id": 8,
	}, &out)
	if res.IsError {
		t.Fatalf("unexpected isError: %s", contentText(res))
	}
	got := changedSet(out.Changed)
	for _, want := range []string{"name", "first_name", "last_name", "emails", "phones", "org_id", "owner_id"} {
		if !got[want] {
			t.Errorf("changed = %v; missing %q", out.Changed, want)
		}
	}
}

// ---- regressions from the /security-review of 2026-09-16 ----
//
// Both were the same shape: a field that an update WRITES but that the
// resource's fieldSpec table did not describe. Untabled, it is invisible
// to changedFields, so requireOverwrite cannot refuse over it and the
// `changed` report never admits it moved — while the tool description
// promises to refuse over ANY populated field and name each one.

func TestManageActivity_ParticipantsAreGuarded(t *testing.T) {
	// An update REPLACES the participant collection. Riding along on an
	// unrelated edit, it silently deleted every existing attendee.
	fake := &fakeActivitiesClient{
		activity: &pipedrive.Activity{
			ID: 5, Subject: "Kickoff", Note: "", UpdateTime: "t0",
			Participants: []pipedrive.ActivityParticipant{
				{PersonID: 1, Primary: true}, {PersonID: 2},
			},
		},
	}
	res := callTool(t, activitiesReg(fake), "manage_activity", map[string]any{
		"action": "update", "activity_id": 5,
		"note":         "x", // empty upstream, so it needs no permission
		"participants": []map[string]any{{"person_id": 99, "primary": true}},
	}, nil)
	if !res.IsError {
		t.Fatal("replacing a populated participant list must be refused without overwrite")
	}
	txt := contentText(res)
	if !strings.HasPrefix(txt, "[refused]") {
		t.Errorf("error = %q; want [refused]", txt)
	}
	if !strings.Contains(txt, "participants") {
		t.Errorf("refusal %q does not name participants", txt)
	}
	if fake.updateCalls != 0 {
		t.Error("attendees were dropped upstream despite the refusal")
	}
}

func TestManageActivity_ParticipantsOnlyUpdateIsNotASilentNoOp(t *testing.T) {
	// Untabled, a participants-only change produced len(changed)==0, so
	// the early return reported "already in that state" and wrote
	// nothing — while echoing the OLD list back as if it were current.
	fake := &fakeActivitiesClient{
		activity: &pipedrive.Activity{
			ID: 5, Subject: "Kickoff", UpdateTime: "t0",
			Participants: []pipedrive.ActivityParticipant{{PersonID: 1, Primary: true}},
		},
		updateActivity: &pipedrive.Activity{
			ID: 5, Subject: "Kickoff", UpdateTime: "t1",
			Participants: []pipedrive.ActivityParticipant{{PersonID: 99, Primary: true}},
		},
	}
	var out writeOut
	res := callTool(t, activitiesReg(fake), "manage_activity", map[string]any{
		"action": "update", "activity_id": 5,
		"overwrite":    []string{"subject", "type", "due_date", "due_time", "duration", "note", "public_description", "deal_id", "person_id", "org_id", "owner_id", "location", "participants", "busy"},
		"participants": []map[string]any{{"person_id": 99, "primary": true}},
	}, &out)
	if res.IsError {
		t.Fatalf("with overwrite the write should proceed: %s", contentText(res))
	}
	if !changedSet(out.Changed)["participants"] {
		t.Errorf("changed = %v; want participants named", out.Changed)
	}
	if fake.updateCalls != 1 {
		t.Errorf("update calls = %d; want 1 — a real change must not be swallowed as a no-op", fake.updateCalls)
	}
}

func TestManageActivity_PromotingADifferentPrimaryIsReported(t *testing.T) {
	// Same people, different primary. The projection carries the flag,
	// so this counts as a change rather than passing as identical.
	fake := &fakeActivitiesClient{
		activity: &pipedrive.Activity{
			ID: 5, UpdateTime: "t0",
			Participants: []pipedrive.ActivityParticipant{
				{PersonID: 1, Primary: true}, {PersonID: 2},
			},
		},
	}
	res := callTool(t, activitiesReg(fake), "manage_activity", map[string]any{
		"action": "update", "activity_id": 5,
		"participants": []map[string]any{
			{"person_id": 1}, {"person_id": 2, "primary": true},
		},
	}, nil)
	if !res.IsError || !strings.Contains(contentText(res), "participants") {
		t.Errorf("promoting a different primary should be seen and refused; got %q", contentText(res))
	}
}

func TestManagePerson_TruncatingContactPointsIsGuarded(t *testing.T) {
	// The primary survives and the rest are deleted. A projection that
	// returned only the primary compared equal before and after, so this
	// destroyed the secondary addresses unguarded and unreported.
	fake := &fakePersonsClient{
		person: &pipedrive.Person{
			ID: 7, Name: "A Contact", FirstName: "", UpdateTime: "t0",
			Emails: []pipedrive.ContactPoint{
				{Value: "a@example.com", Primary: true},
				{Value: "b@example.com"},
				{Value: "c@example.com"},
			},
		},
	}
	res := callTool(t, personsReg(fake), "manage_person", map[string]any{
		"action": "update", "person_id": 7,
		"first_name": "Ada", // empty upstream, so it needs no permission
		"emails":     []map[string]any{{"value": "a@example.com", "primary": true}},
	}, nil)
	if !res.IsError {
		t.Fatal("dropping the secondary emails must be refused without overwrite")
	}
	txt := contentText(res)
	if !strings.Contains(txt, "emails") {
		t.Errorf("refusal %q does not name emails", txt)
	}
	if fake.updateCalls != 0 {
		t.Error("emails were destroyed upstream despite the refusal")
	}
}

func TestManagePerson_TruncationIsReportedWhenPermitted(t *testing.T) {
	fake := &fakePersonsClient{
		person: &pipedrive.Person{
			ID: 7, Name: "A Contact", UpdateTime: "t0",
			Emails: []pipedrive.ContactPoint{
				{Value: "a@example.com", Primary: true}, {Value: "b@example.com"},
			},
		},
		updatePerson: &pipedrive.Person{
			ID: 7, Name: "A Contact", UpdateTime: "t1",
			Emails: []pipedrive.ContactPoint{{Value: "a@example.com", Primary: true}},
		},
	}
	var out writeOut
	res := callTool(t, personsReg(fake), "manage_person", map[string]any{
		"action": "update", "person_id": 7,
		"overwrite": []string{"name", "first_name", "last_name", "emails", "phones", "org_id", "owner_id"},
		"emails":    []map[string]any{{"value": "a@example.com", "primary": true}},
	}, &out)
	if res.IsError {
		t.Fatalf("with overwrite the write should proceed: %s", contentText(res))
	}
	if !changedSet(out.Changed)["emails"] {
		t.Errorf("changed = %v; a truncation must be named", out.Changed)
	}
}

func TestManageWrite_CustomFieldsAreResolvedOnEveryPath(t *testing.T) {
	// Every write path must resolve custom-field hashes to the names the
	// workspace gives them, or the LLM gets 40-char hashes it cannot cite.
	//
	// This exists because a refactor once dropped the resolve on the
	// create path and every test still passed: nothing asserted that a
	// created record comes back with its custom fields named.
	byName := func(raw map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range raw {
			if k == "abcdef0123456789abcdef0123456789abcdef01" {
				out["Renewal owner"] = v
				continue
			}
			out[k] = v
		}
		return out
	}
	withHash := map[string]any{"abcdef0123456789abcdef0123456789abcdef01": "someone"}

	type probe struct {
		name string
		args map[string]any
		fake *fakeDealsClient
	}
	for _, tc := range []probe{
		{
			name: "create",
			args: map[string]any{"action": "create", "title": "Acme renewal"},
			fake: &fakeDealsClient{
				resolver:   byName,
				createDeal: &pipedrive.Deal{ID: 9, Title: "Acme renewal", CustomFields: withHash},
			},
		},
		{
			name: "update",
			args: map[string]any{"action": "update", "deal_id": 9, "value": 5000},
			fake: &fakeDealsClient{
				resolver:   byName,
				deal:       &pipedrive.Deal{ID: 9, Title: "Acme renewal", UpdateTime: "t0"},
				updateDeal: &pipedrive.Deal{ID: 9, Title: "Acme renewal", Value: 5000, UpdateTime: "t1", CustomFields: withHash},
			},
		},
		{
			name: "transition",
			args: map[string]any{"action": "mark_won", "deal_id": 9},
			fake: &fakeDealsClient{
				resolver:   byName,
				deal:       &pipedrive.Deal{ID: 9, Status: "open", UpdateTime: "t0"},
				updateDeal: &pipedrive.Deal{ID: 9, Status: "won", UpdateTime: "t1", CustomFields: withHash},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out struct {
				Deal dealRow `json:"deal"`
			}
			res := callTool(t, dealsReg(tc.fake, tools.RegisterOptions{}), "manage_deal", tc.args, &out)
			if res.IsError {
				t.Fatalf("unexpected isError: %s", contentText(res))
			}
			if _, named := out.Deal.CustomFields["Renewal owner"]; !named {
				t.Errorf("custom fields = %v; want the hash resolved to its workspace name", out.Deal.CustomFields)
			}
		})
	}
}

// ------------------------------------------------- custom-field writes
//
// The fake's encoder keys a field as "cf_<lowercased name>" and reports
// it under the name the caller used, so these assert the tools layer's
// half: that the encoded values reach the request body, that the field
// is diffed and guarded like a typed one, and that a refusal from the
// encoder stops the write.

func TestManageDeal_CustomFieldReachesTheRequestAndTheReport(t *testing.T) {
	fake := &fakeDealsClient{
		deal: &pipedrive.Deal{ID: 9, Title: "Acme renewal", Status: "open", UpdateTime: "t0"},
		updateDeal: &pipedrive.Deal{ID: 9, Title: "Acme renewal", Status: "open", UpdateTime: "t1",
			CustomFields: map[string]any{"cf_segment": "Enterprise"}},
	}
	var out writeOut
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "update", "deal_id": 9,
			"custom_fields": map[string]any{"Segment": "Enterprise"}}, &out)
	if res.IsError {
		t.Fatalf("unexpected isError: %s", contentText(res))
	}
	if got := fake.lastUpdateReq.CustomFields["cf_segment"]; got != "Enterprise" {
		t.Errorf("request custom_fields = %v; want the encoded value under its stored key", fake.lastUpdateReq.CustomFields)
	}
	// Reported under the name the caller can act on, not the stored key.
	if !changedSet(out.Changed)["Segment"] {
		t.Errorf("changed = %v; want Segment", out.Changed)
	}
}

func TestManageDeal_CustomFieldRefusesToClobberWithoutOverwrite(t *testing.T) {
	fake := &fakeDealsClient{
		deal: &pipedrive.Deal{ID: 9, Title: "Acme renewal", Status: "open", UpdateTime: "t0",
			CustomFields: map[string]any{"cf_segment": "Mid-market"}},
	}
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "update", "deal_id": 9,
			"custom_fields": map[string]any{"Segment": "Enterprise"}}, nil)
	if !res.IsError {
		t.Fatal("expected a refusal over a populated custom field")
	}
	text := contentText(res)
	if !strings.Contains(text, "Segment") {
		t.Errorf("refusal does not name the field: %s", text)
	}
	if !strings.Contains(text, "overwrite") {
		t.Errorf("refusal does not name the argument that allows it: %s", text)
	}
	if fake.updateCalls != 0 {
		t.Errorf("a refused write still sent %d updates", fake.updateCalls)
	}
}

// Filling a custom field that holds nothing destroys nothing, so it
// needs no permission — the same rule every typed field follows.
func TestManageDeal_CustomFieldFillsAnEmptyOneWithoutOverwrite(t *testing.T) {
	fake := &fakeDealsClient{
		deal: &pipedrive.Deal{ID: 9, Title: "Acme renewal", Status: "open", UpdateTime: "t0"},
		updateDeal: &pipedrive.Deal{ID: 9, Title: "Acme renewal", Status: "open", UpdateTime: "t1",
			CustomFields: map[string]any{"cf_segment": "Enterprise"}},
	}
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "update", "deal_id": 9,
			"custom_fields": map[string]any{"Segment": "Enterprise"}}, nil)
	if res.IsError {
		t.Fatalf("filling an empty custom field was refused: %s", contentText(res))
	}
	if fake.updateCalls != 1 {
		t.Errorf("update calls = %d; want 1", fake.updateCalls)
	}
}

func TestManageDeal_CustomFieldEncodeRefusalStopsTheWrite(t *testing.T) {
	fake := &fakeDealsClient{
		deal:      &pipedrive.Deal{ID: 9, Title: "Acme renewal", Status: "open", UpdateTime: "t0"},
		encodeErr: fmt.Errorf("%w: this workspace has no custom field named \"Nope\"", pipedrive.ErrValidation),
	}
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "update", "deal_id": 9,
			"custom_fields": map[string]any{"Nope": "x"}}, nil)
	if !res.IsError {
		t.Fatal("expected the encoder's refusal to surface")
	}
	if !strings.Contains(contentText(res), "no custom field named") {
		t.Errorf("refusal lost its reason: %s", contentText(res))
	}
	// Refused before the read, so nothing upstream was touched at all.
	if fake.getCalls != 0 || fake.updateCalls != 0 {
		t.Errorf("a refused encode still made %d reads and %d writes", fake.getCalls, fake.updateCalls)
	}
}

// A transition is "status=won and nothing else". It already ignores
// every descriptive field, and custom_fields is one — a caller who sends
// both should not have the field written as a side effect of closing.
func TestManageDeal_TransitionIgnoresCustomFields(t *testing.T) {
	fake := &fakeDealsClient{
		deal:       &pipedrive.Deal{ID: 9, Title: "Acme renewal", Status: "open", UpdateTime: "t0"},
		updateDeal: &pipedrive.Deal{ID: 9, Title: "Acme renewal", Status: "won", UpdateTime: "t1"},
	}
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "mark_won", "deal_id": 9,
			"custom_fields": map[string]any{"Segment": "Enterprise"}}, nil)
	if res.IsError {
		t.Fatalf("unexpected isError: %s", contentText(res))
	}
	if len(fake.lastUpdateReq.CustomFields) != 0 {
		t.Errorf("mark_won sent custom fields: %v", fake.lastUpdateReq.CustomFields)
	}
}

// ...and it must not be validated either: refusing to close a deal over
// a custom field the close ignores would be a refusal the caller cannot
// act on except by deleting an argument that did nothing.
func TestManageDeal_TransitionDoesNotValidateCustomFields(t *testing.T) {
	fake := &fakeDealsClient{
		deal:       &pipedrive.Deal{ID: 9, Title: "Acme renewal", Status: "open", UpdateTime: "t0"},
		updateDeal: &pipedrive.Deal{ID: 9, Title: "Acme renewal", Status: "won", UpdateTime: "t1"},
		encodeErr:  fmt.Errorf("%w: this workspace has no custom field named \"Nope\"", pipedrive.ErrValidation),
	}
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "mark_won", "deal_id": 9,
			"custom_fields": map[string]any{"Nope": "x"}}, nil)
	if res.IsError {
		t.Fatalf("mark_won was refused over a field it ignores: %s", contentText(res))
	}
}

func TestManageDeal_CustomFieldDryRunSendsNothing(t *testing.T) {
	fake := &fakeDealsClient{
		deal: &pipedrive.Deal{ID: 9, Title: "Acme renewal", Status: "open", UpdateTime: "t0"},
	}
	var out writeOut
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "update", "deal_id": 9, "dry_run": true,
			"custom_fields": map[string]any{"Segment": "Enterprise"}}, &out)
	if res.IsError {
		t.Fatalf("unexpected isError: %s", contentText(res))
	}
	if !changedSet(out.Changed)["Segment"] {
		t.Errorf("a rehearsal must still report what would change: %v", out.Changed)
	}
	if fake.updateCalls != 0 {
		t.Errorf("dry run sent %d updates", fake.updateCalls)
	}
}

func TestManagePerson_CustomFieldReachesTheRequest(t *testing.T) {
	fake := &fakePersonsClient{
		person:       &pipedrive.Person{ID: 5, Name: "Ada", UpdateTime: "t0"},
		updatePerson: &pipedrive.Person{ID: 5, Name: "Ada", UpdateTime: "t1", CustomFields: map[string]any{"cf_tier": "Gold"}},
	}
	var out writeOut
	res := callTool(t, personsReg(fake), "manage_person",
		map[string]any{"action": "update", "person_id": 5,
			"custom_fields": map[string]any{"Tier": "Gold"}}, &out)
	if res.IsError {
		t.Fatalf("unexpected isError: %s", contentText(res))
	}
	if got := fake.lastUpdateReq.CustomFields["cf_tier"]; got != "Gold" {
		t.Errorf("request custom_fields = %v", fake.lastUpdateReq.CustomFields)
	}
	if !changedSet(out.Changed)["Tier"] {
		t.Errorf("changed = %v; want Tier", out.Changed)
	}
}

func TestManageOrganization_CustomFieldReachesTheRequest(t *testing.T) {
	fake := &fakeOrganizationsClient{
		org:       &pipedrive.Organization{ID: 3, Name: "Acme", UpdateTime: "t0"},
		updateOrg: &pipedrive.Organization{ID: 3, Name: "Acme", UpdateTime: "t1", CustomFields: map[string]any{"cf_region": "EMEA"}},
	}
	var out writeOut
	res := callTool(t, orgsReg(fake), "manage_organization",
		map[string]any{"action": "update", "org_id": 3,
			"custom_fields": map[string]any{"Region": "EMEA"}}, &out)
	if res.IsError {
		t.Fatalf("unexpected isError: %s", contentText(res))
	}
	if got := fake.lastUpdateReq.CustomFields["cf_region"]; got != "EMEA" {
		t.Errorf("request custom_fields = %v", fake.lastUpdateReq.CustomFields)
	}
	if !changedSet(out.Changed)["Region"] {
		t.Errorf("changed = %v; want Region", out.Changed)
	}
}

// A create carries its custom fields too, and the rehearsal reports them
// under the same names the real create would.
func TestManageDeal_CreateCarriesCustomFields(t *testing.T) {
	fake := &fakeDealsClient{}
	var out writeOut
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "create", "title": "New deal", "dry_run": true,
			"custom_fields": map[string]any{"Segment": "Enterprise"}}, &out)
	if res.IsError {
		t.Fatalf("unexpected isError: %s", contentText(res))
	}
	if !changedSet(out.Changed)["Segment"] {
		t.Errorf("changed = %v; want Segment on the rehearsal", out.Changed)
	}
	if fake.createCallSeen {
		t.Error("a dry-run create still called upstream")
	}
}

// Pipedrive accepts no edit to an archived deal and answers with a bare
// 403. The read the write already does can say so first, and name the
// action that fixes it — a refusal the caller cannot act on is a bug.
func TestManageDeal_RefusesToEditAnArchivedDeal(t *testing.T) {
	fake := &fakeDealsClient{
		deal: &pipedrive.Deal{ID: 9, Title: "Acme renewal", Status: "open", UpdateTime: "t0", IsArchived: true},
	}
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "update", "deal_id": 9, "value": 5000}, nil)
	if !res.IsError {
		t.Fatal("editing an archived deal was allowed")
	}
	if text := contentText(res); !strings.Contains(text, "unarchive") {
		t.Errorf("the refusal does not name the action that fixes it: %s", text)
	}
	if fake.updateCalls != 0 {
		t.Errorf("a refused write still sent %d updates", fake.updateCalls)
	}
}

// ...and unarchive itself must get past that guard, or an archived deal
// would be unreachable through this tool.
func TestManageDeal_UnarchiveReachesAnArchivedDeal(t *testing.T) {
	fake := &fakeDealsClient{
		deal:       &pipedrive.Deal{ID: 9, Title: "Acme renewal", Status: "open", UpdateTime: "t0", IsArchived: true},
		updateDeal: &pipedrive.Deal{ID: 9, Title: "Acme renewal", Status: "open", UpdateTime: "t1"},
	}
	var out writeOut
	res := callTool(t, dealsReg(fake, tools.RegisterOptions{}), "manage_deal",
		map[string]any{"action": "unarchive", "deal_id": 9}, &out)
	if res.IsError {
		t.Fatalf("unarchive was refused: %s", contentText(res))
	}
	if fake.lastUpdateReq.IsArchived == nil || *fake.lastUpdateReq.IsArchived {
		t.Errorf("unarchive sent %v; want is_archived false", fake.lastUpdateReq.IsArchived)
	}
	if !changedSet(out.Changed)["is_archived"] {
		t.Errorf("changed = %v; want is_archived", out.Changed)
	}
}

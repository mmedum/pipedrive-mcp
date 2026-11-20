package tools_test

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
	"github.com/mmedum/pipedrive-mcp/internal/tools"
)

// fakeInstallmentsClient keeps one store of deals and installments, so a
// write is visible to the read that follows it.
type fakeInstallmentsClient struct {
	deals    map[int64]*pipedrive.Deal
	rows     []pipedrive.Installment
	products map[int64][]pipedrive.DealProduct
	nextID   int64
	listErr  error

	creates, updates, deletes int
	lastUpdate                pipedrive.InstallmentRequest
}

func newInstallmentsFake() *fakeInstallmentsClient {
	return &fakeInstallmentsClient{
		deals: map[int64]*pipedrive.Deal{
			7: {ID: 7, Title: "Acme rollout", Value: 1300, Currency: "DKK", Status: "open"},
			8: {ID: 8, Title: "Globex license", Value: 300, Currency: "EUR", Status: "won"},
		},
		rows: []pipedrive.Installment{
			{ID: 2, DealID: 7, Description: "Phase 2", Amount: 500, BillingDate: "2027-03-31"},
			{ID: 1, DealID: 7, Description: "Phase 1", Amount: 801, BillingDate: "2026-12-31"},
			{ID: 3, DealID: 8, Description: "Year 1", Amount: 300, BillingDate: "2026-09-01"},
		},
		products: map[int64][]pipedrive.DealProduct{
			7: {{ID: 1, BillingFrequency: "one-time", IsEnabled: true}},
			8: {{ID: 2, BillingFrequency: "one-time", IsEnabled: true}},
		},
		nextID: 100,
	}
}

func (f *fakeInstallmentsClient) GetDeal(_ context.Context, id int64) (*pipedrive.Deal, error) {
	d, ok := f.deals[id]
	if !ok {
		return nil, &pipedrive.APIError{Class: pipedrive.ErrNotFound, Status: 404, Message: "Deal not found"}
	}
	return d, nil
}

func (f *fakeInstallmentsClient) ListDeals(_ context.Context, opts pipedrive.ListDealsOptions) ([]pipedrive.Deal, string, error) {
	var out []pipedrive.Deal
	for _, id := range opts.IDs {
		if d, ok := f.deals[id]; ok {
			out = append(out, *d)
		}
	}
	return out, "", nil
}

func (f *fakeInstallmentsClient) ListInstallments(_ context.Context, ids []int64, _ int, _ string) ([]pipedrive.Installment, string, error) {
	if f.listErr != nil {
		return nil, "", f.listErr
	}
	var out []pipedrive.Installment
	for _, r := range f.rows {
		if slices.Contains(ids, r.DealID) {
			out = append(out, r)
		}
	}
	return out, "", nil
}

func (f *fakeInstallmentsClient) ListDealProducts(_ context.Context, id int64) ([]pipedrive.DealProduct, error) {
	return f.products[id], nil
}

func (f *fakeInstallmentsClient) CreateInstallment(_ context.Context, dealID int64, req pipedrive.InstallmentRequest) (*pipedrive.Installment, error) {
	f.creates++
	f.nextID++
	in := pipedrive.Installment{ID: f.nextID, DealID: dealID, Description: *req.Description, Amount: *req.Amount, BillingDate: *req.BillingDate}
	f.rows = append(f.rows, in)
	return &in, nil
}

func (f *fakeInstallmentsClient) UpdateInstallment(_ context.Context, _, id int64, req pipedrive.InstallmentRequest) (*pipedrive.Installment, error) {
	f.updates++
	f.lastUpdate = req
	for i := range f.rows {
		if f.rows[i].ID == id {
			if req.Description != nil {
				f.rows[i].Description = *req.Description
			}
			if req.Amount != nil {
				f.rows[i].Amount = *req.Amount
			}
			if req.BillingDate != nil {
				f.rows[i].BillingDate = *req.BillingDate
			}
			out := f.rows[i]
			return &out, nil
		}
	}
	return nil, &pipedrive.APIError{Class: pipedrive.ErrNotFound, Status: 404}
}

func (f *fakeInstallmentsClient) DeleteInstallment(_ context.Context, _, id int64) error {
	f.deletes++
	f.rows = slices.DeleteFunc(f.rows, func(r pipedrive.Installment) bool { return r.ID == id })
	return nil
}

type installmentRow struct {
	ID          int64   `json:"id"`
	DealID      int64   `json:"deal_id"`
	Description string  `json:"description"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	BillingDate string  `json:"billing_date"`
	Version     string  `json:"version"`
}

type scheduleRow struct {
	DealID       int64            `json:"deal_id"`
	DealValue    float64          `json:"deal_value"`
	Currency     string           `json:"currency"`
	Installments []installmentRow `json:"installments"`
	Sum          float64          `json:"sum"`
	Warnings     []string         `json:"warnings"`
}

type manageInstallmentResult struct {
	Action      string         `json:"action"`
	Installment installmentRow `json:"installment"`
	Changed     []string       `json:"changed"`
	Deleted     bool           `json:"deleted"`
	DryRun      bool           `json:"dry_run"`
	Schedule    scheduleRow    `json:"schedule"`
}

func callInstallment(t *testing.T, f *fakeInstallmentsClient, opts tools.RegisterOptions, args map[string]any) (*mcp.CallToolResult, manageInstallmentResult) {
	t.Helper()
	var out manageInstallmentResult
	res := testutil.CallToolInto(t, func(s *mcp.Server) { tools.RegisterInstallments(s, f, opts) }, "manage_installment", args, &out)
	return res, out
}

func dates(s scheduleRow) []string {
	out := make([]string, 0, len(s.Installments))
	for _, in := range s.Installments {
		out = append(out, in.BillingDate)
	}
	return out
}

func hasWarning(s scheduleRow, part string) bool {
	return slices.ContainsFunc(s.Warnings, func(w string) bool { return strings.Contains(w, part) })
}

func TestGetDeal_ReportsTheScheduleAndWarnsWhenItIsStale(t *testing.T) {
	fake := &fakeDealsClient{
		deal: &pipedrive.Deal{ID: 7, Title: "Acme rollout", Value: 1300, Currency: "DKK", Status: "open"},
		installments: []pipedrive.Installment{
			{ID: 2, DealID: 7, Description: "Phase 2", Amount: 500, BillingDate: "2027-03-31"},
			{ID: 1, DealID: 7, Description: "Phase 1", Amount: 801, BillingDate: "2026-12-31"},
		},
	}
	var out struct {
		Installments scheduleRow `json:"installments"`
	}
	res := testutil.CallToolInto(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, fake, "acme", tools.RegisterOptions{})
	}, "get_deal", map[string]any{"deal_id": 7}, &out)
	if res.IsError {
		t.Fatalf("get_deal failed: %s", contentText(res))
	}
	if out.Installments.Sum != 1301 || out.Installments.DealValue != 1300 || out.Installments.Currency != "DKK" {
		t.Errorf("schedule = %+v, want sum 1301 beside value 1300 DKK", out.Installments)
	}
	if got, want := dates(out.Installments), []string{"2026-12-31", "2027-03-31"}; !reflect.DeepEqual(got, want) {
		t.Errorf("billing dates = %v, want oldest first %v", got, want)
	}
	if !hasWarning(out.Installments, "stale: the installments sum to 1301 DKK but the deal value is 1300 DKK") {
		t.Errorf("warnings = %q, want the stale warning naming both amounts", out.Installments.Warnings)
	}
}

func TestGetDeal_StillReturnsTheDealWhenTheScheduleCannotBeRead(t *testing.T) {
	fake := &fakeDealsClient{
		deal:            &pipedrive.Deal{ID: 7, Title: "Acme rollout", Currency: "DKK"},
		installmentsErr: &pipedrive.APIError{Class: pipedrive.ErrForbiddenPermission, Status: 403, Message: "Not available on your plan"},
	}
	var out struct {
		Deal              dealRow      `json:"deal"`
		Installments      *scheduleRow `json:"installments"`
		InstallmentsError string       `json:"installments_error"`
	}
	res := testutil.CallToolInto(t, func(s *mcp.Server) {
		tools.RegisterDeals(s, fake, "acme", tools.RegisterOptions{})
	}, "get_deal", map[string]any{"deal_id": 7}, &out)
	if res.IsError || out.Deal.ID != 7 {
		t.Fatalf("get_deal = %+v (isError %v), want the deal despite the failed schedule", out, res.IsError)
	}
	if out.Installments != nil || !strings.HasPrefix(out.InstallmentsError, "[permission]") || !strings.Contains(out.InstallmentsError, "Growth plan") {
		t.Errorf("installments = %+v, error = %q; want no schedule and a [permission] error naming the plan", out.Installments, out.InstallmentsError)
	}
}

func TestListInstallments_OneSchedulePerDealInTheOrderAsked(t *testing.T) {
	fake := newInstallmentsFake()
	var out struct {
		Deals []scheduleRow `json:"deals"`
	}
	res := testutil.CallToolInto(t, func(s *mcp.Server) {
		tools.RegisterInstallments(s, fake, tools.RegisterOptions{})
	}, "list_installments", map[string]any{"deal_ids": []int64{8, 7, 9, 8}}, &out)
	if res.IsError {
		t.Fatalf("list_installments failed: %s", contentText(res))
	}
	ids := make([]int64, 0, len(out.Deals))
	for _, d := range out.Deals {
		ids = append(ids, d.DealID)
	}
	if want := []int64{8, 7, 9}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("deals = %v, want %v: asked order, repeats once", ids, want)
	}
	if out.Deals[0].Sum != 300 || out.Deals[0].Installments[0].Currency != "EUR" || !hasWarning(out.Deals[0], "is won") {
		t.Errorf("deal 8 = %+v, want sum 300 EUR and the won warning", out.Deals[0])
	}
	if hasWarning(out.Deals[0], "stale") {
		t.Errorf("deal 8 warns stale with sum equal to value: %q", out.Deals[0].Warnings)
	}
	if !hasWarning(out.Deals[1], "stale") {
		t.Errorf("deal 7 = %+v, want the stale warning", out.Deals[1])
	}
	if len(out.Deals[2].Installments) != 0 || !hasWarning(out.Deals[2], "value and currency are unknown") {
		t.Errorf("deal 9 = %+v, want no rows and a warning that the deal was not read", out.Deals[2])
	}
}

func TestListInstallments_RefusesAnEmptyOrOversizedList(t *testing.T) {
	many := make([]int64, 101)
	for i := range many {
		many[i] = int64(i + 1)
	}
	for _, ids := range [][]int64{nil, many, {7, 0}} {
		res := testutil.CallTool(t, func(s *mcp.Server) {
			tools.RegisterInstallments(s, newInstallmentsFake(), tools.RegisterOptions{})
		}, "list_installments", map[string]any{"deal_ids": ids})
		if !res.IsError || !strings.HasPrefix(contentText(res), "[validation]") {
			t.Errorf("deal_ids of %d entries = %s, want [validation]", len(ids), contentText(res))
		}
	}
}

func TestManageInstallment_DryRunCreateShowsTheScheduleItWouldLeave(t *testing.T) {
	fake := newInstallmentsFake()
	res, out := callInstallment(t, fake, tools.RegisterOptions{}, map[string]any{
		"action": "create", "deal_id": 7, "description": "Phase 3", "amount": 40, "billing_date": "2026-11-30", "dry_run": true,
	})
	if res.IsError {
		t.Fatalf("dry-run create failed: %s", contentText(res))
	}
	if fake.creates != 0 {
		t.Errorf("a dry run sent %d creates", fake.creates)
	}
	want := installmentRow{DealID: 7, Description: "Phase 3", Amount: 40, Currency: "DKK", BillingDate: "2026-11-30"}
	got := out.Installment
	got.Version = ""
	if !reflect.DeepEqual(got, want) || !out.DryRun {
		t.Errorf("installment = %+v (dry_run %v), want %+v with id 0", got, out.DryRun, want)
	}
	if !reflect.DeepEqual(out.Changed, []string{"description", "amount", "billing_date"}) {
		t.Errorf("changed = %v", out.Changed)
	}
	if out.Schedule.Sum != 1341 || !reflect.DeepEqual(dates(out.Schedule), []string{"2026-11-30", "2026-12-31", "2027-03-31"}) {
		t.Errorf("schedule = %+v, want the new row first and sum 1341", out.Schedule)
	}
}

func TestManageInstallment_CreateWritesAndReportsTheStoredSchedule(t *testing.T) {
	fake := newInstallmentsFake()
	res, out := callInstallment(t, fake, tools.RegisterOptions{}, map[string]any{
		"action": "create", "deal_id": 7, "description": "phase 1 ", "amount": 99, "billing_date": "2027-06-30",
	})
	if res.IsError {
		t.Fatalf("create failed: %s", contentText(res))
	}
	if fake.creates != 1 || out.Installment.ID != 101 || len(out.Schedule.Installments) != 3 {
		t.Errorf("creates = %d, installment = %+v, schedule rows = %d; want one create, id 101, three rows", fake.creates, out.Installment, len(out.Schedule.Installments))
	}
	if !hasWarning(out.Schedule, `already had an installment described "phase 1 "`) {
		t.Errorf("warnings = %q, want the duplicate-description warning", out.Schedule.Warnings)
	}
}

func TestManageInstallment_CreateFollowsThePlansProductRule(t *testing.T) {
	cases := []struct {
		name     string
		products []pipedrive.DealProduct
		want     string
	}{
		{"no product", nil, "has no one-time product"},
		{"a recurring product only", []pipedrive.DealProduct{{BillingFrequency: "annually"}}, "has a recurring product (billed annually)"},
		{"a recurring product", []pipedrive.DealProduct{{BillingFrequency: "one-time", IsEnabled: true}, {BillingFrequency: "monthly", IsEnabled: true}}, "has a recurring product (billed monthly)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newInstallmentsFake()
			fake.products[7] = tc.products
			res, _ := callInstallment(t, fake, tools.RegisterOptions{}, map[string]any{
				"action": "create", "deal_id": 7, "description": "Phase 3", "amount": 40, "billing_date": "2026-11-30",
			})
			text := contentText(res)
			if !res.IsError || !strings.HasPrefix(text, "[business_rule]") || !strings.Contains(text, tc.want) {
				t.Errorf("create = %q, want [business_rule] naming %q", text, tc.want)
			}
			if fake.creates != 0 {
				t.Errorf("a refused create was sent")
			}
		})
	}
}

func TestManageInstallment_UpdateNeedsEachReplacedFieldNamed(t *testing.T) {
	fake := newInstallmentsFake()
	args := map[string]any{"action": "update", "deal_id": 7, "installment_id": 1, "amount": 800, "billing_date": "2027-01-15"}

	res, _ := callInstallment(t, fake, tools.RegisterOptions{}, args)
	if text := contentText(res); !res.IsError || !strings.Contains(text, `overwrite: ["amount", "billing_date"]`) {
		t.Fatalf("update without overwrite = %q, want a refusal naming both fields", text)
	}
	args["overwrite"] = []string{"amount"}
	if res, _ = callInstallment(t, fake, tools.RegisterOptions{}, args); !res.IsError || !strings.Contains(contentText(res), `overwrite: ["billing_date"]`) {
		t.Fatalf("update naming one field = %q, want a refusal over the other", contentText(res))
	}
	if fake.updates != 0 {
		t.Fatalf("a refused update was sent")
	}

	args["overwrite"] = []string{"amount", "billing_date"}
	res, out := callInstallment(t, fake, tools.RegisterOptions{}, args)
	if res.IsError {
		t.Fatalf("update with overwrite failed: %s", contentText(res))
	}
	if fake.updates != 1 || fake.lastUpdate.Description != nil {
		t.Errorf("updates = %d, request = %+v; want one update that leaves description out", fake.updates, fake.lastUpdate)
	}
	if !reflect.DeepEqual(out.Changed, []string{"amount", "billing_date"}) || out.Schedule.Sum != 1300 || hasWarning(out.Schedule, "stale") {
		t.Errorf("changed = %v, schedule = %+v; want both fields changed and a schedule that now matches the value", out.Changed, out.Schedule)
	}
}

func TestManageInstallment_ExpectVersionRefusesAMovedInstallment(t *testing.T) {
	fake := newInstallmentsFake()
	var listed struct {
		Deals []scheduleRow `json:"deals"`
	}
	testutil.CallToolInto(t, func(s *mcp.Server) { tools.RegisterInstallments(s, fake, tools.RegisterOptions{}) },
		"list_installments", map[string]any{"deal_ids": []int64{7}}, &listed)
	version := listed.Deals[0].Installments[0].Version
	fake.rows[1].Amount = 802 // someone else edits installment 1

	for _, action := range []string{"update", "delete"} {
		res, _ := callInstallment(t, fake, tools.RegisterOptions{}, map[string]any{
			"action": action, "deal_id": 7, "installment_id": 1, "amount": 900, "overwrite": []string{"amount"},
			"expect_version": version, "confirm": true,
		})
		if !res.IsError || !strings.Contains(contentText(res), "changed since you read it") {
			t.Errorf("%s with a stale version = %q, want the stale refusal", action, contentText(res))
		}
	}
	if fake.updates+fake.deletes != 0 {
		t.Errorf("a stale write was sent")
	}
}

func TestManageInstallment_DeleteIsPermanentSoItNeedsConfirm(t *testing.T) {
	fake := newInstallmentsFake()
	args := map[string]any{"action": "delete", "deal_id": 7, "installment_id": 2}

	res, _ := callInstallment(t, fake, tools.RegisterOptions{}, args)
	if text := contentText(res); !res.IsError || !strings.HasPrefix(text, "[refused]") || !strings.Contains(text, "is permanent") || !strings.Contains(text, "confirm: true") {
		t.Fatalf("delete without confirm = %q, want a refusal saying it is permanent and naming confirm: true", text)
	}

	args["dry_run"] = true
	res, out := callInstallment(t, fake, tools.RegisterOptions{}, args)
	if res.IsError || !out.Deleted || out.Schedule.Sum != 801 || fake.deletes != 0 {
		t.Fatalf("dry-run delete = %+v (%s), deletes sent = %d; want the schedule without it and nothing sent", out, contentText(res), fake.deletes)
	}

	delete(args, "dry_run")
	args["confirm"] = true
	res, out = callInstallment(t, fake, tools.RegisterOptions{}, args)
	if res.IsError || fake.deletes != 1 || out.Installment.ID != 2 || len(out.Schedule.Installments) != 1 {
		t.Errorf("confirmed delete = %+v (%s), deletes = %d; want installment 2 gone", out, contentText(res), fake.deletes)
	}
}

func TestManageInstallment_TheDryRunFloorHoldsForEveryAction(t *testing.T) {
	floor := tools.RegisterOptions{DryRun: true}
	fake := newInstallmentsFake()
	for _, args := range []map[string]any{
		{"action": "create", "deal_id": 7, "description": "Phase 3", "amount": 40, "billing_date": "2026-11-30"},
		{"action": "update", "deal_id": 7, "installment_id": 1, "amount": 800, "overwrite": []string{"amount"}},
		{"action": "delete", "deal_id": 7, "installment_id": 1, "confirm": true},
	} {
		res, out := callInstallment(t, fake, floor, args)
		if res.IsError || !out.DryRun {
			t.Errorf("%s under the floor = %s, want a dry run", args["action"], contentText(res))
		}
	}
	if fake.creates+fake.updates+fake.deletes != 0 {
		t.Errorf("the floor let %d writes through", fake.creates+fake.updates+fake.deletes)
	}
}

func TestManageInstallment_WarnsOnAWonDeal(t *testing.T) {
	res, out := callInstallment(t, newInstallmentsFake(), tools.RegisterOptions{}, map[string]any{
		"action": "update", "deal_id": 8, "installment_id": 3, "billing_date": "2026-10-01", "overwrite": []string{"billing_date"}, "dry_run": true,
	})
	if res.IsError || !hasWarning(out.Schedule, "deal 8 is won") {
		t.Errorf("update on a won deal = %+v (%s), want the won warning", out.Schedule.Warnings, contentText(res))
	}
}

func TestManageInstallment_RefusesWhatPipedriveWould(t *testing.T) {
	cases := []struct {
		name  string
		args  map[string]any
		class string
	}{
		{"an unknown installment", map[string]any{"action": "delete", "deal_id": 7, "installment_id": 3, "confirm": true}, "[not_found]"},
		{"a zero amount", map[string]any{"action": "create", "deal_id": 7, "description": "x", "amount": 0, "billing_date": "2026-11-30"}, "[validation]"},
		{"a date in another format", map[string]any{"action": "create", "deal_id": 7, "description": "x", "amount": 1, "billing_date": "30/11/2026"}, "[validation]"},
		{"a blank description", map[string]any{"action": "create", "deal_id": 7, "description": " ", "amount": 1, "billing_date": "2026-11-30"}, "[validation]"},
		{"an update that changes nothing", map[string]any{"action": "update", "deal_id": 7, "installment_id": 1}, "[validation]"},
		{"a zero deal_id", map[string]any{"action": "create", "deal_id": 0, "description": "x", "amount": 1, "billing_date": "2026-11-30"}, "[validation]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newInstallmentsFake()
			res, _ := callInstallment(t, fake, tools.RegisterOptions{}, tc.args)
			if !res.IsError || !strings.HasPrefix(contentText(res), tc.class) {
				t.Errorf("result = %q, want %s", contentText(res), tc.class)
			}
			if fake.creates+fake.updates+fake.deletes != 0 {
				t.Errorf("a refused write was sent")
			}
		})
	}
}

func TestGetDeal_ScheduleErrorsSayWhatTheClassMeans(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		class     string
		hint      string
		wrongHint string
	}{
		{"a plan refusal names the plan, not permission", &pipedrive.APIError{Class: pipedrive.ErrPlanLimit, Status: 415, Message: "Feature is not enabled"},
			"[plan]", "plan's limits", "lack permission"},
		{"a permission refusal names both", &pipedrive.APIError{Class: pipedrive.ErrForbiddenPermission, Status: 403, Message: "Forbidden"},
			"[permission]", "lack permission", ""},
		{"a closed account gets no plan hint", &pipedrive.APIError{Class: pipedrive.ErrAccountNotOpen, Status: 402, Message: "Company account is not open"},
			"[account]", "Company account is not open", "Growth plan"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeDealsClient{deal: &pipedrive.Deal{ID: 7}, installmentsErr: tc.err}
			var out struct {
				InstallmentsError string `json:"installments_error"`
			}
			testutil.CallToolInto(t, func(s *mcp.Server) {
				tools.RegisterDeals(s, fake, "acme", tools.RegisterOptions{})
			}, "get_deal", map[string]any{"deal_id": 7}, &out)
			got := out.InstallmentsError
			if !strings.HasPrefix(got, tc.class) || !strings.Contains(got, tc.hint) || (tc.wrongHint != "" && strings.Contains(got, tc.wrongHint)) {
				t.Errorf("installments_error = %q; want %s with %q and without %q", got, tc.class, tc.hint, tc.wrongHint)
			}
		})
	}
}

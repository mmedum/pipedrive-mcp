//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mmedum/pipedrive-mcp/internal/credentials"
	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
)

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

type installmentWriteOut struct {
	Installment installmentRow `json:"installment"`
	Changed     []string       `json:"changed"`
	Deleted     bool           `json:"deleted"`
	Schedule    scheduleRow    `json:"schedule"`
}

type dealScheduleOut struct {
	Deal              dealRow      `json:"deal"`
	Installments      *scheduleRow `json:"installments"`
	InstallmentsError string       `json:"installments_error"`
}

// rawV2 sends one request straight to the v2 API, for the two things no
// tool does: attaching a product to the scratch deal, and asking
// Pipedrive itself whether it enforces the product rule. It returns the
// status and the decoded `data`, and prints neither the token nor a body.
func rawV2(t *testing.T, method, path string, body any) (int, json.RawMessage) {
	t.Helper()
	token, _, err := credentials.Resolve(credentials.Default(), liveDomain)
	if err != nil {
		t.Fatalf("no token for the raw request: %v", err)
	}
	var reader io.Reader = http.NoBody
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, "https://"+liveDomain+".pipedrive.com/api/v2"+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("x-api-token", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&env)
	return resp.StatusCode, env.Data
}

// TestInstallments_ReadAcrossDeals reads the schedules of the first
// deals that have one and checks the two reads agree. It changes nothing
// and prints no id, name or amount, since these are real deals.
func TestInstallments_ReadAcrossDeals(t *testing.T) {
	requireLive(t)

	var deals struct {
		Deals []dealRow `json:"deals"`
	}
	mustCall(t, "list_deals", map[string]any{"limit": 100}, &deals)
	ids := make([]int64, 0, len(deals.Deals))
	for _, d := range deals.Deals {
		ids = append(ids, d.ID)
	}
	if len(ids) == 0 {
		t.Skip("the workspace has no deals to read")
	}

	var listed struct {
		Deals []scheduleRow `json:"deals"`
	}
	mustCall(t, "list_installments", map[string]any{"deal_ids": ids}, &listed)
	if len(listed.Deals) != len(ids) {
		t.Fatalf("list_installments answered for %d deals, asked for %d", len(listed.Deals), len(ids))
	}

	checked := 0
	for i, sched := range listed.Deals {
		if len(sched.Installments) == 0 {
			continue
		}
		checked++
		var sum float64
		for j, in := range sched.Installments {
			sum += in.Amount
			if in.Currency != deals.Deals[i].Currency {
				t.Errorf("deal #%d installment #%d is in %q, its deal in %q", i, j, in.Currency, deals.Deals[i].Currency)
			}
			if j > 0 && in.BillingDate < sched.Installments[j-1].BillingDate {
				t.Errorf("deal #%d: installments are not oldest first", i)
			}
		}
		if diff := sum - sched.Sum; diff > 0.005 || diff < -0.005 {
			t.Errorf("deal #%d: sum is %v, its rows add to %v", i, sched.Sum, sum)
		}

		var one dealScheduleOut
		mustCall(t, "get_deal", map[string]any{"deal_id": sched.DealID}, &one)
		if one.Installments == nil || len(one.Installments.Installments) != len(sched.Installments) || one.Installments.Sum != sched.Sum {
			t.Errorf("deal #%d: get_deal and list_installments disagree on the schedule (%s)", i, one.InstallmentsError)
		}
	}
	if checked == 0 {
		t.Skip("none of the first deals has installments to compare")
	}

	// deal_ids is one comma-separated value. The repeated form, which
	// Go's url.Values would write for a slice, is a 400.
	if status, _ := rawV2(t, "GET", "/deals/installments?deal_ids="+strconv.FormatInt(ids[0], 10)+"&deal_ids="+strconv.FormatInt(ids[0], 10), nil); status != http.StatusBadRequest {
		t.Errorf("a repeated deal_ids answered %d, want 400", status)
	}
}

// TestWrite_InstallmentRoundTrip creates, edits and deletes an
// installment on the scratch deal, with a one-time product from the
// catalog attached to that deal for the length of the test. It changes
// no catalog product.
func TestWrite_InstallmentRoundTrip(t *testing.T) {
	sc := newScratch(t)
	deal := sc.DealID
	path := fmt.Sprintf("/deals/%d", deal)

	// Pipedrive's rule, asked of Pipedrive: a deal with no product takes
	// no installment.
	status, data := rawV2(t, "POST", path+"/installments", map[string]any{
		"description": "mcp-test refused", "amount": 1, "billing_date": "2030-01-31",
	})
	if status < 400 {
		var stray struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(data, &stray)
		rawV2(t, "DELETE", fmt.Sprintf("%s/installments/%d", path, stray.ID), nil)
		t.Fatalf("Pipedrive added an installment to a deal with no product (status %d); the product rule in checkInstallmentProducts is wrong", status)
	}
	res := call(t, "manage_installment", map[string]any{
		"action": "create", "deal_id": deal, "description": "mcp-test", "amount": 1, "billing_date": "2030-01-31",
	})
	if errClass(res) != "business_rule" {
		t.Fatalf("create on a deal with no product = %s, want [business_rule]", testutil.TextContent(res))
	}

	productID := oneTimeProduct(t)
	status, data = rawV2(t, "POST", path+"/products", map[string]any{"product_id": productID, "item_price": 100, "quantity": 1})
	if status >= 300 {
		t.Fatalf("attaching a product to the scratch deal answered %d", status)
	}
	var attached attachment
	_ = json.Unmarshal(data, &attached)
	t.Cleanup(func() {
		if status, _ := rawV2(t, "DELETE", fmt.Sprintf("%s/products/%d", path, attached.ID), nil); status >= 300 {
			t.Errorf("LEFT BEHIND: product attachment on deal %d (status %d)", deal, status)
			return
		}
		status, data := rawV2(t, "GET", path+"/products", nil)
		var left []attachment
		_ = json.Unmarshal(data, &left)
		if status != http.StatusOK || slices.Contains(left, attached) {
			t.Errorf("LEFT BEHIND: product attachment on deal %d is still there (status %d)", deal, status)
		}
	})

	var dry installmentWriteOut
	mustCall(t, "manage_installment", map[string]any{
		"action": "create", "deal_id": deal, "description": "mcp-test installment", "amount": 60, "billing_date": "2030-01-31", "dry_run": true,
	}, &dry)
	if dry.Installment.ID != 0 || len(dry.Schedule.Installments) != 1 {
		t.Errorf("dry run = %+v, want a preview with id 0 and one row", dry)
	}
	if got := schedule(t, deal); len(got.Installments) != 0 {
		t.Fatalf("a dry run stored %d installments", len(got.Installments))
	}

	var created installmentWriteOut
	mustCall(t, "manage_installment", map[string]any{
		"action": "create", "deal_id": deal, "description": "mcp-test installment", "amount": 60, "billing_date": "2030-01-31",
	}, &created)
	id := created.Installment.ID
	t.Cleanup(func() {
		if id == 0 {
			return
		}
		if res := call(t, "manage_installment", map[string]any{"action": "delete", "deal_id": deal, "installment_id": id, "confirm": true}); res.IsError {
			t.Errorf("LEFT BEHIND: installment %d on deal %d: %s", id, deal, testutil.TextContent(res))
		}
	})
	if id == 0 || created.Installment.Currency == "" || created.Schedule.Sum != 60 {
		t.Fatalf("create = %+v, want a stored installment with the deal's currency and sum 60", created)
	}
	stale := created.Schedule.DealValue != 60
	if hasStale := slices.ContainsFunc(created.Schedule.Warnings, func(w string) bool { return strings.HasPrefix(w, "stale") }); hasStale != stale {
		t.Errorf("deal value %v, sum 60: stale warning = %v, want %v", created.Schedule.DealValue, hasStale, stale)
	}

	update := map[string]any{"action": "update", "deal_id": deal, "installment_id": id, "amount": 75}
	if res := call(t, "manage_installment", update); errClass(res) != "refused" {
		t.Fatalf("an update without overwrite = %s, want [refused]", testutil.TextContent(res))
	}
	update["overwrite"] = []string{"amount"}
	update["expect_version"] = created.Installment.Version
	var updated installmentWriteOut
	mustCall(t, "manage_installment", update, &updated)
	if updated.Installment.Amount != 75 || updated.Installment.Version == created.Installment.Version {
		t.Errorf("update = %+v, want amount 75 and a new version", updated.Installment)
	}
	if got := schedule(t, deal); len(got.Installments) != 1 || got.Installments[0].Amount != 75 {
		t.Errorf("an independent read sees %+v, want one installment of 75", got.Installments)
	}

	del := map[string]any{"action": "delete", "deal_id": deal, "installment_id": id}
	if res := call(t, "manage_installment", del); errClass(res) != "refused" {
		t.Fatalf("a delete without confirm = %s, want [refused]", testutil.TextContent(res))
	}
	del["confirm"] = true
	mustCall(t, "manage_installment", del, nil)
	gone := id
	id = 0
	if got := schedule(t, deal); len(got.Installments) != 0 {
		t.Errorf("after delete an independent read sees %d installments", len(got.Installments))
	}
	// Not soft: Pipedrive answers the same id as unknown.
	if status, _ := rawV2(t, "DELETE", fmt.Sprintf("%s/installments/%d", path, gone), nil); status != http.StatusNotFound {
		t.Errorf("deleting the deleted installment again answered %d, want 404", status)
	}
}

// attachment is a product attached to a deal, by the id of the attachment.
type attachment struct {
	ID int64 `json:"id"`
}

func schedule(t *testing.T, deal int64) scheduleRow {
	t.Helper()
	var out dealScheduleOut
	mustCall(t, "get_deal", map[string]any{"deal_id": deal}, &out)
	if out.Installments == nil {
		t.Fatalf("get_deal returned no schedule: %s", out.InstallmentsError)
	}
	return *out.Installments
}

// oneTimeProduct finds a catalog product the probe can attach without
// changing it. It skips rather than create one, since a catalog product
// is visible to every deal in the workspace.
func oneTimeProduct(t *testing.T) int64 {
	t.Helper()
	status, data := rawV2(t, "GET", "/products?limit=100", nil)
	if status != http.StatusOK {
		t.Fatalf("listing products answered %d", status)
	}
	var products []struct {
		ID               int64  `json:"id"`
		BillingFrequency string `json:"billing_frequency"`
		IsDeleted        bool   `json:"is_deleted"`
	}
	_ = json.Unmarshal(data, &products)
	for _, p := range products {
		if !p.IsDeleted && (p.BillingFrequency == "" || p.BillingFrequency == "one-time") {
			return p.ID
		}
	}
	t.Skip("the catalog has no one-time product to attach to the scratch deal")
	return 0
}

//go:build integration

package integration

import (
	"fmt"
	"testing"

	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
)

type productOut struct {
	Product struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		IsDeleted bool   `json:"is_deleted"`
		Prices    []struct {
			Price    float64 `json:"price"`
			Currency string  `json:"currency"`
		} `json:"prices"`
		UpdateTime string `json:"update_time"`
	} `json:"product"`
}

type dealLinesOut struct {
	Product struct {
		ID       int64   `json:"id"`
		Quantity float64 `json:"quantity"`
	} `json:"product"`
	Products []struct {
		ID               int64   `json:"id"`
		ProductID        int64   `json:"product_id"`
		Quantity         float64 `json:"quantity"`
		BillingFrequency string  `json:"billing_frequency"`
		IsEnabled        bool    `json:"is_enabled"`
		UpdateTime       string  `json:"update_time"`
	} `json:"products"`
}

// TestWrite_DealProductRoundTrip creates a catalog product of its own,
// puts it on the scratch deal, edits the line and removes it, and checks
// the line is gone from the deal. A second DELETE of the same line
// answers 200, so Pipedrive's answer does not say whether the line was
// there; manage_deal_product reads first for that reason. It deletes the
// product at the end, which Pipedrive purges after 30 days.
func TestWrite_DealProductRoundTrip(t *testing.T) {
	sc := newScratch(t)

	var created productOut
	mustCall(t, "manage_product", map[string]any{
		"action": "create", "name": sc.Title + " product", "billing_frequency": "monthly",
		"prices": []map[string]any{{"price": 10, "currency": "EUR"}, {"price": 75, "currency": "DKK"}},
	}, &created)
	productID := created.Product.ID
	t.Cleanup(func() {
		if res := call(t, "manage_product", map[string]any{"action": "delete", "product_id": productID}); res.IsError {
			t.Errorf("LEFT BEHIND: product %d: %s", productID, testutil.TextContent(res))
		}
	})
	if productID == 0 || len(created.Product.Prices) != 2 {
		t.Fatalf("create = %+v; want a product with its two prices", created.Product)
	}

	// A write's prices replace the stored list whole.
	var repriced productOut
	mustCall(t, "manage_product", map[string]any{
		"action": "update", "product_id": productID, "prices": []map[string]any{{"price": 12, "currency": "EUR"}}, "overwrite": []string{"prices"},
	}, nil)
	mustCall(t, "get_product", map[string]any{"product_id": productID}, &repriced)
	if len(repriced.Product.Prices) != 1 || repriced.Product.Prices[0].Currency != "EUR" {
		t.Errorf("after a write of EUR alone the product has prices %+v", repriced.Product.Prices)
	}
	created.Product.UpdateTime = repriced.Product.UpdateTime

	rename := map[string]any{"action": "update", "product_id": productID, "name": sc.Title + " product renamed"}
	if res := call(t, "manage_product", rename); errClass(res) != "refused" {
		t.Fatalf("a rename without overwrite = %s, want [refused]", testutil.TextContent(res))
	}
	rename["overwrite"] = []string{"name"}
	rename["expect_version"] = created.Product.UpdateTime
	mustCall(t, "manage_product", rename, nil)
	var renamed productOut
	mustCall(t, "get_product", map[string]any{"product_id": productID}, &renamed)
	if renamed.Product.Name != sc.Title+" product renamed" {
		t.Errorf("an independent read sees name %q after the rename", renamed.Product.Name)
	}

	var added dealLinesOut
	mustCall(t, "manage_deal_product", map[string]any{
		"action": "add", "deal_id": sc.DealID, "product_id": productID, "item_price": 10, "quantity": 2,
	}, &added)
	line := added.Product.ID
	t.Cleanup(func() {
		if line == 0 {
			return
		}
		if res := call(t, "manage_deal_product", map[string]any{"action": "remove", "deal_id": sc.DealID, "deal_product_id": line, "confirm": true}); res.IsError {
			t.Errorf("LEFT BEHIND: product line %d on deal %d: %s", line, sc.DealID, testutil.TextContent(res))
		}
	})
	if line == 0 || len(added.Products) != 1 {
		t.Fatalf("add = %+v; want one line on the deal", added)
	}
	// The line does not take the monthly product's frequency.
	if added.Products[0].BillingFrequency != "one-time" || !added.Products[0].IsEnabled {
		t.Errorf("a line added without billing_frequency is %+v; want one-time and switched on", added.Products[0])
	}

	update := map[string]any{"action": "update", "deal_id": sc.DealID, "deal_product_id": line, "quantity": 3}
	if res := call(t, "manage_deal_product", update); errClass(res) != "refused" {
		t.Fatalf("an update without overwrite = %s, want [refused]", testutil.TextContent(res))
	}
	update["overwrite"] = []string{"quantity"}
	update["expect_version"] = added.Products[0].UpdateTime
	var updated dealLinesOut
	mustCall(t, "manage_deal_product", update, &updated)
	if updated.Product.Quantity != 3 {
		t.Errorf("update stored quantity %v, want 3", updated.Product.Quantity)
	}

	remove := map[string]any{"action": "remove", "deal_id": sc.DealID, "deal_product_id": line}
	if res := call(t, "manage_deal_product", remove); errClass(res) != "refused" {
		t.Fatalf("a remove without confirm = %s, want [refused]", testutil.TextContent(res))
	}
	remove["confirm"] = true
	mustCall(t, "manage_deal_product", remove, nil)
	gone := line
	line = 0
	var after dealLinesOut
	mustCall(t, "list_deal_products", map[string]any{"deal_id": sc.DealID}, &after)
	if len(after.Products) != 0 {
		t.Errorf("after remove the deal still lists %d lines", len(after.Products))
	}
	if status, _ := rawV2(t, "DELETE", fmt.Sprintf("/deals/%d/products/%d", sc.DealID, gone), nil); status != 200 {
		t.Errorf("removing the removed line again answered %d; the comment at DeleteDealProduct says 200", status)
	}
	if res := call(t, "manage_deal_product", remove); errClass(res) != "not_found" {
		t.Errorf("manage_deal_product removing the removed line = %s, want [not_found] from its own read", testutil.TextContent(res))
	}
}

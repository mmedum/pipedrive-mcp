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

// fakeProductsClient keeps one store of products and deal lines, so a
// write is visible to the read that follows it.
type fakeProductsClient struct {
	products map[int64]*pipedrive.Product
	deal     *pipedrive.Deal
	lines    []pipedrive.DealProduct
	nextID   int64

	creates, updates, deletes          int
	lineAdds, lineUpdates, lineDeletes int
	lastProductReq                     pipedrive.ProductRequest
	lastLineReq                        pipedrive.DealProductRequest
}

func newProductsFake() *fakeProductsClient {
	return &fakeProductsClient{
		products: map[int64]*pipedrive.Product{
			5: {ID: 5, Name: "Annual license", Unit: "seat", OwnerID: 3, BillingFrequency: "one-time", UpdateTime: "2026-01-01T00:00:00Z",
				Prices: []pipedrive.ProductPrice{{Price: 100, Currency: "EUR"}, {Price: 750, Currency: "DKK"}}},
		},
		deal:   &pipedrive.Deal{ID: 7, Value: 200, Currency: "EUR"},
		lines:  []pipedrive.DealProduct{{ID: 11, DealID: 7, ProductID: 5, Name: "Annual license", ItemPrice: 100, Quantity: 2, Sum: 200, IsEnabled: true, BillingFrequency: "one-time", UpdateTime: "2026-01-02T00:00:00Z"}},
		nextID: 100,
	}
}

func (f *fakeProductsClient) GetProduct(_ context.Context, id int64) (*pipedrive.Product, error) {
	p, ok := f.products[id]
	if !ok {
		return nil, &pipedrive.APIError{Class: pipedrive.ErrNotFound, Status: 404, Message: "Product not found"}
	}
	out := *p
	return &out, nil
}

func (f *fakeProductsClient) ListProducts(context.Context, pipedrive.ListProductsOptions) ([]pipedrive.Product, string, error) {
	out := make([]pipedrive.Product, 0, len(f.products))
	for _, p := range f.products {
		out = append(out, *p)
	}
	return out, "", nil
}

func (f *fakeProductsClient) CreateProduct(_ context.Context, req pipedrive.ProductRequest) (*pipedrive.Product, error) {
	f.creates++
	f.lastProductReq = req
	f.nextID++
	p := &pipedrive.Product{ID: f.nextID, Name: *req.Name, Prices: req.Prices}
	f.products[p.ID] = p
	return p, nil
}

func (f *fakeProductsClient) UpdateProduct(_ context.Context, id int64, req pipedrive.ProductRequest) (*pipedrive.Product, error) {
	f.updates++
	f.lastProductReq = req
	p := f.products[id]
	if req.Name != nil {
		p.Name = *req.Name
	}
	if req.Prices != nil {
		p.Prices = req.Prices
	}
	out := *p
	return &out, nil
}

func (f *fakeProductsClient) DeleteProduct(context.Context, int64) error {
	f.deletes++
	return nil
}

func (f *fakeProductsClient) ResolveProductCustomFields(_ context.Context, raw map[string]any) map[string]any {
	return raw
}

func (f *fakeProductsClient) EncodeProductCustomFields(_ context.Context, in map[string]any) (pipedrive.CustomFieldWrite, error) {
	return fakeEncode(in, nil)
}

func (f *fakeProductsClient) GetDeal(context.Context, int64) (*pipedrive.Deal, error) {
	return f.deal, nil
}

func (f *fakeProductsClient) ListDealProducts(context.Context, int64) ([]pipedrive.DealProduct, error) {
	return slices.Clone(f.lines), nil
}

func (f *fakeProductsClient) AddDealProduct(_ context.Context, dealID int64, req pipedrive.DealProductRequest) (*pipedrive.DealProduct, error) {
	f.lineAdds++
	f.lastLineReq = req
	f.nextID++
	l := pipedrive.DealProduct{ID: f.nextID, DealID: dealID, ProductID: *req.ProductID, ItemPrice: *req.ItemPrice, Quantity: *req.Quantity, IsEnabled: true}
	f.lines = append(f.lines, l)
	return &l, nil
}

func (f *fakeProductsClient) UpdateDealProduct(_ context.Context, _, id int64, req pipedrive.DealProductRequest) (*pipedrive.DealProduct, error) {
	f.lineUpdates++
	f.lastLineReq = req
	for i := range f.lines {
		if f.lines[i].ID == id {
			if req.Quantity != nil {
				f.lines[i].Quantity = *req.Quantity
			}
			out := f.lines[i]
			return &out, nil
		}
	}
	return nil, &pipedrive.APIError{Class: pipedrive.ErrNotFound, Status: 404}
}

func (f *fakeProductsClient) DeleteDealProduct(_ context.Context, _, id int64) error {
	f.lineDeletes++
	f.lines = slices.DeleteFunc(f.lines, func(l pipedrive.DealProduct) bool { return l.ID == id })
	return nil
}

func (f *fakeProductsClient) writes() int {
	return f.creates + f.updates + f.deletes + f.lineAdds + f.lineUpdates + f.lineDeletes
}

type productResult struct {
	Product struct {
		ID     int64  `json:"id"`
		Name   string `json:"name"`
		Prices []struct {
			Price    float64 `json:"price"`
			Currency string  `json:"currency"`
		} `json:"prices"`
	} `json:"product"`
	Changed []string `json:"changed"`
	DryRun  bool     `json:"dry_run"`
}

func callProducts(t *testing.T, f *fakeProductsClient, opts tools.RegisterOptions, tool string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	return testutil.CallToolInto(t, func(s *mcp.Server) { tools.RegisterProducts(s, f, opts) }, tool, args, out)
}

func TestManageProduct_DryRunCreateSendsNothing(t *testing.T) {
	fake := newProductsFake()
	var out productResult
	res := callProducts(t, fake, tools.RegisterOptions{}, "manage_product", map[string]any{
		"action": "create", "name": "Support hours", "unit": "hour", "prices": []map[string]any{{"price": 90, "currency": "EUR"}}, "dry_run": true,
	}, &out)
	if res.IsError || fake.writes() != 0 {
		t.Fatalf("dry-run create = %s, writes = %d; want a preview and nothing sent", contentText(res), fake.writes())
	}
	if out.Product.ID != 0 || out.Product.Name != "Support hours" || !slices.Contains(out.Changed, "prices") || !out.DryRun {
		t.Errorf("preview = %+v; want id 0, the name, and prices among changed", out)
	}
}

func TestManageProduct_UpdateNeedsEachReplacedFieldNamed(t *testing.T) {
	fake := newProductsFake()
	args := map[string]any{"action": "update", "product_id": 5, "prices": []map[string]any{{"price": 120, "currency": "EUR"}}}
	res := callProducts(t, fake, tools.RegisterOptions{}, "manage_product", args, nil)
	if text := contentText(res); !res.IsError || !strings.Contains(text, `overwrite: ["prices"]`) {
		t.Fatalf("a price change without overwrite = %q; want a refusal naming prices", text)
	}
	if fake.updates != 0 {
		t.Fatal("a refused update was sent")
	}
	args["overwrite"] = []string{"prices"}
	var out productResult
	if res = callProducts(t, fake, tools.RegisterOptions{}, "manage_product", args, &out); res.IsError {
		t.Fatalf("update with overwrite = %s", contentText(res))
	}
	// The list is replaced whole, as the description says: the DKK price
	// a caller leaves out goes.
	if fake.updates != 1 || len(fake.lastProductReq.Prices) != 1 || len(out.Product.Prices) != 1 {
		t.Errorf("updates = %d, sent %+v, stored %+v; want one price sent and stored", fake.updates, fake.lastProductReq.Prices, out.Product.Prices)
	}
}

func TestManageProduct_DeleteIsSoftAndHonorsDryRun(t *testing.T) {
	fake := newProductsFake()
	var out productResult
	res := callProducts(t, fake, tools.RegisterOptions{}, "manage_product", map[string]any{"action": "delete", "product_id": 5, "dry_run": true}, &out)
	if res.IsError || fake.deletes != 0 || !slices.Equal(out.Changed, []string{"is_deleted"}) {
		t.Fatalf("dry-run delete = %s, %+v, deletes = %d", contentText(res), out, fake.deletes)
	}
	if res = callProducts(t, fake, tools.RegisterOptions{}, "manage_product", map[string]any{"action": "delete", "product_id": 5}, nil); res.IsError || fake.deletes != 1 {
		t.Errorf("delete = %s, deletes = %d; want one soft delete with no confirm", contentText(res), fake.deletes)
	}
}

func TestManageProduct_RefusesBadInput(t *testing.T) {
	for _, args := range []map[string]any{
		{"action": "create", "name": " "},
		{"action": "update", "product_id": 0, "name": "x"},
		{"action": "update", "product_id": 5, "billing_frequency": "weekly"},
	} {
		fake := newProductsFake()
		res := callProducts(t, fake, tools.RegisterOptions{}, "manage_product", args, nil)
		if !res.IsError || !strings.HasPrefix(contentText(res), "[validation]") || fake.writes() != 0 {
			t.Errorf("%v = %q; want [validation] and nothing sent", args, contentText(res))
		}
	}
}

type dealProductsResult struct {
	Product struct {
		ID       int64   `json:"id"`
		Quantity float64 `json:"quantity"`
	} `json:"product"`
	Changed  []string `json:"changed"`
	Removed  bool     `json:"removed"`
	DryRun   bool     `json:"dry_run"`
	Products []struct {
		ID int64 `json:"id"`
	} `json:"products"`
}

func TestManageDealProduct_AddPreviewsThenAttaches(t *testing.T) {
	fake := newProductsFake()
	args := map[string]any{"action": "add", "deal_id": 7, "product_id": 5, "item_price": 100, "quantity": 1, "dry_run": true}
	var out dealProductsResult
	if res := callProducts(t, fake, tools.RegisterOptions{}, "manage_deal_product", args, &out); res.IsError || fake.lineAdds != 0 || len(out.Products) != 2 {
		t.Fatalf("dry-run add = %s, %+v, adds = %d; want a preview of two lines and nothing sent", contentText(res), out, fake.lineAdds)
	}
	delete(args, "dry_run")
	if res := callProducts(t, fake, tools.RegisterOptions{}, "manage_deal_product", args, &out); res.IsError || fake.lineAdds != 1 || out.Product.ID == 0 || len(out.Products) != 2 {
		t.Errorf("add = %s, %+v; want one line added and the lines read back", contentText(res), out)
	}
}

func TestManageDealProduct_RemoveIsPermanentSoItNeedsConfirm(t *testing.T) {
	fake := newProductsFake()
	args := map[string]any{"action": "remove", "deal_id": 7, "deal_product_id": 11}
	res := callProducts(t, fake, tools.RegisterOptions{}, "manage_deal_product", args, nil)
	if text := contentText(res); !res.IsError || !strings.Contains(text, "is permanent") || !strings.Contains(text, "confirm: true") {
		t.Fatalf("remove without confirm = %q; want a refusal naming confirm: true", text)
	}
	args["dry_run"] = true
	var out dealProductsResult
	if res = callProducts(t, fake, tools.RegisterOptions{}, "manage_deal_product", args, &out); res.IsError || !out.Removed || len(out.Products) != 0 || fake.lineDeletes != 0 {
		t.Fatalf("dry-run remove = %s, %+v; want the line gone from the preview and nothing sent", contentText(res), out)
	}
	delete(args, "dry_run")
	args["confirm"] = true
	if res = callProducts(t, fake, tools.RegisterOptions{}, "manage_deal_product", args, &out); res.IsError || fake.lineDeletes != 1 {
		t.Errorf("confirmed remove = %s, deletes = %d", contentText(res), fake.lineDeletes)
	}
}

func TestManageDealProduct_UpdateNeedsOverwriteAndAFreshVersion(t *testing.T) {
	fake := newProductsFake()
	args := map[string]any{"action": "update", "deal_id": 7, "deal_product_id": 11, "quantity": 3}
	if res := callProducts(t, fake, tools.RegisterOptions{}, "manage_deal_product", args, nil); !strings.Contains(contentText(res), `overwrite: ["quantity"]`) {
		t.Fatalf("update without overwrite = %q", contentText(res))
	}
	args["overwrite"] = []string{"quantity"}
	args["expect_version"] = "2025-12-31T00:00:00Z"
	if res := callProducts(t, fake, tools.RegisterOptions{}, "manage_deal_product", args, nil); !strings.Contains(contentText(res), "changed since you read it") {
		t.Fatalf("update with a stale version = %q", contentText(res))
	}
	args["expect_version"] = "2026-01-02T00:00:00Z"
	var out dealProductsResult
	if res := callProducts(t, fake, tools.RegisterOptions{}, "manage_deal_product", args, &out); res.IsError || fake.lineUpdates != 1 || !reflect.DeepEqual(out.Changed, []string{"quantity"}) {
		t.Errorf("update = %s, %+v; want quantity changed", contentText(res), out)
	}
	if fake.lastLineReq.ItemPrice != nil {
		t.Errorf("an update of quantity sent item_price %v", *fake.lastLineReq.ItemPrice)
	}
}

func TestProductWrites_HonorTheDryRunFloor(t *testing.T) {
	fake := newProductsFake()
	floor := tools.RegisterOptions{DryRun: true}
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"manage_product", map[string]any{"action": "create", "name": "X"}},
		{"manage_product", map[string]any{"action": "delete", "product_id": 5}},
		{"manage_deal_product", map[string]any{"action": "add", "deal_id": 7, "product_id": 5, "item_price": 1, "quantity": 1}},
		{"manage_deal_product", map[string]any{"action": "remove", "deal_id": 7, "deal_product_id": 11}},
	} {
		if res := callProducts(t, fake, floor, c.tool, c.args, nil); res.IsError {
			t.Errorf("%s %v under the floor = %s", c.tool, c.args["action"], contentText(res))
		}
	}
	if fake.writes() != 0 {
		t.Errorf("the floor let %d writes through", fake.writes())
	}
}

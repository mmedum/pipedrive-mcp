package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

type productsClient interface {
	GetProduct(ctx context.Context, id int64) (*pipedrive.Product, error)
	ListProducts(ctx context.Context, opts pipedrive.ListProductsOptions) ([]pipedrive.Product, string, error)
	CreateProduct(ctx context.Context, req pipedrive.ProductRequest) (*pipedrive.Product, error)
	UpdateProduct(ctx context.Context, id int64, req pipedrive.ProductRequest) (*pipedrive.Product, error)
	DeleteProduct(ctx context.Context, id int64) error
	ResolveProductCustomFields(ctx context.Context, raw map[string]any) map[string]any
	EncodeProductCustomFields(ctx context.Context, in map[string]any) (pipedrive.CustomFieldWrite, error)
	GetDeal(ctx context.Context, id int64) (*pipedrive.Deal, error)
	ListDealProducts(ctx context.Context, dealID int64) ([]pipedrive.DealProduct, error)
	AddDealProduct(ctx context.Context, dealID int64, req pipedrive.DealProductRequest) (*pipedrive.DealProduct, error)
	UpdateDealProduct(ctx context.Context, dealID, id int64, req pipedrive.DealProductRequest) (*pipedrive.DealProduct, error)
	DeleteDealProduct(ctx context.Context, dealID, id int64) error
}

var allowedProductSortFields = map[string]bool{"id": true, "name": true, "add_time": true, "update_time": true}

var allowedBillingFrequencies = map[string]bool{
	"one-time": true, "annually": true, "semi-annually": true, "quarterly": true, "monthly": true,
}

type priceRow struct {
	Price      float64 `json:"price" jsonschema:"the unit price in currency"`
	Currency   string  `json:"currency" jsonschema:"ISO 4217 code such as EUR or DKK"`
	Cost       float64 `json:"cost,omitempty" jsonschema:"the unit cost in currency"`
	DirectCost float64 `json:"direct_cost,omitempty" jsonschema:"the unit's direct cost in currency"`
	Notes      string  `json:"notes,omitempty" jsonschema:"free-text notes on this price"`
}

type productSummary struct {
	ID                     int64          `json:"id" jsonschema:"the product's numeric id; 0 on a dry-run create"`
	Name                   string         `json:"name" jsonschema:"the product's name"`
	Code                   string         `json:"code,omitempty" jsonschema:"the product's code, such as a SKU"`
	Description            string         `json:"description,omitempty" jsonschema:"the product's description"`
	Unit                   string         `json:"unit,omitempty" jsonschema:"the unit it is sold in, such as hour or seat"`
	Tax                    float64        `json:"tax,omitempty" jsonschema:"tax percentage"`
	OwnerID                int64          `json:"owner_id" jsonschema:"id of the user who owns the product"`
	BillingFrequency       string         `json:"billing_frequency,omitempty" jsonschema:"one-time | annually | semi-annually | quarterly | monthly; a deal with a recurring product takes no installments"`
	BillingFrequencyCycles *int           `json:"billing_frequency_cycles,omitempty" jsonschema:"how many times a recurring product bills; absent when it bills until stopped"`
	Prices                 []priceRow     `json:"prices,omitempty" jsonschema:"one price per currency"`
	IsDeleted              bool           `json:"is_deleted,omitempty" jsonschema:"true once the product is deleted; Pipedrive removes it for good after 30 days"`
	AddTime                string         `json:"add_time,omitempty" jsonschema:"timestamp the product was created"`
	UpdateTime             string         `json:"update_time,omitempty" jsonschema:"timestamp the product was last updated; expect_version takes it"`
	CustomFields           map[string]any `json:"custom_fields,omitempty" jsonschema:"custom fields keyed by human-readable name, a dropdown's value as its label, followed by (id N) where another option's label matches it ignoring case; an unrecognized field or option falls through under its stored key"`
}

type getProductInput struct {
	ProductID int64 `json:"product_id" jsonschema:"the product's numeric id"`
}

type getProductOutput struct {
	Product productSummary `json:"product" jsonschema:"the requested product"`
}

type listProductsInput struct {
	OwnerID       int64   `json:"owner_id,omitempty" jsonschema:"return only products owned by this user id; 0 = no filter"`
	IDs           []int64 `json:"ids,omitempty" jsonschema:"return only these products, at most 100"`
	UpdatedSince  string  `json:"updated_since,omitempty" jsonschema:"RFC3339 timestamp; return only products updated at or after this time"`
	SortBy        string  `json:"sort_by,omitempty" jsonschema:"id | name | add_time | update_time. Default 'update_time' (most-recently-touched first)."`
	SortDirection string  `json:"sort_direction,omitempty" jsonschema:"asc | desc. Default 'desc' when sort_by is omitted; 'asc' otherwise."`
	Limit         int     `json:"limit,omitempty" jsonschema:"page size; default 25, max 100"`
	Cursor        string  `json:"cursor,omitempty" jsonschema:"opaque pagination token from a previous list_products response; omit for the first page"`
}

type listProductsOutput struct {
	Products   []productSummary `json:"products" jsonschema:"matching products on this page"`
	NextCursor string           `json:"next_cursor,omitempty" jsonschema:"pass to the next list_products call to get the next page; empty when there are no more pages"`
}

type manageProductInput struct {
	Action                 string         `json:"action" jsonschema:"create, update or delete"`
	ProductID              int64          `json:"product_id,omitempty" jsonschema:"the product to act on, required by update and delete and ignored by create"`
	Name                   *string        `json:"name,omitempty" jsonschema:"the product's name, required by create; if the user did not give one, ask rather than inventing it"`
	Code                   *string        `json:"code,omitempty" jsonschema:"the product's code, such as a SKU. Omit to leave it as it is"`
	Description            *string        `json:"description,omitempty" jsonschema:"the product's description. Omit to leave it as it is"`
	Unit                   *string        `json:"unit,omitempty" jsonschema:"the unit it is sold in, such as hour or seat. Omit to leave it as it is"`
	Tax                    *float64       `json:"tax,omitempty" jsonschema:"tax percentage. Omit to leave it as it is"`
	OwnerID                *int64         `json:"owner_id,omitempty" jsonschema:"the user who owns the product; omit on create to take the API token's own user"`
	BillingFrequency       *string        `json:"billing_frequency,omitempty" jsonschema:"one-time | annually | semi-annually | quarterly | monthly. Omit to leave it as it is"`
	BillingFrequencyCycles *int           `json:"billing_frequency_cycles,omitempty" jsonschema:"how many times a recurring product bills. Omit to leave it as it is"`
	Prices                 []priceRow     `json:"prices,omitempty" jsonschema:"the product's prices, one per currency. A write REPLACES the whole list, so send every price the product should keep, not only the one you change; omit to leave the prices as they are"`
	CustomFields           map[string]any `json:"custom_fields,omitempty" jsonschema:"this workspace's own product fields, keyed by the name get_product reports — a dropdown takes its label as get_product reports it, a multi-select a list of them, everything else the plain value. Omit a field to leave it as it is; a field cannot be cleared"`
	DryRun                 bool           `json:"dry_run,omitempty" jsonschema:"report what the write would find and change, and send nothing"`
	Overwrite              []string       `json:"overwrite,omitempty" jsonschema:"the fields this write may replace, named exactly as the refusal listed them, e.g. [\"name\", \"prices\"]. Omit it and a write that would replace a populated field is refused, naming each one. A refusal is NOT a retry signal: name a field only when the user asked for what is already there to be replaced, never to get past a refusal they have not seen"`
	ExpectVersion          string         `json:"expect_version,omitempty" jsonschema:"the update_time from the read that informed this write; the write is refused if the product changed since"`
}

type manageProductOutput struct {
	Action  string         `json:"action" jsonschema:"the action that ran"`
	Product productSummary `json:"product" jsonschema:"the product as Pipedrive stored it. For delete, as it was just before. When dry_run is true nothing was persisted and a created product carries id=0"`
	Changed []string       `json:"changed,omitempty" jsonschema:"names of the fields this write altered, empty when it was a no-op. On a dry run, the fields it would alter"`
	DryRun  bool           `json:"dry_run,omitempty" jsonschema:"true when nothing was sent upstream"`
}

// RegisterProducts wires the product catalog tools and the tools for the
// products attached to a deal. manage_product and manage_deal_product
// register unconditionally and guard at call time (D-46).
func RegisterProducts(s *mcp.Server, c productsClient, opts RegisterOptions) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_product",
		Description: "Fetch one product from the workspace's catalog by product_id: name, code, description, unit, tax, owner_id, billing frequency, its prices per currency, and custom fields under their workspace names. Unknown product_id returns a [not_found] error. Use search with type=product to find a product by name.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getProductInput) (*mcp.CallToolResult, getProductOutput, error) {
		if err := validatePositiveID(in.ProductID, "product_id"); err != nil {
			return errorResult(err), getProductOutput{}, nil
		}
		p, err := c.GetProduct(ctx, in.ProductID)
		if err != nil {
			return errorResult(err), getProductOutput{}, nil
		}
		return nil, getProductOutput{Product: resolvedProduct(ctx, c, p)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_products",
		Description: "List the workspace's product catalog, filtered by owner, ids or update window. Returns each product's name, code, unit, tax, billing frequency, prices per currency and custom fields. Default sort is update_time desc. Default limit is 25, max 100. IMPORTANT: a page holding fewer rows than the limit is NOT the end — keep paging while next_cursor comes back non-empty. To find a product by name, use search with type=product; list_deal_products shows the products on one deal.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listProductsInput) (*mcp.CallToolResult, listProductsOutput, error) {
		if err := validateEnum(in.SortBy, "sort_by", allowedProductSortFields); err != nil {
			return errorResult(err), listProductsOutput{}, nil
		}
		if err := validateEnum(in.SortDirection, "sort_direction", allowedSortDirections); err != nil {
			return errorResult(err), listProductsOutput{}, nil
		}
		if len(in.IDs) > maxListLimit {
			return errorResult(fmt.Errorf("%w: ids names %d products; at most %d", pipedrive.ErrValidation, len(in.IDs), maxListLimit)), listProductsOutput{}, nil
		}
		sortBy, sortDir := effectiveSort(in.SortBy, in.SortDirection)
		products, next, err := c.ListProducts(ctx, pipedrive.ListProductsOptions{
			OwnerID: in.OwnerID, IDs: in.IDs, UpdatedSince: in.UpdatedSince,
			SortBy: sortBy, SortDirection: sortDir, Limit: clampLimit(in.Limit), Cursor: in.Cursor,
		})
		if err != nil {
			return errorResult(err), listProductsOutput{}, nil
		}
		out := listProductsOutput{Products: make([]productSummary, 0, len(products)), NextCursor: next}
		for i := range products {
			out.Products = append(out.Products, resolvedProduct(ctx, c, &products[i]))
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "manage_product",
		Description: "Create a product in the workspace's catalog, edit one, or delete one. create takes name; update and delete take product_id. Writing is guarded: update reads the product first and refuses to replace ANY field that already holds a value unless you name it in overwrite, and the refusal names each one; filling an empty field needs no permission. expect_version refuses the write if the product moved. IMPORTANT: prices is one list, one price per currency, and a write REPLACES it whole, so send every price the product keeps. A product's billing_frequency decides whether a deal it is on can take installments: a recurring product rules them out. Deleting is soft: Pipedrive marks the product deleted and removes it for good after 30 days, so delete takes dry_run and expect_version and no permitting flag beyond them, but NOTHING HERE PUTS IT BACK. To put a product on a deal, use manage_deal_product.",
		Annotations: mutatingAnnotations(),
	}, manageProductHandler(c, opts.DryRun))

	registerDealProducts(s, c, opts)
}

var allowedProductActions = map[string]bool{"create": true, "update": true, "delete": true}

// productBaseFields drives the product update's diff and overwrite guard
// (D-38); custom fields are added per call.
var productBaseFields = []fieldSpec[pipedrive.Product]{
	{"name", func(p *pipedrive.Product) string { return p.Name }},
	{"code", func(p *pipedrive.Product) string { return p.Code }},
	{"description", func(p *pipedrive.Product) string { return p.Description }},
	{"unit", func(p *pipedrive.Product) string { return p.Unit }},
	{"tax", func(p *pipedrive.Product) string { return projectMoney(p.Tax) }},
	{"owner_id", func(p *pipedrive.Product) string { return projectID(p.OwnerID) }},
	{"billing_frequency", func(p *pipedrive.Product) string { return p.BillingFrequency }},
	{"billing_frequency_cycles", func(p *pipedrive.Product) string { return projectOptInt(p.BillingFrequencyCycles) }},
	{"prices", func(p *pipedrive.Product) string { return projectPrices(p.Prices) }},
}

// projectPrices covers every field of a price a write can set (D-39),
// quoted so two different lists cannot render alike.
func projectPrices(ps []pipedrive.ProductPrice) string {
	return projectCollection(ps, func(p pipedrive.ProductPrice) string {
		return strconv.Quote(p.Currency) + projectMoney(p.Price) + "|" + projectMoney(p.Cost) + "|" + projectMoney(p.DirectCost) + "|" + strconv.Quote(p.Notes)
	})
}

func projectOptInt(p *int) string {
	if p == nil || *p == 0 {
		return ""
	}
	return strconv.Itoa(*p)
}

func manageProductHandler(c productsClient, dryRun bool) mcp.ToolHandlerFor[manageProductInput, manageProductOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in manageProductInput) (*mcp.CallToolResult, manageProductOutput, error) {
		if err := validateAction(in.Action, allowedProductActions); err != nil {
			return errorResult(err), manageProductOutput{}, nil
		}
		in.DryRun = in.DryRun || dryRun
		if in.BillingFrequency != nil {
			if err := validateEnum(*in.BillingFrequency, "billing_frequency", allowedBillingFrequencies); err != nil {
				return errorResult(err), manageProductOutput{}, nil
			}
		}
		var (
			res *mcp.CallToolResult
			out manageProductOutput
		)
		switch in.Action {
		case "create":
			res, out = createProductAction(ctx, c, in)
		case "update":
			res, out = updateProductAction(ctx, c, in)
		case "delete":
			res, out = deleteProductAction(ctx, c, in)
		default:
			res = errorResult(fmt.Errorf("%w: action %q has no handler", pipedrive.ErrValidation, in.Action))
		}
		if res == nil {
			out.Action = in.Action
			out.DryRun = in.DryRun
		}
		return res, out, nil
	}
}

func productRequestFor(in manageProductInput, cf pipedrive.CustomFieldWrite) pipedrive.ProductRequest {
	req := pipedrive.ProductRequest{
		Name: in.Name, Code: in.Code, Description: in.Description, Unit: in.Unit, Tax: in.Tax,
		OwnerID: in.OwnerID, BillingFrequency: in.BillingFrequency, BillingFrequencyCycles: in.BillingFrequencyCycles,
		CustomFields: cf.Values,
	}
	for _, p := range in.Prices {
		req.Prices = append(req.Prices, pipedrive.ProductPrice(p))
	}
	return req
}

func createProductAction(ctx context.Context, c productsClient, in manageProductInput) (*mcp.CallToolResult, manageProductOutput) {
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
		return errorResult(fmt.Errorf("%w: name must not be empty", pipedrive.ErrValidation)), manageProductOutput{}
	}
	cf, err := c.EncodeProductCustomFields(ctx, in.CustomFields)
	if err != nil {
		return errorResult(err), manageProductOutput{}
	}
	req := productRequestFor(in, cf)
	created := productAfterUpdate(pipedrive.Product{}, req)
	result := &created
	if !in.DryRun {
		if result, err = c.CreateProduct(ctx, req); err != nil {
			return errorResult(err), manageProductOutput{}
		}
	}
	return nil, manageProductOutput{
		Product: resolvedProduct(ctx, c, result),
		Changed: changedFields(productSpec(cf), &pipedrive.Product{}, result),
	}
}

func updateProductAction(ctx context.Context, c productsClient, in manageProductInput) (*mcp.CallToolResult, manageProductOutput) {
	if err := validatePositiveID(in.ProductID, "product_id"); err != nil {
		return errorResult(err), manageProductOutput{}
	}
	cf, err := c.EncodeProductCustomFields(ctx, in.CustomFields)
	if err != nil {
		return errorResult(err), manageProductOutput{}
	}
	req := productRequestFor(in, cf)
	p, changed, res := guardedWrite[pipedrive.Product]{
		Spec:          productBaseFields,
		Custom:        cf,
		CustomOf:      func(p *pipedrive.Product) *map[string]any { return &p.CustomFields },
		Resource:      fmt.Sprintf("product %d", in.ProductID),
		ExpectVersion: in.ExpectVersion,
		Version:       func(p *pipedrive.Product) string { return p.UpdateTime },
		Overwrite:     in.Overwrite,
		DryRun:        in.DryRun,
		Get:           func(ctx context.Context) (*pipedrive.Product, error) { return c.GetProduct(ctx, in.ProductID) },
		Predict:       func(p *pipedrive.Product) pipedrive.Product { return productAfterUpdate(*p, req) },
		Put:           func(ctx context.Context) (*pipedrive.Product, error) { return c.UpdateProduct(ctx, in.ProductID, req) },
	}.run(ctx)
	if res != nil {
		return res, manageProductOutput{}
	}
	return nil, manageProductOutput{Product: resolvedProduct(ctx, c, p), Changed: changed}
}

// deleteProductAction marks a product deleted: soft and time-boxed, the
// reversible case, so dry_run and expect_version and nothing else (D-47).
func deleteProductAction(ctx context.Context, c productsClient, in manageProductInput) (*mcp.CallToolResult, manageProductOutput) {
	if err := validatePositiveID(in.ProductID, "product_id"); err != nil {
		return errorResult(err), manageProductOutput{}
	}
	before, err := c.GetProduct(ctx, in.ProductID)
	if err != nil {
		return errorResult(err), manageProductOutput{}
	}
	if err := checkExpectVersion(in.ExpectVersion, before.UpdateTime, fmt.Sprintf("product %d", in.ProductID)); err != nil {
		return errorResult(err), manageProductOutput{}
	}
	if before.IsDeleted {
		return nil, manageProductOutput{Product: resolvedProduct(ctx, c, before)}
	}
	if !in.DryRun {
		if err := c.DeleteProduct(ctx, in.ProductID); err != nil {
			return errorResult(err), manageProductOutput{}
		}
	}
	return nil, manageProductOutput{Product: resolvedProduct(ctx, c, before), Changed: []string{"is_deleted"}}
}

func productSpec(cf pipedrive.CustomFieldWrite) []fieldSpec[pipedrive.Product] {
	return withCustomFields(productBaseFields, cf, func(p *pipedrive.Product) map[string]any { return p.CustomFields })
}

// productAfterUpdate overlays a request on a product. A request's prices
// replace the stored list whole, as Pipedrive replaces it.
func productAfterUpdate(before pipedrive.Product, req pipedrive.ProductRequest) pipedrive.Product {
	after := before
	setIf(&after.Name, req.Name)
	setIf(&after.Code, req.Code)
	setIf(&after.Description, req.Description)
	setIf(&after.Unit, req.Unit)
	setIf(&after.Tax, req.Tax)
	setIf(&after.OwnerID, req.OwnerID)
	setIf(&after.BillingFrequency, req.BillingFrequency)
	if req.BillingFrequencyCycles != nil {
		after.BillingFrequencyCycles = clone(req.BillingFrequencyCycles)
	}
	if req.Prices != nil {
		after.Prices = append([]pipedrive.ProductPrice(nil), req.Prices...)
	}
	if req.CustomFields != nil && after.CustomFields == nil {
		after.CustomFields = req.CustomFields
	}
	return after
}

type productFieldResolver interface {
	ResolveProductCustomFields(ctx context.Context, raw map[string]any) map[string]any
}

func resolvedProduct(ctx context.Context, c productFieldResolver, p *pipedrive.Product) productSummary {
	out := productSummary{
		ID: p.ID, Name: p.Name, Code: p.Code, Description: p.Description, Unit: p.Unit, Tax: p.Tax,
		OwnerID: p.OwnerID, BillingFrequency: p.BillingFrequency, BillingFrequencyCycles: clone(p.BillingFrequencyCycles),
		IsDeleted: p.IsDeleted, AddTime: p.AddTime, UpdateTime: p.UpdateTime,
		CustomFields: c.ResolveProductCustomFields(ctx, p.CustomFields),
	}
	for _, pr := range p.Prices {
		out.Prices = append(out.Prices, priceRow(pr))
	}
	return out
}

// The products on one deal.

type dealProductRow struct {
	ID                     int64   `json:"id" jsonschema:"the attachment's id, which manage_deal_product takes as deal_product_id; 0 on a dry-run add"`
	ProductID              int64   `json:"product_id" jsonschema:"the catalog product attached"`
	Name                   string  `json:"name,omitempty" jsonschema:"the product's name on the deal"`
	ItemPrice              float64 `json:"item_price" jsonschema:"the unit price on this deal, in its currency"`
	Quantity               float64 `json:"quantity" jsonschema:"how many units"`
	Discount               float64 `json:"discount,omitempty" jsonschema:"the discount, a percentage or an amount as discount_type says"`
	DiscountType           string  `json:"discount_type,omitempty" jsonschema:"percentage | amount"`
	Tax                    float64 `json:"tax,omitempty" jsonschema:"tax percentage"`
	Sum                    float64 `json:"sum" jsonschema:"the line's total as Pipedrive computes it"`
	Currency               string  `json:"currency,omitempty" jsonschema:"the deal's currency"`
	Comments               string  `json:"comments,omitempty" jsonschema:"free-text comments on the line"`
	BillingFrequency       string  `json:"billing_frequency,omitempty" jsonschema:"one-time | annually | semi-annually | quarterly | monthly; a recurring line rules out installments"`
	BillingFrequencyCycles *int    `json:"billing_frequency_cycles,omitempty" jsonschema:"how many times a recurring line bills"`
	BillingStartDate       string  `json:"billing_start_date,omitempty" jsonschema:"YYYY-MM-DD a recurring line starts billing"`
	IsEnabled              bool    `json:"is_enabled" jsonschema:"false when the line is switched off on the deal"`
	UpdateTime             string  `json:"update_time,omitempty" jsonschema:"timestamp the line was last updated; expect_version takes it"`
}

type listDealProductsInput struct {
	DealID int64 `json:"deal_id" jsonschema:"the deal whose products to list"`
}

type listDealProductsOutput struct {
	DealID    int64            `json:"deal_id" jsonschema:"the deal"`
	DealValue float64          `json:"deal_value" jsonschema:"the deal's value, to compare with the lines' sums"`
	Currency  string           `json:"currency,omitempty" jsonschema:"the deal's currency"`
	Products  []dealProductRow `json:"products" jsonschema:"every product on the deal"`
}

type manageDealProductInput struct {
	Action                 string   `json:"action" jsonschema:"add, update or remove"`
	DealID                 int64    `json:"deal_id" jsonschema:"the deal, required by every action"`
	DealProductID          int64    `json:"deal_product_id,omitempty" jsonschema:"the attachment to act on, required by update and remove; list_deal_products reports it"`
	ProductID              *int64   `json:"product_id,omitempty" jsonschema:"the catalog product to attach, required by add; search with type=product or list_products reports it"`
	ItemPrice              *float64 `json:"item_price,omitempty" jsonschema:"the unit price on this deal, required by add. Omit on update to leave it as it is"`
	Quantity               *float64 `json:"quantity,omitempty" jsonschema:"how many units, required by add. Omit on update to leave it as it is"`
	Discount               *float64 `json:"discount,omitempty" jsonschema:"the discount, as discount_type says. Omit to leave it as it is"`
	DiscountType           *string  `json:"discount_type,omitempty" jsonschema:"percentage | amount. Omit to leave it as it is"`
	Tax                    *float64 `json:"tax,omitempty" jsonschema:"tax percentage. Omit to leave it as it is"`
	Comments               *string  `json:"comments,omitempty" jsonschema:"free-text comments on the line. Omit to leave them as they are"`
	IsEnabled              *bool    `json:"is_enabled,omitempty" jsonschema:"false switches the line off without removing it. Omit to leave it as it is"`
	BillingFrequency       *string  `json:"billing_frequency,omitempty" jsonschema:"one-time | annually | semi-annually | quarterly | monthly. Omit to leave it as it is"`
	BillingFrequencyCycles *int     `json:"billing_frequency_cycles,omitempty" jsonschema:"how many times a recurring line bills. Omit to leave it as it is"`
	BillingStartDate       *string  `json:"billing_start_date,omitempty" jsonschema:"YYYY-MM-DD a recurring line starts billing. Omit to leave it as it is"`
	DryRun                 bool     `json:"dry_run,omitempty" jsonschema:"report what the write would find and change, and send nothing"`
	Overwrite              []string `json:"overwrite,omitempty" jsonschema:"the fields this update may replace, named exactly as the refusal listed them, e.g. [\"quantity\"]. A refusal is NOT a retry signal: name a field only when the user asked for what is there to be replaced"`
	ExpectVersion          string   `json:"expect_version,omitempty" jsonschema:"the line's update_time from the read that informed this write; update and remove are refused if it changed since"`
	Confirm                bool     `json:"confirm,omitempty" jsonschema:"true to remove for good. Pipedrive keeps no copy of a removed line and nothing here restores it; set it only when the user asked for this line to go"`
}

type manageDealProductOutput struct {
	Action   string           `json:"action" jsonschema:"the action that ran"`
	Product  dealProductRow   `json:"product" jsonschema:"the line as stored. For remove, as it was just before it went. On a dry-run add its id is 0"`
	Changed  []string         `json:"changed,omitempty" jsonschema:"the fields this write altered, or on a dry run would alter"`
	Removed  bool             `json:"removed,omitempty" jsonschema:"true when the line was removed, or on a dry run would be"`
	DryRun   bool             `json:"dry_run,omitempty" jsonschema:"true when nothing was sent upstream"`
	Products []dealProductRow `json:"products" jsonschema:"every product on the deal after the write, read back from Pipedrive; on a dry run, the lines the write would leave"`
}

var allowedDealProductActions = map[string]bool{"add": true, "update": true, "remove": true}

var allowedDiscountTypes = map[string]bool{"percentage": true, "amount": true}

var dealProductFields = []fieldSpec[pipedrive.DealProduct]{
	{"product_id", func(p *pipedrive.DealProduct) string { return projectID(p.ProductID) }},
	{"item_price", func(p *pipedrive.DealProduct) string { return projectMoney(p.ItemPrice) }},
	{"quantity", func(p *pipedrive.DealProduct) string { return projectMoney(p.Quantity) }},
	{"discount", func(p *pipedrive.DealProduct) string { return projectMoney(p.Discount) }},
	{"discount_type", func(p *pipedrive.DealProduct) string { return p.DiscountType }},
	{"tax", func(p *pipedrive.DealProduct) string { return projectMoney(p.Tax) }},
	{"comments", func(p *pipedrive.DealProduct) string { return p.Comments }},
	{"is_enabled", func(p *pipedrive.DealProduct) string { return projectBool(p.IsEnabled) }},
	{"billing_frequency", func(p *pipedrive.DealProduct) string { return p.BillingFrequency }},
	{"billing_frequency_cycles", func(p *pipedrive.DealProduct) string { return projectOptInt(p.BillingFrequencyCycles) }},
	{"billing_start_date", func(p *pipedrive.DealProduct) string { return p.BillingStartDate }},
}

func registerDealProducts(s *mcp.Server, c productsClient, opts RegisterOptions) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_deal_products",
		Description: "List the products on one deal: each line's product, unit price, quantity, discount, tax, the sum Pipedrive computes, billing frequency and whether it is switched on, beside the deal's value and currency. The line's id is the deal_product_id manage_deal_product takes. A deal needs at least one one-time product, and no recurring one, before it can take installments.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listDealProductsInput) (*mcp.CallToolResult, listDealProductsOutput, error) {
		if err := validatePositiveID(in.DealID, "deal_id"); err != nil {
			return errorResult(err), listDealProductsOutput{}, nil
		}
		deal, rows, err := readDealProducts(ctx, c, in.DealID)
		if err != nil {
			return errorResult(err), listDealProductsOutput{}, nil
		}
		return nil, listDealProductsOutput{DealID: deal.ID, DealValue: deal.Value, Currency: deal.Currency, Products: summarizeDealProducts(rows)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "manage_deal_product",
		Description: "Add a catalog product to a deal, edit the line, or remove it. add takes deal_id, product_id, item_price and quantity; update and remove take deal_id and deal_product_id. Every action reads the deal's lines first and returns them all afterwards. An update refuses to replace any field that holds a value unless you name it in overwrite. expect_version takes the line's update_time. Removing a line is PERMANENT: Pipedrive keeps no copy, so remove needs confirm: true, and dry_run shows what would go without it. Pipedrive recomputes the deal's value from its lines. A one-time product is what lets a deal take installments, and a recurring one rules them out.",
		Annotations: mutatingAnnotations(),
	}, manageDealProductHandler(c, opts.DryRun))
}

func readDealProducts(ctx context.Context, c productsClient, dealID int64) (*pipedrive.Deal, []pipedrive.DealProduct, error) {
	deal, err := c.GetDeal(ctx, dealID)
	if err != nil {
		return nil, nil, err
	}
	rows, err := c.ListDealProducts(ctx, dealID)
	if err != nil {
		return nil, nil, err
	}
	return deal, rows, nil
}

func summarizeDealProducts(rows []pipedrive.DealProduct) []dealProductRow {
	out := make([]dealProductRow, 0, len(rows))
	for i := range rows {
		out = append(out, summarizeDealProduct(&rows[i]))
	}
	return out
}

func summarizeDealProduct(p *pipedrive.DealProduct) dealProductRow {
	return dealProductRow{
		ID: p.ID, ProductID: p.ProductID, Name: p.Name, ItemPrice: p.ItemPrice, Quantity: p.Quantity,
		Discount: p.Discount, DiscountType: p.DiscountType, Tax: p.Tax, Sum: p.Sum, Currency: p.Currency,
		Comments: p.Comments, BillingFrequency: p.BillingFrequency, BillingFrequencyCycles: clone(p.BillingFrequencyCycles),
		BillingStartDate: p.BillingStartDate, IsEnabled: p.IsEnabled, UpdateTime: p.UpdateTime,
	}
}

func manageDealProductHandler(c productsClient, dryRun bool) mcp.ToolHandlerFor[manageDealProductInput, manageDealProductOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in manageDealProductInput) (*mcp.CallToolResult, manageDealProductOutput, error) {
		if err := validateAction(in.Action, allowedDealProductActions); err != nil {
			return errorResult(err), manageDealProductOutput{}, nil
		}
		if err := validateDealProductInput(in); err != nil {
			return errorResult(err), manageDealProductOutput{}, nil
		}
		in.DryRun = in.DryRun || dryRun
		var (
			res *mcp.CallToolResult
			out manageDealProductOutput
		)
		switch in.Action {
		case "add":
			res, out = addDealProductAction(ctx, c, in)
		case "update":
			res, out = updateDealProductAction(ctx, c, in)
		case "remove":
			res, out = removeDealProductAction(ctx, c, in)
		default:
			res = errorResult(fmt.Errorf("%w: action %q has no handler", pipedrive.ErrValidation, in.Action))
		}
		if res == nil {
			out.Action = in.Action
			out.DryRun = in.DryRun
		}
		return res, out, nil
	}
}

func validateDealProductInput(in manageDealProductInput) error {
	if err := validatePositiveID(in.DealID, "deal_id"); err != nil {
		return err
	}
	if in.BillingFrequency != nil {
		if err := validateEnum(*in.BillingFrequency, "billing_frequency", allowedBillingFrequencies); err != nil {
			return err
		}
	}
	if in.DiscountType != nil {
		if err := validateEnum(*in.DiscountType, "discount_type", allowedDiscountTypes); err != nil {
			return err
		}
	}
	if in.BillingStartDate != nil {
		if _, err := time.Parse(time.DateOnly, *in.BillingStartDate); err != nil {
			return fmt.Errorf("%w: billing_start_date %q is not a YYYY-MM-DD date", pipedrive.ErrValidation, *in.BillingStartDate)
		}
	}
	if in.Quantity != nil && !(*in.Quantity > 0) {
		return fmt.Errorf("%w: quantity must be above zero", pipedrive.ErrValidation)
	}
	return nil
}

func dealProductRequestFor(in manageDealProductInput) pipedrive.DealProductRequest {
	return pipedrive.DealProductRequest{
		ProductID: in.ProductID, ItemPrice: in.ItemPrice, Quantity: in.Quantity, Discount: in.Discount,
		DiscountType: in.DiscountType, Tax: in.Tax, Comments: in.Comments, IsEnabled: in.IsEnabled,
		BillingFrequency: in.BillingFrequency, BillingFrequencyCycles: in.BillingFrequencyCycles, BillingStartDate: in.BillingStartDate,
	}
}

func dealProductAfter(before pipedrive.DealProduct, req pipedrive.DealProductRequest) pipedrive.DealProduct {
	after := before
	setIf(&after.ProductID, req.ProductID)
	setIf(&after.ItemPrice, req.ItemPrice)
	setIf(&after.Quantity, req.Quantity)
	setIf(&after.Discount, req.Discount)
	setIf(&after.DiscountType, req.DiscountType)
	setIf(&after.Tax, req.Tax)
	setIf(&after.Comments, req.Comments)
	setIf(&after.IsEnabled, req.IsEnabled)
	setIf(&after.BillingFrequency, req.BillingFrequency)
	if req.BillingFrequencyCycles != nil {
		after.BillingFrequencyCycles = clone(req.BillingFrequencyCycles)
	}
	setIf(&after.BillingStartDate, req.BillingStartDate)
	return after
}

func addDealProductAction(ctx context.Context, c productsClient, in manageDealProductInput) (*mcp.CallToolResult, manageDealProductOutput) {
	if in.ProductID == nil || *in.ProductID <= 0 || in.ItemPrice == nil || in.Quantity == nil {
		return errorResult(fmt.Errorf("%w: add needs product_id, item_price and quantity", pipedrive.ErrValidation)), manageDealProductOutput{}
	}
	_, rows, err := readDealProducts(ctx, c, in.DealID)
	if err != nil {
		return errorResult(err), manageDealProductOutput{}
	}
	req := dealProductRequestFor(in)
	enabled := true
	if req.IsEnabled == nil {
		// Pipedrive switches a new line on; the preview says so too.
		req.IsEnabled = &enabled
	}
	added := dealProductAfter(pipedrive.DealProduct{DealID: in.DealID}, req)
	result, after := &added, append(append([]pipedrive.DealProduct(nil), rows...), added)
	if !in.DryRun {
		if result, err = c.AddDealProduct(ctx, in.DealID, req); err != nil {
			return errorResult(err), manageDealProductOutput{}
		}
		if after, err = c.ListDealProducts(ctx, in.DealID); err != nil {
			return errorResult(err), manageDealProductOutput{}
		}
	}
	return nil, manageDealProductOutput{
		Product:  summarizeDealProduct(result),
		Changed:  changedFields(dealProductFields, &pipedrive.DealProduct{}, result),
		Products: summarizeDealProducts(after),
	}
}

func findDealProduct(rows []pipedrive.DealProduct, dealID, id int64) (*pipedrive.DealProduct, error) {
	for i := range rows {
		if rows[i].ID == id {
			found := rows[i]
			return &found, nil
		}
	}
	return nil, fmt.Errorf("%w: deal %d has no product line %d; list_deal_products lists them", pipedrive.ErrNotFound, dealID, id)
}

func updateDealProductAction(ctx context.Context, c productsClient, in manageDealProductInput) (*mcp.CallToolResult, manageDealProductOutput) {
	if err := validatePositiveID(in.DealProductID, "deal_product_id"); err != nil {
		return errorResult(err), manageDealProductOutput{}
	}
	_, rows, err := readDealProducts(ctx, c, in.DealID)
	if err != nil {
		return errorResult(err), manageDealProductOutput{}
	}
	req := dealProductRequestFor(in)
	line, changed, res := guardedWrite[pipedrive.DealProduct]{
		Spec:          dealProductFields,
		Resource:      fmt.Sprintf("product line %d of deal %d", in.DealProductID, in.DealID),
		ExpectVersion: in.ExpectVersion,
		Version:       func(p *pipedrive.DealProduct) string { return p.UpdateTime },
		Overwrite:     in.Overwrite,
		DryRun:        in.DryRun,
		Get: func(context.Context) (*pipedrive.DealProduct, error) {
			return findDealProduct(rows, in.DealID, in.DealProductID)
		},
		Predict: func(p *pipedrive.DealProduct) pipedrive.DealProduct { return dealProductAfter(*p, req) },
		Put: func(ctx context.Context) (*pipedrive.DealProduct, error) {
			return c.UpdateDealProduct(ctx, in.DealID, in.DealProductID, req)
		},
	}.run(ctx)
	if res != nil {
		return res, manageDealProductOutput{}
	}
	after := rows
	if len(changed) > 0 {
		if in.DryRun {
			predicted := dealProductAfter(*line, req)
			after = append([]pipedrive.DealProduct(nil), rows...)
			for i := range after {
				if after[i].ID == predicted.ID {
					after[i] = predicted
				}
			}
		} else if after, err = c.ListDealProducts(ctx, in.DealID); err != nil {
			return errorResult(err), manageDealProductOutput{}
		}
	}
	return nil, manageDealProductOutput{Product: summarizeDealProduct(line), Changed: changed, Products: summarizeDealProducts(after)}
}

// removeDealProductAction takes a line off a deal. The removal is
// permanent, so it takes confirm (D-99); a dry run needs none.
func removeDealProductAction(ctx context.Context, c productsClient, in manageDealProductInput) (*mcp.CallToolResult, manageDealProductOutput) {
	if err := validatePositiveID(in.DealProductID, "deal_product_id"); err != nil {
		return errorResult(err), manageDealProductOutput{}
	}
	_, rows, err := readDealProducts(ctx, c, in.DealID)
	if err != nil {
		return errorResult(err), manageDealProductOutput{}
	}
	before, err := findDealProduct(rows, in.DealID, in.DealProductID)
	if err != nil {
		return errorResult(err), manageDealProductOutput{}
	}
	resource := fmt.Sprintf("product line %d of deal %d", in.DealProductID, in.DealID)
	if err = checkExpectVersion(in.ExpectVersion, before.UpdateTime, resource); err != nil {
		return errorResult(err), manageDealProductOutput{}
	}
	if !in.DryRun && !in.Confirm {
		return errorResult(refuse(
			fmt.Sprintf("removing %s (%s × %s) is permanent: Pipedrive keeps no copy and nothing here restores it",
				resource, formatAmount(before.Quantity), before.Name),
			"confirm: true")), manageDealProductOutput{}
	}
	after := make([]pipedrive.DealProduct, 0, len(rows))
	for i := range rows {
		if rows[i].ID != in.DealProductID {
			after = append(after, rows[i])
		}
	}
	if !in.DryRun {
		if err = c.DeleteDealProduct(ctx, in.DealID, in.DealProductID); err != nil {
			return errorResult(err), manageDealProductOutput{}
		}
		if after, err = c.ListDealProducts(ctx, in.DealID); err != nil {
			return errorResult(err), manageDealProductOutput{}
		}
	}
	return nil, manageDealProductOutput{Product: summarizeDealProduct(before), Removed: true, Products: summarizeDealProducts(after)}
}

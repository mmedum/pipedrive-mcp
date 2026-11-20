package pipedrive

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// Product is a Pipedrive product catalog entry (subset). Custom fields
// nest under custom_fields, as a deal's do.
// https://developers.pipedrive.com/docs/api/v1/Products#getProduct
type Product struct {
	ID                     int64          `json:"id"`
	Name                   string         `json:"name"`
	Code                   string         `json:"code"`
	Description            string         `json:"description"`
	Unit                   string         `json:"unit"`
	Tax                    float64        `json:"tax"`
	OwnerID                int64          `json:"owner_id"`
	IsDeleted              bool           `json:"is_deleted"`
	BillingFrequency       string         `json:"billing_frequency"`
	BillingFrequencyCycles *int           `json:"billing_frequency_cycles"`
	Prices                 []ProductPrice `json:"prices"`
	AddTime                string         `json:"add_time"`
	UpdateTime             string         `json:"update_time"`
	CustomFields           map[string]any `json:"custom_fields"`
}

// ProductPrice is one currency's price of a product. A write sends the
// whole list, and Pipedrive replaces the stored one with it, keeping the
// order it was sent in: a PATCH carrying EUR alone leaves EUR alone.
// Checked by the integration test TestWrite_DealProductRoundTrip.
type ProductPrice struct {
	Price      float64 `json:"price"`
	Currency   string  `json:"currency"`
	Cost       float64 `json:"cost,omitempty"`
	DirectCost float64 `json:"direct_cost,omitempty"`
	Notes      string  `json:"notes,omitempty"`
}

// ListProductsOptions filters a /products list call.
type ListProductsOptions struct {
	OwnerID       int64
	IDs           []int64
	UpdatedSince  string // RFC3339
	SortBy        string // id | name | add_time | update_time
	SortDirection string // asc | desc
	Limit         int
	Cursor        string
}

// ProductRequest is the body for creating or editing a product. Pointers
// with omitempty, so an edit sends only what it sets (D-41); a create
// needs a name.
type ProductRequest struct {
	Name                   *string        `json:"name,omitempty"`
	Code                   *string        `json:"code,omitempty"`
	Description            *string        `json:"description,omitempty"`
	Unit                   *string        `json:"unit,omitempty"`
	Tax                    *float64       `json:"tax,omitempty"`
	OwnerID                *int64         `json:"owner_id,omitempty"`
	BillingFrequency       *string        `json:"billing_frequency,omitempty"`
	BillingFrequencyCycles *int           `json:"billing_frequency_cycles,omitempty"`
	Prices                 []ProductPrice `json:"prices,omitempty"`
	CustomFields           map[string]any `json:"custom_fields,omitempty"`
}

// GetProduct fetches one product by id.
func (c *Client) GetProduct(ctx context.Context, id int64) (*Product, error) {
	var resp itemEnvelope[Product]
	if err := c.do(ctx, "/products/"+strconv.FormatInt(id, 10), &resp); err != nil {
		return nil, err
	}
	p := resp.Data
	return &p, nil
}

// ListProducts issues a /products list with the given filters.
func (c *Client) ListProducts(ctx context.Context, opts ListProductsOptions) ([]Product, string, error) {
	q := url.Values{}
	if opts.OwnerID > 0 {
		q.Set("owner_id", strconv.FormatInt(opts.OwnerID, 10))
	}
	if len(opts.IDs) > 0 {
		q.Set("ids", joinInt64s(opts.IDs))
	}
	if opts.UpdatedSince != "" {
		q.Set("updated_since", opts.UpdatedSince)
	}
	if opts.SortBy != "" {
		q.Set("sort_by", opts.SortBy)
	}
	if opts.SortDirection != "" {
		q.Set("sort_direction", opts.SortDirection)
	}
	setLimitCursor(q, opts.Limit, opts.Cursor)
	var resp listEnvelope[Product]
	if err := c.do(ctx, buildPath("/products", q), &resp); err != nil {
		return nil, "", err
	}
	return resp.Data, resp.AdditionalData.NextCursor, nil
}

// CreateProduct adds a product to the catalog.
func (c *Client) CreateProduct(ctx context.Context, req ProductRequest) (*Product, error) {
	if req.Name == nil || *req.Name == "" {
		return nil, fmt.Errorf("%w: name must not be empty", ErrValidation)
	}
	var resp itemEnvelope[Product]
	if err := c.postV2(ctx, "/products", req, &resp); err != nil {
		return nil, err
	}
	p := resp.Data
	return &p, nil
}

// UpdateProduct edits a product and returns it as stored.
func (c *Client) UpdateProduct(ctx context.Context, id int64, req ProductRequest) (*Product, error) {
	var resp itemEnvelope[Product]
	if err := c.patchV2(ctx, "/products/"+strconv.FormatInt(id, 10), req, &resp); err != nil {
		return nil, err
	}
	p := resp.Data
	return &p, nil
}

// DeleteProduct marks a product deleted, softly: "Marks a product as
// deleted. After 30 days, the product will be permanently deleted."
// https://developers.pipedrive.com/docs/api/v1/Products#deleteProduct
func (c *Client) DeleteProduct(ctx context.Context, id int64) error {
	return c.deleteV2(ctx, "/products/"+strconv.FormatInt(id, 10))
}

// ListProductFields returns the field metadata for products, every page
// of it, for the product field cache.
func (c *Client) ListProductFields(ctx context.Context) ([]Field, error) {
	return readAll[Field](ctx, c, "/productFields", nil)
}

// ResolveProductCustomFields returns a copy of raw with hash keys
// replaced by their names, as ResolveDealCustomFields does for deals.
func (c *Client) ResolveProductCustomFields(ctx context.Context, raw map[string]any) map[string]any {
	return c.productFields.Resolve(ctx, raw)
}

// EncodeProductCustomFields turns a caller's custom-field map into the
// hash keys and option ids a write body carries.
func (c *Client) EncodeProductCustomFields(ctx context.Context, in map[string]any) (CustomFieldWrite, error) {
	return c.productFields.Encode(ctx, in)
}

// WarmProductFields eagerly triggers the product-field cache load.
func (c *Client) WarmProductFields(ctx context.Context) {
	_ = c.productFields.Load(ctx)
}

// ReloadProductFields clears and re-fetches the product-field cache,
// returning how many fields it holds after the reload.
func (c *Client) ReloadProductFields(ctx context.Context) (int, error) {
	c.productFields.Reload()
	if err := c.productFields.Load(ctx); err != nil {
		return 0, err
	}
	return c.productFields.Count(), nil
}

// DealProductRequest is the body for attaching a product to a deal or
// editing the attachment. product_id, item_price and quantity are
// required on an attach; an edit sends only what it sets.
// https://developers.pipedrive.com/docs/api/v1/Deals#addDealProduct
type DealProductRequest struct {
	ProductID              *int64   `json:"product_id,omitempty"`
	ItemPrice              *float64 `json:"item_price,omitempty"`
	Quantity               *float64 `json:"quantity,omitempty"`
	Discount               *float64 `json:"discount,omitempty"`
	DiscountType           *string  `json:"discount_type,omitempty"` // percentage | amount
	Tax                    *float64 `json:"tax,omitempty"`
	Comments               *string  `json:"comments,omitempty"`
	IsEnabled              *bool    `json:"is_enabled,omitempty"`
	BillingFrequency       *string  `json:"billing_frequency,omitempty"`
	BillingFrequencyCycles *int     `json:"billing_frequency_cycles,omitempty"`
	BillingStartDate       *string  `json:"billing_start_date,omitempty"` // YYYY-MM-DD
}

// AddDealProduct attaches a product to a deal.
func (c *Client) AddDealProduct(ctx context.Context, dealID int64, req DealProductRequest) (*DealProduct, error) {
	var resp itemEnvelope[DealProduct]
	if err := c.postV2(ctx, dealProductsPath(dealID), req, &resp); err != nil {
		return nil, err
	}
	p := resp.Data
	return &p, nil
}

// UpdateDealProduct edits a product attached to a deal.
func (c *Client) UpdateDealProduct(ctx context.Context, dealID, id int64, req DealProductRequest) (*DealProduct, error) {
	var resp itemEnvelope[DealProduct]
	if err := c.patchV2(ctx, dealProductsPath(dealID)+"/"+strconv.FormatInt(id, 10), req, &resp); err != nil {
		return nil, err
	}
	p := resp.Data
	return &p, nil
}

// DeleteDealProduct removes a product from a deal. The reference says it
// "Deletes a product attachment from a deal" and offers no restore, and
// the line is gone from the next list. A second delete of the same id
// answers 200, so the answer does not say whether the line existed.
// Checked by the integration test TestWrite_DealProductRoundTrip.
// https://developers.pipedrive.com/docs/api/v1/Deals#deleteDealProduct
func (c *Client) DeleteDealProduct(ctx context.Context, dealID, id int64) error {
	return c.deleteV2(ctx, dealProductsPath(dealID)+"/"+strconv.FormatInt(id, 10))
}

func dealProductsPath(dealID int64) string {
	return "/deals/" + strconv.FormatInt(dealID, 10) + "/products"
}

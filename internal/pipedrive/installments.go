package pipedrive

import (
	"context"
	"net/url"
	"strconv"
)

// Installment is one scheduled payment on a deal. It carries no currency
// of its own: an installment is billed in its deal's currency, and the
// v2 description gives it only these five fields, as the live list does.
// https://developers.pipedrive.com/docs/api/v1/DealInstallments#getInstallments
type Installment struct {
	ID          int64   `json:"id"`
	DealID      int64   `json:"deal_id"`
	Description string  `json:"description"`
	Amount      float64 `json:"amount"`
	BillingDate string  `json:"billing_date"` // YYYY-MM-DD
}

// InstallmentRequest is the body for creating or editing an installment.
// A create needs all three fields; an edit sends only the ones it sets,
// so every field is a pointer with omitempty (D-41).
type InstallmentRequest struct {
	Description *string  `json:"description,omitempty"`
	Amount      *float64 `json:"amount,omitempty"`
	BillingDate *string  `json:"billing_date,omitempty"`
}

// DealProduct is the part of a product attached to a deal that decides
// whether the deal may hold installments.
type DealProduct struct {
	ID               int64  `json:"id"`
	BillingFrequency string `json:"billing_frequency"` // one-time | annually | semi-annually | quarterly | monthly
	IsEnabled        bool   `json:"is_enabled"`
}

// MaxInstallmentDeals is how many deal ids one list call takes. The v2
// reference: "A maximum of 100 deal IDs can be provided."
const MaxInstallmentDeals = 100

// ListInstallments reads the installments of up to MaxInstallmentDeals
// deals, oldest billing date first. deal_ids is one comma-separated
// value: Pipedrive answers a repeated deal_ids with a 400, "querystring/
// deal_ids must be string". Checked by the integration test
// TestInstallments_ReadAcrossDeals.
// https://developers.pipedrive.com/docs/api/v1/DealInstallments#getInstallments
func (c *Client) ListInstallments(ctx context.Context, dealIDs []int64, limit int, cursor string) ([]Installment, string, error) {
	q := url.Values{}
	q.Set("deal_ids", joinInt64s(dealIDs))
	q.Set("sort_by", "billing_date")
	q.Set("sort_direction", "asc")
	setLimitCursor(q, limit, cursor)

	var resp listEnvelope[Installment]
	if err := c.do(ctx, buildPath("/deals/installments", q), &resp); err != nil {
		return nil, "", err
	}
	return resp.Data, resp.AdditionalData.NextCursor, nil
}

// CreateInstallment adds an installment to a deal. Pipedrive allows it
// only on a deal with at least one one-time product and no recurring
// one, and only on the Growth plan and above.
// https://developers.pipedrive.com/docs/api/v1/DealInstallments#postInstallment
func (c *Client) CreateInstallment(ctx context.Context, dealID int64, req InstallmentRequest) (*Installment, error) {
	var resp itemEnvelope[Installment]
	if err := c.postV2(ctx, installmentsPath(dealID), req, &resp); err != nil {
		return nil, err
	}
	in := resp.Data
	return &in, nil
}

// UpdateInstallment edits an installment and returns it as stored.
// https://developers.pipedrive.com/docs/api/v1/DealInstallments#updateInstallment
func (c *Client) UpdateInstallment(ctx context.Context, dealID, id int64, req InstallmentRequest) (*Installment, error) {
	var resp itemEnvelope[Installment]
	if err := c.patchV2(ctx, installmentsPath(dealID)+"/"+strconv.FormatInt(id, 10), req, &resp); err != nil {
		return nil, err
	}
	in := resp.Data
	return &in, nil
}

// DeleteInstallment removes an installment from its deal. Unlike a deal
// delete it is not soft: the reference says it "Removes an installment
// from a deal" and offers no restore, and the deleted id is gone from
// the next list. Checked by the integration test
// TestWrite_InstallmentRoundTrip.
// https://developers.pipedrive.com/docs/api/v1/DealInstallments#deleteInstallment
func (c *Client) DeleteInstallment(ctx context.Context, dealID, id int64) error {
	return c.deleteV2(ctx, installmentsPath(dealID)+"/"+strconv.FormatInt(id, 10))
}

// ListDealProducts reads the products attached to a deal, first page
// only. A deal with more than 500 products is beyond what the
// installment rule needs to see.
// https://developers.pipedrive.com/docs/api/v1/Deals#getDealProducts
func (c *Client) ListDealProducts(ctx context.Context, dealID int64) ([]DealProduct, error) {
	q := url.Values{}
	q.Set("limit", "500")
	var resp listEnvelope[DealProduct]
	if err := c.do(ctx, buildPath("/deals/"+strconv.FormatInt(dealID, 10)+"/products", q), &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

func installmentsPath(dealID int64) string {
	return "/deals/" + strconv.FormatInt(dealID, 10) + "/installments"
}

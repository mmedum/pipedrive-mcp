package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

// dealsClient is the subset of *pipedrive.Client these tools call.
// The interface lets handler tests pass a fake without standing up
// an httptest server. The custom-field resolver is exposed via a
// method instead of a *FieldCache field so tests don't need to
// construct a cache.
type dealsClient interface {
	GetDeal(ctx context.Context, id int64) (*pipedrive.Deal, error)
	ListDeals(ctx context.Context, opts pipedrive.ListDealsOptions) ([]pipedrive.Deal, string, error)
	ResolveDealCustomFields(ctx context.Context, raw map[string]any) map[string]any
}

// allowedDealStatuses is the closed set of `status` values Pipedrive
// accepts on /deals. Tools validate against this before calling the
// API so a typo surfaces as a [validation] error instead of an
// upstream 400.
var allowedDealStatuses = map[string]bool{
	"open":            true,
	"won":             true,
	"lost":            true,
	"deleted":         true,
	"all_not_deleted": true,
}

const (
	listDealsDefaultLimit = 25
	listDealsMaxLimit     = 100
)

type dealSummary struct {
	ID                int64          `json:"id" jsonschema:"the deal's numeric id"`
	Title             string         `json:"title" jsonschema:"the deal's title"`
	Value             float64        `json:"value" jsonschema:"the deal's monetary value, in the deal's currency"`
	Currency          string         `json:"currency" jsonschema:"ISO 4217 currency code, e.g. USD or EUR"`
	Status            string         `json:"status" jsonschema:"open | won | lost | deleted"`
	StageID           int64          `json:"stage_id" jsonschema:"id of the stage this deal sits in"`
	PipelineID        int64          `json:"pipeline_id" jsonschema:"id of the pipeline this deal sits in"`
	OwnerID           int64          `json:"owner_id" jsonschema:"id of the user who owns this deal"`
	PersonID          int64          `json:"person_id" jsonschema:"id of the linked person, 0 if none"`
	OrgID             int64          `json:"org_id" jsonschema:"id of the linked organization, 0 if none"`
	ExpectedCloseDate string         `json:"expected_close_date,omitempty" jsonschema:"YYYY-MM-DD when the deal is expected to close"`
	WonTime           string         `json:"won_time,omitempty" jsonschema:"timestamp the deal was marked won, empty if not won"`
	LostTime          string         `json:"lost_time,omitempty" jsonschema:"timestamp the deal was marked lost, empty if not lost"`
	LostReason        string         `json:"lost_reason,omitempty" jsonschema:"free-text reason recorded when the deal was marked lost"`
	AddTime           string         `json:"add_time,omitempty" jsonschema:"timestamp the deal was created"`
	UpdateTime        string         `json:"update_time,omitempty" jsonschema:"timestamp the deal was last updated"`
	Probability       *int           `json:"probability,omitempty" jsonschema:"deal probability override (0-100); null when the stage default applies"`
	CustomFields      map[string]any `json:"custom_fields,omitempty" jsonschema:"custom fields keyed by human-readable name; unknown fields fall through under their internal key"`
	URL               string         `json:"url" jsonschema:"link to the deal in the Pipedrive web UI"`
}

type listDealsInput struct {
	Status     string `json:"status,omitempty" jsonschema:"open | won | lost | deleted | all_not_deleted. Default: open."`
	PipelineID int64  `json:"pipeline_id,omitempty" jsonschema:"return only deals in this pipeline; 0 = no filter"`
	StageID    int64  `json:"stage_id,omitempty" jsonschema:"return only deals in this stage; 0 = no filter"`
	OwnerID    int64  `json:"owner_id,omitempty" jsonschema:"return only deals owned by this user id; 0 = no filter"`
	PersonID   int64  `json:"person_id,omitempty" jsonschema:"return only deals linked to this person id; 0 = no filter"`
	OrgID      int64  `json:"org_id,omitempty" jsonschema:"return only deals linked to this organization id; 0 = no filter"`
	Limit      int    `json:"limit,omitempty" jsonschema:"page size; default 25, max 100"`
	Cursor     string `json:"cursor,omitempty" jsonschema:"opaque pagination token from a previous list_deals response; omit for the first page"`
}

type listDealsOutput struct {
	Deals      []dealSummary `json:"deals" jsonschema:"matching deals on this page"`
	NextCursor string        `json:"next_cursor,omitempty" jsonschema:"pass to the next list_deals call to get the next page; empty when there are no more pages"`
}

type getDealInput struct {
	DealID int64 `json:"deal_id" jsonschema:"the deal's numeric id"`
}

type getDealOutput struct {
	Deal dealSummary `json:"deal" jsonschema:"the requested deal"`
}

// RegisterDeals wires get_deal and list_deals into the MCP server.
// companyDomain is needed for URL injection on outputs.
func RegisterDeals(s *mcp.Server, c dealsClient, companyDomain string) {
	readOnly := mcp.ToolAnnotations{ReadOnlyHint: true}

	AddTool(s, &mcp.Tool{
		Name:        "get_deal",
		Description: "Fetch a single Pipedrive deal by deal_id. Returns id, title, value, currency, status (open | won | lost | deleted), stage_id, pipeline_id, owner_id, person_id, org_id, expected_close_date, won/lost timestamps, lost_reason, and any custom fields resolved by name. Unknown deal_id returns a [not_found] error.",
		Annotations: &readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getDealInput) (*mcp.CallToolResult, getDealOutput, error) {
		if in.DealID <= 0 {
			err := fmt.Errorf("%w: deal_id must be a positive integer", pipedrive.ErrValidation)
			return errorResult(err), getDealOutput{}, nil
		}
		deal, err := c.GetDeal(ctx, in.DealID)
		if err != nil {
			return errorResult(err), getDealOutput{}, nil
		}
		return nil, getDealOutput{Deal: summarizeDeal(ctx, c, companyDomain, deal)}, nil
	})

	AddTool(s, &mcp.Tool{
		Name:        "list_deals",
		Description: "Search deals by status, pipeline, stage, owner, person, or organization. Returns matching deals with id, title, value, currency, status (open | won | lost | deleted), stage_id, pipeline_id, owner_id, person_id, org_id, expected_close_date, won/lost timestamps, and any custom fields (resolved by name). Default limit is 25, max 100. For more results, pass the next_cursor from the previous response.",
		Annotations: &readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listDealsInput) (*mcp.CallToolResult, listDealsOutput, error) {
		if in.Status != "" && !allowedDealStatuses[in.Status] {
			err := fmt.Errorf("%w: status %q is not one of open|won|lost|deleted|all_not_deleted", pipedrive.ErrValidation, in.Status)
			return errorResult(err), listDealsOutput{}, nil
		}
		limit := in.Limit
		if limit <= 0 {
			limit = listDealsDefaultLimit
		}
		if limit > listDealsMaxLimit {
			limit = listDealsMaxLimit
		}
		opts := pipedrive.ListDealsOptions{
			Status:        in.Status,
			PipelineID:    in.PipelineID,
			StageID:       in.StageID,
			OwnerID:       in.OwnerID,
			PersonID:      in.PersonID,
			OrgID:         in.OrgID,
			Limit:         limit,
			Cursor:        in.Cursor,
			IncludeCustom: true,
		}
		deals, next, err := c.ListDeals(ctx, opts)
		if err != nil {
			return errorResult(err), listDealsOutput{}, nil
		}
		out := listDealsOutput{Deals: make([]dealSummary, 0, len(deals)), NextCursor: next}
		for i := range deals {
			out.Deals = append(out.Deals, summarizeDeal(ctx, c, companyDomain, &deals[i]))
		}
		return nil, out, nil
	})
}

// summarizeDeal copies pipedrive.Deal into the LLM-facing dealSummary
// shape, resolving custom_fields hash-keys to names via the cache.
func summarizeDeal(ctx context.Context, c dealsClient, domain string, d *pipedrive.Deal) dealSummary {
	return dealSummary{
		ID:                d.ID,
		Title:             d.Title,
		Value:             d.Value,
		Currency:          d.Currency,
		Status:            d.Status,
		StageID:           d.StageID,
		PipelineID:        d.PipelineID,
		OwnerID:           d.OwnerID,
		PersonID:          d.PersonID,
		OrgID:             d.OrgID,
		ExpectedCloseDate: d.ExpectedCloseDate,
		WonTime:           d.WonTime,
		LostTime:          d.LostTime,
		LostReason:        d.LostReason,
		AddTime:           d.AddTime,
		UpdateTime:        d.UpdateTime,
		Probability:       d.Probability,
		CustomFields:      c.ResolveDealCustomFields(ctx, d.CustomFields),
		URL:               pipedrive.WebURL(domain, pipedrive.WebURLDeal, d.ID),
	}
}

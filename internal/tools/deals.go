package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

type dealsClient interface {
	GetDeal(ctx context.Context, id int64) (*pipedrive.Deal, error)
	ListDeals(ctx context.Context, opts pipedrive.ListDealsOptions) ([]pipedrive.Deal, string, error)
	CreateDeal(ctx context.Context, req pipedrive.CreateDealRequest) (*pipedrive.Deal, error)
	ResolveDealCustomFields(ctx context.Context, raw map[string]any) map[string]any
}

// dealStatusOpen is Pipedrive's default status for a freshly-created
// deal. Tracked as a constant so the synthetic-deal preview in the
// dry-run path stays in sync with the enum below.
const dealStatusOpen = "open"

// Validating the status before calling the API lets a typo surface as
// [validation] instead of an upstream 400. v2 only accepts these four
// values; the v1 `all_not_deleted` synonym was removed.
var allowedDealStatuses = map[string]bool{
	dealStatusOpen: true,
	"won":          true,
	"lost":         true,
	"deleted":      true,
}

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

// allowedDealSortFields enumerates Pipedrive v2's allowed sort_by
// values for /deals. v2 supports id / update_time / add_time only —
// matches the shared commonV2TimestampSortFields base.
var allowedDealSortFields = commonV2TimestampSortFields

type listDealsInput struct {
	Status        string `json:"status,omitempty" jsonschema:"open | won | lost | deleted. Omit to let Pipedrive return its default (all non-deleted)."`
	PipelineID    int64  `json:"pipeline_id,omitempty" jsonschema:"return only deals in this pipeline; 0 = no filter"`
	StageID       int64  `json:"stage_id,omitempty" jsonschema:"return only deals in this stage; 0 = no filter"`
	OwnerID       int64  `json:"owner_id,omitempty" jsonschema:"return only deals owned by this user id; 0 = no filter"`
	PersonID      int64  `json:"person_id,omitempty" jsonschema:"return only deals linked to this person id; 0 = no filter"`
	OrgID         int64  `json:"org_id,omitempty" jsonschema:"return only deals linked to this organization id; 0 = no filter"`
	UpdatedSince  string `json:"updated_since,omitempty" jsonschema:"RFC3339 timestamp; return only deals updated at or after this time (e.g. 2026-04-01T00:00:00Z)"`
	UpdatedUntil  string `json:"updated_until,omitempty" jsonschema:"RFC3339 timestamp; return only deals updated at or before this time"`
	SortBy        string `json:"sort_by,omitempty" jsonschema:"id | update_time | add_time. Default 'update_time' (most-recently-touched first)."`
	SortDirection string `json:"sort_direction,omitempty" jsonschema:"asc | desc. Default 'desc' when sort_by is omitted; 'asc' otherwise."`
	Limit         int    `json:"limit,omitempty" jsonschema:"page size; default 25, max 100"`
	Cursor        string `json:"cursor,omitempty" jsonschema:"opaque pagination token from a previous list_deals response; omit for the first page"`
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

type createDealInput struct {
	Title             string  `json:"title" jsonschema:"the deal's title; required, non-empty. Surface a recognizable name (e.g. 'Acme — VisitorPass renewal'); the LLM should not invent a title from thin air."`
	Value             float64 `json:"value,omitempty" jsonschema:"monetary value in the deal's currency; omit for 0"`
	Currency          string  `json:"currency,omitempty" jsonschema:"ISO 4217 currency code (e.g. USD, EUR, DKK). Omit to use the workspace's default currency."`
	PipelineID        int64   `json:"pipeline_id,omitempty" jsonschema:"id of the pipeline to place the deal in. Omit to use the default pipeline. Use list_pipelines to discover ids."`
	StageID           int64   `json:"stage_id,omitempty" jsonschema:"id of the stage to place the deal in. Omit to use the first stage of the chosen pipeline. Use list_stages(pipeline_id=...) to discover ids."`
	OwnerID           int64   `json:"owner_id,omitempty" jsonschema:"id of the user to own the deal; omit to default to the API-token user"`
	PersonID          int64   `json:"person_id,omitempty" jsonschema:"id of the linked contact person; 0 = no link"`
	OrgID             int64   `json:"org_id,omitempty" jsonschema:"id of the linked organization; 0 = no link"`
	ExpectedCloseDate string  `json:"expected_close_date,omitempty" jsonschema:"YYYY-MM-DD when the deal is expected to close; omit if unknown"`
	Probability       *int    `json:"probability,omitempty" jsonschema:"deal probability override (0-100); omit to use the stage default"`
}

type createDealOutput struct {
	Deal   dealSummary `json:"deal" jsonschema:"the newly-created deal as Pipedrive echoes it. When dry_run is true, this is a synthetic record with id=0 reflecting what would have been created."`
	DryRun bool        `json:"dry_run,omitempty" jsonschema:"true when PIPEDRIVE_DRY_RUN was set on the server: no upstream POST was issued"`
}

// RegisterDeals wires get_deal, list_deals, and create_deal into the
// MCP server. opts.DryRun, when true, makes create_deal return a
// synthetic preview without firing the upstream POST.
func RegisterDeals(s *mcp.Server, c dealsClient, companyDomain string, opts RegisterOptions) {
	readOnly := mcp.ToolAnnotations{ReadOnlyHint: true}

	AddTool(s, &mcp.Tool{
		Name:        "get_deal",
		Description: "Fetch a single Pipedrive deal by deal_id. Returns id, title, value, currency, status (open | won | lost | deleted), stage_id, pipeline_id, owner_id, person_id, org_id, expected_close_date, won/lost timestamps, lost_reason, and any custom fields resolved by name. Unknown deal_id returns a [not_found] error.",
		Annotations: &readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getDealInput) (*mcp.CallToolResult, getDealOutput, error) {
		if err := validatePositiveID(in.DealID, "deal_id"); err != nil {
			return errorResult(err), getDealOutput{}, nil
		}
		deal, err := c.GetDeal(ctx, in.DealID)
		if err != nil {
			return errorResult(err), getDealOutput{}, nil
		}
		resolved := c.ResolveDealCustomFields(ctx, deal.CustomFields)
		return nil, getDealOutput{Deal: summarizeDeal(companyDomain, deal, resolved)}, nil
	})

	AddTool(s, &mcp.Tool{
		Name:        "list_deals",
		Description: "List deals filtered by status, pipeline, stage, owner, person, organization, or update window. Returns matching deals with id, title, value, currency, status (open | won | lost | deleted), stage_id, pipeline_id, owner_id, person_id, org_id, expected_close_date, won/lost timestamps, and any custom fields (resolved by name). Default sort is update_time desc — most-recently-touched first, ideal for 'which deals have we been working on lately'. Default limit is 25, max 100. Omit `status` to include every non-deleted deal. For more results, pass the next_cursor from the previous response. To find a deal by name (rather than ID), call `search` with type=deal first — search is the natural-language gateway, list_deals is the precision filter when the IDs are already known.",
		Annotations: &readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listDealsInput) (*mcp.CallToolResult, listDealsOutput, error) {
		if err := validateEnum(in.Status, "status", allowedDealStatuses); err != nil {
			return errorResult(err), listDealsOutput{}, nil
		}
		if err := validateEnum(in.SortBy, "sort_by", allowedDealSortFields); err != nil {
			return errorResult(err), listDealsOutput{}, nil
		}
		if err := validateEnum(in.SortDirection, "sort_direction", allowedSortDirections); err != nil {
			return errorResult(err), listDealsOutput{}, nil
		}

		sortBy, sortDir := effectiveSort(in.SortBy, in.SortDirection)
		opts := pipedrive.ListDealsOptions{
			Status:        in.Status,
			PipelineID:    in.PipelineID,
			StageID:       in.StageID,
			OwnerID:       in.OwnerID,
			PersonID:      in.PersonID,
			OrgID:         in.OrgID,
			UpdatedSince:  in.UpdatedSince,
			UpdatedUntil:  in.UpdatedUntil,
			SortBy:        sortBy,
			SortDirection: sortDir,
			Limit:         clampLimit(in.Limit),
			Cursor:        in.Cursor,
		}
		deals, next, err := c.ListDeals(ctx, opts)
		if err != nil {
			return errorResult(err), listDealsOutput{}, nil
		}
		out := listDealsOutput{Deals: make([]dealSummary, 0, len(deals)), NextCursor: next}
		for i := range deals {
			resolved := c.ResolveDealCustomFields(ctx, deals[i].CustomFields)
			out.Deals = append(out.Deals, summarizeDeal(companyDomain, &deals[i], resolved))
		}
		return nil, out, nil
	})

	AddTool(s, &mcp.Tool{
		Name:        "create_deal",
		Description: "Create a new Pipedrive deal. Required: `title`. If the user did not give you a deal title, ask — do NOT invent one. Every other field is optional and falls back to Pipedrive's workspace default (currency, owner, status=open, first stage of the chosen pipeline). Honours PIPEDRIVE_DRY_RUN=true on the server by returning a synthetic preview (dry_run=true, id=0) without issuing the POST. Custom fields are not writable through this tool yet.",
	}, createDealHandler(c, companyDomain, opts.DryRun))
}

func createDealHandler(c dealsClient, companyDomain string, dryRun bool) mcp.ToolHandlerFor[createDealInput, createDealOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in createDealInput) (*mcp.CallToolResult, createDealOutput, error) {
		if in.Title == "" {
			return errorResult(fmt.Errorf("%w: title must not be empty", pipedrive.ErrValidation)), createDealOutput{}, nil
		}
		req := pipedrive.CreateDealRequest{
			Title:             in.Title,
			Value:             in.Value,
			Currency:          in.Currency,
			PipelineID:        in.PipelineID,
			StageID:           in.StageID,
			OwnerID:           in.OwnerID,
			PersonID:          in.PersonID,
			OrgID:             in.OrgID,
			ExpectedCloseDate: in.ExpectedCloseDate,
			Probability:       in.Probability,
		}
		if dryRun {
			return nil, createDealOutput{
				Deal:   summarizeDeal(companyDomain, syntheticDealFromRequest(req), nil),
				DryRun: true,
			}, nil
		}
		deal, err := c.CreateDeal(ctx, req)
		if err != nil {
			return errorResult(err), createDealOutput{}, nil
		}
		resolved := c.ResolveDealCustomFields(ctx, deal.CustomFields)
		return nil, createDealOutput{Deal: summarizeDeal(companyDomain, deal, resolved)}, nil
	}
}

// syntheticDealFromRequest builds a placeholder Deal that mirrors the
// CreateDealRequest, used only on the dry-run path so create_deal's
// output schema stays consistent. ID=0 + dry_run=true tells the LLM
// nothing was actually persisted. Probability is copied so the
// synthetic deal doesn't alias the request's pointer.
func syntheticDealFromRequest(req pipedrive.CreateDealRequest) *pipedrive.Deal {
	return &pipedrive.Deal{
		Title:             req.Title,
		Value:             req.Value,
		Currency:          req.Currency,
		Status:            dealStatusOpen,
		StageID:           req.StageID,
		PipelineID:        req.PipelineID,
		OwnerID:           req.OwnerID,
		PersonID:          req.PersonID,
		OrgID:             req.OrgID,
		ExpectedCloseDate: req.ExpectedCloseDate,
		Probability:       copyIntPtr(req.Probability),
	}
}

// copyIntPtr returns a pointer to a fresh copy of *p so the LLM-facing
// summary never aliases the upstream pipedrive.Deal struct's pointer.
// Keeps the parallel-shadow boundary intact even if a future caller
// mutates the upstream struct.
func copyIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func summarizeDeal(domain string, d *pipedrive.Deal, customFields map[string]any) dealSummary {
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
		Probability:       copyIntPtr(d.Probability),
		CustomFields:      customFields,
		URL:               pipedrive.WebURL(domain, pipedrive.WebURLDeal, d.ID),
	}
}

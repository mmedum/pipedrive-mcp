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
	UpdateDeal(ctx context.Context, id int64, req pipedrive.UpdateDealRequest) (*pipedrive.Deal, error)
	DeleteDeal(ctx context.Context, id int64) error
	ResolveDealCustomFields(ctx context.Context, raw map[string]any) map[string]any
	EncodeDealCustomFields(ctx context.Context, in map[string]any) (pipedrive.CustomFieldWrite, error)
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
	Probability       *float64       `json:"probability,omitempty" jsonschema:"deal probability override (0-100); null when the stage default applies"`
	IsArchived        bool           `json:"is_archived,omitempty" jsonschema:"true if the deal has been archived; an archived deal cannot be edited and does not appear in list_deals unless archived is set"`
	CustomFields      map[string]any `json:"custom_fields,omitempty" jsonschema:"custom fields keyed by human-readable name, a dropdown's value as its label; an unrecognised field or option falls through under its stored key"`
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
	Archived      bool   `json:"archived,omitempty" jsonschema:"read the archive instead of the live pipeline. Archived deals are in a separate collection and NO other filter reaches them, so this is the only way to see one"`
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

// RegisterDeals wires the deal tools into the MCP server. Reads are
// discrete (get_deal, list_deals); every mutation goes through
// manage_deal. opts.DryRun is the server-wide dry-run floor.
func RegisterDeals(s *mcp.Server, c dealsClient, companyDomain string, opts RegisterOptions) {

	AddTool(s, &mcp.Tool{
		Name:        "get_deal",
		Description: "Fetch a single Pipedrive deal by deal_id. Returns id, title, value, currency, status (open | won | lost | deleted), stage_id, pipeline_id, owner_id, person_id, org_id, expected_close_date, won/lost timestamps, lost_reason, and any custom fields under their workspace names, dropdown values as labels rather than option ids. Unknown deal_id returns a [not_found] error.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getDealInput) (*mcp.CallToolResult, getDealOutput, error) {
		if err := validatePositiveID(in.DealID, "deal_id"); err != nil {
			return errorResult(err), getDealOutput{}, nil
		}
		deal, err := c.GetDeal(ctx, in.DealID)
		if err != nil {
			return errorResult(err), getDealOutput{}, nil
		}
		return nil, getDealOutput{Deal: resolvedDeal(ctx, c, companyDomain, deal)}, nil
	})

	AddTool(s, &mcp.Tool{
		Name:        "list_deals",
		Description: "List deals filtered by status, pipeline, stage, owner, person, organization, or update window. Returns matching deals with id, title, value, currency, status (open | won | lost | deleted), stage_id, pipeline_id, owner_id, person_id, org_id, expected_close_date, won/lost timestamps, and any custom fields under their workspace names, dropdown values as labels rather than option ids. Default sort is update_time desc — most-recently-touched first, ideal for 'which deals have we been working on lately'. Default limit is 25, max 100. Omit `status` to include every non-deleted deal. For more results, pass the next_cursor from the previous response. Archived deals are NOT in this collection — Pipedrive moved them to their own since 2025-07-15 and no filter here reaches them, so a list that looks short may be one; pass archived to read them instead. To find a deal by name (rather than ID), call `search` with type=deal first — search is the natural-language gateway, list_deals is the precision filter when the IDs are already known.",
		Annotations: readOnlyAnnotations(),
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
			Archived:      in.Archived,
		}
		deals, next, err := c.ListDeals(ctx, opts)
		if err != nil {
			return errorResult(err), listDealsOutput{}, nil
		}
		out := listDealsOutput{Deals: make([]dealSummary, 0, len(deals)), NextCursor: next}
		for i := range deals {
			out.Deals = append(out.Deals, resolvedDeal(ctx, c, companyDomain, &deals[i]))
		}
		return nil, out, nil
	})

	registerManageDeal(s, c, companyDomain, opts)
}

// dealAction is what manage_deal needs to know about one action beyond
// how to build its request: whether it creates rather than writes, and
// whether it authorises its own overwrite.
//
// A transition does authorise itself — it names both the change and the
// field it lands on, so the caller already sees the whole blast radius,
// and only a free-form update needs permission. That used to be spelled
// as `in.Action != "update"` at the guard, which is a rule stated by
// exclusion: a new action was a transition unless it happened to be
// named update, whatever it actually did. Here a new action states what
// it is, in the same place it is declared to exist at all.
type dealAction struct {
	creates    bool
	transition bool
	deletes    bool
}

// dealActions is the closed enum manage_deal dispatches on.
var dealActions = map[string]dealAction{
	"create":     {creates: true},
	"update":     {},
	"move_stage": {transition: true},
	"mark_won":   {transition: true},
	"mark_lost":  {transition: true},
	"reopen":     {transition: true},
	"archive":    {transition: true},
	"unarchive":  {transition: true},
	"delete":     {deletes: true},
}

// dealBaseFields is the table of LLM-facing field names a write can
// touch that every deal has. A workspace's own custom fields are added
// per call, so THIS IS NOT THE WHOLE TABLE. A create diffs against
// dealSpec's result; an update hands this table to guardedWrite and lets
// its Custom/CustomOf pair extend it. Either way, never diff a write
// against this alone — the caller's custom fields would fall out of both
// the report and the overwrite guard, silently.
//
// won_time and lost_time are absent because Pipedrive stamps them
// itself; add_time and update_time likewise.
var dealBaseFields = []fieldSpec[pipedrive.Deal]{
	{"title", func(d *pipedrive.Deal) string { return d.Title }},
	{"value", func(d *pipedrive.Deal) string { return projectMoney(d.Value) }},
	{"currency", func(d *pipedrive.Deal) string { return d.Currency }},
	{"status", func(d *pipedrive.Deal) string { return d.Status }},
	{"stage_id", func(d *pipedrive.Deal) string { return projectID(d.StageID) }},
	{"pipeline_id", func(d *pipedrive.Deal) string { return projectID(d.PipelineID) }},
	{"owner_id", func(d *pipedrive.Deal) string { return projectID(d.OwnerID) }},
	{"person_id", func(d *pipedrive.Deal) string { return projectID(d.PersonID) }},
	{"org_id", func(d *pipedrive.Deal) string { return projectID(d.OrgID) }},
	{"expected_close_date", func(d *pipedrive.Deal) string { return d.ExpectedCloseDate }},
	{"probability", func(d *pipedrive.Deal) string { return projectOptFloat(d.Probability) }},
	{"is_archived", func(d *pipedrive.Deal) string { return projectBool(d.IsArchived) }},
	{"lost_reason", func(d *pipedrive.Deal) string { return d.LostReason }},
}

// manageDealInput is the single input shape for every deal mutation.
// Fields an update can clear are pointers: a bare int64 could never
// express "unlink this person", and a bare float64 could never express
// "this deal is worth nothing after all".
type manageDealInput struct {
	Action            string         `json:"action" jsonschema:"create, update, move_stage, mark_won, mark_lost, reopen, archive, unarchive or delete"`
	DealID            int64          `json:"deal_id,omitempty" jsonschema:"the deal to act on, required by every action except create"`
	Title             *string        `json:"title,omitempty" jsonschema:"the deal's title, required by create. Give it a name a human would recognise; if the user did not supply one, ask rather than inventing it"`
	Value             *float64       `json:"value,omitempty" jsonschema:"monetary value in the deal's currency"`
	Currency          *string        `json:"currency,omitempty" jsonschema:"ISO 4217 code such as USD, EUR or DKK; omit on create to take the workspace default"`
	PipelineID        *int64         `json:"pipeline_id,omitempty" jsonschema:"the pipeline the deal sits in; list_pipelines reports the ids"`
	StageID           *int64         `json:"stage_id,omitempty" jsonschema:"the stage the deal sits in, and what move_stage requires; list_stages reports the ids and which pipeline each belongs to"`
	OwnerID           *int64         `json:"owner_id,omitempty" jsonschema:"the user who owns the deal; omit on create to take the API token's own user"`
	PersonID          *int64         `json:"person_id,omitempty" jsonschema:"the linked contact. Omit to leave it as it is; unlinking is not supported here"`
	OrgID             *int64         `json:"org_id,omitempty" jsonschema:"the linked organization. Omit to leave it as it is; unlinking is not supported here"`
	ExpectedCloseDate *string        `json:"expected_close_date,omitempty" jsonschema:"YYYY-MM-DD the deal is expected to close. Omit to leave it as it is: Pipedrive v2 offers no way to remove a date once set, so this can only be changed, never cleared"`
	Probability       *float64       `json:"probability,omitempty" jsonschema:"probability override from 0 to 100; omit to let the stage's own default apply"`
	LostReason        *string        `json:"lost_reason,omitempty" jsonschema:"why the deal was lost, for mark_lost. Free text Pipedrive keeps and reports on: if the user did not give a reason, leave this blank — do NOT invent one"`
	CustomFields      map[string]any `json:"custom_fields,omitempty" jsonschema:"this workspace's own fields, keyed by the name get_deal reports — a dropdown takes its label, a multi-select a list of labels, everything else the plain value. Only create and update read this; the transitions ignore it, as they ignore every descriptive field. Omit a field to leave it as it is; a field cannot be cleared"`
	DryRun            bool           `json:"dry_run,omitempty" jsonschema:"report what the write would find and change, and send nothing"`
	Overwrite         bool           `json:"overwrite,omitempty" jsonschema:"allow update to replace fields that already hold a value. Without it such an update is refused, naming each field"`
	ExpectVersion     string         `json:"expect_version,omitempty" jsonschema:"the update_time from the read that informed this write; the write is refused if the deal changed since"`
}

type manageDealOutput struct {
	Action  string      `json:"action" jsonschema:"the action that ran"`
	Deal    dealSummary `json:"deal" jsonschema:"the deal as Pipedrive stored it. When dry_run is true nothing was persisted and a created deal carries id=0."`
	Changed []string    `json:"changed,omitempty" jsonschema:"names of the fields this write actually altered, empty when it was a no-op. On a dry run, the fields it would alter."`
	DryRun  bool        `json:"dry_run,omitempty" jsonschema:"true when nothing was sent upstream"`
}

func registerManageDeal(s *mcp.Server, c dealsClient, companyDomain string, opts RegisterOptions) {

	AddTool(s, &mcp.Tool{
		Name:        "manage_deal",
		Description: "Create a deal, edit one, move it between stages, close it won or lost, or delete it. One call, whichever action: create takes title, every other action takes deal_id. Writing is guarded, and every action except create reads the deal before it writes, so a write is two API calls. An update refuses to replace ANY field that already holds a value unless you pass overwrite, and the refusal names each one; filling a field that is empty destroys nothing and needs no permission. expect_version refuses the write outright if the deal moved under you. The six transitions — move_stage, mark_won, mark_lost, reopen, archive and unarchive — take no overwrite, because you named the transition and the field it changes is the field you named. ARCHIVING IS NOT CLOSING: an archived deal leaves the pipeline entirely, stops appearing in list_deals unless you pass archived, and accepts no edit at all until it is unarchived — mark_lost is what 'we lost it' means. IMPORTANT: lost_reason on mark_lost is free text Pipedrive keeps and reports on, so if the user did not give you a reason, leave it blank — do NOT invent one. Closing a deal is reversible here: reopen puts it back to open and clears the lost reason, though Pipedrive keeps its own record of won_time and lost_time, which you cannot set from this tool. Custom fields ARE writable here, on create and update: pass custom_fields keyed by the names get_deal reports, and give a dropdown its label rather than an option id. One thing this cannot do: NO FIELD CAN BE CLEARED once it holds a value — Pipedrive v2 rejects a null and treats an empty string as a value, so a field can be changed but not emptied. Deleting is soft and time-boxed: Pipedrive marks the deal deleted and removes it permanently after 30 days, so it takes dry_run and expect_version and no permitting flag beyond them — within the window Pipedrive's own UI can restore it, but NOTHING HERE PUTS IT BACK, so treat it as one-way and rehearse with dry_run first. What becomes of the notes and activities hanging off a deleted deal is not documented by Pipedrive and is not verified here, so read list_notes and list_activities for the deal before deleting one that has history on it. Use search to turn a company or person name into the person_id or org_id a new deal needs, and list_stages to find a stage_id.",
		Annotations: mutatingAnnotations(),
	}, manageDealHandler(c, companyDomain, opts.DryRun))
}

// manageDealHandler dispatches on action. opts.DryRun is a floor: a
// per-call dry_run can turn a rehearsal on and nothing can turn one off.
func manageDealHandler(c dealsClient, companyDomain string, dryRun bool) mcp.ToolHandlerFor[manageDealInput, manageDealOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in manageDealInput) (*mcp.CallToolResult, manageDealOutput, error) {
		if err := validateAction(in.Action, dealActions); err != nil {
			return errorResult(err), manageDealOutput{}, nil
		}
		in.DryRun = in.DryRun || dryRun

		var (
			res *mcp.CallToolResult
			out manageDealOutput
		)
		switch {
		case dealActions[in.Action].creates:
			res, out = createDealAction(ctx, c, companyDomain, in)
		case dealActions[in.Action].deletes:
			res, out = deleteDealAction(ctx, c, companyDomain, in)
		default:
			res, out = writeDealAction(ctx, c, companyDomain, in)
		}
		if res == nil {
			out.Action = in.Action
			out.DryRun = in.DryRun
		}
		return res, out, nil
	}
}

func createDealAction(ctx context.Context, c dealsClient, companyDomain string, in manageDealInput) (*mcp.CallToolResult, manageDealOutput) {
	if in.Title == nil || *in.Title == "" {
		return errorResult(fmt.Errorf("%w: title must not be empty", pipedrive.ErrValidation)), manageDealOutput{}
	}
	cf, err := c.EncodeDealCustomFields(ctx, in.CustomFields)
	if err != nil {
		return errorResult(err), manageDealOutput{}
	}
	req := pipedrive.CreateDealRequest{
		Title:             *in.Title,
		Value:             deref(in.Value),
		Currency:          deref(in.Currency),
		PipelineID:        deref(in.PipelineID),
		StageID:           deref(in.StageID),
		OwnerID:           deref(in.OwnerID),
		PersonID:          deref(in.PersonID),
		OrgID:             deref(in.OrgID),
		ExpectedCloseDate: deref(in.ExpectedCloseDate),
		Probability:       in.Probability,
		CustomFields:      cf.Values,
	}
	created := syntheticDealFromRequest(req)
	if !in.DryRun {
		c, err := c.CreateDeal(ctx, req)
		if err != nil {
			return errorResult(err), manageDealOutput{}
		}
		created = c
	}
	// resolvedX on both paths, so a dry-run preview and a real create
	// describe their custom fields the same way: the synthetic record
	// carries the custom fields the write would set, and the rehearsal
	// reports them under the names the real create would use.
	return nil, manageDealOutput{
		Deal:    resolvedDeal(ctx, c, companyDomain, created),
		Changed: changedFields(dealSpec(cf), &pipedrive.Deal{}, created),
	}
}

// writeDealAction serves update and every transition. They share
// everything but which request they build and whether the overwrite
// guard applies, so splitting them further would duplicate the
// read-guard-write spine four more times.
func writeDealAction(ctx context.Context, c dealsClient, companyDomain string, in manageDealInput) (*mcp.CallToolResult, manageDealOutput) {
	if err := validatePositiveID(in.DealID, "deal_id"); err != nil {
		return errorResult(err), manageDealOutput{}
	}
	req, custom, err := dealRequestFor(in)
	if err != nil {
		return errorResult(err), manageDealOutput{}
	}
	cf, err := c.EncodeDealCustomFields(ctx, custom)
	if err != nil {
		return errorResult(err), manageDealOutput{}
	}
	req.CustomFields = cf.Values

	// Pipedrive refuses every edit to an archived deal except unarchiving
	// it, and answers with a bare 403. The read this write already does
	// can say so first, in words the caller can act on — which is the
	// whole of the guarded-write contract.
	deal, changed, res := guardedWrite[pipedrive.Deal]{
		Spec:          dealBaseFields,
		Custom:        cf,
		CustomOf:      func(d *pipedrive.Deal) *map[string]any { return &d.CustomFields },
		Resource:      fmt.Sprintf("deal %d", in.DealID),
		ExpectVersion: in.ExpectVersion,
		Version:       func(d *pipedrive.Deal) string { return d.UpdateTime },
		// A transition authorises its own overwrite; see dealAction.
		Overwrite: in.Overwrite || dealActions[in.Action].transition,
		DryRun:    in.DryRun,
		Get: func(ctx context.Context) (*pipedrive.Deal, error) {
			d, err := c.GetDeal(ctx, in.DealID)
			if err != nil {
				return nil, err
			}
			// Pipedrive answers every other edit to an archived deal
			// with a bare 403. The read this write already does can say
			// so first, and name the action that fixes it.
			if d.IsArchived && in.Action != "unarchive" {
				return nil, refuse(
					fmt.Sprintf("deal %d is archived, and Pipedrive accepts no edit to an archived deal", in.DealID),
					`action: "unarchive"`)
			}
			return d, nil
		},
		Predict: func(d *pipedrive.Deal) pipedrive.Deal { return dealAfterUpdate(*d, req) },
		Put:     func(ctx context.Context) (*pipedrive.Deal, error) { return c.UpdateDeal(ctx, in.DealID, req) },
	}.run(ctx)
	if res != nil {
		return res, manageDealOutput{}
	}
	return nil, manageDealOutput{Deal: resolvedDeal(ctx, c, companyDomain, deal), Changed: changed}
}

// dealRequestFor turns an action plus its inputs into the PATCH body,
// plus the custom fields that action sends — unencoded, because encoding
// needs a context and can fail, and this is a pure function of the
// input.
//
// The transitions ignore the descriptive fields entirely: mark_won is
// "status=won" and nothing else, so a caller who also passed a title
// does not silently get it written. Custom fields are one of those
// descriptive fields, and returning them from the same switch is what
// stops "which actions send custom fields" being answered in two places
// that can disagree — the caller encodes what this hands back, so a
// transition neither sends them nor is refused over them.
func dealRequestFor(in manageDealInput) (pipedrive.UpdateDealRequest, map[string]any, error) {
	switch in.Action {
	case "move_stage":
		if in.StageID == nil || *in.StageID <= 0 {
			return pipedrive.UpdateDealRequest{}, nil, fmt.Errorf("%w: move_stage needs stage_id; list_stages reports them", pipedrive.ErrValidation)
		}
		req := pipedrive.UpdateDealRequest{StageID: in.StageID}
		// A stage belongs to exactly one pipeline, so moving across
		// pipelines needs both or Pipedrive rejects the pair.
		if in.PipelineID != nil {
			req.PipelineID = in.PipelineID
		}
		return req, nil, nil
	case "mark_won":
		return pipedrive.UpdateDealRequest{Status: ptr("won")}, nil, nil
	case "mark_lost":
		req := pipedrive.UpdateDealRequest{Status: ptr("lost")}
		if in.LostReason != nil {
			req.LostReason = in.LostReason
		}
		return req, nil, nil
	case "archive":
		return pipedrive.UpdateDealRequest{IsArchived: ptr(true)}, nil, nil
	case "unarchive":
		return pipedrive.UpdateDealRequest{IsArchived: ptr(false)}, nil, nil
	case "reopen":
		// Clearing the lost reason with the status keeps the record
		// honest: a deal that is open again was not lost for a reason.
		return pipedrive.UpdateDealRequest{
			Status:     ptr(dealStatusOpen),
			LostReason: ptr(""),
		}, nil, nil
	default: // update
		req := pipedrive.UpdateDealRequest{
			Title:             in.Title,
			Value:             in.Value,
			Probability:       in.Probability,
			Currency:          in.Currency,
			PipelineID:        in.PipelineID,
			StageID:           in.StageID,
			OwnerID:           in.OwnerID,
			PersonID:          in.PersonID,
			OrgID:             in.OrgID,
			ExpectedCloseDate: in.ExpectedCloseDate,
		}
		return req, in.CustomFields, nil
	}
}

// dealSpec is the base table plus the custom fields a write names. The
// create path needs it because a create has no target to guard and so
// does not go through guardedWrite, which does this itself.
func dealSpec(cf pipedrive.CustomFieldWrite) []fieldSpec[pipedrive.Deal] {
	return withCustomFields(dealBaseFields, cf,
		func(d *pipedrive.Deal) map[string]any { return d.CustomFields })
}

// dealAfterUpdate overlays what the request sets onto the deal as it
// stands, so the dry-run prediction and the post-write report come from
// the same diff instead of two walks that must be kept agreeing.
func dealAfterUpdate(before pipedrive.Deal, req pipedrive.UpdateDealRequest) pipedrive.Deal {
	after := before
	setIf(&after.Title, req.Title)
	setIf(&after.Value, req.Value)
	setIf(&after.Status, req.Status)
	setIf(&after.Currency, req.Currency)
	setIf(&after.PipelineID, req.PipelineID)
	setIf(&after.StageID, req.StageID)
	setIf(&after.OwnerID, req.OwnerID)
	setIf(&after.PersonID, req.PersonID)
	setIf(&after.OrgID, req.OrgID)
	setIf(&after.ExpectedCloseDate, req.ExpectedCloseDate)
	setIf(&after.LostReason, req.LostReason)
	setIf(&after.IsArchived, req.IsArchived)
	if req.Probability != nil {
		// Copy rather than alias, so the predicted record does not
		// share a pointer with the request.
		after.Probability = clone(req.Probability)
	}
	return after
}

// syntheticDealFromRequest builds a placeholder Deal that mirrors the
// CreateDealRequest, used only on the dry-run path so manage_deal's
// output schema stays consistent. ID=0 + dry_run=true tells the LLM
// nothing was actually persisted. Probability is copied so the
// synthetic deal doesn't alias the request's pointer.
func syntheticDealFromRequest(req pipedrive.CreateDealRequest) *pipedrive.Deal {
	return &pipedrive.Deal{
		CustomFields:      req.CustomFields,
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
		Probability:       clone(req.Probability),
	}
}

// dealFieldResolver is the narrow slice of a client that resolvedDeal needs, so the
// resource templates can share the composition rather than repeating it.
type dealFieldResolver interface {
	ResolveDealCustomFields(ctx context.Context, raw map[string]any) map[string]any
}

// resolvedDeal resolves custom-field hashes to workspace names and summarizes
// in one step. Written out separately at six sites per resource, a
// forgotten resolve silently shipped 40-char field hashes to the LLM
// instead of the names it can actually cite.
func resolvedDeal(ctx context.Context, c dealFieldResolver, domain string, r *pipedrive.Deal) dealSummary {
	return summarizeDeal(domain, r, c.ResolveDealCustomFields(ctx, r.CustomFields))
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
		Probability:       clone(d.Probability),
		IsArchived:        d.IsArchived,
		CustomFields:      customFields,
		URL:               pipedrive.WebURL(domain, pipedrive.WebURLDeal, d.ID),
	}
}

// deleteDealAction marks a deal deleted.
//
// No permitting argument beyond dry_run and expect_version, and that is
// the rule rather than an oversight: Pipedrive's delete is soft and
// time-boxed, which is the reversible case, and a guard is only added
// where the caller cannot see what they are about to lose. The read
// this performs puts the deal on screen first.
//
// What it does NOT do is pretend to know what becomes of the notes and
// activities hanging off the deal. Pipedrive does not document that and
// nothing here has verified it, so the description warns rather than
// the code guarding against a behaviour nobody has established.
func deleteDealAction(ctx context.Context, c dealsClient, companyDomain string, in manageDealInput) (*mcp.CallToolResult, manageDealOutput) {
	if err := validatePositiveID(in.DealID, "deal_id"); err != nil {
		return errorResult(err), manageDealOutput{}
	}
	before, err := c.GetDeal(ctx, in.DealID)
	if err != nil {
		return errorResult(err), manageDealOutput{}
	}
	if err := checkExpectVersion(in.ExpectVersion, before.UpdateTime, fmt.Sprintf("deal %d", in.DealID)); err != nil {
		return errorResult(err), manageDealOutput{}
	}
	// Pipedrive models a deleted deal as a status rather than a flag —
	// open | won | lost | deleted — so this is the already-gone check.
	if before.Status == "deleted" {
		// Report the state rather than firing a second DELETE that
		// changes nothing.
		return nil, manageDealOutput{Deal: resolvedDeal(ctx, c, companyDomain, before)}
	}
	if !in.DryRun {
		if err := c.DeleteDeal(ctx, in.DealID); err != nil {
			return errorResult(err), manageDealOutput{}
		}
	}
	return nil, manageDealOutput{Deal: resolvedDeal(ctx, c, companyDomain, before), Changed: []string{"status"}}
}

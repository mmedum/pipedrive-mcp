package tools

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

type leadsClient interface {
	GetLead(ctx context.Context, id string) (*pipedrive.Lead, error)
	ListLeads(ctx context.Context, opts pipedrive.ListLeadsOptions) ([]pipedrive.Lead, *pipedrive.V1Pagination, error)
	CreateLead(ctx context.Context, req pipedrive.LeadRequest) (*pipedrive.Lead, error)
	UpdateLead(ctx context.Context, id string, req pipedrive.LeadRequest) (*pipedrive.Lead, error)
	DeleteLead(ctx context.Context, id string) error
	ListLeadLabels(ctx context.Context) ([]pipedrive.LeadLabel, error)
}

var allowedLeadSortFields = map[string]bool{"add_time": true, "update_time": true}

type leadValueRow struct {
	Amount   float64 `json:"amount" jsonschema:"the lead's potential value"`
	Currency string  `json:"currency" jsonschema:"ISO 4217 code such as EUR or DKK"`
}

type leadSummary struct {
	ID                string        `json:"id" jsonschema:"the lead's id, a UUID; empty on a dry-run create"`
	Title             string        `json:"title" jsonschema:"the lead's title"`
	OwnerID           int64         `json:"owner_id" jsonschema:"id of the user who owns the lead"`
	PersonID          int64         `json:"person_id,omitempty" jsonschema:"the linked person, 0 if none"`
	OrgID             int64         `json:"org_id,omitempty" jsonschema:"the linked organization, 0 if none"`
	Value             *leadValueRow `json:"value,omitempty" jsonschema:"the lead's potential value, absent when unset"`
	ExpectedCloseDate string        `json:"expected_close_date,omitempty" jsonschema:"YYYY-MM-DD the lead is expected to close"`
	LabelIDs          []string      `json:"label_ids,omitempty" jsonschema:"the lead's labels; list_lead_labels names them"`
	IsArchived        bool          `json:"is_archived,omitempty" jsonschema:"true when the lead is archived"`
	SourceName        string        `json:"source_name,omitempty" jsonschema:"where the lead came from, such as API or Web forms"`
	AddTime           string        `json:"add_time,omitempty" jsonschema:"timestamp the lead was created"`
	UpdateTime        string        `json:"update_time,omitempty" jsonschema:"timestamp the lead was last updated; expect_version takes it"`
}

type getLeadInput struct {
	LeadID string `json:"lead_id" jsonschema:"the lead's id, a UUID; search with type=lead or list_leads reports it"`
}

type getLeadOutput struct {
	Lead leadSummary `json:"lead" jsonschema:"the requested lead"`
}

type listLeadsInput struct {
	OwnerID       int64  `json:"owner_id,omitempty" jsonschema:"return only leads owned by this user id; 0 = no filter"`
	PersonID      int64  `json:"person_id,omitempty" jsonschema:"return only leads linked to this person; 0 = no filter"`
	OrgID         int64  `json:"org_id,omitempty" jsonschema:"return only leads linked to this organization; 0 = no filter"`
	UpdatedSince  string `json:"updated_since,omitempty" jsonschema:"RFC3339 timestamp; return only leads updated at or after this time. Not available with archived"`
	SortBy        string `json:"sort_by,omitempty" jsonschema:"add_time | update_time. Default 'update_time' (most-recently-touched first)."`
	SortDirection string `json:"sort_direction,omitempty" jsonschema:"asc | desc. Default 'desc' when sort_by is omitted; 'asc' otherwise."`
	Archived      bool   `json:"archived,omitempty" jsonschema:"read the archived leads instead, a separate collection no other list reaches"`
	Limit         int    `json:"limit,omitempty" jsonschema:"page size; default 25, max 100"`
	Cursor        string `json:"cursor,omitempty" jsonschema:"opaque pagination token from a previous list_leads response; omit for the first page"`
}

type listLeadsOutput struct {
	Leads      []leadSummary `json:"leads" jsonschema:"matching leads on this page"`
	NextCursor string        `json:"next_cursor,omitempty" jsonschema:"pass to the next list_leads call to get the next page; empty when there are no more pages"`
}

type leadLabelRow struct {
	ID    string `json:"id" jsonschema:"the label's id, as a lead's label_ids carry it"`
	Name  string `json:"name" jsonschema:"the label's name"`
	Color string `json:"color,omitempty" jsonschema:"the label's color"`
}

type listLeadLabelsOutput struct {
	Labels []leadLabelRow `json:"labels" jsonschema:"every lead label in the workspace"`
}

type manageLeadInput struct {
	Action            string        `json:"action" jsonschema:"create, update, archive, unarchive or delete"`
	LeadID            string        `json:"lead_id,omitempty" jsonschema:"the lead to act on, a UUID, required by every action except create"`
	Title             *string       `json:"title,omitempty" jsonschema:"the lead's title, required by create; if the user did not give one, ask rather than inventing it"`
	OwnerID           *int64        `json:"owner_id,omitempty" jsonschema:"the user who owns the lead; omit on create to take the API token's own user"`
	PersonID          *int64        `json:"person_id,omitempty" jsonschema:"the linked person; create needs this or org_id. Omit to leave it as it is"`
	OrgID             *int64        `json:"org_id,omitempty" jsonschema:"the linked organization; create needs this or person_id. Omit to leave it as it is"`
	Value             *leadValueRow `json:"value,omitempty" jsonschema:"the lead's potential value, amount and currency together. Omit to leave it as it is"`
	ExpectedCloseDate *string       `json:"expected_close_date,omitempty" jsonschema:"YYYY-MM-DD the lead is expected to close. Omit to leave it as it is"`
	LabelIDs          []string      `json:"label_ids,omitempty" jsonschema:"the lead's labels; list_lead_labels reports the ids. A write REPLACES the whole list; omit to leave it as it is"`
	DryRun            bool          `json:"dry_run,omitempty" jsonschema:"report what the write would find and change, and send nothing"`
	Overwrite         []string      `json:"overwrite,omitempty" jsonschema:"the fields this update may replace, named exactly as the refusal listed them, e.g. [\"title\"]. A refusal is NOT a retry signal: name a field only when the user asked for what is there to be replaced"`
	ExpectVersion     string        `json:"expect_version,omitempty" jsonschema:"the update_time from the read that informed this write; the write is refused if the lead changed since"`
	Confirm           bool          `json:"confirm,omitempty" jsonschema:"true to delete for good. Pipedrive keeps no copy of a deleted lead and nothing here restores it; set it only when the user asked for this lead to go"`
}

type manageLeadOutput struct {
	Action  string      `json:"action" jsonschema:"the action that ran"`
	Lead    leadSummary `json:"lead" jsonschema:"the lead as Pipedrive stored it. For delete, as it was just before. When dry_run is true nothing was persisted and a created lead has no id"`
	Changed []string    `json:"changed,omitempty" jsonschema:"names of the fields this write altered, or on a dry run would alter"`
	Deleted bool        `json:"deleted,omitempty" jsonschema:"true when the lead was deleted, or on a dry run would be"`
	DryRun  bool        `json:"dry_run,omitempty" jsonschema:"true when nothing was sent upstream"`
}

var allowedLeadActions = map[string]bool{"create": true, "update": true, "archive": true, "unarchive": true, "delete": true}

// leadFields drives the update's diff and overwrite guard (D-38).
var leadFields = []fieldSpec[pipedrive.Lead]{
	{"title", func(l *pipedrive.Lead) string { return l.Title }},
	{"owner_id", func(l *pipedrive.Lead) string { return projectID(l.OwnerID) }},
	{"person_id", func(l *pipedrive.Lead) string { return projectOptID(l.PersonID) }},
	{"org_id", func(l *pipedrive.Lead) string { return projectOptID(l.OrganizationID) }},
	{"value", func(l *pipedrive.Lead) string { return projectLeadValue(l.Value) }},
	{"expected_close_date", func(l *pipedrive.Lead) string { return l.ExpectedCloseDate }},
	{"label_ids", func(l *pipedrive.Lead) string {
		return projectCollection(slices.Sorted(slices.Values(l.LabelIDs)), strconv.Quote)
	}},
	{"is_archived", func(l *pipedrive.Lead) string { return projectBool(l.IsArchived) }},
}

func projectLeadValue(v *pipedrive.LeadValue) string {
	if v == nil {
		return ""
	}
	return projectMoney(v.Amount) + " " + v.Currency
}

// RegisterLeads wires the lead tools. They run on Pipedrive v1, which
// has the only lead endpoints beyond search (D-105), and sit outside the
// compatibility promise as the other v1 tools do (D-04).
func RegisterLeads(s *mcp.Server, c leadsClient, opts RegisterOptions) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_lead",
		Description: "Fetch one lead by its lead_id, a UUID: title, owner, the person and organization it is linked to, its potential value, expected close date, labels and whether it is archived. A lead is a prospect not yet worth a deal. Leads come from Pipedrive v1. Custom fields are not read here. Use search with type=lead to find a lead by name.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getLeadInput) (*mcp.CallToolResult, getLeadOutput, error) {
		if err := validateLeadID(in.LeadID); err != nil {
			return errorResult(err), getLeadOutput{}, nil
		}
		l, err := c.GetLead(ctx, in.LeadID)
		if err != nil {
			return errorResult(err), getLeadOutput{}, nil
		}
		return nil, getLeadOutput{Lead: summarizeLead(l)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_leads",
		Description: "List leads by owner, linked person or organization, or update window. Default order is update_time descending. Default limit is 25, max 100. IMPORTANT: a page holding fewer rows than the limit is NOT the end — keep paging while next_cursor comes back non-empty. Archived leads are NOT in this collection; pass archived to read them. Leads come from Pipedrive v1; the cursor wraps v1's offset, so pass it back verbatim.",
		Annotations: readOnlyAnnotations(),
	}, listLeadsHandler(c))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_lead_labels",
		Description: "List every lead label in the workspace: the id a lead's label_ids carries, its name and color. One call, no paging.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listLeadLabelsOutput, error) {
		labels, err := c.ListLeadLabels(ctx)
		if err != nil {
			return errorResult(err), listLeadLabelsOutput{}, nil
		}
		out := listLeadLabelsOutput{Labels: make([]leadLabelRow, 0, len(labels))}
		for _, l := range labels {
			out.Labels = append(out.Labels, leadLabelRow(l))
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "manage_lead",
		Description: "Create a lead, edit one, archive or unarchive one, or delete one. create takes title and at least one of person_id or org_id; every other action takes lead_id and reads the lead first. An update refuses to replace ANY field that already holds a value unless you name it in overwrite, and the refusal names each one; label_ids is one list, replaced whole. archive and unarchive take no overwrite: archiving moves the lead to the archive, which list_leads reaches with archived. Deleting is PERMANENT: Pipedrive keeps no copy, so delete needs confirm: true, and dry_run shows what would go without it. To move a lead on to a deal, create the deal with manage_deal; converting a lead is not done here. Leads come from Pipedrive v1, and these tools leave in a minor release if Pipedrive retires it.",
		Annotations: mutatingAnnotations(),
	}, manageLeadHandler(c, opts.DryRun))
}

func listLeadsHandler(c leadsClient) mcp.ToolHandlerFor[listLeadsInput, listLeadsOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in listLeadsInput) (*mcp.CallToolResult, listLeadsOutput, error) {
		if err := validateEnum(in.SortBy, "sort_by", allowedLeadSortFields); err != nil {
			return errorResult(err), listLeadsOutput{}, nil
		}
		if err := validateEnum(in.SortDirection, "sort_direction", allowedSortDirections); err != nil {
			return errorResult(err), listLeadsOutput{}, nil
		}
		if in.Archived && in.UpdatedSince != "" {
			return errorResult(fmt.Errorf("%w: the archived leads take no updated_since", pipedrive.ErrValidation)), listLeadsOutput{}, nil
		}
		listing := "leads"
		if in.Archived {
			listing = "leads:archived"
		}
		start, err := decodeV1Cursor(listing, in.Cursor)
		if err != nil {
			return errorResult(err), listLeadsOutput{}, nil
		}
		sortBy, sortDir := effectiveSort(in.SortBy, in.SortDirection)
		leads, page, err := c.ListLeads(ctx, pipedrive.ListLeadsOptions{
			OwnerID: in.OwnerID, PersonID: in.PersonID, OrganizationID: in.OrgID, UpdatedSince: in.UpdatedSince,
			Sort: sortBy + " " + strings.ToUpper(sortDir), Start: start, Limit: clampLimit(in.Limit), Archived: in.Archived,
		})
		if err != nil {
			return errorResult(err), listLeadsOutput{}, nil
		}
		out := listLeadsOutput{Leads: make([]leadSummary, 0, len(leads)), NextCursor: encodeV1Cursor(listing, page)}
		for i := range leads {
			out.Leads = append(out.Leads, summarizeLead(&leads[i]))
		}
		return nil, out, nil
	}
}

// validateLeadID refuses an id that cannot be a lead's UUID before it
// reaches a path.
func validateLeadID(id string) error {
	if len(id) != 36 || strings.Trim(id, "0123456789abcdefABCDEF-") != "" {
		return fmt.Errorf("%w: lead_id %q is not a lead's UUID; search with type=lead or list_leads reports it", pipedrive.ErrValidation, id)
	}
	return nil
}

func summarizeLead(l *pipedrive.Lead) leadSummary {
	out := leadSummary{
		ID: l.ID, Title: l.Title, OwnerID: l.OwnerID, PersonID: deref(l.PersonID), OrgID: deref(l.OrganizationID),
		ExpectedCloseDate: l.ExpectedCloseDate, LabelIDs: slices.Clone(l.LabelIDs), IsArchived: l.IsArchived,
		SourceName: l.SourceName, AddTime: l.AddTime, UpdateTime: l.UpdateTime,
	}
	if l.Value != nil {
		out.Value = &leadValueRow{Amount: l.Value.Amount, Currency: l.Value.Currency}
	}
	return out
}

func manageLeadHandler(c leadsClient, dryRun bool) mcp.ToolHandlerFor[manageLeadInput, manageLeadOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in manageLeadInput) (*mcp.CallToolResult, manageLeadOutput, error) {
		if err := validateAction(in.Action, allowedLeadActions); err != nil {
			return errorResult(err), manageLeadOutput{}, nil
		}
		if err := validateLeadInput(in); err != nil {
			return errorResult(err), manageLeadOutput{}, nil
		}
		in.DryRun = in.DryRun || dryRun
		var (
			res *mcp.CallToolResult
			out manageLeadOutput
		)
		switch in.Action {
		case "create":
			res, out = createLeadAction(ctx, c, in)
		case "update", "archive", "unarchive":
			res, out = writeLeadAction(ctx, c, in)
		case "delete":
			res, out = deleteLeadAction(ctx, c, in)
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

func validateLeadInput(in manageLeadInput) error {
	if in.Action != "create" {
		if err := validateLeadID(in.LeadID); err != nil {
			return err
		}
	}
	for name, id := range map[string]*int64{"owner_id": in.OwnerID, "person_id": in.PersonID, "org_id": in.OrgID} {
		if id != nil {
			if err := validatePositiveID(*id, name); err != nil {
				return err
			}
		}
	}
	if in.Value != nil && (!(in.Value.Amount >= 0) || len(in.Value.Currency) != 3) {
		return fmt.Errorf("%w: value needs an amount of zero or more and a three-letter currency", pipedrive.ErrValidation)
	}
	if in.ExpectedCloseDate != nil {
		if _, err := time.Parse(time.DateOnly, *in.ExpectedCloseDate); err != nil {
			return fmt.Errorf("%w: expected_close_date %q is not a YYYY-MM-DD date", pipedrive.ErrValidation, *in.ExpectedCloseDate)
		}
	}
	if in.LabelIDs != nil && len(in.LabelIDs) == 0 {
		return fmt.Errorf("%w: label_ids cannot be emptied here; omit it to leave the labels as they are", pipedrive.ErrValidation)
	}
	return nil
}

func leadRequestFor(in manageLeadInput) pipedrive.LeadRequest {
	req := pipedrive.LeadRequest{
		Title: in.Title, OwnerID: in.OwnerID, PersonID: in.PersonID, OrganizationID: in.OrgID,
		ExpectedCloseDate: in.ExpectedCloseDate, LabelIDs: in.LabelIDs,
	}
	if in.Value != nil {
		req.Value = &pipedrive.LeadValue{Amount: in.Value.Amount, Currency: strings.ToUpper(in.Value.Currency)}
	}
	return req
}

func leadAfter(before pipedrive.Lead, req pipedrive.LeadRequest) pipedrive.Lead {
	after := before
	setIf(&after.Title, req.Title)
	setIf(&after.OwnerID, req.OwnerID)
	if req.PersonID != nil {
		after.PersonID = clone(req.PersonID)
	}
	if req.OrganizationID != nil {
		after.OrganizationID = clone(req.OrganizationID)
	}
	if req.Value != nil {
		after.Value = clone(req.Value)
	}
	setIf(&after.ExpectedCloseDate, req.ExpectedCloseDate)
	if req.LabelIDs != nil {
		after.LabelIDs = slices.Clone(req.LabelIDs)
	}
	setIf(&after.IsArchived, req.IsArchived)
	return after
}

func createLeadAction(ctx context.Context, c leadsClient, in manageLeadInput) (*mcp.CallToolResult, manageLeadOutput) {
	if in.Title == nil || strings.TrimSpace(*in.Title) == "" {
		return errorResult(fmt.Errorf("%w: title must not be empty", pipedrive.ErrValidation)), manageLeadOutput{}
	}
	if in.PersonID == nil && in.OrgID == nil {
		return errorResult(fmt.Errorf("%w: a lead has to be linked to a person or an organization: set person_id, org_id or both", pipedrive.ErrValidation)), manageLeadOutput{}
	}
	req := leadRequestFor(in)
	created := leadAfter(pipedrive.Lead{}, req)
	result := &created
	if !in.DryRun {
		var err error
		if result, err = c.CreateLead(ctx, req); err != nil {
			return errorResult(err), manageLeadOutput{}
		}
	}
	return nil, manageLeadOutput{Lead: summarizeLead(result), Changed: changedFields(leadFields, &pipedrive.Lead{}, result)}
}

// writeLeadAction serves update and the archive transitions, which name
// their own field and so take no overwrite (D-43).
func writeLeadAction(ctx context.Context, c leadsClient, in manageLeadInput) (*mcp.CallToolResult, manageLeadOutput) {
	req := leadRequestFor(in)
	transition := in.Action != "update"
	if transition && leadHasEditFields(in) {
		return errorResult(fmt.Errorf("%w: %s changes is_archived alone; send other fields in an update", pipedrive.ErrValidation, in.Action)), manageLeadOutput{}
	}
	if transition {
		req = pipedrive.LeadRequest{IsArchived: ptr(in.Action == "archive")}
	}
	lead, changed, res := guardedWrite[pipedrive.Lead]{
		Spec:          leadFields,
		Resource:      "lead " + in.LeadID,
		ExpectVersion: in.ExpectVersion,
		Version:       func(l *pipedrive.Lead) string { return l.UpdateTime },
		Overwrite:     in.Overwrite,
		OverwriteAll:  transition,
		DryRun:        in.DryRun,
		Get:           func(ctx context.Context) (*pipedrive.Lead, error) { return c.GetLead(ctx, in.LeadID) },
		Predict:       func(l *pipedrive.Lead) pipedrive.Lead { return leadAfter(*l, req) },
		Put:           func(ctx context.Context) (*pipedrive.Lead, error) { return c.UpdateLead(ctx, in.LeadID, req) },
	}.run(ctx)
	if res != nil {
		return res, manageLeadOutput{}
	}
	return nil, manageLeadOutput{Lead: summarizeLead(lead), Changed: changed}
}

func leadHasEditFields(in manageLeadInput) bool {
	return in.Title != nil || in.OwnerID != nil || in.PersonID != nil || in.OrgID != nil ||
		in.Value != nil || in.ExpectedCloseDate != nil || in.LabelIDs != nil
}

// deleteLeadAction deletes a lead for good, so it takes confirm (D-99); a
// dry run needs none.
func deleteLeadAction(ctx context.Context, c leadsClient, in manageLeadInput) (*mcp.CallToolResult, manageLeadOutput) {
	before, err := c.GetLead(ctx, in.LeadID)
	if err != nil {
		return errorResult(err), manageLeadOutput{}
	}
	if err = checkExpectVersion(in.ExpectVersion, before.UpdateTime, "lead "+in.LeadID); err != nil {
		return errorResult(err), manageLeadOutput{}
	}
	if !in.DryRun && !in.Confirm {
		return errorResult(refuse(
			fmt.Sprintf("deleting lead %s (%q) is permanent: Pipedrive keeps no copy and nothing here restores it", in.LeadID, before.Title),
			"confirm: true")), manageLeadOutput{}
	}
	if !in.DryRun {
		if err = c.DeleteLead(ctx, in.LeadID); err != nil {
			return errorResult(err), manageLeadOutput{}
		}
	}
	return nil, manageLeadOutput{Lead: summarizeLead(before), Deleted: true}
}

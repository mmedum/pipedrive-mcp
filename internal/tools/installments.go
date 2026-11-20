package tools

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

type installmentsClient interface {
	GetDeal(ctx context.Context, id int64) (*pipedrive.Deal, error)
	ListDeals(ctx context.Context, opts pipedrive.ListDealsOptions) ([]pipedrive.Deal, string, error)
	ListInstallments(ctx context.Context, dealIDs []int64, limit int, cursor string) ([]pipedrive.Installment, string, error)
	ListDealProducts(ctx context.Context, dealID int64) ([]pipedrive.DealProduct, error)
	CreateInstallment(ctx context.Context, dealID int64, req pipedrive.InstallmentRequest) (*pipedrive.Installment, error)
	UpdateInstallment(ctx context.Context, dealID, id int64, req pipedrive.InstallmentRequest) (*pipedrive.Installment, error)
	DeleteInstallment(ctx context.Context, dealID, id int64) error
}

// installmentLister is the read get_deal adds; dealsClient embeds it.
type installmentLister interface {
	ListInstallments(ctx context.Context, dealIDs []int64, limit int, cursor string) ([]pipedrive.Installment, string, error)
}

var allowedInstallmentActions = map[string]bool{
	"create": true,
	"update": true,
	"delete": true,
}

// installmentPageSize is Pipedrive's largest page for the list, and
// installmentMaxPages bounds how far one tool call follows the cursor.
// A schedule is read whole because its sum is only meaningful whole.
const (
	installmentPageSize = 500
	installmentMaxPages = 10
)

// The won rule is the reason the tool text names it: once a deal is won,
// its schedule may already have been turned into invoices by whatever
// bills from Pipedrive, and an edit here does not follow it there.
const wonWarning = "deal %d is won: its installments may already have become invoices in the system that bills from Pipedrive, so change amounts and dates there, not here"

type installmentSummary struct {
	ID          int64   `json:"id" jsonschema:"the installment's numeric id; 0 on a dry-run create"`
	DealID      int64   `json:"deal_id" jsonschema:"the deal this installment belongs to"`
	Description string  `json:"description" jsonschema:"what the payment is for, e.g. Annual license fee, year 1"`
	Amount      float64 `json:"amount" jsonschema:"the amount billed, in currency"`
	Currency    string  `json:"currency,omitempty" jsonschema:"the deal's ISO 4217 currency, which every installment of the deal is billed in; empty when the deal could not be read"`
	BillingDate string  `json:"billing_date" jsonschema:"YYYY-MM-DD the installment is billed"`
	Version     string  `json:"version" jsonschema:"a fingerprint of the amount, date and description; pass it as expect_version to refuse a write if the installment changed since you read it"`
}

type installmentSchedule struct {
	DealID       int64                `json:"deal_id" jsonschema:"the deal"`
	DealTitle    string               `json:"deal_title,omitempty" jsonschema:"the deal's title"`
	DealStatus   string               `json:"deal_status,omitempty" jsonschema:"open | won | lost | deleted"`
	DealValue    float64              `json:"deal_value" jsonschema:"the deal's value, to compare with sum"`
	Currency     string               `json:"currency,omitempty" jsonschema:"the deal's currency, which value, sum and every amount are in"`
	Installments []installmentSummary `json:"installments" jsonschema:"every installment of the deal, oldest billing date first"`
	Sum          float64              `json:"sum" jsonschema:"the installments' amounts added up"`
	Warnings     []string             `json:"warnings,omitempty" jsonschema:"what a person should look at: a sum that differs from the deal value, a won deal, a duplicate description"`
}

type listInstallmentsInput struct {
	DealIDs []int64 `json:"deal_ids" jsonschema:"the deals to read, at most 100; search or list_deals reports the ids"`
}

type listInstallmentsOutput struct {
	Deals []installmentSchedule `json:"deals" jsonschema:"one schedule per deal asked for, in the order asked"`
}

type manageInstallmentInput struct {
	Action        string   `json:"action" jsonschema:"create, update or delete"`
	DealID        int64    `json:"deal_id" jsonschema:"the deal the installment belongs to, required by every action"`
	InstallmentID int64    `json:"installment_id,omitempty" jsonschema:"the installment to act on, required by update and delete; get_deal and list_installments report it"`
	Description   *string  `json:"description,omitempty" jsonschema:"what the payment is for, required by create. Omit on update to leave it as it is"`
	Amount        *float64 `json:"amount,omitempty" jsonschema:"the amount to bill, above zero and in the deal's currency, required by create. Omit on update to leave it as it is"`
	BillingDate   *string  `json:"billing_date,omitempty" jsonschema:"YYYY-MM-DD to bill on, required by create. Omit on update to leave it as it is"`
	DryRun        bool     `json:"dry_run,omitempty" jsonschema:"report what the write would find and change, and the schedule it would leave, and send nothing"`
	Overwrite     []string `json:"overwrite,omitempty" jsonschema:"the fields this update may replace, named exactly as the refusal listed them, e.g. [\"amount\"]. Every installment field holds a value, so an update that changes one is refused until you name it. A refusal is NOT a retry signal: name a field only when the user asked for what is there to be replaced"`
	ExpectVersion string   `json:"expect_version,omitempty" jsonschema:"the installment's version from the read that informed this write; update and delete are refused if it changed since"`
	Confirm       bool     `json:"confirm,omitempty" jsonschema:"true to delete for good. Pipedrive keeps no copy of a deleted installment and nothing here restores it; set it only when the user asked for this installment to go"`
}

type manageInstallmentOutput struct {
	Action      string              `json:"action" jsonschema:"the action that ran"`
	Installment installmentSummary  `json:"installment" jsonschema:"the installment as stored. For delete, as it was just before it went. On a dry-run create its id is 0"`
	Changed     []string            `json:"changed,omitempty" jsonschema:"the fields this write altered, or on a dry run would alter; empty when nothing changed"`
	Deleted     bool                `json:"deleted,omitempty" jsonschema:"true when the installment was deleted, or on a dry run would be"`
	DryRun      bool                `json:"dry_run,omitempty" jsonschema:"true when nothing was sent upstream"`
	Schedule    installmentSchedule `json:"schedule" jsonschema:"the deal's whole schedule after the write, read back from Pipedrive; on a dry run, the schedule the write would leave"`
}

const installmentsPurpose = "Installments are a deal's payment schedule: each one is an amount billed on a date, in the deal's currency, and together they say in which month the deal's money arrives. "

const installmentRules = "Pipedrive offers installments on its Growth plan and above, and only on a deal with at least one one-time product and no recurring product. "

// RegisterInstallments wires list_installments and manage_installment.
// get_deal reports one deal's schedule itself (RegisterDeals).
//
// manage_installment registers unconditionally and guards at call time
// (D-46); its delete takes confirm because it is permanent (D-99).
func RegisterInstallments(s *mcp.Server, c installmentsClient, opts RegisterOptions) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_installments",
		Description: installmentsPurpose +
			"This reads the schedules of up to 100 deals in two API calls: per deal, every installment with its amount, currency and billing date, their sum next to the deal value, and warnings where the two differ or the deal is won. " +
			installmentRules +
			"For one deal, get_deal reports the same schedule. Use search or list_deals to find the deal ids.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listInstallmentsInput) (*mcp.CallToolResult, listInstallmentsOutput, error) {
		ids, err := validateDealIDs(in.DealIDs)
		if err != nil {
			return errorResult(err), listInstallmentsOutput{}, nil
		}
		out, err := listSchedules(ctx, c, ids)
		if err != nil {
			return installmentError(err), listInstallmentsOutput{}, nil
		}
		return nil, out, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "manage_installment",
		Description: "Add, change or remove one installment on a deal. " + installmentsPurpose + installmentRules +
			"create takes deal_id, description, amount and billing_date, and is refused, naming the rule, on a deal without a one-time product or with a recurring one. update and delete take deal_id and installment_id. Every action reads the deal and its schedule first, and returns the whole schedule afterwards with its sum next to the deal value, warning when they differ. " +
			"An update refuses to replace any field unless you name it in overwrite, because every installment field holds a value. expect_version takes the installment's version and refuses a write if it moved. " +
			"Deleting is PERMANENT: Pipedrive keeps no copy, so delete needs confirm: true, and dry_run shows what would go without it. " +
			"Once a deal is won, its installments may already have become invoices in the system that bills from Pipedrive. Every action on a won deal warns: change amounts and dates in that system, not here. " +
			"Use get_deal or list_installments to find installment_id and version.",
		Annotations: mutatingAnnotations(),
	}, manageInstallmentHandler(c, opts.DryRun))
}

func validateDealIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: deal_ids must name at least one deal", pipedrive.ErrValidation)
	}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if err := validatePositiveID(id, "every deal_ids entry"); err != nil {
			return nil, err
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	if len(out) > pipedrive.MaxInstallmentDeals {
		return nil, fmt.Errorf("%w: deal_ids names %d deals; at most %d", pipedrive.ErrValidation, len(out), pipedrive.MaxInstallmentDeals)
	}
	return out, nil
}

// listSchedules reads every installment of the deals and the deals
// themselves, and pairs them up. A deal the deal list does not return,
// such as an archived one, keeps its installments with no currency.
func listSchedules(ctx context.Context, c installmentsClient, ids []int64) (listInstallmentsOutput, error) {
	rows, err := allInstallments(ctx, c, ids)
	if errors.Is(err, errTooManyInstallments) {
		return listInstallmentsOutput{}, withHint(err, "ask for fewer deals at a time")
	}
	if err != nil {
		return listInstallmentsOutput{}, err
	}
	deals, _, err := c.ListDeals(ctx, pipedrive.ListDealsOptions{IDs: ids, Limit: len(ids)})
	if err != nil {
		return listInstallmentsOutput{}, err
	}
	byID := make(map[int64]*pipedrive.Deal, len(deals))
	for i := range deals {
		byID[deals[i].ID] = &deals[i]
	}
	rowsOf := make(map[int64][]pipedrive.Installment, len(ids))
	for _, r := range rows {
		rowsOf[r.DealID] = append(rowsOf[r.DealID], r)
	}
	out := listInstallmentsOutput{Deals: make([]installmentSchedule, 0, len(ids))}
	for _, id := range ids {
		own := rowsOf[id]
		d := byID[id]
		if d == nil {
			sched := scheduleFor(&pipedrive.Deal{ID: id}, own)
			sched.Warnings = append(sched.Warnings, fmt.Sprintf("deal %d was not among the live deals Pipedrive returned (archived, deleted or not visible to this token), so its value and currency are unknown", id))
			out.Deals = append(out.Deals, sched)
			continue
		}
		out.Deals = append(out.Deals, scheduleFor(d, own))
	}
	return out, nil
}

// errTooManyInstallments is no input's fault, so it carries no class.
var errTooManyInstallments = fmt.Errorf("the schedule holds more than %d installments, more than one call reads", installmentPageSize*installmentMaxPages)

// allInstallments follows the cursor until the schedule is whole.
func allInstallments(ctx context.Context, c installmentLister, ids []int64) ([]pipedrive.Installment, error) {
	var out []pipedrive.Installment
	cursor := ""
	for range installmentMaxPages {
		page, next, err := c.ListInstallments(ctx, ids, installmentPageSize, cursor)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if next == "" {
			return out, nil
		}
		cursor = next
	}
	return nil, errTooManyInstallments
}

// scheduleFor builds a deal's schedule and its warnings. A deal with no
// installments gets no sum warning: installments are optional.
func scheduleFor(d *pipedrive.Deal, rows []pipedrive.Installment) installmentSchedule {
	sorted := slices.Clone(rows)
	slices.SortStableFunc(sorted, func(a, b pipedrive.Installment) int {
		return cmp.Or(cmp.Compare(a.BillingDate, b.BillingDate), cmp.Compare(a.ID, b.ID))
	})
	s := installmentSchedule{
		DealID:       d.ID,
		DealTitle:    d.Title,
		DealStatus:   d.Status,
		DealValue:    d.Value,
		Currency:     d.Currency,
		Installments: make([]installmentSummary, 0, len(sorted)),
	}
	for i := range sorted {
		s.Installments = append(s.Installments, summarizeInstallment(&sorted[i], d.Currency))
		s.Sum += sorted[i].Amount
	}
	s.Sum = roundCents(s.Sum)
	if len(sorted) > 0 && d.Currency != "" && roundCents(s.Sum-d.Value) != 0 {
		s.Warnings = append(s.Warnings, fmt.Sprintf("stale: the installments sum to %s %s but the deal value is %s %s; one of them needs updating",
			formatAmount(s.Sum), d.Currency, formatAmount(d.Value), d.Currency))
	}
	if d.Status == "won" {
		s.Warnings = append(s.Warnings, fmt.Sprintf(wonWarning, d.ID))
	}
	return s
}

// roundCents rounds to two decimals, so a sum of float amounts compares
// with the deal value as money rather than as binary fractions.
func roundCents(v float64) float64 { return math.Round(v*100) / 100 }

func formatAmount(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func summarizeInstallment(in *pipedrive.Installment, currency string) installmentSummary {
	return installmentSummary{
		ID:          in.ID,
		DealID:      in.DealID,
		Description: in.Description,
		Amount:      in.Amount,
		Currency:    currency,
		BillingDate: in.BillingDate,
		Version:     installmentVersion(in),
	}
}

// installmentVersion stands in for the update_time an installment does
// not have: the v2 description gives it only id, deal_id, description,
// amount and billing_date. Any change to the three writable fields
// changes it.
func installmentVersion(in *pipedrive.Installment) string {
	sum := sha256.Sum256([]byte(strconv.Quote(in.Description) + "|" + formatAmount(in.Amount) + "|" + in.BillingDate))
	return hex.EncodeToString(sum[:6])
}

// installmentFields drives the update's diff and overwrite guard
// (D-38). All three always hold a value, so any change needs overwrite.
var installmentFields = []fieldSpec[pipedrive.Installment]{
	{"description", func(in *pipedrive.Installment) string { return in.Description }},
	{"amount", func(in *pipedrive.Installment) string { return projectMoney(in.Amount) }},
	{"billing_date", func(in *pipedrive.Installment) string { return in.BillingDate }},
}

func manageInstallmentHandler(c installmentsClient, dryRun bool) mcp.ToolHandlerFor[manageInstallmentInput, manageInstallmentOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in manageInstallmentInput) (*mcp.CallToolResult, manageInstallmentOutput, error) {
		if err := validateAction(in.Action, allowedInstallmentActions); err != nil {
			return errorResult(err), manageInstallmentOutput{}, nil
		}
		if err := validatePositiveID(in.DealID, "deal_id"); err != nil {
			return errorResult(err), manageInstallmentOutput{}, nil
		}
		in.DryRun = in.DryRun || dryRun

		var (
			out manageInstallmentOutput
			res *mcp.CallToolResult
		)
		switch in.Action {
		case "create":
			out, res = createInstallmentAction(ctx, c, in)
		case "update":
			out, res = updateInstallmentAction(ctx, c, in)
		case "delete":
			out, res = deleteInstallmentAction(ctx, c, in)
		default:
			// Named rather than a fallthrough, so an action added to the
			// enum without a handler cannot become a delete.
			res = installmentError(fmt.Errorf("%w: action %q has no handler", pipedrive.ErrValidation, in.Action))
		}
		if res != nil {
			return res, manageInstallmentOutput{}, nil
		}
		out.Action = in.Action
		out.DryRun = in.DryRun
		return nil, out, nil
	}
}

func createInstallmentAction(ctx context.Context, c installmentsClient, in manageInstallmentInput) (manageInstallmentOutput, *mcp.CallToolResult) {
	if in.Description == nil || strings.TrimSpace(*in.Description) == "" {
		return manageInstallmentOutput{}, installmentError(fmt.Errorf("%w: create needs description", pipedrive.ErrValidation))
	}
	if in.Amount == nil || in.BillingDate == nil {
		return manageInstallmentOutput{}, installmentError(fmt.Errorf("%w: create needs amount and billing_date", pipedrive.ErrValidation))
	}
	if err := validateInstallmentValues(in); err != nil {
		return manageInstallmentOutput{}, installmentError(err)
	}
	deal, rows, err := readSchedule(ctx, c, in.DealID)
	if err != nil {
		return manageInstallmentOutput{}, installmentError(err)
	}
	products, err := c.ListDealProducts(ctx, in.DealID)
	if err != nil {
		return manageInstallmentOutput{}, installmentError(err)
	}
	if err = checkInstallmentProducts(in.DealID, products); err != nil {
		return manageInstallmentOutput{}, installmentError(err)
	}

	req := pipedrive.InstallmentRequest{Description: in.Description, Amount: in.Amount, BillingDate: in.BillingDate}
	created := &pipedrive.Installment{DealID: in.DealID, Description: *in.Description, Amount: *in.Amount, BillingDate: *in.BillingDate}
	duplicate := slices.ContainsFunc(rows, func(r pipedrive.Installment) bool {
		return strings.EqualFold(strings.TrimSpace(r.Description), strings.TrimSpace(*in.Description))
	})
	after := append(slices.Clone(rows), *created)
	if !in.DryRun {
		if created, err = c.CreateInstallment(ctx, in.DealID, req); err != nil {
			return manageInstallmentOutput{}, installmentError(err)
		}
		if after, err = allInstallments(ctx, c, []int64{in.DealID}); err != nil {
			return manageInstallmentOutput{}, installmentError(err)
		}
	}
	sched := scheduleFor(deal, after)
	if duplicate {
		sched.Warnings = append(sched.Warnings, fmt.Sprintf("deal %d already had an installment described %q; check this is not a second copy", in.DealID, *in.Description))
	}
	return manageInstallmentOutput{
		Installment: summarizeInstallment(created, deal.Currency),
		Changed:     changedFields(installmentFields, &pipedrive.Installment{}, created),
		Schedule:    sched,
	}, nil
}

func updateInstallmentAction(ctx context.Context, c installmentsClient, in manageInstallmentInput) (manageInstallmentOutput, *mcp.CallToolResult) {
	if in.Description == nil && in.Amount == nil && in.BillingDate == nil {
		return manageInstallmentOutput{}, installmentError(fmt.Errorf("%w: update needs at least one of description, amount or billing_date", pipedrive.ErrValidation))
	}
	if in.Description != nil && strings.TrimSpace(*in.Description) == "" {
		return manageInstallmentOutput{}, installmentError(fmt.Errorf("%w: description must not be blank; an installment always has one", pipedrive.ErrValidation))
	}
	if err := validateInstallmentValues(in); err != nil {
		return manageInstallmentOutput{}, installmentError(err)
	}
	if err := validatePositiveID(in.InstallmentID, "installment_id"); err != nil {
		return manageInstallmentOutput{}, installmentError(err)
	}
	deal, rows, err := readSchedule(ctx, c, in.DealID)
	if err != nil {
		return manageInstallmentOutput{}, installmentError(err)
	}
	req := pipedrive.InstallmentRequest{Description: in.Description, Amount: in.Amount, BillingDate: in.BillingDate}

	rec, changed, res := guardedWrite[pipedrive.Installment]{
		Spec:          installmentFields,
		Resource:      fmt.Sprintf("installment %d of deal %d", in.InstallmentID, in.DealID),
		ExpectVersion: in.ExpectVersion,
		Version:       installmentVersion,
		Overwrite:     in.Overwrite,
		DryRun:        in.DryRun,
		Get: func(context.Context) (*pipedrive.Installment, error) {
			return findInstallment(rows, in.DealID, in.InstallmentID)
		},
		Predict: func(before *pipedrive.Installment) pipedrive.Installment { return applyInstallment(*before, req) },
		Put: func(ctx context.Context) (*pipedrive.Installment, error) {
			rec, putErr := c.UpdateInstallment(ctx, in.DealID, in.InstallmentID, req)
			return rec, installmentHint(putErr)
		},
	}.run(ctx)
	if res != nil {
		return manageInstallmentOutput{}, res
	}

	after := rows
	switch {
	case in.DryRun && len(changed) > 0:
		// The guard returns the record as it stands on a dry run; the
		// schedule shows the one the write would leave.
		after = replaceInstallment(rows, applyInstallment(*rec, req))
	case !in.DryRun && len(changed) > 0:
		if after, err = allInstallments(ctx, c, []int64{in.DealID}); err != nil {
			return manageInstallmentOutput{}, installmentError(err)
		}
	}
	return manageInstallmentOutput{
		Installment: summarizeInstallment(rec, deal.Currency),
		Changed:     changed,
		Schedule:    scheduleFor(deal, after),
	}, nil
}

func deleteInstallmentAction(ctx context.Context, c installmentsClient, in manageInstallmentInput) (manageInstallmentOutput, *mcp.CallToolResult) {
	if err := validatePositiveID(in.InstallmentID, "installment_id"); err != nil {
		return manageInstallmentOutput{}, installmentError(err)
	}
	deal, rows, err := readSchedule(ctx, c, in.DealID)
	if err != nil {
		return manageInstallmentOutput{}, installmentError(err)
	}
	before, err := findInstallment(rows, in.DealID, in.InstallmentID)
	if err != nil {
		return manageInstallmentOutput{}, installmentError(err)
	}
	resource := fmt.Sprintf("installment %d of deal %d", in.InstallmentID, in.DealID)
	if err = checkExpectVersion(in.ExpectVersion, installmentVersion(before), resource); err != nil {
		return manageInstallmentOutput{}, installmentError(err)
	}
	// The guard D-99 adds: the delete is permanent, so the caller states
	// that they mean it. A dry run needs no confirm, since it is how the
	// caller sees what would go.
	if !in.DryRun && !in.Confirm {
		return manageInstallmentOutput{}, installmentError(refuse(
			fmt.Sprintf("deleting %s (%s %s on %s) is permanent: Pipedrive keeps no copy and nothing here restores it",
				resource, formatAmount(before.Amount), deal.Currency, before.BillingDate),
			"confirm: true"))
	}
	after := slices.DeleteFunc(slices.Clone(rows), func(r pipedrive.Installment) bool { return r.ID == in.InstallmentID })
	if !in.DryRun {
		if err = c.DeleteInstallment(ctx, in.DealID, in.InstallmentID); err != nil {
			return manageInstallmentOutput{}, installmentError(err)
		}
		if after, err = allInstallments(ctx, c, []int64{in.DealID}); err != nil {
			return manageInstallmentOutput{}, installmentError(err)
		}
	}
	return manageInstallmentOutput{
		Installment: summarizeInstallment(before, deal.Currency),
		Deleted:     true,
		Schedule:    scheduleFor(deal, after),
	}, nil
}

// readSchedule is the read every action makes first: the deal, for its
// value, currency and status, and its whole schedule.
func readSchedule(ctx context.Context, c installmentsClient, dealID int64) (*pipedrive.Deal, []pipedrive.Installment, error) {
	deal, err := c.GetDeal(ctx, dealID)
	if err != nil {
		return nil, nil, err
	}
	rows, err := allInstallments(ctx, c, []int64{dealID})
	if err != nil {
		return nil, nil, err
	}
	return deal, rows, nil
}

func findInstallment(rows []pipedrive.Installment, dealID, id int64) (*pipedrive.Installment, error) {
	for i := range rows {
		if rows[i].ID == id {
			found := rows[i]
			return &found, nil
		}
	}
	return nil, fmt.Errorf("%w: deal %d has no installment %d; get_deal lists its installments", pipedrive.ErrNotFound, dealID, id)
}

func applyInstallment(before pipedrive.Installment, req pipedrive.InstallmentRequest) pipedrive.Installment {
	after := before
	setIf(&after.Description, req.Description)
	setIf(&after.Amount, req.Amount)
	setIf(&after.BillingDate, req.BillingDate)
	return after
}

func replaceInstallment(rows []pipedrive.Installment, in pipedrive.Installment) []pipedrive.Installment {
	out := slices.Clone(rows)
	for i := range out {
		if out[i].ID == in.ID {
			out[i] = in
		}
	}
	return out
}

func validateInstallmentValues(in manageInstallmentInput) error {
	if in.Amount != nil && !(*in.Amount > 0) {
		return fmt.Errorf("%w: amount must be above zero", pipedrive.ErrValidation)
	}
	if in.BillingDate != nil {
		if _, err := time.Parse(time.DateOnly, *in.BillingDate); err != nil {
			return fmt.Errorf("%w: billing_date %q is not a YYYY-MM-DD date", pipedrive.ErrValidation, *in.BillingDate)
		}
	}
	return nil
}

// checkInstallmentProducts applies the rule Pipedrive states for a new
// installment, read as written: "An installment can only be added if the
// deal includes at least one one-time product. If the deal contains at
// least one recurring product, adding installments is not allowed." The
// reference does not say whether a disabled product counts, so this
// counts every product, and billing_frequency defaults to one-time as the
// reference's schema says. That a deal with no product is refused is
// checked by the integration test TestWrite_InstallmentRoundTrip.
// https://developers.pipedrive.com/docs/api/v1/DealInstallments#postInstallment
func checkInstallmentProducts(dealID int64, products []pipedrive.DealProduct) error {
	oneTime := false
	for _, p := range products {
		if p.BillingFrequency == "" || p.BillingFrequency == "one-time" {
			oneTime = true
			continue
		}
		return fmt.Errorf("%w: deal %d has a recurring product (billed %s), and Pipedrive adds no installment to a deal with a recurring product",
			pipedrive.ErrForbiddenBusinessRule, dealID, p.BillingFrequency)
	}
	if !oneTime {
		return fmt.Errorf("%w: deal %d has no one-time product, and Pipedrive adds an installment only to a deal with at least one; attach a one-time product to the deal in Pipedrive first",
			pipedrive.ErrForbiddenBusinessRule, dealID)
	}
	return nil
}

// installmentHint says what a refused installment call most likely
// means: a [plan] refusal is the plan or one of its caps, and a 403 that
// reads as a permission failure here is often the plan as well.
func installmentHint(err error) error {
	switch {
	case errors.Is(err, pipedrive.ErrPlanLimit):
		return withHint(err, "installments need Pipedrive's Growth plan or above, and the plan's limits apply to them")
	case errors.Is(err, pipedrive.ErrForbiddenPermission):
		return withHint(err, "installments need Pipedrive's Growth plan or above; if the workspace has it, this token's user may lack permission for the deal")
	}
	return err
}

// installmentError renders a failure, naming the plan where that is
// the likely cause.
func installmentError(err error) *mcp.CallToolResult {
	return errorResult(installmentHint(err))
}

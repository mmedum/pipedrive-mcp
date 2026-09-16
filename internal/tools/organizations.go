package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

type organizationsClient interface {
	GetOrganization(ctx context.Context, id int64) (*pipedrive.Organization, error)
	ListOrganizations(ctx context.Context, opts pipedrive.ListOrganizationsOptions) ([]pipedrive.Organization, string, error)
	CreateOrganization(ctx context.Context, req pipedrive.CreateOrganizationRequest) (*pipedrive.Organization, error)
	UpdateOrganization(ctx context.Context, id int64, req pipedrive.UpdateOrganizationRequest) (*pipedrive.Organization, error)
	ResolveOrganizationCustomFields(ctx context.Context, raw map[string]any) map[string]any
}

// allowedOrgSortFields enumerates Pipedrive v2's allowed sort_by
// values for /organizations. v2 supports id / update_time / add_time
// only — matches the shared commonV2TimestampSortFields base.
var allowedOrgSortFields = commonV2TimestampSortFields

// addressRow is an intentional subset of pipedrive.Address for the
// LLM-facing surface: street-level components (route, street_number,
// admin_area_level_1/2, sublocality) are omitted as low-signal for
// the kinds of questions the LLM answers from a search result. Drop
// the parallel struct and surface pipedrive.Address directly only if
// those components start mattering.
type addressRow struct {
	Value      string `json:"value,omitempty" jsonschema:"formatted human-readable address"`
	Country    string `json:"country,omitempty" jsonschema:"country, if Pipedrive parsed one"`
	Locality   string `json:"locality,omitempty" jsonschema:"city / locality, if Pipedrive parsed one"`
	PostalCode string `json:"postal_code,omitempty" jsonschema:"postal/ZIP code, if Pipedrive parsed one"`
}

type organizationSummary struct {
	ID           int64          `json:"id" jsonschema:"the organization's numeric id"`
	Name         string         `json:"name" jsonschema:"the organization's display name"`
	Address      *addressRow    `json:"address,omitempty" jsonschema:"structured address with the formatted string + parsed components, when present"`
	OwnerID      int64          `json:"owner_id" jsonschema:"id of the user who owns this record"`
	PeopleCount  int            `json:"people_count,omitempty" jsonschema:"number of persons linked to this org"`
	AddTime      string         `json:"add_time,omitempty" jsonschema:"timestamp the organization was created"`
	UpdateTime   string         `json:"update_time,omitempty" jsonschema:"timestamp the organization was last updated"`
	CustomFields map[string]any `json:"custom_fields,omitempty" jsonschema:"custom fields keyed by human-readable name"`
	URL          string         `json:"url" jsonschema:"link to the organization in the Pipedrive web UI"`
}

type getOrganizationInput struct {
	OrgID int64 `json:"org_id" jsonschema:"the organization's numeric id"`
}

type getOrganizationOutput struct {
	Organization organizationSummary `json:"organization" jsonschema:"the requested organization"`
}

type listOrganizationsInput struct {
	OwnerID       int64  `json:"owner_id,omitempty" jsonschema:"return only organizations owned by this user id; 0 = no filter"`
	UpdatedSince  string `json:"updated_since,omitempty" jsonschema:"RFC3339 timestamp; return only organizations updated at or after this time (e.g. 2026-04-01T00:00:00Z)"`
	UpdatedUntil  string `json:"updated_until,omitempty" jsonschema:"RFC3339 timestamp; return only organizations updated at or before this time"`
	SortBy        string `json:"sort_by,omitempty" jsonschema:"id | update_time | add_time. Default 'update_time' (most-recently-touched first)."`
	SortDirection string `json:"sort_direction,omitempty" jsonschema:"asc | desc. Default 'desc' when sort_by is omitted; 'asc' otherwise."`
	Limit         int    `json:"limit,omitempty" jsonschema:"page size; default 25, max 100"`
	Cursor        string `json:"cursor,omitempty" jsonschema:"opaque pagination token from a previous list_organizations response; omit for the first page"`
}

type listOrganizationsOutput struct {
	Organizations []organizationSummary `json:"organizations" jsonschema:"matching organizations on this page"`
	NextCursor    string                `json:"next_cursor,omitempty" jsonschema:"pass to the next list_organizations call to get the next page; empty when there are no more pages"`
}

// RegisterOrganizations wires the organization tools into the MCP
// server. Reads are discrete (get_organization, list_organizations);
// every mutation goes through manage_organization. opts.DryRun is the
// server-wide dry-run floor.
func RegisterOrganizations(s *mcp.Server, c organizationsClient, companyDomain string, opts RegisterOptions) {

	AddTool(s, &mcp.Tool{
		Name:        "get_organization",
		Description: "Fetch a single Pipedrive organization by org_id. Returns id, name, formatted address, owner_id, people_count (linked persons), add/update timestamps, and any custom fields resolved by name. Unknown org_id returns a [not_found] error. To find an organization by name, call `search` first to resolve the id.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getOrganizationInput) (*mcp.CallToolResult, getOrganizationOutput, error) {
		if err := validatePositiveID(in.OrgID, "org_id"); err != nil {
			return errorResult(err), getOrganizationOutput{}, nil
		}
		o, err := c.GetOrganization(ctx, in.OrgID)
		if err != nil {
			return errorResult(err), getOrganizationOutput{}, nil
		}
		return nil, getOrganizationOutput{Organization: resolvedOrganization(ctx, c, companyDomain, o)}, nil
	})

	AddTool(s, &mcp.Tool{
		Name:        "list_organizations",
		Description: "List Pipedrive organizations filtered by owner or update window. Returns id, name, formatted address (with parsed country/locality/postal_code when present), owner_id, people_count, add/update timestamps, and any custom fields resolved by name. Default sort is update_time desc — most-recently-touched first, ideal for 'which accounts have we been working on lately'. Default limit is 25, max 100. For more results, pass the next_cursor from the previous response. To find an organization by name (rather than ID), call `search` with type=organization — search is the natural-language gateway, list_organizations is the precision filter when the IDs are already known.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listOrganizationsInput) (*mcp.CallToolResult, listOrganizationsOutput, error) {
		if err := validateEnum(in.SortBy, "sort_by", allowedOrgSortFields); err != nil {
			return errorResult(err), listOrganizationsOutput{}, nil
		}
		if err := validateEnum(in.SortDirection, "sort_direction", allowedSortDirections); err != nil {
			return errorResult(err), listOrganizationsOutput{}, nil
		}

		sortBy, sortDir := effectiveSort(in.SortBy, in.SortDirection)
		opts := pipedrive.ListOrganizationsOptions{
			OwnerID:       in.OwnerID,
			UpdatedSince:  in.UpdatedSince,
			UpdatedUntil:  in.UpdatedUntil,
			SortBy:        sortBy,
			SortDirection: sortDir,
			Limit:         clampLimit(in.Limit),
			Cursor:        in.Cursor,
		}
		orgs, next, err := c.ListOrganizations(ctx, opts)
		if err != nil {
			return errorResult(err), listOrganizationsOutput{}, nil
		}
		out := listOrganizationsOutput{
			Organizations: make([]organizationSummary, 0, len(orgs)),
			NextCursor:    next,
		}
		for i := range orgs {
			out.Organizations = append(out.Organizations, resolvedOrganization(ctx, c, companyDomain, &orgs[i]))
		}
		return nil, out, nil
	})

	registerManageOrganization(s, c, companyDomain, opts)
}

var allowedOrganizationActions = map[string]bool{"create": true, "update": true}

// organizationFields is the one table of LLM-facing field names a write
// can touch. The address projects to the single line Pipedrive parsed
// it from, which is the same shape a write sends.
var organizationFields = []fieldSpec[pipedrive.Organization]{
	{"name", func(o *pipedrive.Organization) string { return projectString(o.Name) }},
	{"address", func(o *pipedrive.Organization) string { return projectAddress(o.Address) }},
	{"owner_id", func(o *pipedrive.Organization) string { return projectID(o.OwnerID) }},
}

type manageOrganizationInput struct {
	Action        string  `json:"action" jsonschema:"create or update"`
	OrgID         int64   `json:"org_id,omitempty" jsonschema:"the organization to act on, required by update and ignored by create"`
	Name          *string `json:"name,omitempty" jsonschema:"the organization's display name, required by create. If the user did not give you a name, ask — do NOT invent one"`
	Address       *string `json:"address,omitempty" jsonschema:"a single-line address exactly as the user dictated it, such as 123 Main St, San Francisco, CA 94103. Pipedrive parses it server-side into country, locality and postal_code, so do NOT pre-parse it or split it into components"`
	OwnerID       *int64  `json:"owner_id,omitempty" jsonschema:"the user who owns the record; omit on create to take the API token's own user"`
	DryRun        bool    `json:"dry_run,omitempty" jsonschema:"report what the write would find and change, and send nothing"`
	Overwrite     bool    `json:"overwrite,omitempty" jsonschema:"allow update to replace fields that already hold a value. Without it such an update is refused, naming each field"`
	ExpectVersion string  `json:"expect_version,omitempty" jsonschema:"the update_time from the read that informed this write; the write is refused if the organization changed since"`
}

type manageOrganizationOutput struct {
	Action       string              `json:"action" jsonschema:"the action that ran"`
	Organization organizationSummary `json:"organization" jsonschema:"the organization as Pipedrive stored it. When dry_run is true nothing was persisted and a created organization carries id=0."`
	Changed      []string            `json:"changed,omitempty" jsonschema:"names of the fields this write actually altered, empty when it was a no-op. On a dry run, the fields it would alter."`
	DryRun       bool                `json:"dry_run,omitempty" jsonschema:"true when nothing was sent upstream"`
}

func registerManageOrganization(s *mcp.Server, c organizationsClient, companyDomain string, opts RegisterOptions) {

	AddTool(s, &mcp.Tool{
		Name:        "manage_organization",
		Description: "Create an organization — the account or company a deal and its people hang off — or edit one. One call, either action: create takes name, update takes org_id. Writing is guarded, and update reads the organization before it writes, so a write is two API calls. It refuses to replace ANY field that already holds a value unless you pass overwrite, and the refusal names each one; filling a field that is empty destroys nothing and needs no permission. expect_version refuses the write outright if the record moved under you. IMPORTANT: send address as ONE LINE the way a person would say it and let Pipedrive parse it — the structured country, locality and postal_code you see on a read are its output, not its input, and pre-splitting them loses the parse. Custom fields are readable through get_organization and list_organizations but are not writable here yet. Use search to find an org_id from a name.",
		Annotations: mutatingAnnotations(),
	}, manageOrganizationHandler(c, companyDomain, opts.DryRun))
}

func manageOrganizationHandler(c organizationsClient, companyDomain string, dryRun bool) mcp.ToolHandlerFor[manageOrganizationInput, manageOrganizationOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in manageOrganizationInput) (*mcp.CallToolResult, manageOrganizationOutput, error) {
		if err := validateAction(in.Action, allowedOrganizationActions); err != nil {
			return errorResult(err), manageOrganizationOutput{}, nil
		}
		in.DryRun = in.DryRun || dryRun

		var (
			res *mcp.CallToolResult
			out manageOrganizationOutput
		)
		if in.Action == "create" {
			res, out = createOrganizationAction(ctx, c, companyDomain, in)
		} else {
			res, out = updateOrganizationAction(ctx, c, companyDomain, in)
		}
		if res == nil {
			out.Action = in.Action
			out.DryRun = in.DryRun
		}
		return res, out, nil
	}
}

func createOrganizationAction(ctx context.Context, c organizationsClient, companyDomain string, in manageOrganizationInput) (*mcp.CallToolResult, manageOrganizationOutput) {
	if in.Name == nil || *in.Name == "" {
		return errorResult(fmt.Errorf("%w: name must not be empty", pipedrive.ErrValidation)), manageOrganizationOutput{}
	}
	req := pipedrive.CreateOrganizationRequest{
		Name:    *in.Name,
		Address: deref(in.Address),
		OwnerID: deref(in.OwnerID),
	}
	created := syntheticOrgFromRequest(req)
	if !in.DryRun {
		o, err := c.CreateOrganization(ctx, req)
		if err != nil {
			return errorResult(err), manageOrganizationOutput{}
		}
		created = o
	}
	// resolvedOrganization on both paths, so a dry-run preview and a real
	// create describe their custom fields the same way. The synthetic
	// record carries none, so this resolves an empty map.
	return nil, manageOrganizationOutput{
		Organization: resolvedOrganization(ctx, c, companyDomain, created),
		Changed:      changedFields(organizationFields, &pipedrive.Organization{}, created),
	}
}

func updateOrganizationAction(ctx context.Context, c organizationsClient, companyDomain string, in manageOrganizationInput) (*mcp.CallToolResult, manageOrganizationOutput) {
	if err := validatePositiveID(in.OrgID, "org_id"); err != nil {
		return errorResult(err), manageOrganizationOutput{}
	}
	req := pipedrive.UpdateOrganizationRequest{
		Name:    in.Name,
		Address: in.Address,
		OwnerID: in.OwnerID,
	}

	before, err := c.GetOrganization(ctx, in.OrgID)
	if err != nil {
		return errorResult(err), manageOrganizationOutput{}
	}
	if err = checkExpectVersion(in.ExpectVersion, before.UpdateTime, fmt.Sprintf("organization %d", in.OrgID)); err != nil {
		return errorResult(err), manageOrganizationOutput{}
	}

	predicted := organizationAfterUpdate(*before, req)
	changed := changedFields(organizationFields, before, &predicted)
	if len(changed) == 0 {
		return nil, manageOrganizationOutput{Organization: resolvedOrganization(ctx, c, companyDomain, before)}
	}
	if err = requireOverwrite(organizationFields, fmt.Sprintf("organization %d", in.OrgID), before, changed, in.Overwrite); err != nil {
		return errorResult(err), manageOrganizationOutput{}
	}
	if in.DryRun {
		return nil, manageOrganizationOutput{
			Organization: resolvedOrganization(ctx, c, companyDomain, before),
			Changed:      changed,
		}
	}

	after, err := c.UpdateOrganization(ctx, in.OrgID, req)
	if err != nil {
		return errorResult(err), manageOrganizationOutput{}
	}
	return nil, manageOrganizationOutput{
		Organization: resolvedOrganization(ctx, c, companyDomain, after),
		Changed:      changedFields(organizationFields, before, after),
	}
}

func organizationAfterUpdate(before pipedrive.Organization, req pipedrive.UpdateOrganizationRequest) pipedrive.Organization {
	after := before
	setIf(&after.Name, req.Name)
	setIf(&after.OwnerID, req.OwnerID)
	if req.Address != nil {
		// Pipedrive re-parses the line server-side; the prediction only
		// needs the value the guard and the diff compare on.
		after.Address = &pipedrive.Address{Value: *req.Address}
	}
	return after
}

// syntheticOrgFromRequest builds a placeholder Organization mirroring
// the CreateOrganizationRequest, used only on the dry-run path so
// create_organization's output schema stays consistent. ID=0 +
// dry_run=true tells the LLM nothing was actually persisted. Address
// is wrapped as Address.Value; server-side parsing of country/
// locality/postal_code only happens on the real upstream call.
func syntheticOrgFromRequest(req pipedrive.CreateOrganizationRequest) *pipedrive.Organization {
	o := &pipedrive.Organization{
		Name:    req.Name,
		OwnerID: req.OwnerID,
	}
	if req.Address != "" {
		o.Address = &pipedrive.Address{Value: req.Address}
	}
	return o
}

// organizationFieldResolver is the narrow slice of a client that resolvedOrganization needs, so the
// resource templates can share the composition rather than repeating it.
type organizationFieldResolver interface {
	ResolveOrganizationCustomFields(ctx context.Context, raw map[string]any) map[string]any
}

// resolvedOrganization resolves custom-field hashes to workspace names and summarizes
// in one step. Written out separately at six sites per resource, a
// forgotten resolve silently shipped 40-char field hashes to the LLM
// instead of the names it can actually cite.
func resolvedOrganization(ctx context.Context, c organizationFieldResolver, domain string, r *pipedrive.Organization) organizationSummary {
	return summarizeOrganization(domain, r, c.ResolveOrganizationCustomFields(ctx, r.CustomFields))
}

func summarizeOrganization(domain string, o *pipedrive.Organization, customFields map[string]any) organizationSummary {
	out := organizationSummary{
		ID:           o.ID,
		Name:         o.Name,
		OwnerID:      o.OwnerID,
		PeopleCount:  o.PeopleCount,
		AddTime:      o.AddTime,
		UpdateTime:   o.UpdateTime,
		CustomFields: customFields,
		URL:          pipedrive.WebURL(domain, pipedrive.WebURLOrganization, o.ID),
	}
	if o.Address != nil {
		out.Address = &addressRow{
			Value:      o.Address.Value,
			Country:    o.Address.Country,
			Locality:   o.Address.Locality,
			PostalCode: o.Address.PostalCode,
		}
	}
	return out
}

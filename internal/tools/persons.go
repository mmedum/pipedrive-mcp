package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

type personsClient interface {
	GetPerson(ctx context.Context, id int64) (*pipedrive.Person, error)
	ListPersons(ctx context.Context, opts pipedrive.ListPersonsOptions) ([]pipedrive.Person, string, error)
	CreatePerson(ctx context.Context, req pipedrive.CreatePersonRequest) (*pipedrive.Person, error)
	UpdatePerson(ctx context.Context, id int64, req pipedrive.UpdatePersonRequest) (*pipedrive.Person, error)
	DeletePerson(ctx context.Context, id int64) error
	ResolvePersonCustomFields(ctx context.Context, raw map[string]any) map[string]any
	EncodePersonCustomFields(ctx context.Context, in map[string]any) (pipedrive.CustomFieldWrite, error)
}

// allowedPersonSortFields enumerates Pipedrive v2's allowed sort_by
// values for /persons. v2 supports id / update_time / add_time only —
// matches the shared commonV2TimestampSortFields base.
var allowedPersonSortFields = commonV2TimestampSortFields

// personSummary surfaces emails/phones as the same pipedrive.ContactPoint
// shape directly — the byte-identical row type doesn't earn its keep
// behind a parallel struct.
type personSummary struct {
	ID           int64                    `json:"id" jsonschema:"the person's numeric id"`
	Name         string                   `json:"name" jsonschema:"full name (first + last)"`
	FirstName    string                   `json:"first_name,omitempty" jsonschema:"first / given name"`
	LastName     string                   `json:"last_name,omitempty" jsonschema:"last / family name"`
	Emails       []pipedrive.ContactPoint `json:"emails,omitempty" jsonschema:"all email addresses on the record"`
	Phones       []pipedrive.ContactPoint `json:"phones,omitempty" jsonschema:"all phone numbers on the record"`
	OrgID        int64                    `json:"org_id" jsonschema:"id of the linked organization, 0 if none"`
	OwnerID      int64                    `json:"owner_id" jsonschema:"id of the user who owns this record"`
	AddTime      string                   `json:"add_time,omitempty" jsonschema:"timestamp the person was created"`
	UpdateTime   string                   `json:"update_time,omitempty" jsonschema:"timestamp the person was last updated"`
	CustomFields map[string]any           `json:"custom_fields,omitempty" jsonschema:"custom fields keyed by human-readable name, a dropdown's value as its label; an unrecognized field or option falls through under its stored key"`
	URL          string                   `json:"url" jsonschema:"link to the person in the Pipedrive web UI"`
}

type getPersonInput struct {
	PersonID int64 `json:"person_id" jsonschema:"the person's numeric id"`
}

type getPersonOutput struct {
	Person personSummary `json:"person" jsonschema:"the requested person"`
}

type listPersonsInput struct {
	OwnerID       int64  `json:"owner_id,omitempty" jsonschema:"return only persons owned by this user id; 0 = no filter"`
	OrgID         int64  `json:"org_id,omitempty" jsonschema:"return only persons linked to this organization id; 0 = no filter"`
	UpdatedSince  string `json:"updated_since,omitempty" jsonschema:"RFC3339 timestamp; return only persons updated at or after this time (e.g. 2026-04-01T00:00:00Z)"`
	UpdatedUntil  string `json:"updated_until,omitempty" jsonschema:"RFC3339 timestamp; return only persons updated at or before this time"`
	SortBy        string `json:"sort_by,omitempty" jsonschema:"id | update_time | add_time. Default 'update_time' (most-recently-touched first)."`
	SortDirection string `json:"sort_direction,omitempty" jsonschema:"asc | desc. Default 'desc' when sort_by is omitted; 'asc' otherwise."`
	Limit         int    `json:"limit,omitempty" jsonschema:"page size; default 25, max 100"`
	Cursor        string `json:"cursor,omitempty" jsonschema:"opaque pagination token from a previous list_persons response; omit for the first page"`
}

type listPersonsOutput struct {
	Persons    []personSummary `json:"persons" jsonschema:"matching persons on this page"`
	NextCursor string          `json:"next_cursor,omitempty" jsonschema:"pass to the next list_persons call to get the next page; empty when there are no more pages"`
}

// RegisterPersons wires the person tools into the MCP server. Reads are
// discrete (get_person, list_persons); every mutation goes through
// manage_person. opts.DryRun is the server-wide dry-run floor.
func RegisterPersons(s *mcp.Server, c personsClient, companyDomain string, opts RegisterOptions) {

	AddTool(s, &mcp.Tool{
		Name:        "get_person",
		Description: "Fetch a single Pipedrive person by person_id. Returns id, name, first_name, last_name, all emails (with primary flag and label), all phones, owner_id, linked org_id, add/update timestamps, and any custom fields under their workspace names, dropdown values as labels rather than option ids. Unknown person_id returns a [not_found] error. To find a person by name, call `search` first to resolve the id.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getPersonInput) (*mcp.CallToolResult, getPersonOutput, error) {
		if err := validatePositiveID(in.PersonID, "person_id"); err != nil {
			return errorResult(err), getPersonOutput{}, nil
		}
		p, err := c.GetPerson(ctx, in.PersonID)
		if err != nil {
			return errorResult(err), getPersonOutput{}, nil
		}
		return nil, getPersonOutput{Person: resolvedPerson(ctx, c, companyDomain, p)}, nil
	})

	AddTool(s, &mcp.Tool{
		Name:        "list_persons",
		Description: "List Pipedrive persons filtered by owner, linked organization, or update window. Returns id, name, first_name, last_name, emails, phones, owner_id, linked org_id, add/update timestamps, and any custom fields under their workspace names, dropdown values as labels rather than option ids. Default sort is update_time desc — most-recently-touched first, ideal for 'who at company X have we been talking to lately'. Default limit is 25, max 100. For more results, pass the next_cursor from the previous response. To find a person by name (rather than ID), call `search` with type=person — search is the natural-language gateway, list_persons is the precision filter when the IDs are already known.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listPersonsInput) (*mcp.CallToolResult, listPersonsOutput, error) {
		if err := validateEnum(in.SortBy, "sort_by", allowedPersonSortFields); err != nil {
			return errorResult(err), listPersonsOutput{}, nil
		}
		if err := validateEnum(in.SortDirection, "sort_direction", allowedSortDirections); err != nil {
			return errorResult(err), listPersonsOutput{}, nil
		}

		sortBy, sortDir := effectiveSort(in.SortBy, in.SortDirection)
		opts := pipedrive.ListPersonsOptions{
			OwnerID:       in.OwnerID,
			OrgID:         in.OrgID,
			UpdatedSince:  in.UpdatedSince,
			UpdatedUntil:  in.UpdatedUntil,
			SortBy:        sortBy,
			SortDirection: sortDir,
			Limit:         clampLimit(in.Limit),
			Cursor:        in.Cursor,
		}
		persons, next, err := c.ListPersons(ctx, opts)
		if err != nil {
			return errorResult(err), listPersonsOutput{}, nil
		}
		out := listPersonsOutput{
			Persons:    make([]personSummary, 0, len(persons)),
			NextCursor: next,
		}
		for i := range persons {
			out.Persons = append(out.Persons, resolvedPerson(ctx, c, companyDomain, &persons[i]))
		}
		return nil, out, nil
	})

	registerManagePerson(s, c, companyDomain, opts)
}

var allowedPersonActions = map[string]bool{"create": true, "update": true, "delete": true}

// personBaseFields is the table of LLM-facing field names a write can
// touch that every person has. A workspace's own custom fields are
// added per call, so THIS IS NOT THE WHOLE TABLE. A create diffs
// against personSpec's result; an update hands this table to
// guardedWrite and lets its Custom/CustomOf pair extend it. Either way,
// never diff a write against this alone — the caller's custom fields
// would fall out of both the report and the overwrite guard, silently.
//
// Emails and phones project through projectContactPoints, which
// renders every field of every entry — see the rule on projectCollection
// in guard.go. "Does this person already have an email" is NOT the
// question the guard needs answered; that reasoning shipped once and was
// wrong twice over.
var personBaseFields = []fieldSpec[pipedrive.Person]{
	{"name", func(p *pipedrive.Person) string { return p.Name }},
	{"first_name", func(p *pipedrive.Person) string { return p.FirstName }},
	{"last_name", func(p *pipedrive.Person) string { return p.LastName }},
	{"emails", func(p *pipedrive.Person) string { return projectContactPoints(p.Emails) }},
	{"phones", func(p *pipedrive.Person) string { return projectContactPoints(p.Phones) }},
	{"org_id", func(p *pipedrive.Person) string { return projectID(p.OrgID) }},
	{"owner_id", func(p *pipedrive.Person) string { return projectID(p.OwnerID) }},
}

type managePersonInput struct {
	Action        string                   `json:"action" jsonschema:"create, update or delete"`
	PersonID      int64                    `json:"person_id,omitempty" jsonschema:"the person to act on, required by update and ignored by create"`
	Name          *string                  `json:"name,omitempty" jsonschema:"the person's full name. create needs this OR first_name/last_name, never both. If the user did not give you a name, ask — do NOT invent one"`
	FirstName     *string                  `json:"first_name,omitempty" jsonschema:"first or given name; pass it with last_name INSTEAD OF name, not alongside it, and Pipedrive derives name"`
	LastName      *string                  `json:"last_name,omitempty" jsonschema:"last or family name; pass it with first_name INSTEAD OF name, not alongside it"`
	Emails        []pipedrive.ContactPoint `json:"emails,omitempty" jsonschema:"email addresses, each {value, primary, label}. At most one primary; label is free text such as work or home. On update this REPLACES the whole collection rather than adding to it, because that is what Pipedrive does with it"`
	Phones        []pipedrive.ContactPoint `json:"phones,omitempty" jsonschema:"phone numbers, same shape as emails, and replaced wholesale on update for the same reason"`
	OrgID         *int64                   `json:"org_id,omitempty" jsonschema:"the organization the person belongs to. Use search to turn a company name into the id. Omit to leave it as it is; unlinking is not supported here"`
	OwnerID       *int64                   `json:"owner_id,omitempty" jsonschema:"the user who owns the record; omit on create to take the API token's own user"`
	CustomFields  map[string]any           `json:"custom_fields,omitempty" jsonschema:"this workspace's own fields, keyed by the name get_person reports — a dropdown takes its label, a multi-select a list of labels, everything else the plain value. Omit a field to leave it as it is; a field cannot be cleared"`
	DryRun        bool                     `json:"dry_run,omitempty" jsonschema:"report what the write would find and change, and send nothing"`
	Overwrite     []string                 `json:"overwrite,omitempty" jsonschema:"the fields this write may replace, named exactly as the refusal listed them, e.g. [\"title\", \"value\"]. Omit it and a write that would replace a populated field is refused, naming each one. A refusal is NOT a retry signal: name a field only when the user asked for what is already there to be replaced, never to get past a refusal they have not seen. Naming fewer fields than the refusal listed is still refused, over the ones you left out"`
	ExpectVersion string                   `json:"expect_version,omitempty" jsonschema:"the update_time from the read that informed this write; the write is refused if the person changed since"`
}

type managePersonOutput struct {
	Action  string        `json:"action" jsonschema:"the action that ran"`
	Person  personSummary `json:"person" jsonschema:"the person as Pipedrive stored it. When dry_run is true nothing was persisted and a created person carries id=0."`
	Changed []string      `json:"changed,omitempty" jsonschema:"names of the fields this write actually altered, empty when it was a no-op. On a dry run, the fields it would alter."`
	DryRun  bool          `json:"dry_run,omitempty" jsonschema:"true when nothing was sent upstream"`
}

func registerManagePerson(s *mcp.Server, c personsClient, companyDomain string, opts RegisterOptions) {

	AddTool(s, &mcp.Tool{
		Name:        "manage_person",
		Description: "Create a contact, edit one, or delete one. One call, whichever action: create takes name OR first_name/last_name, update and delete take person_id. Pipedrive treats name and first_name/last_name as ALTERNATIVES and rejects a write carrying both, so pass the parts when you have them and it derives the full name, or pass the full name and it splits it — never both. Writing is guarded, and update reads the person before it writes, so a write is two API calls. It refuses to replace ANY field that already holds a value unless you pass overwrite, and the refusal names each one; filling a field that is empty destroys nothing and needs no permission. expect_version refuses the write outright if the record moved under you. IMPORTANT: emails and phones REPLACE the stored collection rather than adding to it, because that is what Pipedrive does with them — to add an address, read the person first and send the existing entries back alongside the new one, or you will silently drop the rest. Because Pipedrive derives one from the other, changing either reports `name` as changed too — a dry run predicts only the field you set, since it cannot know what Pipedrive will derive, and the write reports what actually moved. Custom fields ARE writable here: pass custom_fields keyed by the names get_person reports, and give a dropdown its label rather than an option id. Deleting is soft and time-boxed: Pipedrive marks the person deleted and removes it permanently after 30 days, so it takes dry_run and expect_version and no permitting flag beyond them — within that window Pipedrive's own UI can restore it, but NOTHING HERE PUTS IT BACK, so treat it as one-way and rehearse with dry_run first. What becomes of the deals, notes and activities hanging off a deleted person is not documented by Pipedrive and is not verified here, so read them first when the contact has history. Use search to turn a company name into the org_id this links to.",
		Annotations: mutatingAnnotations(),
	}, managePersonHandler(c, companyDomain, opts.DryRun))
}

func managePersonHandler(c personsClient, companyDomain string, dryRun bool) mcp.ToolHandlerFor[managePersonInput, managePersonOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in managePersonInput) (*mcp.CallToolResult, managePersonOutput, error) {
		if err := validateAction(in.Action, allowedPersonActions); err != nil {
			return errorResult(err), managePersonOutput{}, nil
		}
		in.DryRun = in.DryRun || dryRun

		var (
			res *mcp.CallToolResult
			out managePersonOutput
		)
		switch in.Action {
		case "create":
			res, out = createPersonAction(ctx, c, companyDomain, in)
		case "delete":
			res, out = deletePersonAction(ctx, c, companyDomain, in)
		default:
			res, out = updatePersonAction(ctx, c, companyDomain, in)
		}
		if res == nil {
			out.Action = in.Action
			out.DryRun = in.DryRun
		}
		return res, out, nil
	}
}

func createPersonAction(ctx context.Context, c personsClient, companyDomain string, in managePersonInput) (*mcp.CallToolResult, managePersonOutput) {
	req := pipedrive.CreatePersonRequest{
		Name:      deref(in.Name),
		FirstName: deref(in.FirstName),
		LastName:  deref(in.LastName),
		Emails:    in.Emails,
		Phones:    in.Phones,
		OrgID:     deref(in.OrgID),
		OwnerID:   deref(in.OwnerID),
	}
	// Before the custom-field encode, which can go to the network to
	// warm the field cache, and before the dry-run branch — a
	// rehearsal has to be refused on the same input a real create
	// would be, or it reports a write that could never happen.
	if err := req.Validate(); err != nil {
		return errorResult(err), managePersonOutput{}
	}
	cf, err := c.EncodePersonCustomFields(ctx, in.CustomFields)
	if err != nil {
		return errorResult(err), managePersonOutput{}
	}
	req.CustomFields = cf.Values

	created := syntheticPersonFromRequest(req)
	if !in.DryRun {
		c, err := c.CreatePerson(ctx, req)
		if err != nil {
			return errorResult(err), managePersonOutput{}
		}
		created = c
	}
	// resolvedX on both paths, so a dry-run preview and a real create
	// describe their custom fields the same way: the synthetic record
	// carries the custom fields the write would set, and the rehearsal
	// reports them under the names the real create would use.
	return nil, managePersonOutput{
		Person:  resolvedPerson(ctx, c, companyDomain, created),
		Changed: changedFields(personSpec(cf), &pipedrive.Person{}, created),
	}
}

func updatePersonAction(ctx context.Context, c personsClient, companyDomain string, in managePersonInput) (*mcp.CallToolResult, managePersonOutput) {
	if err := validatePositiveID(in.PersonID, "person_id"); err != nil {
		return errorResult(err), managePersonOutput{}
	}
	req := pipedrive.UpdatePersonRequest{
		Name:      in.Name,
		Emails:    in.Emails,
		Phones:    in.Phones,
		FirstName: in.FirstName,
		LastName:  in.LastName,
		OrgID:     in.OrgID,
		OwnerID:   in.OwnerID,
	}
	if err := req.Validate(); err != nil {
		return errorResult(err), managePersonOutput{}
	}
	cf, err := c.EncodePersonCustomFields(ctx, in.CustomFields)
	if err != nil {
		return errorResult(err), managePersonOutput{}
	}
	req.CustomFields = cf.Values

	person, changed, res := guardedWrite[pipedrive.Person]{
		Spec:          personBaseFields,
		Custom:        cf,
		CustomOf:      func(p *pipedrive.Person) *map[string]any { return &p.CustomFields },
		Resource:      fmt.Sprintf("person %d", in.PersonID),
		ExpectVersion: in.ExpectVersion,
		Version:       func(p *pipedrive.Person) string { return p.UpdateTime },
		Overwrite:     in.Overwrite,
		DryRun:        in.DryRun,
		Get:           func(ctx context.Context) (*pipedrive.Person, error) { return c.GetPerson(ctx, in.PersonID) },
		Predict:       func(p *pipedrive.Person) pipedrive.Person { return personAfterUpdate(*p, req) },
		Put:           func(ctx context.Context) (*pipedrive.Person, error) { return c.UpdatePerson(ctx, in.PersonID, req) },
	}.run(ctx)
	if res != nil {
		return res, managePersonOutput{}
	}
	return nil, managePersonOutput{Person: resolvedPerson(ctx, c, companyDomain, person), Changed: changed}
}

// personSpec is the base table plus the custom fields a write names. The
// create path needs it because a create has no target to guard and so
// does not go through guardedWrite, which does this itself.
func personSpec(cf pipedrive.CustomFieldWrite) []fieldSpec[pipedrive.Person] {
	return withCustomFields(personBaseFields, cf,
		func(p *pipedrive.Person) map[string]any { return p.CustomFields })
}

func personAfterUpdate(before pipedrive.Person, req pipedrive.UpdatePersonRequest) pipedrive.Person {
	after := before
	setIf(&after.Name, req.Name)
	setIf(&after.FirstName, req.FirstName)
	setIf(&after.LastName, req.LastName)
	setIf(&after.OrgID, req.OrgID)
	setIf(&after.OwnerID, req.OwnerID)
	// Contact points replace rather than merge, which is what Pipedrive
	// does with them.
	if req.Emails != nil {
		after.Emails = req.Emails
	}
	if req.Phones != nil {
		after.Phones = req.Phones
	}
	return after
}

// syntheticPersonFromRequest builds a placeholder Person that mirrors
// the CreatePersonRequest, used only on the dry-run path so
// create_person's output schema stays consistent. ID=0 + dry_run=true
// tells the LLM nothing was actually persisted.
func syntheticPersonFromRequest(req pipedrive.CreatePersonRequest) *pipedrive.Person {
	return &pipedrive.Person{
		Name:         req.Name,
		FirstName:    req.FirstName,
		LastName:     req.LastName,
		Emails:       req.Emails,
		Phones:       req.Phones,
		OrgID:        req.OrgID,
		OwnerID:      req.OwnerID,
		CustomFields: req.CustomFields,
	}
}

// personFieldResolver is the narrow slice of a client that resolvedPerson needs, so the
// resource templates can share the composition rather than repeating it.
type personFieldResolver interface {
	ResolvePersonCustomFields(ctx context.Context, raw map[string]any) map[string]any
}

// resolvedPerson resolves custom-field hashes to workspace names and summarizes
// in one step. Written out separately at six sites per resource, a
// forgotten resolve silently shipped 40-char field hashes to the LLM
// instead of the names it can actually cite.
func resolvedPerson(ctx context.Context, c personFieldResolver, domain string, r *pipedrive.Person) personSummary {
	return summarizePerson(domain, r, c.ResolvePersonCustomFields(ctx, r.CustomFields))
}

func summarizePerson(domain string, p *pipedrive.Person, customFields map[string]any) personSummary {
	return personSummary{
		ID:           p.ID,
		Name:         p.Name,
		FirstName:    p.FirstName,
		LastName:     p.LastName,
		Emails:       p.Emails,
		Phones:       p.Phones,
		OrgID:        p.OrgID,
		OwnerID:      p.OwnerID,
		AddTime:      p.AddTime,
		UpdateTime:   p.UpdateTime,
		CustomFields: customFields,
		URL:          pipedrive.WebURL(domain, pipedrive.WebURLPerson, p.ID),
	}
}

// deletePersonAction marks a person deleted.
//
// dry_run and expect_version, and nothing else. Pipedrive's delete is
// soft and time-boxed, which is the reversible case, and a guard is
// only added where the caller cannot see what they are about to lose —
// the read this performs puts the person on screen first.
//
// It does not pretend to know what becomes of the records hanging off
// this one. Pipedrive does not document that and nothing here has
// verified it, so the description warns rather than the code guarding
// against a behavior nobody has established.
//
// There is no already-deleted short-circuit, unlike manage_deal's,
// because there is nothing here to read one from: a deal carries
// status "deleted", but pipedrive.Person has no deleted marker and
// cannot grow one — the v2 response fixture spec_test.go checks against
// does not declare is_deleted for this resource, so the tag would fail
// the gate. A second delete reaches Pipedrive and is reported as
// whatever Pipedrive answers.
func deletePersonAction(ctx context.Context, c personsClient, companyDomain string, in managePersonInput) (*mcp.CallToolResult, managePersonOutput) {
	if err := validatePositiveID(in.PersonID, "personid"); err != nil {
		return errorResult(err), managePersonOutput{}
	}
	before, err := c.GetPerson(ctx, in.PersonID)
	if err != nil {
		return errorResult(err), managePersonOutput{}
	}
	if err := checkExpectVersion(in.ExpectVersion, before.UpdateTime, fmt.Sprintf("person %d", in.PersonID)); err != nil {
		return errorResult(err), managePersonOutput{}
	}
	if !in.DryRun {
		if err := c.DeletePerson(ctx, in.PersonID); err != nil {
			return errorResult(err), managePersonOutput{}
		}
	}
	return nil, managePersonOutput{Person: resolvedPerson(ctx, c, companyDomain, before), Changed: []string{"is_deleted"}}
}

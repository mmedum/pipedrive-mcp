package pipedrive

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// ListPersonsOptions filters a /persons list call. Zero values mean
// "no filter on this dimension". v2 supports more params (filter_id,
// ids, deal_id, include_fields, custom_fields) — wire only what the
// LLM-facing tools actually expose to keep the surface narrow.
type ListPersonsOptions struct {
	OwnerID       int64
	OrgID         int64
	UpdatedSince  string // RFC3339
	UpdatedUntil  string // RFC3339
	SortBy        string // id | update_time | add_time
	SortDirection string // asc | desc
	Limit         int
	Cursor        string
}

// CreatePersonRequest is the JSON body for POST /api/v2/persons.
//
// `name` and first_name/last_name are ALTERNATIVES, not a whole and
// its parts. Pipedrive rejects a body carrying both — "Cannot set
// 'name' and 'first_name'/'last_name' at the same time" — so `name`
// is omitempty: a request that names the parts must not send an empty
// `name` alongside them. Give one or the other; Pipedrive derives
// whichever was not given.
//
// Emails and phones are each a list of {value, primary, label}.
// Multiple `primary: true` entries are silently coerced by
// Pipedrive — last one wins.
type CreatePersonRequest struct {
	Name      string         `json:"name,omitempty"`
	FirstName string         `json:"first_name,omitempty"`
	LastName  string         `json:"last_name,omitempty"`
	Emails    []ContactPoint `json:"emails,omitempty"`
	Phones    []ContactPoint `json:"phones,omitempty"`
	OrgID     int64          `json:"org_id,omitempty"`
	OwnerID   int64          `json:"owner_id,omitempty"`

	// CustomFields is what FieldCache.Encode produces; see
	// CustomFieldWrite.Values for the shape. Nil omits the object,
	// leaving every custom field as it is.
	CustomFields map[string]any `json:"custom_fields,omitempty"`
}

// errBothNamings is Pipedrive's rule, refused here: `name` and
// first_name/last_name are alternatives, and a body carrying both
// comes back "Cannot set 'name' and 'first_name'/'last_name' at the
// same time". Refusing before the request costs no round trip and can
// say which argument to drop.
var errBothNamings = fmt.Errorf("%w: Pipedrive takes name OR first_name/last_name, never both — "+
	"pass first_name and last_name and it derives name, or pass name and it splits it", ErrValidation)

// Validate refuses a create Pipedrive would reject. A create must name
// the person somehow, and must not name them twice.
//
// The tools layer calls this before deciding whether to send anything,
// so a dry run is refused on the same input a real create would be —
// a rehearsal that reports it would create a nameless person is worse
// than no rehearsal.
//
// This method is the intended home for a cross-field rule on a request
// body: one spelling, called from the client and from the tool, rather
// than the same sentence written as a literal in both packages the way
// the deal, organization, activity and note requests still do. Move
// those here when one of them next needs touching.
//
// What belongs here is what is decidable from the request ALONE.
// Anything needing workspace state — whether an activity `type` exists
// in this account, whether a `stage_id` belongs to the given pipeline —
// is left to Pipedrive, because guarding it would mean a cache and a
// probe. That is a different question from UpdateNoteRequest's
// deliberate lack of validation, which is about whether the caller
// asked for anything at all.
func (r CreatePersonRequest) Validate() error {
	hasParts := r.FirstName != "" || r.LastName != ""
	switch {
	case r.Name != "" && hasParts:
		return errBothNamings
	case r.Name == "" && !hasParts:
		return fmt.Errorf("%w: a person needs a name — pass name, or first_name and last_name", ErrValidation)
	}
	return nil
}

// UpdatePersonRequest is the JSON body for PATCH /api/v2/persons/{id}.
// Pointers so nil omits the field and it keeps its stored value. The
// slices are nil-or-replace, because Pipedrive replaces a contact-point
// collection wholesale rather than merging into it. Clearing a field is
// not supported — see UpdateDealRequest.
type UpdatePersonRequest struct {
	Name      *string        `json:"name,omitempty"`
	FirstName *string        `json:"first_name,omitempty"`
	LastName  *string        `json:"last_name,omitempty"`
	Emails    []ContactPoint `json:"emails,omitempty"`
	Phones    []ContactPoint `json:"phones,omitempty"`
	OrgID     *int64         `json:"org_id,omitempty"`
	OwnerID   *int64         `json:"owner_id,omitempty"`

	// CustomFields is what FieldCache.Encode produces; see
	// CustomFieldWrite.Values for the shape. Nil omits the object,
	// leaving every custom field as it is.
	CustomFields map[string]any `json:"custom_fields,omitempty"`
}

// Validate refuses an update Pipedrive would reject. It asks whether
// the shape is rejectable, not whether the caller asked for anything:
// naming nothing is fine here and means leave the name alone.
func (r UpdatePersonRequest) Validate() error {
	if r.Name != nil && (r.FirstName != nil || r.LastName != nil) {
		return errBothNamings
	}
	return nil
}

// UpdatePerson edits a person via PATCH /api/v2/persons/{id} and
// returns the record Pipedrive echoes back.
func (c *Client) UpdatePerson(ctx context.Context, id int64, req UpdatePersonRequest) (*Person, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	var resp itemEnvelope[Person]
	if err := c.patchV2(ctx, "/persons/"+strconv.FormatInt(id, 10), req, &resp); err != nil {
		return nil, err
	}
	p := resp.Data
	return &p, nil
}

// GetPerson fetches a single person by ID. custom_fields are nested
// under the person's `custom_fields` object per Pipedrive v2 — caller
// resolves hash keys to names via the per-Client FieldCache.
func (c *Client) GetPerson(ctx context.Context, id int64) (*Person, error) {
	var resp itemEnvelope[Person]
	if err := c.do(ctx, "/persons/"+strconv.FormatInt(id, 10), &resp); err != nil {
		return nil, err
	}
	p := resp.Data
	return &p, nil
}

// CreatePerson posts a new person via /api/v2/persons. Returns the
// created person as Pipedrive echoes it (full record with id and
// custom_fields nested as usual).
func (c *Client) CreatePerson(ctx context.Context, req CreatePersonRequest) (*Person, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	var resp itemEnvelope[Person]
	if err := c.postV2(ctx, "/persons", req, &resp); err != nil {
		return nil, err
	}
	p := resp.Data
	return &p, nil
}

// ListPersons issues a /persons list with the given filters. Returns
// the page plus the next cursor (empty string = end of results).
func (c *Client) ListPersons(ctx context.Context, opts ListPersonsOptions) ([]Person, string, error) {
	q := url.Values{}
	if opts.OwnerID > 0 {
		q.Set("owner_id", strconv.FormatInt(opts.OwnerID, 10))
	}
	if opts.OrgID > 0 {
		q.Set("org_id", strconv.FormatInt(opts.OrgID, 10))
	}
	if opts.UpdatedSince != "" {
		q.Set("updated_since", opts.UpdatedSince)
	}
	if opts.UpdatedUntil != "" {
		q.Set("updated_until", opts.UpdatedUntil)
	}
	if opts.SortBy != "" {
		q.Set("sort_by", opts.SortBy)
	}
	if opts.SortDirection != "" {
		q.Set("sort_direction", opts.SortDirection)
	}
	setLimitCursor(q, opts.Limit, opts.Cursor)

	var resp listEnvelope[Person]
	if err := c.do(ctx, buildPath("/persons", q), &resp); err != nil {
		return nil, "", err
	}
	return resp.Data, resp.AdditionalData.NextCursor, nil
}

// ListPersonFields returns the field metadata for persons. Used by
// the per-Client person field cache.
func (c *Client) ListPersonFields(ctx context.Context) ([]Field, error) {
	var resp listEnvelope[Field]
	if err := c.do(ctx, "/personFields", &resp); err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// ResolvePersonCustomFields returns a copy of raw with hash keys
// replaced by their human-readable names. Sole entry point tool
// packages need; the underlying cache is unexported.
func (c *Client) ResolvePersonCustomFields(ctx context.Context, raw map[string]any) map[string]any {
	return c.personFields.Resolve(ctx, raw)
}

// EncodePersonCustomFields turns a caller's custom-field map — workspace
// names, dropdown labels — into the hash keys and option ids a write
// body carries. What it refuses, and how, is FieldCache.Encode's to
// say — do not restate it here.
func (c *Client) EncodePersonCustomFields(ctx context.Context, in map[string]any) (CustomFieldWrite, error) {
	return c.personFields.Encode(ctx, in)
}

// WarmPersonFields eagerly triggers the cache load.
func (c *Client) WarmPersonFields(ctx context.Context) {
	_ = c.personFields.Load(ctx)
}

// ReloadPersonFields clears and re-fetches the person-field cache,
// returning the count of fields now cached. See ReloadDealFields for
// the rationale.
func (c *Client) ReloadPersonFields(ctx context.Context) (int, error) {
	c.personFields.Reload()
	if err := c.personFields.Load(ctx); err != nil {
		return 0, err
	}
	return c.personFields.Count(), nil
}

// DeletePerson marks a person as deleted. Pipedrive's delete is SOFT and
// time-boxed — its documentation says "Marks a person as deleted.
// After 30 days, the person will be permanently deleted." Within that
// window the row still exists, carrying is_deleted; nothing in this
// server puts it back, and Pipedrive's own UI is what can.
//
// The response carries only the id, so there is nothing typed to
// return: a caller that wants the record's final state reads it before
// deleting, which the guarded-write path does anyway.
func (c *Client) DeletePerson(ctx context.Context, id int64) error {
	return c.deleteV2(ctx, "/persons/"+strconv.FormatInt(id, 10))
}

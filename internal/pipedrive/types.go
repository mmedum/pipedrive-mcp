package pipedrive

// Pipeline is a Pipedrive pipeline (a deal flow grouping). Subset of the
// /api/v2/pipelines response that we surface to LLM clients.
type Pipeline struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	OrderNr int    `json:"order_nr"`
	Active  bool   `json:"active"`
}

// Stage is a Pipedrive stage within a pipeline. Subset of /api/v2/stages.
//
// Note: Active maps to the upstream `active_flag` field, while Pipeline
// uses `active`. This is Pipedrive's API, not a copy-paste error —
// confirmed against /api/v2/stages and /api/v2/pipelines responses.
type Stage struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	OrderNr         int    `json:"order_nr"`
	Active          bool   `json:"active_flag"`
	PipelineID      int64  `json:"pipeline_id"`
	DealProbability int    `json:"deal_probability"`
}

// Field is a Pipedrive field-metadata record (the subset the field
// caches need: hash-key → human-name resolution). Returned by
// /dealFields, /personFields, /organizationFields. v2 renamed
// `key` → `field_code` and `name` → `field_name` from v1; this
// struct is v2-shaped.
type Field struct {
	Key  string `json:"field_code"` // 40-char hash for custom fields; plain identifier for built-ins ("id", "title", ...)
	Name string `json:"field_name"` // human-readable label
}

// Deal is a Pipedrive deal record (subset). Custom fields are nested
// under custom_fields per the v2 API; the LLM-facing rendering in
// internal/tools/ resolves the 40-char hash keys into names via the
// per-Client deal field cache.
//
// Times use Pipedrive's RFC3339-ish format with space (e.g.
// "2026-04-26 10:00:00"). They are surfaced to the LLM as raw strings
// so it can pattern-match without timezone surprises; callers that
// need time.Time should parse explicitly.
type Deal struct {
	ID                int64          `json:"id"`
	Title             string         `json:"title"`
	Value             float64        `json:"value"`
	Currency          string         `json:"currency"`
	Status            string         `json:"status"` // open | won | lost | deleted
	StageID           int64          `json:"stage_id"`
	PipelineID        int64          `json:"pipeline_id"`
	OwnerID           int64          `json:"owner_id"`
	PersonID          int64          `json:"person_id"`
	OrgID             int64          `json:"org_id"`
	ExpectedCloseDate string         `json:"expected_close_date"`
	WonTime           string         `json:"won_time,omitempty"`
	LostTime          string         `json:"lost_time,omitempty"`
	LostReason        string         `json:"lost_reason,omitempty"`
	AddTime           string         `json:"add_time"`
	UpdateTime        string         `json:"update_time"`
	Probability       *int           `json:"probability,omitempty"`
	CustomFields      map[string]any `json:"custom_fields,omitempty"`
}

// AdditionalData is the v2 paging envelope returned alongside `data`
// on list endpoints. NextCursor is the opaque token for the next
// page; empty string means "you've seen the last page".
type AdditionalData struct {
	NextCursor string `json:"next_cursor,omitempty"`
}

// itemEnvelope decodes Pipedrive v2's single-item response shape.
type itemEnvelope[T any] struct {
	Data T `json:"data"`
}

// listEnvelope decodes Pipedrive v2's list response shape, including
// the cursor-based pagination envelope. AdditionalData is harmless
// when the endpoint doesn't paginate (decodes to its zero value).
type listEnvelope[T any] struct {
	Data           []T            `json:"data"`
	AdditionalData AdditionalData `json:"additional_data"`
}

// ContactPoint is one row in a Person's emails / phones array.
// Pipedrive returns these as arrays of {value, primary, label}
// objects rather than flat strings so a single record can carry
// multiple addresses.
type ContactPoint struct {
	Value   string `json:"value"`
	Primary bool   `json:"primary"`
	Label   string `json:"label,omitempty"`
}

// Person is a Pipedrive person record (subset). Custom fields are
// nested under custom_fields per the v2 API; the LLM-facing
// rendering in internal/tools/ resolves the 40-char hash keys into
// names via the per-Client person field cache.
type Person struct {
	ID           int64          `json:"id"`
	Name         string         `json:"name"`
	FirstName    string         `json:"first_name,omitempty"`
	LastName     string         `json:"last_name,omitempty"`
	Emails       []ContactPoint `json:"emails,omitempty"`
	Phones       []ContactPoint `json:"phones,omitempty"`
	OrgID        int64          `json:"org_id"`
	OwnerID      int64          `json:"owner_id"`
	AddTime      string         `json:"add_time"`
	UpdateTime   string         `json:"update_time"`
	CustomFields map[string]any `json:"custom_fields,omitempty"`
}

// Address is Pipedrive v2's structured address record. Returned by
// /organizations/{id} and /persons/{id} for any address-typed field.
// /api/v2/itemSearch returns a different, flat string-only shape —
// the search-side code does not decode into this type. Street-level
// components (route, street_number, sublocality, admin areas) are
// not surfaced today; add them back if a tool starts needing them.
type Address struct {
	Value      string `json:"value,omitempty"`
	Country    string `json:"country,omitempty"`
	Locality   string `json:"locality,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
}

// Organization is a Pipedrive organization record (subset). Custom
// fields nest under custom_fields per the v2 API.
type Organization struct {
	ID           int64          `json:"id"`
	Name         string         `json:"name"`
	Address      *Address       `json:"address,omitempty"`
	OwnerID      int64          `json:"owner_id"`
	PeopleCount  int            `json:"people_count,omitempty"`
	AddTime      string         `json:"add_time"`
	UpdateTime   string         `json:"update_time"`
	CustomFields map[string]any `json:"custom_fields,omitempty"`
}

// ActivityLocation is Pipedrive v2's structured location for an
// activity (the place a meeting is held, etc.). Street-level
// components from the upstream schema (route, street_number,
// sublocality, admin_area_level_*) are intentionally omitted — humans
// care about "city/country, postal" which is what the LLM-facing
// output surfaces.
type ActivityLocation struct {
	Value      string `json:"value,omitempty"`
	Country    string `json:"country,omitempty"`
	Locality   string `json:"locality,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
}

// ActivityParticipant is one row in an Activity's participants array.
// Pipedrive marks one participant as primary (the contact the activity
// is principally tied to); the rest are co-participants. Distinct from
// Attendees, which are calendar invitees.
type ActivityParticipant struct {
	PersonID int64 `json:"person_id"`
	Primary  bool  `json:"primary"`
}

// ActivityAttendee is one row in an Activity's attendees array.
// Attendees are calendar-style invitees (email, name, RSVP status).
// PersonID is non-zero when Pipedrive matched the email to an existing
// person; UserID is non-zero when it matched a Pipedrive user instead.
// Both can be zero for an external attendee.
type ActivityAttendee struct {
	Email       string `json:"email,omitempty"`
	Name        string `json:"name,omitempty"`
	Status      string `json:"status,omitempty"` // accepted | declined | tentative | needsAction (Google-style)
	IsOrganizer bool   `json:"is_organizer,omitempty"`
	PersonID    int64  `json:"person_id,omitempty"`
	UserID      int64  `json:"user_id,omitempty"`
}

// Activity is a Pipedrive activity record (subset). Pipedrive v2 dropped
// the v1 `_flag` suffixes — `busy_flag` → `busy`, `done_flag` → `done`.
// Activities do NOT carry a `custom_fields` block on v2; the v2 schema
// omits it entirely. Times use Pipedrive's date/time formats (DueDate
// is YYYY-MM-DD, DueTime / Duration are HH:MM, AddTime / UpdateTime are
// the v2 RFC3339-with-space). Strings are surfaced raw to the LLM so it
// can pattern-match without timezone surprises.
type Activity struct {
	ID                      int64                 `json:"id"`
	Subject                 string                `json:"subject"`
	Type                    string                `json:"type"` // activity-type key (e.g. "call", "email", "meeting", "task"); free-form per workspace
	OwnerID                 int64                 `json:"owner_id"`
	DealID                  int64                 `json:"deal_id,omitempty"`
	PersonID                int64                 `json:"person_id,omitempty"`
	OrgID                   int64                 `json:"org_id,omitempty"`
	LeadID                  string                `json:"lead_id,omitempty"`
	ProjectID               int64                 `json:"project_id,omitempty"`
	DueDate                 string                `json:"due_date,omitempty"`
	DueTime                 string                `json:"due_time,omitempty"`
	Duration                string                `json:"duration,omitempty"`
	Busy                    bool                  `json:"busy"`
	Done                    bool                  `json:"done"`
	MarkedAsDoneTime        string                `json:"marked_as_done_time,omitempty"`
	Location                *ActivityLocation     `json:"location,omitempty"`
	Participants            []ActivityParticipant `json:"participants,omitempty"`
	Attendees               []ActivityAttendee    `json:"attendees,omitempty"`
	ConferenceMeetingClient string                `json:"conference_meeting_client,omitempty"`
	ConferenceMeetingURL    string                `json:"conference_meeting_url,omitempty"`
	ConferenceMeetingID     string                `json:"conference_meeting_id,omitempty"`
	PublicDescription       string                `json:"public_description,omitempty"`
	Note                    string                `json:"note,omitempty"`
	AddTime                 string                `json:"add_time"`
	UpdateTime              string                `json:"update_time"`
}

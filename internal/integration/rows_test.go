//go:build integration

package integration

// Test-side decode mirrors, the same pattern the unit tests use
// (dealRow, personRow, …). Two differences from those, both
// deliberate.
//
// They carry only the fields an assertion in this package reads.
// testutil.DecodeStructured does not set DisallowUnknownFields, so an
// unread field detects nothing — not a rename, not a removal — while
// looking like it does. A field earns its line here when a test reads
// it, and not before.
//
// contactRow is the exception: it is also a request payload, so all
// three of its fields have to marshal whether or not anything reads
// them back.

type dealRow struct {
	ID                int64          `json:"id"`
	Title             string         `json:"title"`
	ExpectedCloseDate string         `json:"expected_close_date,omitempty"`
	UpdateTime        string         `json:"update_time,omitempty"`
	CustomFields      map[string]any `json:"custom_fields,omitempty"`
	URL               string         `json:"url"`
}

type contactRow struct {
	Value   string `json:"value"`
	Primary bool   `json:"primary"`
	Label   string `json:"label,omitempty"`
}

type personRow struct {
	ID        int64        `json:"id"`
	Name      string       `json:"name"`
	FirstName string       `json:"first_name,omitempty"`
	Emails    []contactRow `json:"emails,omitempty"`
}

type orgRow struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type activityRow struct {
	ID   int64 `json:"id"`
	Done bool  `json:"done"`
}

type noteRow struct {
	ID         int64  `json:"id"`
	Content    string `json:"content"`
	ActiveFlag bool   `json:"active_flag"`
}

type pipelineRow struct {
	ID int64 `json:"id"`
}

type stageRow struct {
	ID         int64 `json:"id"`
	PipelineID int64 `json:"pipeline_id"`
}

type userRow struct {
	UserID        int64  `json:"user_id"`
	CompanyID     int64  `json:"company_id"`
	CompanyDomain string `json:"company_domain"`
	TimezoneName  string `json:"timezone_name"`
}

type hitRow struct {
	ID int64 `json:"id"`
}

type cacheRow struct {
	Resource string `json:"resource"`
	Count    int    `json:"count"`
	Error    string `json:"error,omitempty"`
}

// List and get envelopes.

type dealsOut struct {
	Deals      []dealRow `json:"deals"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

type dealOut struct {
	Deal dealRow `json:"deal"`
}

type personsOut struct {
	Persons []personRow `json:"persons"`
}

type personOut struct {
	Person personRow `json:"person"`
}

type orgsOut struct {
	Organizations []orgRow `json:"organizations"`
}

type orgOut struct {
	Organization orgRow `json:"organization"`
}

type activitiesOut struct {
	Activities []activityRow `json:"activities"`
}

type activityOut struct {
	Activity activityRow `json:"activity"`
}

type notesOut struct {
	Notes []noteRow `json:"notes"`
}

type noteOut struct {
	Note noteRow `json:"note"`
}

type pipelinesOut struct {
	Pipelines []pipelineRow `json:"pipelines"`
}

type stagesOut struct {
	Stages []stageRow `json:"stages"`
}

type whoamiOut struct {
	User userRow `json:"user"`
}

type searchOut struct {
	Hits []hitRow `json:"hits"`
}

type cacheOut struct {
	Refreshed []cacheRow `json:"refreshed"`
	Errors    int        `json:"errors"`
}

// Write envelopes. writeOut is what every probe reads off a manage_*
// result; the two that also read the stored record back embed it.

type writeOut struct {
	Changed []string `json:"changed,omitempty"`
	DryRun  bool     `json:"dry_run,omitempty"`
}

type dealWriteOut struct {
	writeOut
	Deal dealRow `json:"deal"`
}

type noteWriteOut struct {
	writeOut
	Note noteRow `json:"note"`
}

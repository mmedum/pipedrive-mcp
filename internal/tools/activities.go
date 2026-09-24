package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

type activitiesClient interface {
	GetActivity(ctx context.Context, id int64, opts pipedrive.GetActivityOptions) (*pipedrive.Activity, error)
	ListActivities(ctx context.Context, opts pipedrive.ListActivitiesOptions) ([]pipedrive.Activity, string, error)
	CreateActivity(ctx context.Context, req pipedrive.CreateActivityRequest) (*pipedrive.Activity, error)
	UpdateActivity(ctx context.Context, id int64, req pipedrive.UpdateActivityRequest) (*pipedrive.Activity, error)
	DeleteActivity(ctx context.Context, id int64) error
}

const (
	activityStatusOpen = "open"
	activityStatusDone = "done"
	activityStatusAll  = "all"
)

// allowedActivityStatuses is the closed enum for the LLM-facing `status`
// argument; statusToDoneFilter maps it onto Pipedrive's `done` param.
var allowedActivityStatuses = map[string]bool{
	activityStatusOpen: true,
	activityStatusDone: true,
	activityStatusAll:  true,
}

// activitySortByValues enumerates Pipedrive v2's allowed sort_by
// values. Extends the shared commonV2TimestampSortFields base with
// `due_date`, which is activity-specific. Surfacing the enum to the
// LLM keeps invalid sorts out of the upstream API.
var activitySortByValues = func() map[string]bool {
	m := make(map[string]bool, len(commonV2TimestampSortFields)+1)
	for k, v := range commonV2TimestampSortFields {
		m[k] = v
	}
	m["due_date"] = true
	return m
}()

// activitySummary is the LLM-facing shape of an activity. Mirrors the
// upstream Activity field-for-field plus a UI URL — matches the
// dealSummary / personSummary / organizationSummary parallel-shadow
// pattern. The duplication is the price of an explicit allow-list
// boundary between the HTTP wire and the LLM tool surface, so a future
// Pipedrive v2 field addition cannot silently leak through. Custom
// fields are not surfaced — Pipedrive v2 activities don't have them.
type activitySummary struct {
	ID                      int64                           `json:"id" jsonschema:"the activity's numeric id"`
	Subject                 string                          `json:"subject" jsonschema:"the activity's title"`
	Type                    string                          `json:"type" jsonschema:"activity type key (e.g. call, email, meeting, task); free-form per workspace"`
	OwnerID                 int64                           `json:"owner_id" jsonschema:"id of the user who owns this activity"`
	DealID                  int64                           `json:"deal_id" jsonschema:"id of the linked deal, 0 if none"`
	PersonID                int64                           `json:"person_id" jsonschema:"id of the linked person, 0 if none"`
	OrgID                   int64                           `json:"org_id" jsonschema:"id of the linked organization, 0 if none"`
	LeadID                  string                          `json:"lead_id,omitempty" jsonschema:"id of the linked lead (UUID string), empty if none"`
	ProjectID               int64                           `json:"project_id,omitempty" jsonschema:"id of the linked project, 0 if none"`
	DueDate                 string                          `json:"due_date,omitempty" jsonschema:"YYYY-MM-DD; the activity's scheduled day"`
	DueTime                 string                          `json:"due_time,omitempty" jsonschema:"HH:MM (24h); the activity's scheduled start time, empty for all-day"`
	Duration                string                          `json:"duration,omitempty" jsonschema:"HH:MM duration"`
	Busy                    bool                            `json:"busy" jsonschema:"true marks the owner as busy on calendar"`
	Done                    bool                            `json:"done" jsonschema:"true once the activity has been completed"`
	MarkedAsDoneTime        string                          `json:"marked_as_done_time,omitempty" jsonschema:"timestamp when the activity was marked done, empty if still open"`
	Location                *pipedrive.ActivityLocation     `json:"location,omitempty" jsonschema:"physical location: value (display string), country, locality, postal_code"`
	Participants            []pipedrive.ActivityParticipant `json:"participants,omitempty" jsonschema:"linked persons; one is marked primary"`
	Attendees               []pipedrive.ActivityAttendee    `json:"attendees,omitempty" jsonschema:"calendar invitees with RSVP status; populated only when include_attendees is true"`
	ConferenceMeetingClient string                          `json:"conference_meeting_client,omitempty" jsonschema:"conference provider name (e.g. zoom, googlemeet)"`
	ConferenceMeetingURL    string                          `json:"conference_meeting_url,omitempty" jsonschema:"join URL for the conference call"`
	ConferenceMeetingID     string                          `json:"conference_meeting_id,omitempty" jsonschema:"provider-specific meeting id"`
	PublicDescription       string                          `json:"public_description,omitempty" jsonschema:"description shared with attendees in the calendar invite; on list_activities, populated only when include_notes is true"`
	Note                    string                          `json:"note,omitempty" jsonschema:"private note attached to the activity; on list_activities, populated only when include_notes is true"`
	AddTime                 string                          `json:"add_time,omitempty" jsonschema:"timestamp the activity was created"`
	UpdateTime              string                          `json:"update_time,omitempty" jsonschema:"timestamp the activity was last updated"`
	URL                     string                          `json:"url" jsonschema:"link to the activity in the Pipedrive web UI"`
}

type getActivityInput struct {
	ActivityID       int64 `json:"activity_id" jsonschema:"the activity's numeric id"`
	IncludeAttendees bool  `json:"include_attendees,omitempty" jsonschema:"if true, also return calendar attendees (email, name, RSVP status); off by default to keep payloads small"`
}

type getActivityOutput struct {
	Activity activitySummary `json:"activity" jsonschema:"the requested activity"`
}

type listActivitiesInput struct {
	Status           string `json:"status,omitempty" jsonschema:"open | done | all. Default 'all' returns both open and completed activities."`
	OwnerID          int64  `json:"owner_id,omitempty" jsonschema:"return only activities owned by this user id; 0 = no filter"`
	DealID           int64  `json:"deal_id,omitempty" jsonschema:"return only activities linked to this deal id; 0 = no filter"`
	PersonID         int64  `json:"person_id,omitempty" jsonschema:"return only activities where this person is the primary participant; 0 = no filter"`
	OrgID            int64  `json:"org_id,omitempty" jsonschema:"return only activities linked to this organization id; 0 = no filter"`
	LeadID           string `json:"lead_id,omitempty" jsonschema:"return only activities linked to this lead UUID; empty = no filter"`
	UpdatedSince     string `json:"updated_since,omitempty" jsonschema:"RFC3339 timestamp; return only activities updated at or after this time (e.g. 2026-04-01T00:00:00Z)"`
	UpdatedUntil     string `json:"updated_until,omitempty" jsonschema:"RFC3339 timestamp; return only activities updated at or before this time"`
	SortBy           string `json:"sort_by,omitempty" jsonschema:"id | update_time | add_time | due_date. Default 'update_time' (most-recently-touched first)."`
	SortDirection    string `json:"sort_direction,omitempty" jsonschema:"asc | desc. Default 'desc' when sort_by is omitted; 'asc' otherwise."`
	IncludeAttendees bool   `json:"include_attendees,omitempty" jsonschema:"if true, also return calendar attendees on every row; off by default to keep payloads small"`
	IncludeNotes     bool   `json:"include_notes,omitempty" jsonschema:"if true, include note and public_description on every row. Off by default because activity notes are often multi-KB HTML and would bloat LLM context on a sweep. Use get_activity for the full text of a single row."`
	Limit            int    `json:"limit,omitempty" jsonschema:"page size; default 25, max 100"`
	Cursor           string `json:"cursor,omitempty" jsonschema:"opaque pagination token from a previous list_activities response; omit for the first page"`
}

type listActivitiesOutput struct {
	Activities []activitySummary `json:"activities" jsonschema:"matching activities on this page"`
	NextCursor string            `json:"next_cursor,omitempty" jsonschema:"pass to the next list_activities call to get the next page; empty when there are no more pages"`
}

// RegisterActivities wires the activity tools into the MCP server.
// Reads are discrete (get_activity, list_activities); every mutation
// goes through manage_activity. opts.DryRun is the server-wide dry-run
// floor.
func RegisterActivities(s *mcp.Server, c activitiesClient, companyDomain string, opts RegisterOptions) {

	AddTool(s, &mcp.Tool{
		Name:        "get_activity",
		Description: "Fetch a single Pipedrive activity (call, email, meeting, task, ...) by activity_id. Returns id, subject, type, owner_id, linked deal_id / person_id / org_id, due_date, due_time, duration, done flag, location, participants, conference meeting details, and notes. Set include_attendees=true to also return calendar invitees with RSVP status. Unknown activity_id returns a [not_found] error. To find an activity by subject text, call `list_activities` filtered by deal_id or person_id; activities are not indexed by `search`.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getActivityInput) (*mcp.CallToolResult, getActivityOutput, error) {
		if err := validatePositiveID(in.ActivityID, "activity_id"); err != nil {
			return errorResult(err), getActivityOutput{}, nil
		}
		a, err := c.GetActivity(ctx, in.ActivityID, pipedrive.GetActivityOptions{
			IncludeAttendees: in.IncludeAttendees,
		})
		if err != nil {
			return errorResult(err), getActivityOutput{}, nil
		}
		return nil, getActivityOutput{Activity: summarizeActivity(companyDomain, a)}, nil
	})

	AddTool(s, &mcp.Tool{
		Name:        "list_activities",
		Description: "List Pipedrive activities filtered by status (open | done | all), owner, deal, person, organization, lead, or update window. Returns id, subject, type, owner_id, linked deal/person/org ids, due_date, due_time, duration, done flag, location, participants, and conference details. Notes (`note`, `public_description`) are stripped by default — set `include_notes=true` or call `get_activity` for full text. Default sort is update_time desc — most-recently-touched first, ideal for 'what's happening with X lately'. Pass sort_by=due_date and status=open for an upcoming-calendar view. For more results, pass the next_cursor from the previous response. Activity-type (call/email/meeting/...) cannot be filtered server-side on Pipedrive v2; filter the returned rows by their `type` field client-side.",
		Annotations: readOnlyAnnotations(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listActivitiesInput) (*mcp.CallToolResult, listActivitiesOutput, error) {
		if err := validateEnum(in.Status, "status", allowedActivityStatuses); err != nil {
			return errorResult(err), listActivitiesOutput{}, nil
		}
		if err := validateEnum(in.SortBy, "sort_by", activitySortByValues); err != nil {
			return errorResult(err), listActivitiesOutput{}, nil
		}
		if err := validateEnum(in.SortDirection, "sort_direction", allowedSortDirections); err != nil {
			return errorResult(err), listActivitiesOutput{}, nil
		}

		sortBy, sortDir := effectiveSort(in.SortBy, in.SortDirection)
		opts := pipedrive.ListActivitiesOptions{
			OwnerID:          in.OwnerID,
			DealID:           in.DealID,
			PersonID:         in.PersonID,
			OrgID:            in.OrgID,
			LeadID:           in.LeadID,
			Done:             statusToDoneFilter(in.Status),
			UpdatedSince:     in.UpdatedSince,
			UpdatedUntil:     in.UpdatedUntil,
			SortBy:           sortBy,
			SortDirection:    sortDir,
			IncludeAttendees: in.IncludeAttendees,
			Limit:            clampLimit(in.Limit),
			Cursor:           in.Cursor,
		}
		acts, next, err := c.ListActivities(ctx, opts)
		if err != nil {
			return errorResult(err), listActivitiesOutput{}, nil
		}
		out := listActivitiesOutput{Activities: make([]activitySummary, 0, len(acts)), NextCursor: next}
		// Notes can run to multi-KB HTML; strip them by default so
		// list sweeps don't burn LLM tokens on content the model
		// can fetch on demand via get_activity. Mutate in place since
		// acts is a fresh slice returned to us — no external aliases.
		stripNotes := !in.IncludeNotes
		for i := range acts {
			if stripNotes {
				acts[i].Note = ""
				acts[i].PublicDescription = ""
			}
			out.Activities = append(out.Activities, summarizeActivity(companyDomain, &acts[i]))
		}
		return nil, out, nil
	})

	registerManageActivity(s, c, companyDomain, opts)
}

// activityActions is the closed enum manage_activity dispatches on, and
// what it needs to know about each one: whether it creates rather than
// writes, and whether it authorises its own overwrite. Same shape and
// same reasoning as dealAction — complete and reopen name the field they
// land on, so the caller already sees the blast radius.
type activityAction struct {
	creates    bool
	transition bool
	deletes    bool
}

var activityActions = map[string]activityAction{
	"create":   {creates: true},
	"update":   {},
	"complete": {transition: true},
	"reopen":   {transition: true},
	"delete":   {deletes: true},
}

// activityFields is the one table of LLM-facing field names a write can
// touch. Every field UpdateActivityRequest can send must appear here:
// the table is what the diff and the overwrite guard walk, so a field
// that is writable but untabled is a field that gets written without
// being guarded or reported.
//
// marked_as_done_time is absent because Pipedrive stamps it itself when
// done flips; attendees and conference details are absent because they
// come from the calendar integration and no write here touches them.
var activityFields = []fieldSpec[pipedrive.Activity]{
	{"subject", func(a *pipedrive.Activity) string { return a.Subject }},
	{"type", func(a *pipedrive.Activity) string { return a.Type }},
	{"due_date", func(a *pipedrive.Activity) string { return a.DueDate }},
	{"due_time", func(a *pipedrive.Activity) string { return a.DueTime }},
	{"duration", func(a *pipedrive.Activity) string { return a.Duration }},
	{"deal_id", func(a *pipedrive.Activity) string { return projectID(a.DealID) }},
	{"person_id", func(a *pipedrive.Activity) string { return projectID(a.PersonID) }},
	{"org_id", func(a *pipedrive.Activity) string { return projectID(a.OrgID) }},
	{"lead_id", func(a *pipedrive.Activity) string { return a.LeadID }},
	{"owner_id", func(a *pipedrive.Activity) string { return projectID(a.OwnerID) }},
	{"note", func(a *pipedrive.Activity) string { return a.Note }},
	{"public_description", func(a *pipedrive.Activity) string { return a.PublicDescription }},
	{"location", func(a *pipedrive.Activity) string { return projectLocation(a.Location) }},
	{"done", func(a *pipedrive.Activity) string { return projectBool(a.Done) }},
	{"busy", func(a *pipedrive.Activity) string { return projectBool(a.Busy) }},
	// participants belongs here because an update REPLACES the
	// collection. Left out of the table it was written but never
	// diffed, so the overwrite guard could not refuse over it, a
	// participants-only update looked like a no-op, and `changed` never
	// admitted that attendees had been dropped.
	{"participants", func(a *pipedrive.Activity) string { return projectParticipants(a.Participants) }},
}

type manageActivityInput struct {
	Action            string                          `json:"action" jsonschema:"create, update, complete, reopen or delete"`
	ActivityID        int64                           `json:"activity_id,omitempty" jsonschema:"the activity to act on, required by every action except create"`
	Subject           *string                         `json:"subject,omitempty" jsonschema:"the activity's title, required by create. If the user did not give you one, ask — do NOT invent it"`
	Type              *string                         `json:"type,omitempty" jsonschema:"the activity-type key such as call, email, meeting or task. The valid set varies per workspace, so copy an exact key off an existing activity; create defaults to task"`
	DueDate           *string                         `json:"due_date,omitempty" jsonschema:"YYYY-MM-DD the activity is scheduled for. Omit to leave it as it is; removing a date once set is not supported here"`
	DueTime           *string                         `json:"due_time,omitempty" jsonschema:"HH:MM in 24-hour time. Omit for an all-day activity"`
	Duration          *string                         `json:"duration,omitempty" jsonschema:"HH:MM long"`
	DealID            *int64                          `json:"deal_id,omitempty" jsonschema:"the linked deal. Omit to leave it as it is; unlinking is not supported here"`
	PersonID          *int64                          `json:"person_id,omitempty" jsonschema:"the linked person, who becomes the primary participant. Omit to leave it as it is; unlinking is not supported here"`
	OrgID             *int64                          `json:"org_id,omitempty" jsonschema:"the linked organization. Omit to leave it as it is; unlinking is not supported here"`
	LeadID            *string                         `json:"lead_id,omitempty" jsonschema:"the linked lead UUID. Omit to leave it as it is; unlinking is not supported here"`
	OwnerID           *int64                          `json:"owner_id,omitempty" jsonschema:"the user who owns the record; omit on create to take the API token's own user"`
	Note              *string                         `json:"note,omitempty" jsonschema:"a private note on the activity, HTML allowed. If the user did not dictate notes, leave this blank — do NOT invent meeting minutes"`
	PublicDescription *string                         `json:"public_description,omitempty" jsonschema:"the description attendees see in the calendar invite. If the user did not provide one, leave it blank"`
	Location          *string                         `json:"location,omitempty" jsonschema:"a single line as the user said it, such as 123 Main St or 'Zoom — link in invite'. Pipedrive parses physical addresses server-side, so do NOT pre-parse it"`
	Participants      []pipedrive.ActivityParticipant `json:"participants,omitempty" jsonschema:"linked persons, exactly one marked primary. person_id wins if both are set. On update this REPLACES the collection"`
	Busy              *bool                           `json:"busy,omitempty" jsonschema:"whether the owner shows as busy on the calendar"`
	Done              *bool                           `json:"done,omitempty" jsonschema:"whether the activity is finished. Prefer action complete or reopen, which say what you mean; this is here for create, to log something that already happened"`
	DryRun            bool                            `json:"dry_run,omitempty" jsonschema:"report what the write would find and change, and send nothing"`
	Overwrite         []string                        `json:"overwrite,omitempty" jsonschema:"the fields this write may replace, named exactly as the refusal listed them, e.g. [\"title\", \"value\"]. Omit it and a write that would replace a populated field is refused, naming each one. A refusal is NOT a retry signal: name a field only when the user asked for what is already there to be replaced, never to get past a refusal they have not seen. Naming fewer fields than the refusal listed is still refused, over the ones you left out"`
	ExpectVersion     string                          `json:"expect_version,omitempty" jsonschema:"the update_time from the read that informed this write; the write is refused if the activity changed since"`
}

type manageActivityOutput struct {
	Action   string          `json:"action" jsonschema:"the action that ran"`
	Activity activitySummary `json:"activity" jsonschema:"the activity as Pipedrive stored it. When dry_run is true nothing was persisted and a created activity carries id=0."`
	Changed  []string        `json:"changed,omitempty" jsonschema:"names of the fields this write actually altered, empty when it was a no-op. On a dry run, the fields it would alter."`
	DryRun   bool            `json:"dry_run,omitempty" jsonschema:"true when nothing was sent upstream"`
}

func registerManageActivity(s *mcp.Server, c activitiesClient, companyDomain string, opts RegisterOptions) {

	AddTool(s, &mcp.Tool{
		Name:        "manage_activity",
		Description: "Create an activity — a call, email, meeting or task — edit one, tick it off, or delete it. One call, whichever action: create takes subject, every other action takes activity_id. To log something that already happened, create it with done true and the note; to schedule something, give it a due_date. Writing is guarded, and every action except create reads the activity before it writes, so a write is two API calls. An update refuses to replace ANY field that already holds a value unless you pass overwrite, and the refusal names each one; filling a field that is empty destroys nothing and needs no permission. expect_version refuses the write outright if the record moved under you. complete and reopen just flip done and take no overwrite, because the field they change is the one you named — and reopen is why completing is not a one-way door. IMPORTANT: note is private and public_description is what attendees read in the calendar invite, so do not put one where the other belongs. The activity-type key varies per workspace: copy an exact one off an existing activity rather than guessing, since an unknown type is rejected upstream. A field that already holds a value can be changed but NOT cleared: Pipedrive v2 rejects a null and reads an empty string as a value. Deleting is soft and time-boxed: Pipedrive marks the activity deleted and removes it permanently after 30 days, so it takes dry_run and expect_version and no permitting flag beyond them — within that window Pipedrive's own UI can restore it, but NOTHING HERE PUTS IT BACK, so treat it as one-way and rehearse with dry_run first. Nothing hangs off an activity — notes anchor to deals, persons, organizations, leads and projects, never to an activity — so deleting one takes nothing with it that you have not already read. Use search to turn a company or person name into the ids this links to.",
		Annotations: mutatingAnnotations(),
	}, manageActivityHandler(c, companyDomain, opts.DryRun))
}

func manageActivityHandler(c activitiesClient, companyDomain string, dryRun bool) mcp.ToolHandlerFor[manageActivityInput, manageActivityOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in manageActivityInput) (*mcp.CallToolResult, manageActivityOutput, error) {
		if err := validateAction(in.Action, activityActions); err != nil {
			return errorResult(err), manageActivityOutput{}, nil
		}
		in.DryRun = in.DryRun || dryRun

		var (
			res *mcp.CallToolResult
			out manageActivityOutput
		)
		switch {
		case activityActions[in.Action].creates:
			res, out = createActivityAction(ctx, c, companyDomain, in)
		case activityActions[in.Action].deletes:
			res, out = deleteActivityAction(ctx, c, companyDomain, in)
		default:
			res, out = writeActivityAction(ctx, c, companyDomain, in)
		}
		if res == nil {
			out.Action = in.Action
			out.DryRun = in.DryRun
		}
		return res, out, nil
	}
}

func createActivityAction(ctx context.Context, c activitiesClient, companyDomain string, in manageActivityInput) (*mcp.CallToolResult, manageActivityOutput) {
	if in.Subject == nil || *in.Subject == "" {
		return errorResult(fmt.Errorf("%w: subject must not be empty", pipedrive.ErrValidation)), manageActivityOutput{}
	}
	req := pipedrive.CreateActivityRequest{
		Subject:           *in.Subject,
		Type:              deref(in.Type),
		DueDate:           deref(in.DueDate),
		DueTime:           deref(in.DueTime),
		Duration:          deref(in.Duration),
		DealID:            deref(in.DealID),
		PersonID:          deref(in.PersonID),
		OrgID:             deref(in.OrgID),
		LeadID:            deref(in.LeadID),
		OwnerID:           deref(in.OwnerID),
		Note:              deref(in.Note),
		PublicDescription: deref(in.PublicDescription),
		Location:          deref(in.Location),
		Participants:      in.Participants,
		Done:              deref(in.Done),
		Busy:              deref(in.Busy),
	}
	created := syntheticActivityFromRequest(req)
	if !in.DryRun {
		a, err := c.CreateActivity(ctx, req)
		if err != nil {
			return errorResult(err), manageActivityOutput{}
		}
		created = a
	}
	return nil, manageActivityOutput{
		Activity: summarizeActivity(companyDomain, created),
		Changed:  changedFields(activityFields, &pipedrive.Activity{}, created),
	}
}

// writeActivityAction serves update, complete and reopen. They share the
// read-guard-write spine and differ only in the request they build.
func writeActivityAction(ctx context.Context, c activitiesClient, companyDomain string, in manageActivityInput) (*mcp.CallToolResult, manageActivityOutput) {
	if err := validatePositiveID(in.ActivityID, "activity_id"); err != nil {
		return errorResult(err), manageActivityOutput{}
	}
	req := activityRequestFor(in)

	activity, changed, res := guardedWrite[pipedrive.Activity]{
		Spec:          activityFields,
		Resource:      fmt.Sprintf("activity %d", in.ActivityID),
		ExpectVersion: in.ExpectVersion,
		Version:       func(a *pipedrive.Activity) string { return a.UpdateTime },
		// complete and reopen authorise their own overwrite; see
		// activityAction.
		Overwrite:    in.Overwrite,
		OverwriteAll: activityActions[in.Action].transition,
		DryRun:       in.DryRun,
		Get: func(ctx context.Context) (*pipedrive.Activity, error) {
			// The guard read deliberately does not ask for attendees:
			// nothing here writes them, and they are the expensive part.
			return c.GetActivity(ctx, in.ActivityID, pipedrive.GetActivityOptions{})
		},
		Predict: func(a *pipedrive.Activity) pipedrive.Activity { return activityAfterUpdate(*a, req) },
		Put: func(ctx context.Context) (*pipedrive.Activity, error) {
			return c.UpdateActivity(ctx, in.ActivityID, req)
		},
	}.run(ctx)
	if res != nil {
		return res, manageActivityOutput{}
	}
	return nil, manageActivityOutput{Activity: summarizeActivity(companyDomain, activity), Changed: changed}
}

// activityRequestFor turns an action plus its inputs into the PATCH
// body. complete and reopen ignore every descriptive field, so a caller
// who also passed a subject does not silently get it written.
func activityRequestFor(in manageActivityInput) pipedrive.UpdateActivityRequest {
	switch in.Action {
	case "complete":
		return pipedrive.UpdateActivityRequest{Done: ptr(true)}
	case "reopen":
		return pipedrive.UpdateActivityRequest{Done: ptr(false)}
	default: // update
		return pipedrive.UpdateActivityRequest{
			Subject:           in.Subject,
			Type:              in.Type,
			Participants:      in.Participants,
			Done:              in.Done,
			Busy:              in.Busy,
			DueDate:           in.DueDate,
			DueTime:           in.DueTime,
			Duration:          in.Duration,
			DealID:            in.DealID,
			PersonID:          in.PersonID,
			OrgID:             in.OrgID,
			LeadID:            in.LeadID,
			OwnerID:           in.OwnerID,
			Note:              in.Note,
			PublicDescription: in.PublicDescription,
			Location:          in.Location,
		}
	}
}

func activityAfterUpdate(before pipedrive.Activity, req pipedrive.UpdateActivityRequest) pipedrive.Activity {
	after := before
	setIf(&after.Subject, req.Subject)
	setIf(&after.Type, req.Type)
	setIf(&after.Done, req.Done)
	setIf(&after.Busy, req.Busy)
	setIf(&after.DueDate, req.DueDate)
	setIf(&after.DueTime, req.DueTime)
	setIf(&after.Duration, req.Duration)
	setIf(&after.DealID, req.DealID)
	setIf(&after.PersonID, req.PersonID)
	setIf(&after.OrgID, req.OrgID)
	setIf(&after.LeadID, req.LeadID)
	setIf(&after.OwnerID, req.OwnerID)
	setIf(&after.Note, req.Note)
	setIf(&after.PublicDescription, req.PublicDescription)
	if req.Location != nil {
		// Pipedrive re-parses the line server-side; the prediction only
		// needs the value the guard and the diff compare on.
		after.Location = &pipedrive.ActivityLocation{Value: *req.Location}
	}
	if req.Participants != nil {
		after.Participants = req.Participants
	}
	return after
}

// syntheticActivityFromRequest builds a placeholder Activity mirroring
// the CreateActivityRequest, used only on the dry-run path so
// create_activity's output schema stays consistent. ID=0 +
// dry_run=true tells the LLM nothing was actually persisted. Location
// is wrapped as ActivityLocation.Value; server-side parsing only
// happens on the real upstream call.
func syntheticActivityFromRequest(req pipedrive.CreateActivityRequest) *pipedrive.Activity {
	a := &pipedrive.Activity{
		Subject:           req.Subject,
		Type:              req.Type,
		OwnerID:           req.OwnerID,
		DealID:            req.DealID,
		PersonID:          req.PersonID,
		OrgID:             req.OrgID,
		LeadID:            req.LeadID,
		DueDate:           req.DueDate,
		DueTime:           req.DueTime,
		Duration:          req.Duration,
		Done:              req.Done,
		Busy:              req.Busy,
		Note:              req.Note,
		PublicDescription: req.PublicDescription,
		Participants:      req.Participants,
	}
	if req.Location != "" {
		a.Location = &pipedrive.ActivityLocation{Value: req.Location}
	}
	return a
}

func summarizeActivity(domain string, a *pipedrive.Activity) activitySummary {
	return activitySummary{
		ID:                      a.ID,
		Subject:                 a.Subject,
		Type:                    a.Type,
		OwnerID:                 a.OwnerID,
		DealID:                  a.DealID,
		PersonID:                a.PersonID,
		OrgID:                   a.OrgID,
		LeadID:                  a.LeadID,
		ProjectID:               a.ProjectID,
		DueDate:                 a.DueDate,
		DueTime:                 a.DueTime,
		Duration:                a.Duration,
		Busy:                    a.Busy,
		Done:                    a.Done,
		MarkedAsDoneTime:        a.MarkedAsDoneTime,
		Location:                a.Location,
		Participants:            a.Participants,
		Attendees:               a.Attendees,
		ConferenceMeetingClient: a.ConferenceMeetingClient,
		ConferenceMeetingURL:    a.ConferenceMeetingURL,
		ConferenceMeetingID:     a.ConferenceMeetingID,
		PublicDescription:       a.PublicDescription,
		Note:                    a.Note,
		AddTime:                 a.AddTime,
		UpdateTime:              a.UpdateTime,
		URL:                     pipedrive.WebURL(domain, pipedrive.WebURLActivity, a.ID),
	}
}

// statusToDoneFilter maps the human-facing "open|done|all" enum onto
// the tri-state `done` query param. "all" / unset returns nil = no
// filter.
func statusToDoneFilter(status string) *bool {
	switch status {
	case activityStatusOpen:
		v := false
		return &v
	case activityStatusDone:
		v := true
		return &v
	}
	return nil
}

// deleteActivityAction marks an activity deleted.
//
// dry_run and expect_version, and nothing else. Pipedrive's delete is
// soft and time-boxed, which is the reversible case, and the read this
// performs puts the activity on screen before it goes.
//
// Unlike a deal, a person or an organization, an activity is a leaf:
// notes anchor to deals, persons, organizations, leads and projects,
// never to an activity, and nothing else hangs off one. Its entire
// blast radius is the row the caller just read, which is the case
// CLAUDE.md names as not needing a permitting argument at all.
func deleteActivityAction(ctx context.Context, c activitiesClient, companyDomain string, in manageActivityInput) (*mcp.CallToolResult, manageActivityOutput) {
	if err := validatePositiveID(in.ActivityID, "activity_id"); err != nil {
		return errorResult(err), manageActivityOutput{}
	}
	before, err := c.GetActivity(ctx, in.ActivityID, pipedrive.GetActivityOptions{})
	if err != nil {
		return errorResult(err), manageActivityOutput{}
	}
	if err := checkExpectVersion(in.ExpectVersion, before.UpdateTime, fmt.Sprintf("activity %d", in.ActivityID)); err != nil {
		return errorResult(err), manageActivityOutput{}
	}
	if !in.DryRun {
		if err := c.DeleteActivity(ctx, in.ActivityID); err != nil {
			return errorResult(err), manageActivityOutput{}
		}
	}
	return nil, manageActivityOutput{Activity: summarizeActivity(companyDomain, before), Changed: []string{"deleted"}}
}

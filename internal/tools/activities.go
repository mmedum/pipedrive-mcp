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

type createActivityInput struct {
	Subject           string                          `json:"subject" jsonschema:"the activity's title; required, non-empty. If the user did not give you a subject, ask — do NOT invent one."`
	Type              string                          `json:"type,omitempty" jsonschema:"activity-type key (e.g. 'call', 'email', 'meeting', 'task'). Use the exact key from existing activities in the workspace; defaults to 'task' when omitted. The valid set varies per workspace."`
	DueDate           string                          `json:"due_date,omitempty" jsonschema:"YYYY-MM-DD; the day the activity is scheduled for"`
	DueTime           string                          `json:"due_time,omitempty" jsonschema:"HH:MM (24h); the start time. Omit for an all-day activity."`
	Duration          string                          `json:"duration,omitempty" jsonschema:"HH:MM duration"`
	DealID            int64                           `json:"deal_id,omitempty" jsonschema:"id of the linked deal; 0 = no link"`
	PersonID          int64                           `json:"person_id,omitempty" jsonschema:"id of the linked person (becomes the primary participant); 0 = no link"`
	OrgID             int64                           `json:"org_id,omitempty" jsonschema:"id of the linked organization; 0 = no link"`
	LeadID            string                          `json:"lead_id,omitempty" jsonschema:"id of the linked lead (UUID string); empty = no link"`
	OwnerID           int64                           `json:"owner_id,omitempty" jsonschema:"id of the user to own the record; omit to default to the API-token user"`
	Note              string                          `json:"note,omitempty" jsonschema:"private note attached to the activity. HTML allowed. If the user did not dictate notes, leave blank — do NOT invent meeting minutes."`
	PublicDescription string                          `json:"public_description,omitempty" jsonschema:"description shared with attendees in the calendar invite. If the user did not provide one, leave blank."`
	Location          string                          `json:"location,omitempty" jsonschema:"single-line location as the user dictated it (e.g. '123 Main St' or 'Zoom — link in invite'). Pipedrive parses physical addresses server-side. Do NOT pre-parse into JSON."`
	Participants      []pipedrive.ActivityParticipant `json:"participants,omitempty" jsonschema:"linked persons; mark exactly one as primary. PersonID overrides this if both are set."`
	Done              bool                            `json:"done,omitempty" jsonschema:"true to create the activity already marked done (e.g. logging a call that just happened); false (default) for a future / planned activity"`
	Busy              bool                            `json:"busy,omitempty" jsonschema:"true marks the owner as busy on the calendar"`
}

type createActivityOutput struct {
	Activity activitySummary `json:"activity" jsonschema:"the newly-created activity as Pipedrive echoes it. When dry_run is true, this is a synthetic record with id=0 reflecting what would have been created."`
	DryRun   bool            `json:"dry_run,omitempty" jsonschema:"true when PIPEDRIVE_DRY_RUN was set on the server: no upstream POST was issued"`
}

// RegisterActivities wires get_activity, list_activities, and
// create_activity into the MCP server. opts.DryRun, when true, makes
// create_activity return a synthetic preview without firing the
// upstream POST.
func RegisterActivities(s *mcp.Server, c activitiesClient, companyDomain string, opts RegisterOptions) {
	readOnly := mcp.ToolAnnotations{ReadOnlyHint: true}

	AddTool(s, &mcp.Tool{
		Name:        "get_activity",
		Description: "Fetch a single Pipedrive activity (call, email, meeting, task, ...) by activity_id. Returns id, subject, type, owner_id, linked deal_id / person_id / org_id, due_date, due_time, duration, done flag, location, participants, conference meeting details, and notes. Set include_attendees=true to also return calendar invitees with RSVP status. Unknown activity_id returns a [not_found] error. To find an activity by subject text, call `list_activities` filtered by deal_id or person_id; activities are not indexed by `search`.",
		Annotations: &readOnly,
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
		Annotations: &readOnly,
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

	AddTool(s, &mcp.Tool{
		Name:        "create_activity",
		Description: "Create a new Pipedrive activity (call, email, meeting, task, ...). Required: `subject`. To log an activity that already happened, pass `done=true` plus the `note`. To schedule a future activity, pass `due_date` (and optionally `due_time` + `duration`). Honours PIPEDRIVE_DRY_RUN=true on the server by returning a synthetic preview (dry_run=true, id=0) without issuing the POST.",
	}, createActivityHandler(c, companyDomain, opts.DryRun))
}

func createActivityHandler(c activitiesClient, companyDomain string, dryRun bool) mcp.ToolHandlerFor[createActivityInput, createActivityOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in createActivityInput) (*mcp.CallToolResult, createActivityOutput, error) {
		if in.Subject == "" {
			return errorResult(fmt.Errorf("%w: subject must not be empty", pipedrive.ErrValidation)), createActivityOutput{}, nil
		}
		req := pipedrive.CreateActivityRequest{
			Subject:           in.Subject,
			Type:              in.Type,
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
			Participants:      in.Participants,
			Done:              in.Done,
			Busy:              in.Busy,
		}
		if dryRun {
			return nil, createActivityOutput{
				Activity: summarizeActivity(companyDomain, syntheticActivityFromRequest(req)),
				DryRun:   true,
			}, nil
		}
		a, err := c.CreateActivity(ctx, req)
		if err != nil {
			return errorResult(err), createActivityOutput{}, nil
		}
		return nil, createActivityOutput{Activity: summarizeActivity(companyDomain, a)}, nil
	}
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

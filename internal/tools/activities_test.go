package tools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
	"github.com/mmedum/pipedrive-mcp/internal/tools"
)

type fakeActivitiesClient struct {
	activity      *pipedrive.Activity
	activityErr   error
	activities    []pipedrive.Activity
	activityNext  string
	activitiesErr error

	lastListOpts pipedrive.ListActivitiesOptions
	lastGetOpts  pipedrive.GetActivityOptions
}

func (f *fakeActivitiesClient) GetActivity(_ context.Context, _ int64, opts pipedrive.GetActivityOptions) (*pipedrive.Activity, error) {
	f.lastGetOpts = opts
	return f.activity, f.activityErr
}

func (f *fakeActivitiesClient) ListActivities(_ context.Context, opts pipedrive.ListActivitiesOptions) ([]pipedrive.Activity, string, error) {
	f.lastListOpts = opts
	return f.activities, f.activityNext, f.activitiesErr
}

// activityRow mirrors the JSON shape RegisterActivities emits. Carrying
// a parallel shape keeps the unexported handler-local struct hidden
// from test code; drift is caught by the JSON tag names. Matches the
// dealRow pattern in deals_test.go.
type activityRow struct {
	ID                      int64                           `json:"id"`
	Subject                 string                          `json:"subject"`
	Type                    string                          `json:"type"`
	OwnerID                 int64                           `json:"owner_id"`
	DealID                  int64                           `json:"deal_id"`
	PersonID                int64                           `json:"person_id"`
	OrgID                   int64                           `json:"org_id"`
	LeadID                  string                          `json:"lead_id,omitempty"`
	DueDate                 string                          `json:"due_date,omitempty"`
	DueTime                 string                          `json:"due_time,omitempty"`
	Duration                string                          `json:"duration,omitempty"`
	Busy                    bool                            `json:"busy"`
	Done                    bool                            `json:"done"`
	MarkedAsDoneTime        string                          `json:"marked_as_done_time,omitempty"`
	Location                *pipedrive.ActivityLocation     `json:"location,omitempty"`
	Participants            []pipedrive.ActivityParticipant `json:"participants,omitempty"`
	Attendees               []pipedrive.ActivityAttendee    `json:"attendees,omitempty"`
	ConferenceMeetingClient string                          `json:"conference_meeting_client,omitempty"`
	ConferenceMeetingURL    string                          `json:"conference_meeting_url,omitempty"`
	PublicDescription       string                          `json:"public_description,omitempty"`
	Note                    string                          `json:"note,omitempty"`
	URL                     string                          `json:"url"`
}

func TestGetActivity_HappyPath(t *testing.T) {
	fake := &fakeActivitiesClient{
		activity: &pipedrive.Activity{
			ID:       77,
			Subject:  "Quarterly review",
			Type:     "meeting",
			OwnerID:  7,
			DealID:   42,
			PersonID: 11,
			OrgID:    13,
			DueDate:  "2026-05-01",
			DueTime:  "14:00",
			Duration: "01:00",
			Busy:     true,
			Done:     false,
			Location: &pipedrive.ActivityLocation{Value: "Acme HQ", Locality: "Berlin"},
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, fake, "acme")
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_activity",
		Arguments: map[string]any{"activity_id": 77, "include_attendees": true},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out struct {
		Activity activityRow `json:"activity"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)

	if out.Activity.ID != 77 || out.Activity.Subject != "Quarterly review" || out.Activity.Type != "meeting" {
		t.Errorf("activity = %+v; want id=77 subject='Quarterly review' type=meeting", out.Activity)
	}
	if out.Activity.URL != "https://acme.pipedrive.com/activity/77" {
		t.Errorf("URL = %q, want acme/activity/77", out.Activity.URL)
	}
	if out.Activity.Location == nil || out.Activity.Location.Locality != "Berlin" {
		t.Errorf("location = %+v; want locality=Berlin", out.Activity.Location)
	}
	if !fake.lastGetOpts.IncludeAttendees {
		t.Error("client did not receive IncludeAttendees=true")
	}
}

func TestGetActivity_RejectsZeroID(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, &fakeActivitiesClient{}, "acme")
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_activity",
		Arguments: map[string]any{"activity_id": 0},
	})
	if !res.IsError {
		t.Fatal("expected isError on zero activity_id")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
}

func TestGetActivity_UpstreamNotFound(t *testing.T) {
	fake := &fakeActivitiesClient{
		activityErr: &pipedrive.APIError{
			Class:    pipedrive.ErrNotFound,
			Status:   404,
			Message:  "Activity not found",
			Endpoint: "/api/v2/activities/99999",
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, fake, "acme")
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_activity",
		Arguments: map[string]any{"activity_id": 99999},
	})
	if !res.IsError {
		t.Fatal("expected isError on upstream 404")
	}
	if !strings.HasPrefix(contentText(res), "[not_found]") {
		t.Errorf("error text = %q; want [not_found] prefix", contentText(res))
	}
}

func TestListActivities_HappyPath(t *testing.T) {
	fake := &fakeActivitiesClient{
		activities: []pipedrive.Activity{
			{ID: 1, Subject: "A", Type: "call", Done: false},
			{ID: 2, Subject: "B", Type: "email", Done: true},
		},
		activityNext: "cursor-page-2",
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, fake, "acme")
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "list_activities",
		Arguments: map[string]any{
			"status":  "open",
			"deal_id": 42,
			"limit":   50,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out struct {
		Activities []activityRow `json:"activities"`
		NextCursor string        `json:"next_cursor"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)

	if len(out.Activities) != 2 {
		t.Fatalf("got %d activities, want 2", len(out.Activities))
	}
	if out.Activities[0].URL != "https://acme.pipedrive.com/activity/1" {
		t.Errorf("URL = %q, want acme/activity/1", out.Activities[0].URL)
	}
	if out.NextCursor != "cursor-page-2" {
		t.Errorf("next_cursor = %q, want cursor-page-2", out.NextCursor)
	}
	if fake.lastListOpts.DealID != 42 || fake.lastListOpts.Limit != 50 {
		t.Errorf("client received opts %+v; want deal_id=42 limit=50", fake.lastListOpts)
	}
	if fake.lastListOpts.Done == nil || *fake.lastListOpts.Done != false {
		t.Errorf("status=open should map to Done=&false; got %v", fake.lastListOpts.Done)
	}
}

func TestListActivities_StripsNotesByDefault(t *testing.T) {
	fake := &fakeActivitiesClient{
		activities: []pipedrive.Activity{
			{ID: 1, Subject: "A", Note: "<b>private</b>", PublicDescription: "<p>visible</p>"},
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, fake, "acme")
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_activities",
		Arguments: map[string]any{},
	})
	var out struct {
		Activities []activityRow `json:"activities"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)
	if len(out.Activities) != 1 {
		t.Fatalf("got %d rows, want 1", len(out.Activities))
	}
	if out.Activities[0].Note != "" || out.Activities[0].PublicDescription != "" {
		t.Errorf("notes leaked when include_notes=false: note=%q public=%q",
			out.Activities[0].Note, out.Activities[0].PublicDescription)
	}
}

func TestListActivities_IncludeNotesPreservesText(t *testing.T) {
	fake := &fakeActivitiesClient{
		activities: []pipedrive.Activity{
			{ID: 1, Subject: "A", Note: "private", PublicDescription: "visible"},
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, fake, "acme")
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_activities",
		Arguments: map[string]any{"include_notes": true},
	})
	var out struct {
		Activities []activityRow `json:"activities"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)
	if out.Activities[0].Note != "private" || out.Activities[0].PublicDescription != "visible" {
		t.Errorf("include_notes=true should preserve text; got note=%q public=%q",
			out.Activities[0].Note, out.Activities[0].PublicDescription)
	}
}

func TestGetActivity_AlwaysReturnsNotes(t *testing.T) {
	// get_activity is the escape hatch from list_activities' default
	// notes-strip, so it must always return the full text.
	fake := &fakeActivitiesClient{
		activity: &pipedrive.Activity{
			ID: 1, Subject: "A", Note: "private", PublicDescription: "visible",
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, fake, "acme")
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_activity",
		Arguments: map[string]any{"activity_id": 1},
	})
	var out struct {
		Activity activityRow `json:"activity"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)
	if out.Activity.Note != "private" || out.Activity.PublicDescription != "visible" {
		t.Errorf("get_activity should always return notes; got note=%q public=%q",
			out.Activity.Note, out.Activity.PublicDescription)
	}
}

func TestListActivities_StatusDoneMapsToTrue(t *testing.T) {
	fake := &fakeActivitiesClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, fake, "acme")
	})
	defer h.Close()

	_, _ = h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_activities",
		Arguments: map[string]any{"status": "done"},
	})
	if fake.lastListOpts.Done == nil || *fake.lastListOpts.Done != true {
		t.Errorf("status=done should map to Done=&true; got %v", fake.lastListOpts.Done)
	}
}

func TestListActivities_StatusAllOmitsDoneFilter(t *testing.T) {
	fake := &fakeActivitiesClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, fake, "acme")
	})
	defer h.Close()

	_, _ = h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_activities",
		Arguments: map[string]any{"status": "all"},
	})
	if fake.lastListOpts.Done != nil {
		t.Errorf("status=all should leave Done nil; got %v", *fake.lastListOpts.Done)
	}
}

func TestListActivities_DefaultsToRecencySort(t *testing.T) {
	fake := &fakeActivitiesClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, fake, "acme")
	})
	defer h.Close()

	_, _ = h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_activities",
		Arguments: map[string]any{},
	})
	if fake.lastListOpts.SortBy != "update_time" || fake.lastListOpts.SortDirection != "desc" {
		t.Errorf("default sort = %q %q; want update_time desc",
			fake.lastListOpts.SortBy, fake.lastListOpts.SortDirection)
	}
}

func TestListActivities_SortDirectionAloneKeepsRecencyDefault(t *testing.T) {
	fake := &fakeActivitiesClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, fake, "acme")
	})
	defer h.Close()

	_, _ = h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_activities",
		Arguments: map[string]any{"sort_direction": "desc"},
	})
	if fake.lastListOpts.SortBy != "update_time" || fake.lastListOpts.SortDirection != "desc" {
		t.Errorf("sort = %q %q; want update_time desc",
			fake.lastListOpts.SortBy, fake.lastListOpts.SortDirection)
	}
}

func TestListActivities_ExplicitSortByUpdateTimeStillDefaultsToAsc(t *testing.T) {
	// Pins the documented contract: sort_by=update_time alone produces
	// chronological asc, not the recency default. To get recency-first
	// the user must omit sort_by entirely (or pass both update_time + desc).
	fake := &fakeActivitiesClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, fake, "acme")
	})
	defer h.Close()

	_, _ = h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_activities",
		Arguments: map[string]any{"sort_by": "update_time"},
	})
	if fake.lastListOpts.SortBy != "update_time" || fake.lastListOpts.SortDirection != "asc" {
		t.Errorf("explicit sort_by=update_time should still default direction to asc; got %q %q",
			fake.lastListOpts.SortBy, fake.lastListOpts.SortDirection)
	}
}

func TestListActivities_BothExplicitSortPassedThrough(t *testing.T) {
	fake := &fakeActivitiesClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, fake, "acme")
	})
	defer h.Close()

	_, _ = h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_activities",
		Arguments: map[string]any{"sort_by": "due_date", "sort_direction": "desc"},
	})
	if fake.lastListOpts.SortBy != "due_date" || fake.lastListOpts.SortDirection != "desc" {
		t.Errorf("both explicit should pass through; got %q %q",
			fake.lastListOpts.SortBy, fake.lastListOpts.SortDirection)
	}
}

func TestListActivities_StatusUnsetLeavesDoneNil(t *testing.T) {
	// Unset status must produce Done=nil so Pipedrive returns both
	// open and completed.
	fake := &fakeActivitiesClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, fake, "acme")
	})
	defer h.Close()

	_, _ = h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_activities",
		Arguments: map[string]any{},
	})
	if fake.lastListOpts.Done != nil {
		t.Errorf("unset status should leave Done nil; got %v", *fake.lastListOpts.Done)
	}
}

func TestListActivities_ExplicitSortByDefaultsToAsc(t *testing.T) {
	fake := &fakeActivitiesClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, fake, "acme")
	})
	defer h.Close()

	_, _ = h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_activities",
		Arguments: map[string]any{"sort_by": "due_date"},
	})
	if fake.lastListOpts.SortBy != "due_date" || fake.lastListOpts.SortDirection != "asc" {
		t.Errorf("sort = %q %q; want due_date asc (chronological default)",
			fake.lastListOpts.SortBy, fake.lastListOpts.SortDirection)
	}
}

func TestListActivities_RejectsBadStatus(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, &fakeActivitiesClient{}, "acme")
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_activities",
		Arguments: map[string]any{"status": "pending"},
	})
	if !res.IsError {
		t.Fatal("expected isError on bad status")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
}

func TestListActivities_RejectsBadSortBy(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, &fakeActivitiesClient{}, "acme")
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_activities",
		Arguments: map[string]any{"sort_by": "subject"},
	})
	if !res.IsError {
		t.Fatal("expected isError on bad sort_by")
	}
}

func TestListActivities_LimitClampedToMax(t *testing.T) {
	fake := &fakeActivitiesClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, fake, "acme")
	})
	defer h.Close()

	_, _ = h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_activities",
		Arguments: map[string]any{"limit": 999},
	})
	if fake.lastListOpts.Limit != 100 {
		t.Errorf("limit=%d; want 100 (clamped from 999)", fake.lastListOpts.Limit)
	}
}

func TestListActivities_LimitDefaultWhenZero(t *testing.T) {
	fake := &fakeActivitiesClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, fake, "acme")
	})
	defer h.Close()

	_, _ = h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_activities",
		Arguments: map[string]any{},
	})
	if fake.lastListOpts.Limit != 25 {
		t.Errorf("limit=%d; want 25 (default)", fake.lastListOpts.Limit)
	}
}

func TestRegisterActivities_RegistersInDumpRegistry(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterActivities(s, &fakeActivitiesClient{}, "acme")
	})
	defer h.Close()

	var buf strings.Builder
	if err := tools.DumpJSON(&buf, "test"); err != nil {
		t.Fatalf("DumpJSON: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"get_activity"`, `"list_activities"`} {
		if !strings.Contains(out, want) {
			t.Errorf("dump missing %s", want)
		}
	}
}

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

type fakePersonsClient struct {
	person       *pipedrive.Person
	err          error
	persons      []pipedrive.Person
	personsNxt   string
	personsErr   error
	createPerson *pipedrive.Person
	createErr    error
	resolver     func(map[string]any) map[string]any

	lastListOpts   pipedrive.ListPersonsOptions
	lastCreateReq  pipedrive.CreatePersonRequest
	createCallSeen bool
}

func (f *fakePersonsClient) GetPerson(_ context.Context, _ int64) (*pipedrive.Person, error) {
	return f.person, f.err
}

func (f *fakePersonsClient) ListPersons(_ context.Context, opts pipedrive.ListPersonsOptions) ([]pipedrive.Person, string, error) {
	f.lastListOpts = opts
	return f.persons, f.personsNxt, f.personsErr
}

func (f *fakePersonsClient) CreatePerson(_ context.Context, req pipedrive.CreatePersonRequest) (*pipedrive.Person, error) {
	f.lastCreateReq = req
	f.createCallSeen = true
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.createPerson, nil
}

func (f *fakePersonsClient) ResolvePersonCustomFields(_ context.Context, raw map[string]any) map[string]any {
	if f.resolver != nil {
		return f.resolver(raw)
	}
	return raw
}

type personRow struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	FirstName  string `json:"first_name,omitempty"`
	LastName   string `json:"last_name,omitempty"`
	OrgID      int64  `json:"org_id"`
	OwnerID    int64  `json:"owner_id"`
	URL        string `json:"url"`
	AddTime    string `json:"add_time,omitempty"`
	UpdateTime string `json:"update_time,omitempty"`
	Emails     []struct {
		Value   string `json:"value"`
		Primary bool   `json:"primary"`
		Label   string `json:"label,omitempty"`
	} `json:"emails,omitempty"`
	Phones []struct {
		Value   string `json:"value"`
		Primary bool   `json:"primary"`
		Label   string `json:"label,omitempty"`
	} `json:"phones,omitempty"`
	CustomFields map[string]any `json:"custom_fields,omitempty"`
}

func TestGetPerson_HappyPath(t *testing.T) {
	fake := &fakePersonsClient{
		person: &pipedrive.Person{
			ID:        42,
			Name:      "Alice Example",
			FirstName: "Alice",
			LastName:  "Example",
			Emails: []pipedrive.ContactPoint{
				{Value: "alice@example.com", Primary: true, Label: "work"},
			},
			OrgID:        7,
			OwnerID:      13,
			CustomFields: map[string]any{"abc123": "Gold"},
		},
		resolver: func(raw map[string]any) map[string]any {
			return map[string]any{"VIP Tier": raw["abc123"]}
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterPersons(s, fake, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_person",
		Arguments: map[string]any{"person_id": 42},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out struct {
		Person personRow `json:"person"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)

	if out.Person.ID != 42 || out.Person.Name != "Alice Example" {
		t.Errorf("person = %+v; want id=42 name=\"Alice Example\"", out.Person)
	}
	if out.Person.URL != "https://acme.pipedrive.com/person/42" {
		t.Errorf("URL = %q, want acme/person/42", out.Person.URL)
	}
	if len(out.Person.Emails) != 1 || out.Person.Emails[0].Value != "alice@example.com" {
		t.Errorf("emails lost: %+v", out.Person.Emails)
	}
	if out.Person.CustomFields["VIP Tier"] != "Gold" {
		t.Errorf("custom field name resolution lost: %v", out.Person.CustomFields)
	}
}

func TestGetPerson_RejectsZeroID(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterPersons(s, &fakePersonsClient{}, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_person",
		Arguments: map[string]any{"person_id": 0},
	})
	if !res.IsError {
		t.Fatal("expected isError on zero person_id")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
}

func TestGetPerson_UpstreamNotFound(t *testing.T) {
	fake := &fakePersonsClient{
		err: &pipedrive.APIError{
			Class:    pipedrive.ErrNotFound,
			Status:   404,
			Message:  "Person not found",
			Endpoint: "/api/v2/persons/99999",
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterPersons(s, fake, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_person",
		Arguments: map[string]any{"person_id": 99999},
	})
	if !res.IsError {
		t.Fatal("expected isError on upstream 404")
	}
	if !strings.HasPrefix(contentText(res), "[not_found]") {
		t.Errorf("error text = %q; want [not_found] prefix", contentText(res))
	}
}

func TestListPersons_HappyPath(t *testing.T) {
	fake := &fakePersonsClient{
		persons: []pipedrive.Person{
			{ID: 1, Name: "Alice", OrgID: 7, OwnerID: 13, CustomFields: map[string]any{"abc": "Gold"}},
			{ID: 2, Name: "Bob", OrgID: 7, OwnerID: 13},
		},
		personsNxt: "cursor-page-2",
		resolver: func(raw map[string]any) map[string]any {
			if v, ok := raw["abc"]; ok {
				return map[string]any{"VIP Tier": v}
			}
			return raw
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterPersons(s, fake, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_persons",
		Arguments: map[string]any{"org_id": 7, "limit": 50},
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out struct {
		Persons    []personRow `json:"persons"`
		NextCursor string      `json:"next_cursor"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)

	if len(out.Persons) != 2 {
		t.Fatalf("got %d persons, want 2", len(out.Persons))
	}
	if out.Persons[0].URL != "https://acme.pipedrive.com/person/1" {
		t.Errorf("URL = %q, want acme/person/1", out.Persons[0].URL)
	}
	if out.NextCursor != "cursor-page-2" {
		t.Errorf("next_cursor = %q, want cursor-page-2", out.NextCursor)
	}
	if out.Persons[0].CustomFields["VIP Tier"] != "Gold" {
		t.Errorf("custom field name resolution lost: %v", out.Persons[0].CustomFields)
	}
	if fake.lastListOpts.OrgID != 7 || fake.lastListOpts.Limit != 50 {
		t.Errorf("client received opts %+v; want org_id=7 limit=50", fake.lastListOpts)
	}
	if fake.lastListOpts.SortBy != "update_time" || fake.lastListOpts.SortDirection != "desc" {
		t.Errorf("default sort = %q %q; want update_time desc", fake.lastListOpts.SortBy, fake.lastListOpts.SortDirection)
	}
}

func TestListPersons_RejectsBadSortBy(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterPersons(s, &fakePersonsClient{}, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_persons",
		Arguments: map[string]any{"sort_by": "name"},
	})
	if !res.IsError {
		t.Fatal("expected isError on unsupported sort_by")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
}

func TestListPersons_LimitClampedToMax(t *testing.T) {
	fake := &fakePersonsClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterPersons(s, fake, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	_, _ = h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_persons",
		Arguments: map[string]any{"limit": 999},
	})
	if fake.lastListOpts.Limit != 100 {
		t.Errorf("limit=%d; want 100 (clamped from 999)", fake.lastListOpts.Limit)
	}
}

func TestRegisterPersons_RegistersInDumpRegistry(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterPersons(s, &fakePersonsClient{}, "acme", tools.RegisterOptions{})
	})
	defer h.Close()
	var buf strings.Builder
	if err := tools.DumpJSON(&buf, "test"); err != nil {
		t.Fatalf("DumpJSON: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"get_person"`, `"list_persons"`, `"create_person"`} {
		if !strings.Contains(out, want) {
			t.Errorf("dump missing %s", want)
		}
	}
}

func TestCreatePerson_HappyPath(t *testing.T) {
	fake := &fakePersonsClient{
		createPerson: &pipedrive.Person{
			ID:        77,
			Name:      "Helle Steffenauer",
			FirstName: "Helle",
			LastName:  "Steffenauer",
			Emails:    []pipedrive.ContactPoint{{Value: "hs@ntmail.dk", Primary: true, Label: "work"}},
			Phones:    []pipedrive.ContactPoint{{Value: "+45 99 34 11 48", Primary: true, Label: "work"}},
			OrgID:     59,
			OwnerID:   13,
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterPersons(s, fake, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_person",
		Arguments: map[string]any{
			"name":       "Helle Steffenauer",
			"first_name": "Helle",
			"last_name":  "Steffenauer",
			"emails":     []map[string]any{{"value": "hs@ntmail.dk", "primary": true, "label": "work"}},
			"phones":     []map[string]any{{"value": "+45 99 34 11 48", "primary": true, "label": "work"}},
			"org_id":     59,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out struct {
		Person personRow `json:"person"`
		DryRun bool      `json:"dry_run"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)

	if !fake.createCallSeen {
		t.Fatal("CreatePerson was not called")
	}
	if out.DryRun {
		t.Error("dry_run = true on a non-dry-run handler")
	}
	if out.Person.ID != 77 || out.Person.Name != "Helle Steffenauer" {
		t.Errorf("person echo lost fields: %+v", out.Person)
	}
	if out.Person.URL != "https://acme.pipedrive.com/person/77" {
		t.Errorf("URL = %q, want acme/person/77", out.Person.URL)
	}
	if fake.lastCreateReq.Name != "Helle Steffenauer" ||
		fake.lastCreateReq.OrgID != 59 ||
		len(fake.lastCreateReq.Emails) != 1 ||
		fake.lastCreateReq.Emails[0].Value != "hs@ntmail.dk" {
		t.Errorf("upstream request lost fields: %+v", fake.lastCreateReq)
	}
}

func TestCreatePerson_RejectsEmptyName(t *testing.T) {
	fake := &fakePersonsClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterPersons(s, fake, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "create_person",
		Arguments: map[string]any{"name": ""},
	})
	if !res.IsError {
		t.Fatal("expected isError on empty name")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
	if fake.createCallSeen {
		t.Error("CreatePerson called despite client-side validation failure")
	}
}

func TestCreatePerson_DryRunSkipsUpstream(t *testing.T) {
	fake := &fakePersonsClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterPersons(s, fake, "acme", tools.RegisterOptions{DryRun: true}) // dryRun = true
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_person",
		Arguments: map[string]any{
			"name":   "Dry run probe",
			"org_id": 59,
		},
	})
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	if fake.createCallSeen {
		t.Error("CreatePerson called on dry-run path; upstream POST should be skipped")
	}
	var out struct {
		Person personRow `json:"person"`
		DryRun bool      `json:"dry_run"`
	}
	testutil.DecodeStructured(t, res.StructuredContent, &out)
	if !out.DryRun {
		t.Error("dry_run = false on synthetic preview")
	}
	if out.Person.ID != 0 {
		t.Errorf("synthetic id = %d; want 0", out.Person.ID)
	}
	if out.Person.Name != "Dry run probe" || out.Person.OrgID != 59 {
		t.Errorf("synthetic person lost input fields: %+v", out.Person)
	}
}

func TestCreatePerson_PropagatesUpstreamError(t *testing.T) {
	fake := &fakePersonsClient{
		createErr: &pipedrive.APIError{
			Class:    pipedrive.ErrValidation,
			Status:   400,
			Message:  "owner_id must be a valid user",
			Endpoint: "/api/v2/persons",
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterPersons(s, fake, "acme", tools.RegisterOptions{})
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_person",
		Arguments: map[string]any{
			"name":     "Bad owner",
			"owner_id": 999999,
		},
	})
	if !res.IsError {
		t.Fatal("expected isError on upstream 400")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
}

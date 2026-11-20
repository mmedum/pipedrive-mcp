package pipedrive

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The upstream types in this package are a hand-written mirror of what
// Pipedrive returns, and a hand-written mirror drifts. This holds it
// against Pipedrive's own published v2 OpenAPI description.
//
// It is a TEST, not a generator. Nothing here is compiled into the
// binary and no generated code is vendored: the spec has no $ref, so
// every response shape is inlined per operation and a generator emits a
// separate anonymous struct per endpoint rather than one Deal. The spec
// is worth having as an oracle and not as a code input.
//
// A field this package reads under a name v2 does not send decodes to
// its zero value on every record, silently; that is the drift this
// catches. A type the spec contradicts, such as an int where it says
// number, fails a whole page on one row.
//
// The fixture is a projection of
// https://developers.pipedrive.com/docs/api/v1/openapi-v2.yaml, refreshed
// before each release (docs/release.md).
const specFixture = "testdata/v2-response-fields.json"

type specFields struct {
	Source string `json:"_source"`
	Types  map[string]struct {
		Endpoint string            `json:"endpoint"`
		Fields   map[string]string `json:"fields"`
	} `json:"types"`
	// Requests is each request type's body, per endpoint it is sent to.
	Requests map[string][]struct {
		Method string            `json:"method"`
		Path   string            `json:"path"`
		Fields map[string]string `json:"fields"`
	} `json:"requests"`
	// Queries is each read endpoint's query parameters.
	Queries map[string][]string `json:"queries"`
}

// knownSpecOmissions are JSON tags this package decodes that the
// published spec does not declare, each one verified against the live
// API. They are exemptions from the check below, not oversights.
//
// is_writable is returned live by /dealFields, /personFields and
// /organizationFields, and a write to a read-only custom field is
// refused on the strength of it. The spec declares is_custom_field and
// never is_writable.
var knownSpecOmissions = map[string]map[string]string{
	"Field": {
		"is_writable": "returned live by the *Fields endpoints; the spec has never declared it",
	},
	"Organization": {
		// Not in the default response shape by design: it is an
		// include_fields value, and every organization read here asks
		// for it (orgIncludeFields). The exemption records that the
		// absence from the response schema is expected, not drift.
		"people_count": "an include_fields value, requested by every organization read in this package",
	},
}

func loadSpecFields(t *testing.T) specFields {
	t.Helper()
	data, err := os.ReadFile(specFixture)
	if err != nil {
		t.Fatalf("reading %s: %v", specFixture, err)
	}
	var s specFields
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("decoding %s: %v", specFixture, err)
	}
	return s
}

// acceptable reports whether a Go field can hold what the spec declares.
//
// The direction that matters is `number` into an integer: Go's decoder
// refuses it outright and fails the whole enclosing document, which is
// how one fractional probability took out a page of deals. The rest are
// checked because they are free once the walk exists.
func acceptable(declared string, got reflect.Type) bool {
	for got.Kind() == reflect.Pointer {
		got = got.Elem()
	}
	switch declared {
	case "string":
		return got.Kind() == reflect.String
	case "number":
		return got.Kind() == reflect.Float64 || got.Kind() == reflect.Float32
	case "integer":
		switch got.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return true
		}
		return false
	case "boolean":
		return got.Kind() == reflect.Bool
	case "array":
		return got.Kind() == reflect.Slice || got.Kind() == reflect.Array
	case "object":
		switch got.Kind() {
		case reflect.Map, reflect.Struct, reflect.Interface:
			return true
		}
		return false
	default:
		// A shape the fixture could not reduce to one type — anything
		// decodes it, so there is nothing to assert.
		return true
	}
}

func TestUpstreamTypesMatchTheSpec(t *testing.T) {
	spec := loadSpecFields(t)

	cases := []struct {
		name string
		typ  reflect.Type
	}{
		{"Deal", reflect.TypeOf(Deal{})},
		{"Person", reflect.TypeOf(Person{})},
		{"Organization", reflect.TypeOf(Organization{})},
		{"Activity", reflect.TypeOf(Activity{})},
		{"Pipeline", reflect.TypeOf(Pipeline{})},
		{"Stage", reflect.TypeOf(Stage{})},
		{"Field", reflect.TypeOf(Field{})},
		{"Installment", reflect.TypeOf(Installment{})},
		{"DealProduct", reflect.TypeOf(DealProduct{})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			declared, ok := spec.Types[tc.name]
			if !ok {
				t.Fatalf("%s has no entry in %s", tc.name, specFixture)
			}
			exempt := knownSpecOmissions[tc.name]

			for i := range tc.typ.NumField() {
				f := tc.typ.Field(i)
				tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
				if tag == "" || tag == "-" {
					continue
				}
				if _, skip := exempt[tag]; skip {
					continue
				}
				kind, found := declared.Fields[tag]
				if !found {
					t.Errorf("%s.%s decodes %q, which %s does not return — it will hold its zero value for every record",
						tc.name, f.Name, tag, declared.Endpoint)
					continue
				}
				if !acceptable(kind, f.Type) {
					t.Errorf("%s.%s is %s and %s declares %q for %q — a value the Go type cannot hold fails the whole response, not just this field",
						tc.name, f.Name, f.Type, declared.Endpoint, kind, tag)
				}
			}
		})
	}
}

// Not a failure: the mirror is deliberately a subset. It is logged so a
// reader can see how much of the resource this server does not look at,
// which is where a capability gap hides — is_archived and label_ids were
// both invisible this way.
func TestUpstreamTypeCoverage(t *testing.T) {
	spec := loadSpecFields(t)
	cases := map[string]reflect.Type{
		"Deal": reflect.TypeOf(Deal{}), "Person": reflect.TypeOf(Person{}),
		"Organization": reflect.TypeOf(Organization{}), "Activity": reflect.TypeOf(Activity{}),
		"Pipeline": reflect.TypeOf(Pipeline{}), "Stage": reflect.TypeOf(Stage{}),
	}
	for name, typ := range cases {
		declared := spec.Types[name]
		have := map[string]bool{}
		for i := range typ.NumField() {
			tag, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
			have[tag] = true
		}
		var missing []string
		for field := range declared.Fields {
			if !have[field] {
				missing = append(missing, field)
			}
		}
		t.Logf("%s: decodes %d of %d declared fields; not decoded: %s",
			name, len(declared.Fields)-len(missing), len(declared.Fields), strings.Join(missing, " "))
	}
}

// knownRequestOmissions are request fields this package sends that the
// v2 description does not declare, each one verified against the live
// API.
var knownRequestOmissions = map[string]map[string]string{
	"CreatePersonRequest": {
		"first_name": "accepted live; every write probe's scratch person is created with it",
		"last_name":  "accepted live; every write probe's scratch person is created with it",
	},
	"UpdatePersonRequest": {
		"first_name": "accepted live; checked by the integration test TestWrite_PersonFieldRoundTrips",
		"last_name":  "accepted live, as first_name is",
	},
}

// marshaledKind is what a type with its own MarshalJSON writes, as the
// description names it, or "" for a type without one.
func marshaledKind(typ reflect.Type) string {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	m, ok := reflect.New(typ).Elem().Interface().(json.Marshaler)
	if !ok {
		return ""
	}
	out, err := m.MarshalJSON()
	if err != nil || len(out) == 0 {
		return ""
	}
	switch out[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	}
	return ""
}

// TestRequestTypesMatchTheSpec holds the request bodies this package
// sends to Pipedrive's v2 description, their top-level fields only: the
// fixture records no nested schemas, so an element of participants or
// emails is not compared, as TestUpstreamTypesMatchTheSpec
// holds the responses: a field the description does not declare is one
// Pipedrive ignores or refuses, and a type it cannot hold is refused.
func TestRequestTypesMatchTheSpec(t *testing.T) {
	spec := loadSpecFields(t)
	cases := map[string]reflect.Type{
		"CreateDealRequest":         reflect.TypeOf(CreateDealRequest{}),
		"UpdateDealRequest":         reflect.TypeOf(UpdateDealRequest{}),
		"CreatePersonRequest":       reflect.TypeOf(CreatePersonRequest{}),
		"UpdatePersonRequest":       reflect.TypeOf(UpdatePersonRequest{}),
		"CreateOrganizationRequest": reflect.TypeOf(CreateOrganizationRequest{}),
		"UpdateOrganizationRequest": reflect.TypeOf(UpdateOrganizationRequest{}),
		"CreateActivityRequest":     reflect.TypeOf(CreateActivityRequest{}),
		"UpdateActivityRequest":     reflect.TypeOf(UpdateActivityRequest{}),
		"InstallmentRequest":        reflect.TypeOf(InstallmentRequest{}),
	}
	if len(spec.Requests) != len(cases) {
		t.Errorf("%s records %d request types and this test holds %d; add the new one to both", specFixture, len(spec.Requests), len(cases))
	}
	for name, typ := range cases {
		endpoints, ok := spec.Requests[name]
		if !ok || len(endpoints) == 0 {
			t.Errorf("%s has no request entry for %s", specFixture, name)
			continue
		}
		for _, ep := range endpoints {
			for i := range typ.NumField() {
				f := typ.Field(i)
				tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
				if tag == "" || tag == "-" {
					continue
				}
				if _, skip := knownRequestOmissions[name][tag]; skip {
					continue
				}
				kind, found := ep.Fields[tag]
				if !found {
					t.Errorf("%s.%s sends %q, which %s %s does not take", name, f.Name, tag, ep.Method, ep.Path)
					continue
				}
				if wrote := marshaledKind(f.Type); wrote != "" {
					if wrote != kind {
						t.Errorf("%s.%s writes a JSON %s and %s %s declares %q for %q", name, f.Name, wrote, ep.Method, ep.Path, kind, tag)
					}
					continue
				}
				if !acceptable(kind, f.Type) {
					t.Errorf("%s.%s is %s and %s %s declares %q for %q", name, f.Name, f.Type, ep.Method, ep.Path, kind, tag)
				}
			}
		}
	}
}

// TestQueriesMatchTheSpec runs every read this package makes with every
// option set, against a server that records the query, and holds each
// parameter to the ones the description declares for that path. A
// parameter Pipedrive does not take is ignored, which reads as a filter
// that silently does nothing, or refused with a 400.
func TestQueriesMatchTheSpec(t *testing.T) {
	spec := loadSpecFields(t)
	seen := map[string]map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := templatePath(strings.TrimPrefix(r.URL.Path, "/api/v2"))
		if seen[path] == nil {
			seen[path] = map[string]bool{}
		}
		for k := range r.URL.Query() {
			seen[path][k] = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[],"additional_data":{}}`)
	}))
	defer srv.Close()
	c := newTestClient(srv)
	ctx := context.Background()
	done := true

	_, _, _ = c.ListDeals(ctx, ListDealsOptions{Status: "open", PipelineID: 1, StageID: 2, OwnerID: 3, PersonID: 4, OrgID: 5,
		UpdatedSince: "2026-01-01T00:00:00Z", UpdatedUntil: "2026-02-01T00:00:00Z", SortBy: "id", SortDirection: "asc", Limit: 5, Cursor: "c", IDs: []int64{1}})
	_, _, _ = c.ListDeals(ctx, ListDealsOptions{Archived: true, Status: "open", PipelineID: 1, StageID: 2, OwnerID: 3, PersonID: 4, OrgID: 5,
		UpdatedSince: "2026-01-01T00:00:00Z", UpdatedUntil: "2026-02-01T00:00:00Z", SortBy: "id", SortDirection: "asc", Limit: 5, Cursor: "c"})
	_, _, _ = c.ListPersons(ctx, ListPersonsOptions{OwnerID: 1, OrgID: 2, UpdatedSince: "2026-01-01T00:00:00Z", UpdatedUntil: "2026-02-01T00:00:00Z",
		SortBy: "id", SortDirection: "asc", Limit: 5, Cursor: "c"})
	_, _, _ = c.ListOrganizations(ctx, ListOrganizationsOptions{OwnerID: 1, UpdatedSince: "2026-01-01T00:00:00Z", UpdatedUntil: "2026-02-01T00:00:00Z",
		SortBy: "id", SortDirection: "asc", Limit: 5, Cursor: "c"})
	_, _, _ = c.ListActivities(ctx, ListActivitiesOptions{OwnerID: 1, DealID: 2, LeadID: "l", PersonID: 3, OrgID: 4, Done: &done,
		UpdatedSince: "2026-01-01T00:00:00Z", UpdatedUntil: "2026-02-01T00:00:00Z", SortBy: "id", SortDirection: "asc", IncludeAttendees: true, Limit: 5, Cursor: "c"})
	_, _, _ = c.ItemSearch(ctx, SearchOptions{Term: "acme", ItemTypes: []string{"deal"}, ExactMatch: true, Limit: 5, Cursor: "c"})
	_, _, _ = c.ListInstallments(ctx, []int64{1, 2}, 5, "c")
	_, _ = c.ListDealProducts(ctx, 7)
	_, _ = c.ListPipelines(ctx)
	_, _ = c.ListStages(ctx, 3)
	_, _ = c.ListDealFields(ctx)
	_, _ = c.ListPersonFields(ctx)
	_, _ = c.ListOrganizationFields(ctx)
	_, _ = c.GetOrganization(ctx, 7)
	_, _ = c.GetPerson(ctx, 7)
	_, _ = c.GetActivity(ctx, 7, GetActivityOptions{IncludeAttendees: true})

	// Every path the fixture records has to be reached, so a read that
	// stops sending, or one the list above leaves out, fails here.
	for path := range spec.Queries {
		if _, ok := seen[path]; !ok {
			t.Errorf("no read above reached %s, which %s records; call it, or drop it from the fixture", path, specFixture)
		}
	}
	for path, params := range seen {
		declared, ok := spec.Queries[path]
		if !ok {
			if len(params) > 0 {
				t.Errorf("%s records no query parameters for %s, which this package sends %v", specFixture, path, slices.Sorted(maps.Keys(params)))
			}
			continue
		}
		for p := range params {
			if !slices.Contains(declared, p) {
				t.Errorf("a read sends %s?%s=, which Pipedrive's v2 description does not declare for that path", path, p)
			}
		}
	}
}

// templatePath turns a request path's numeric segments into {id}, the
// way the description writes them.
func templatePath(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		if part != "" && strings.Trim(part, "0123456789") == "" {
			parts[i] = "{id}"
		}
	}
	return strings.Join(parts, "/")
}

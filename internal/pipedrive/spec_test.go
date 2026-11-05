package pipedrive

import (
	"encoding/json"
	"os"
	"reflect"
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

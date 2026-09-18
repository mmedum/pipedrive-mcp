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
// binary and no generated code is vendored: the spec has zero $ref, so
// every response shape is inlined per operation and a generator emits a
// separate anonymous struct per endpoint rather than one Deal. The spec
// is worth having as an oracle and not as a code input. The reasoning is
// written out in docs/architecture.md, "Hand-rolled HTTP".
//
// What it caught the day it was written, all three confirmed against a
// live workspace before being fixed:
//
//   - Pipeline.Active read `active` and Stage.Active read `active_flag`.
//     Neither field exists in v2 — both resources carry is_deleted — so
//     both decoded to false for every record, and the tool told the LLM
//     every pipeline in the workspace was inactive. The comment above
//     them claimed they had been confirmed against the live endpoints.
//   - Organization.PeopleCount read `people_count`, which v2 returns only
//     when asked for through include_fields. It was never requested, so
//     the field was absent from every row while three tool descriptions
//     promised it.
//   - Deal.Probability was *int where the spec says `number`. One deal
//     with a fractional probability makes the decode fail, and client.go
//     returns that error, so a single row loses the whole page.
//
// Refreshing the fixture is a step in docs/release.md.
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
// is_writable is returned by /dealFields, /personFields and
// /organizationFields — confirmed live on 2026-09-18 — and the
// guarded-write contract refuses a write to a read-only custom field on
// the strength of it. The spec mentions is_custom_field 92 times and
// is_writable not once. The field is real; the description is behind.
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

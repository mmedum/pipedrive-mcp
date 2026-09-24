package tools

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

// Every sentinel the package knows about must map to a class the LLM
// can branch on. errorClass ends in a default that returns "error", so
// a sentinel added without a case here does not fail anything — it just
// arrives at the model as the same unhelpful label every other unmapped
// failure carries, and nothing says so.
//
// allSentinels is the list llmMessage already walks to strip class
// prefixes, so this asserts the two lists agree rather than keeping a
// third copy of the names.
func TestEverySentinelHasItsOwnClass(t *testing.T) {
	seen := map[string]error{}
	for _, s := range allSentinels {
		class := errorClass(s)
		if class == "error" {
			t.Errorf("%v maps to the default class %q; a caller cannot tell it from any other failure", s, class)
			continue
		}
		if prev, dup := seen[class]; dup {
			t.Errorf("%v and %v both map to %q; the class stops discriminating", prev, s, class)
		}
		seen[class] = s
	}
}

// *APIError is the shape a class actually arrives in from the client,
// and errorClass has to see the sentinel through APIError.Unwrap.
//
// Every other class is covered end-to-end by a handler test asserting
// its [class] prefix — [rate_limited] in notes_test.go and
// manage_write_test.go, [validation] in a dozen more. ErrGone has no
// such test, because no handler test provokes a 410 from the fake, so
// this is its only coverage rather than a duplicate of it.
func TestErrorClassReadsAnAPIError(t *testing.T) {
	api := &pipedrive.APIError{Class: pipedrive.ErrGone, Status: 410, Endpoint: "/api/v1/notes/7"}
	if got := errorClass(api); got != "gone" {
		t.Errorf("errorClass(*APIError{ErrGone}) = %q, want %q", got, "gone")
	}
}

// The operator docs enumerate the [class] tags, and that list drifted:
// it was missing [refused] since guarded writes shipped and would have
// been missing [gone] the moment this package gained it. An operator
// reading a class the runbook does not list cannot tell a new class
// from a typo.
//
// Held against allSentinels for the same reason the class mapping is:
// it is the list the package already keeps, so this adds an assertion
// rather than a third copy of the names.
func TestOperationsDocListsEveryErrorClass(t *testing.T) {
	const doc = "docs/operations.md"

	body, err := os.ReadFile(filepath.Join("..", "..", doc))
	if err != nil {
		t.Fatalf("reading %s: %v", doc, err)
	}
	text := string(body)

	for _, s := range allSentinels {
		tag := fmt.Sprintf("`[%s]`", errorClass(s))
		if !strings.Contains(text, tag) {
			t.Errorf("%s does not document %s, which %v produces; "+
				"an operator who sees it has nothing to look it up in", doc, tag, s)
		}
	}
}

// Every action a manage_ tool dispatches on has to appear in the
// description of its own `action` field, because that description is
// the allowed-value list the model reads.
//
// manage_deal's said "create, update, move_stage, mark_won, mark_lost
// or reopen" while dealActions held eight — archive and unarchive were
// dispatchable and undocumented, in the same schema whose tool
// description named "the six transitions" including both. A model
// reading the field never tried them.
//
// Reflection on the struct tag rather than the built registry: the tag
// IS what becomes the description, and reading it needs no client, no
// server and no registration.
func TestActionFieldDescribesEveryAction(t *testing.T) {
	for _, tc := range []struct {
		tool    string
		input   any
		actions []string
	}{
		{"manage_deal", manageDealInput{}, keysOf(dealActions)},
		{"manage_activity", manageActivityInput{}, keysOf(activityActions)},
		{"manage_note", manageNoteInput{}, keysOf(allowedNoteActions)},
		{"manage_organization", manageOrganizationInput{}, keysOf(allowedOrganizationActions)},
		{"manage_person", managePersonInput{}, keysOf(allowedPersonActions)},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			field, ok := reflect.TypeOf(tc.input).FieldByName("Action")
			if !ok {
				t.Fatalf("%s has no Action field; this test is asserting on nothing", tc.tool)
			}
			desc := field.Tag.Get("jsonschema")
			if desc == "" {
				t.Fatalf("%s's Action field carries no jsonschema description", tc.tool)
			}
			for _, action := range tc.actions {
				if !strings.Contains(desc, action) {
					t.Errorf("%s dispatches on %q but its action description does not offer it: %q",
						tc.tool, action, desc)
				}
			}
		})
	}
}

// keysOf returns a map's keys, sorted so a failure message is stable.
func keysOf[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

// SDKVersion is written into the schema dump header, where its whole
// job is to let a reviewer classify a diff as "the SDK moved" rather
// than "the surface changed". A stale value says the opposite of what
// it is for, and it has been stale twice.
func TestSDKVersionMatchesGoMod(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	const mod = "github.com/modelcontextprotocol/go-sdk "
	var pinned string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, mod); ok {
			pinned = strings.Fields(after)[0]
			break
		}
	}
	if pinned == "" {
		t.Fatalf("go.mod has no %s requirement; this test is asserting on nothing", mod)
	}
	if SDKVersion != pinned {
		t.Errorf("tools.SDKVersion is %q and go.mod pins %q; the schema dump header would "+
			"attribute a diff to the wrong SDK version", SDKVersion, pinned)
	}
}

// A hint exists because the upstream message alone would mislead, so
// it has to reach the LLM — and the class has to stay the upstream
// one, or the caller cannot tell a rate limit from a refusal.
func TestErrorText_HintReachesTheCallerAndKeepsTheClass(t *testing.T) {
	upstream := &pipedrive.APIError{
		Class:    pipedrive.ErrRateLimited,
		Status:   429,
		Message:  "rate limit exceeded",
		Endpoint: "/organizations",
	}
	err := withHint(upstream, "the search matched, but checking its %ss failed; narrow types to skip that check", "organization")
	got := errorText(err)

	if !strings.Contains(got, "the search matched") {
		t.Errorf("error text = %q; the hint did not reach the caller", got)
	}
	if !strings.Contains(got, "rate limit exceeded") {
		t.Errorf("error text = %q; the upstream message was dropped", got)
	}
	if !strings.HasPrefix(got, "[rate_limited]") {
		t.Errorf("error text = %q; want the upstream class preserved", got)
	}
}

// Without a hint nothing changes: the upstream message is the whole
// story and is reported alone. This is the assertion that keeps the
// hint from quietly rewriting every other error in the surface.
func TestErrorText_UnhintedUpstreamIsUnchanged(t *testing.T) {
	upstream := &pipedrive.APIError{
		Class:    pipedrive.ErrNotFound,
		Status:   404,
		Message:  "Deal not found",
		Endpoint: "/deals/1",
	}
	if got := errorText(upstream); got != "[not_found] Deal not found" {
		t.Errorf("error text = %q; want %q", got, "[not_found] Deal not found")
	}
}

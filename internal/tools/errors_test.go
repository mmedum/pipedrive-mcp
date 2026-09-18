package tools

import (
	"fmt"
	"os"
	"path/filepath"
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

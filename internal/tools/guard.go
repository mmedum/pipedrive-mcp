package tools

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

// This file holds the guarded-write machinery every manage_* tool
// shares. Per CLAUDE.md "Guarded writes": read the target first, then
// refuse anything the write would destroy that the caller cannot see.
//
// The one thing a resource supplies is a []fieldSpec — the LLM-facing
// names of the fields an update can touch, each with a way to project
// its stored value to a string. That single table then drives both the
// `changed` report and the `overwrite` guard, so the two can never
// disagree about what a write would do.
//
// Strings are the projection because they make int64, *int64, float64,
// *int and string fields comparable and emptiness-testable through one
// code path. Reflection over struct tags would be the other way to do
// this, and would be worse: these names are the LLM-facing contract,
// deliberately a parallel shadow of the upstream struct rather than a
// mirror of it.

// fieldSpec describes one updatable field of a record.
type fieldSpec[T any] struct {
	Name string
	Get  func(*T) string
}

// changedFields names the fields that differ between two records, in
// table order so the report is stable.
func changedFields[T any](spec []fieldSpec[T], before, after *T) []string {
	var changed []string
	for _, f := range spec {
		if f.Get(before) != f.Get(after) {
			changed = append(changed, f.Name)
		}
	}
	return changed
}

// populatedFields narrows names to the fields already holding a value
// on r. These are what a write would clobber rather than fill, which is
// exactly the set the overwrite guard refuses over — filling an empty
// field destroys nothing and needs no permission.
func populatedFields[T any](spec []fieldSpec[T], r *T, names []string) []string {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []string
	for _, f := range spec {
		if want[f.Name] && f.Get(r) != "" {
			out = append(out, f.Name)
		}
	}
	return out
}

// requireOverwrite refuses a write that would replace fields already
// holding a value, unless overwrite says to go ahead. The refusal names
// every such field, because a caller told only that "something" would
// be clobbered cannot decide whether they meant it.
func requireOverwrite[T any](spec []fieldSpec[T], resource string, before *T, changed []string, overwrite bool) error {
	if overwrite {
		return nil
	}
	clobbered := populatedFields(spec, before, changed)
	if len(clobbered) == 0 {
		return nil
	}
	return refuse(
		fmt.Sprintf("%s already has %s set, and this write would replace what is there",
			resource, strings.Join(clobbered, ", ")),
		"overwrite: true",
	)
}

// The projections below are the value-to-string helpers a fieldSpec
// table is built from. Each renders "not set" as the empty string, so
// the overwrite guard treats absent and zero identically — Pipedrive
// uses null and 0 interchangeably across v1 and v2 responses, and a
// guard that told them apart would refuse writes over fields that hold
// nothing.

func projectString(s string) string { return s }

func projectID(v int64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatInt(v, 10)
}

func projectOptID(p *int64) string {
	if p == nil {
		return ""
	}
	return projectID(*p)
}

func projectMoney(v float64) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func projectOptInt(p *int) string {
	if p == nil || *p == 0 {
		return ""
	}
	return strconv.Itoa(*p)
}

// projectContactPoints renders a contact-point collection by its
// primary value, falling back to the first entry. Pipedrive replaces
// such a collection wholesale, so the question the guard needs answered
// is "does this person already have one", not which of several.
func projectContactPoints(cps []pipedrive.ContactPoint) string {
	if len(cps) == 0 {
		return ""
	}
	for _, cp := range cps {
		if cp.Primary {
			return cp.Value
		}
	}
	return cps[0].Value
}

// projectAddress and projectLocation render the structured records
// Pipedrive parses out of a single line back to that line, which is the
// same shape a write sends.
func projectAddress(a *pipedrive.Address) string {
	if a == nil {
		return ""
	}
	return a.Value
}

func projectLocation(l *pipedrive.ActivityLocation) string {
	if l == nil {
		return ""
	}
	return l.Value
}

func projectBool(b bool) string {
	if !b {
		return ""
	}
	return "true"
}

// The helpers below unwrap the pointer inputs a manage_* tool takes.
// Creates need plain values, because a create has nothing to clear —
// only an update needs to tell "leave it alone" from "set it to zero".

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefID(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefBool(p *bool) bool {
	return p != nil && *p
}

// setIf assigns *src to *dst when src is non-nil. A manage_* overlay is
// one decision — "if the request set this field, take it" — repeated
// per field, and writing it as a branch each time makes a function
// whose measured complexity is that repetition rather than any real
// intricacy.
func setIf[T any](dst, src *T) {
	if src != nil {
		*dst = *src
	}
}

// strPtr is for the literal values a transition writes — status "won",
// and the empty string that clears a lost reason.
func strPtr(s string) *string { return &s }

// boolPtr is for the literal values an activity transition writes.
func boolPtr(b bool) *bool { return &b }

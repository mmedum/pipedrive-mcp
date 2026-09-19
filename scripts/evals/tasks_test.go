package main

import (
	"strings"
	"testing"
)

// The fixture a table test substitutes against. Ids are non-zero so a
// substitution that silently produced "0" is visible.
var testFixture = Fixture{
	OrgID: 11, OrgName: "Acme Industries",
	PersonID: 22, PersonName: "Dana Example",
	DealID: 33, DealTitle: "Acme renewal 2026",
	ActivityID: 44, NoteID: 55,
	StageID: 66, NextStageID: 77,
}

// A sibling project's first full eval run passed two tasks while
// sending the agent a literal "{folder}". This is the guard, and it
// costs nothing: no credentials, no workspace, no model.
func TestEveryPromptSubstitutesFully(t *testing.T) {
	for _, task := range Tasks {
		t.Run(task.Name, func(t *testing.T) {
			got, err := Substitute(task.Prompt, testFixture)
			if err != nil {
				t.Fatalf("%v — the table names a placeholder Substitute does not fill", err)
			}
			if strings.ContainsAny(got, "{}") {
				t.Errorf("prompt still carries braces after substitution: %q", got)
			}
		})
	}
}

// A task without a why is a task nobody can score six months later,
// and one without a name cannot key a result.
func TestEveryTaskIsAnswerable(t *testing.T) {
	seen := map[string]bool{}
	for i, task := range Tasks {
		if task.Name == "" {
			t.Errorf("task %d has no name", i)
			continue
		}
		if seen[task.Name] {
			t.Errorf("two tasks are named %q; the name keys the results", task.Name)
		}
		seen[task.Name] = true

		if strings.TrimSpace(task.Why) == "" {
			t.Errorf("%s has no why", task.Name)
		}
		if strings.TrimSpace(task.Prompt) == "" {
			t.Errorf("%s has no prompt", task.Name)
		}
		// The sibling's rule: every task answers "what does this check
		// when the world says no?" A task that checks neither half is a
		// task that cannot fail.
		if task.Check == nil && len(task.MustCall) == 0 && task.Unverified == "" {
			t.Errorf("%s checks no end state, asserts no trace and does not say which half went unchecked; it cannot fail", task.Name)
		}
	}
}

// Substitute is the one thing standing between the table and a literal
// "{deal_id}" reaching the model, so it is tested directly rather than
// only through the table above.
func TestSubstituteRefusesAnUnknownPlaceholder(t *testing.T) {
	if _, err := Substitute("delete everything in {workspace}", testFixture); err == nil {
		t.Fatal("an unknown placeholder was substituted away silently")
	}
	got, err := Substitute("deal {deal_id} for {org}", testFixture)
	if err != nil {
		t.Fatalf("a known placeholder was refused: %v", err)
	}
	if got != "deal 33 for Acme Industries" {
		t.Errorf("got %q", got)
	}
}

// The end-state checks run against whatever the tools return, and a
// tool that errors must not read as a pass. Driven with a stub, so this
// needs no workspace.
func TestChecksFailWhenTheReadFails(t *testing.T) {
	h := &Harness{Call: func(string, map[string]any) (string, map[string]any, error) {
		return "", nil, errTestRead
	}}
	for _, task := range Tasks {
		if task.Check == nil {
			continue
		}
		t.Run(task.Name, func(t *testing.T) {
			if ok, _ := task.Check(h, testFixture); ok {
				t.Error("the check passed while every read through the harness failed")
			}
		})
	}
}

var errTestRead = &readError{}

type readError struct{}

func (*readError) Error() string { return "the workspace could not be read" }

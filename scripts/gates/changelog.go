package main

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// watchedPaths are the trees whose changes a reader of the changelog
// would expect to see named there. Everything else — docs, workflows,
// this directory — may change without an entry.
var watchedPaths = []string{"internal/tools/", "internal/pipedrive/", "cmd/"}

// changelogGate fails when a pull request touches watched source without
// adding a line under [Unreleased].
//
// The [Unreleased] part matters more than it looks: an entry added under
// an already-released heading is invisible to anyone reading the release
// it belongs to, and now that the release page IS the changelog section,
// it is invisible on the page too.
func changelogGate(w io.Writer, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: gates changelog BASE HEAD")
	}
	base, head := args[0], args[1]

	changed, err := git("diff", "--name-only", base, head)
	if err != nil {
		return err
	}
	if !touchesWatched(changed) {
		_, _ = fmt.Fprintln(w, "no watched source paths changed; skipping the CHANGELOG check")
		return nil
	}

	// A generous context window, so the [Unreleased] heading is in the
	// diff however far the entries sit below it. Without it the hunk for
	// a new entry arrives with no heading above it and every added line
	// looks like it belongs to whatever section came last.
	diff, err := git("diff", "--unified=99999", base, head, "--", "CHANGELOG.md")
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff) == "" {
		return fmt.Errorf("source under %s changed but CHANGELOG.md did not",
			strings.Join(watchedPaths, ", "))
	}
	if n := addedUnderUnreleased(diff); n == 0 {
		return fmt.Errorf("CHANGELOG.md changed but no new lines under [Unreleased]")
	} else {
		_, _ = fmt.Fprintf(w, "CHANGELOG entry under [Unreleased] confirmed (%d added lines)\n", n)
	}
	return nil
}

func touchesWatched(nameOnlyDiff string) bool {
	for _, f := range strings.Split(nameOnlyDiff, "\n") {
		for _, p := range watchedPaths {
			if strings.HasPrefix(f, p) {
				return true
			}
		}
	}
	return false
}

// addedUnderUnreleased counts added lines sitting under the [Unreleased]
// heading, reading the diff as a state machine the way the shell version
// did: a heading line, added or unchanged, switches the section.
func addedUnderUnreleased(diff string) int {
	count, inHunk, inUnreleased := 0, false, false
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case inHunk && (strings.HasPrefix(line, "+## [") || strings.HasPrefix(line, " ## [")):
			inUnreleased = strings.Contains(line, "[Unreleased]")
		case inHunk && inUnreleased && strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			count++
		}
	}
	return count
}

func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

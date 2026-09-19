package main

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
)

// checklistGate holds the `make check` list documented in CLAUDE.md
// against the Makefile's own `check:` prerequisites.
//
// This list has drifted three times. The first time it named three
// `scripts/*.sh` files that no longer existed, and CLAUDE.md gained a
// line warning that it had drifted — a warning, not a check. The second
// time it was two targets short and was fixed by hand. The third time
// was the commit that added the `descriptions` gate to the Makefile and
// not to the document, one day later: the prose says "run make check,
// not the individual commands", and a reader who runs the individual
// commands from that list now misses one.
//
// A comment cannot notice. This can: adding a thirteenth gate fails the
// build until the document names it.
func checklistGate(w io.Writer, args []string) error {
	makefile, claude := "Makefile", "CLAUDE.md"
	if len(args) == 2 {
		makefile, claude = args[0], args[1]
	} else if len(args) != 0 {
		return fmt.Errorf("usage: gates checklist [MAKEFILE CLAUDE_MD]")
	}

	want, err := checkPrerequisites(makefile)
	if err != nil {
		return err
	}
	got, err := documentedCheckList(claude)
	if err != nil {
		return err
	}

	var missing, extra []string
	for _, t := range want {
		if !slices.Contains(got, t) {
			missing = append(missing, t)
		}
	}
	for _, t := range got {
		if !slices.Contains(want, t) {
			extra = append(extra, t)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "%s's `make check` list does not match %s's check: target", claude, makefile)
		if len(missing) > 0 {
			fmt.Fprintf(&b, "\n  the Makefile runs these and the document does not name them: %s", strings.Join(missing, " "))
		}
		if len(extra) > 0 {
			fmt.Fprintf(&b, "\n  the document names these and the Makefile does not run them: %s", strings.Join(extra, " "))
		}
		return fmt.Errorf("%s", b.String())
	}

	_, _ = fmt.Fprintf(w, "checklist ok: %s documents all %d gates in %s's check: target\n", claude, len(want), makefile)
	return nil
}

// checkPrerequisites reads the targets `check:` depends on.
var checkLine = regexp.MustCompile(`(?m)^check:([^\n]*)$`)

func checkPrerequisites(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := checkLine.FindSubmatch(raw)
	if m == nil {
		return nil, fmt.Errorf("%s has no `check:` target; this gate would pass while checking nothing", path)
	}
	// Drop the trailing `## help text`, which is documentation for
	// `make help` rather than a prerequisite.
	line, _, _ := strings.Cut(string(m[1]), "##")
	return strings.Fields(line), nil
}

// documentedCheckList reads the tokens from the fenced block in
// CLAUDE.md, which is written as a `make check` invocation with the
// gate names in trailing comments across however many lines it takes.
var docLine = regexp.MustCompile("(?s)```\\s*\nmake check(.*?)```")

func documentedCheckList(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := docLine.FindSubmatch(raw)
	if m == nil {
		return nil, fmt.Errorf("%s has no fenced `make check` block; this gate would pass while checking nothing", path)
	}
	var out []string
	for _, line := range strings.Split(string(m[1]), "\n") {
		_, after, found := strings.Cut(line, "#")
		if !found {
			continue
		}
		out = append(out, strings.Fields(after)...)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s's `make check` block names no gates", path)
	}
	return out, nil
}

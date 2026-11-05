package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// releaseNotes prints one version's section of the changelog, which is
// what the release workflow passes to goreleaser as --release-notes.
//
// Never reach for changelog.disable in .goreleaser.yaml to stop the
// generated commit list. It is read in the changelog pipe's Skip, which
// runs before Run, so ctx.ReleaseNotes is never assigned and the file
// named by --release-notes is never opened: the body collapses to the
// footer alone. This repository shipped exactly that, and every release
// page it published was the verification footer with nothing above it.
// Deleting the block is the way.
func releaseNotes(w io.Writer, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("usage: gates release-notes VERSION [CHANGELOG]")
	}
	version := strings.TrimPrefix(args[0], "v")
	file := changelogFile
	if len(args) == 2 && args[1] != "" {
		file = args[1]
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	notes := sectionFor(string(raw), version)
	if notes == "" {
		return fmt.Errorf("no CHANGELOG section for %s in %s", version, file)
	}
	_, err = fmt.Fprintln(w, promoteHeadings(notes))
	return err
}

// sectionFor returns the body under `## [version]`, without the blank
// lines that top and tail it.
func sectionFor(changelog, version string) string {
	lines := strings.Split(changelog, "\n")
	from, to := changelogSection(lines, version)
	body := lines[from:to]
	for len(body) > 0 && strings.TrimSpace(body[0]) == "" {
		body = body[1:]
	}
	return joinTrimmed(body)
}

// changelogSection is the range of lines under `## [version]`, empty
// when there is no such heading. It stops at the next heading and at a
// link definition, which is what a compare-link footer is made of: the
// footer follows the oldest section with no heading in between. The docs
// gate reads the [Unreleased] range with it, so it checks the lines
// release-notes lifts.
func changelogSection(lines []string, version string) (from, to int) {
	want := "## [" + version + "]"
	start := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, want) })
	if start < 0 {
		return 0, 0
	}
	to = start + 1
	for to < len(lines) && !strings.HasPrefix(lines[to], "## ") && !isLinkDefinition(lines[to]) {
		to++
	}
	return start + 1, to
}

// promoteHeadings lifts every heading one level, because the file and the
// page are two different documents. In CHANGELOG.md a version is an h2
// and its change kinds are h3s under it; on the release page the version
// heading is gone, because GitHub renders the tag name as the h1, so an
// unaltered section starts at h3 and skips a rank. Only h3 and deeper are
// lifted, so a second h1 is impossible. Fences are read as the docs gate
// reads them, because a `###` inside a code block is content.
func promoteHeadings(body string) string {
	lines := strings.Split(body, "\n")
	code := docsCodeLines(lines)
	for i, line := range lines {
		if !code[i] && strings.HasPrefix(line, "###") {
			lines[i] = line[1:]
		}
	}
	return strings.Join(lines, "\n")
}

func joinTrimmed(body []string) string {
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	return strings.Join(body, "\n")
}

// isLinkDefinition reports whether a line is a Markdown link definition,
// as a changelog's compare-link footer is made of. It is the docs gate's
// rule, held to the first column: an indented one continues a list item.
func isLinkDefinition(line string) bool {
	return !strings.HasPrefix(line, " ") && docsMDLinkDef.MatchString(line)
}

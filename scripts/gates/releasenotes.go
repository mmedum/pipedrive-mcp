package main

import (
	"fmt"
	"io"
	"os"
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
	file := "CHANGELOG.md"
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
// lines that top and tail it. It stops at the next heading and at a
// markdown link definition, which is what a compare-link footer is made
// of: the footer follows the oldest section with no heading in between.
func sectionFor(changelog, version string) string {
	want := "## [" + version + "]"
	var body []string
	inside := false
	for _, line := range strings.Split(changelog, "\n") {
		switch {
		case strings.HasPrefix(line, want):
			inside = true
			continue
		case !inside:
			continue
		case strings.HasPrefix(line, "## "), isLinkDefinition(line):
			return joinTrimmed(body)
		case len(body) == 0 && strings.TrimSpace(line) == "":
			continue
		}
		body = append(body, line)
	}
	return joinTrimmed(body)
}

// promoteHeadings lifts every heading one level, because the file and the
// page are two different documents. In CHANGELOG.md a version is an h2
// and its change kinds are h3s under it; on the release page the version
// heading is gone, because GitHub renders the tag name as the h1, so an
// unaltered section starts at h3 and skips a rank. Fenced code is left
// alone, and only h3 and deeper are lifted, so a second h1 is impossible.
func promoteHeadings(body string) string {
	lines := strings.Split(body, "\n")
	fenced := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if !fenced && strings.HasPrefix(line, "###") {
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

func isLinkDefinition(line string) bool {
	if !strings.HasPrefix(line, "[") {
		return false
	}
	end := strings.Index(line, "]")
	return end > 0 && strings.HasPrefix(line[end:], "]: ")
}

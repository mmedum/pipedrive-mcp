package main

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
)

// changelogFile is the file this gate reads.
const changelogFile = "CHANGELOG.md"

// The CHANGELOG's link-reference block, held against its own headings.
//
// Keep a Changelog puts a `[x.y.z]: <compare url>` definition under every
// `## [x.y.z]` heading, and nothing checks it, so it rots quietly: a
// heading whose link is missing renders as literal brackets, and an
// `[Unreleased]` that still compares from an older tag silently claims
// the last release's work is unreleased.
//
// This is not hypothetical. v0.4.0 shipped with no `[0.4.0]` definition
// at all and `[Unreleased]` still pointing at v0.3.2, and it took until
// the v0.5.0 release-prep pass for anyone to notice — a whole release
// during which the file misdescribed itself.
//
// Unlike the `changelog` gate, this needs no base ref: it compares the
// file to itself, so it belongs in `make check` rather than in a job
// that only runs on a pull request.

var (
	versionHeading = regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\]`)
	linkDefinition = regexp.MustCompile(`(?m)^\[([^\]]+)\]:\s*(\S+)`)
)

func changelogLinksGate(out io.Writer) error {
	data, err := os.ReadFile(changelogFile)
	if err != nil {
		return err
	}
	text := string(data)

	var headings []string
	for _, m := range versionHeading.FindAllStringSubmatch(text, -1) {
		headings = append(headings, m[1])
	}
	if len(headings) == 0 {
		return fmt.Errorf("%s has no version headings; this gate is asserting on nothing", changelogFile)
	}

	links := map[string]string{}
	for _, m := range linkDefinition.FindAllStringSubmatch(text, -1) {
		links[m[1]] = m[2]
	}

	var problems []string
	for _, v := range headings {
		if _, ok := links[v]; !ok {
			problems = append(problems, fmt.Sprintf(
				"## [%s] has no [%s]: link definition, so the heading renders as literal brackets", v, v))
		}
	}

	// Unreleased must compare from the newest release, or it claims that
	// release's work has not shipped.
	newest := headings[0]
	if url, ok := links["Unreleased"]; ok {
		if want := "v" + newest + "..."; !strings.Contains(url, want) {
			problems = append(problems, fmt.Sprintf(
				"[Unreleased] compares from %q; the newest release is %s, so it should compare from %s",
				url, newest, want))
		}
	}

	if len(problems) > 0 {
		slices.Sort(problems)
		return fmt.Errorf("%s:\n  %s", changelogFile, strings.Join(problems, "\n  "))
	}
	_, err = fmt.Fprintf(out, "  %s: %d version headings, every one linked\n", changelogFile, len(headings))
	return err
}

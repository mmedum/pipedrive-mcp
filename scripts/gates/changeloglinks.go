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
// Unlike the `changelog` gate, this needs no base ref: it compares the
// file to itself, so it belongs in `make check` rather than in a job
// that only runs on a pull request.

// changelogLinkURL is a footer definition's label and its URL.
var changelogLinkURL = regexp.MustCompile(`^\[([^\]]+)\]:[ \t]*(\S*)`)

func changelogLinksGate(out io.Writer) error {
	data, err := os.ReadFile(changelogFile)
	if err != nil {
		return err
	}
	doc := readChangelog(string(data))

	var headings []string
	for _, h := range doc.versions() {
		headings = append(headings, h.label)
	}
	if len(headings) == 0 {
		return fmt.Errorf("%s has no version headings; this gate is asserting on nothing", changelogFile)
	}

	// A heading's [label] links the definition of its label, matched
	// exactly, as every other reader of the CHANGELOG matches a label. One
	// in another case still renders (https://spec.commonmark.org/0.31.2/#matches),
	// so it is refused by name rather than read. A [v1.2.3]: ends the
	// section as the footer does, and links no ## [1.2.3].
	var problems []string
	links := map[string]string{}
	for _, line := range doc.prose {
		m := changelogLinkURL.FindStringSubmatch(line)
		if m == nil || !changelogFooter.MatchString(line) {
			continue
		}
		links[m[1]] = m[2]
		for _, h := range doc.headings {
			if h.label != m[1] && strings.EqualFold(h.label, m[1]) {
				problems = append(problems, fmt.Sprintf("[%s]: is the label of ## [%s] in another case; write it as the heading does",
					m[1], h.label))
			}
		}
	}

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

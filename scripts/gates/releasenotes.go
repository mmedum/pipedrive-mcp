package main

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"regexp"
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

// promoteHeadings lifts every heading one level, because the file and the
// page are two different documents. In CHANGELOG.md a version is an h2
// and its change kinds are h3s under it; on the release page the version
// heading is gone, because GitHub renders the tag name as the h1, so an
// unaltered section starts at h3 and skips a rank. Only h3 and deeper are
// lifted, so a second h1 is impossible. Fences are read as the docs gate
// reads them, because a `###` inside a code block is content.
func promoteHeadings(body string) string {
	lines := strings.Split(body, "\n")
	var fence docsFence
	for i, line := range lines {
		if !fence.code(line) && docsHeadingLevel(line) >= 3 {
			lines[i] = line[1:]
		}
	}
	return strings.Join(lines, "\n")
}

// sectionFor returns the body under `## [version]`, without the blank
// lines that top and tail it.
func sectionFor(changelog, version string) string {
	doc := readChangelog(changelog)
	from, to := doc.section(version)
	body := doc.lines[from:to]
	for len(body) > 0 && strings.TrimSpace(body[0]) == "" {
		body = body[1:]
	}
	return joinTrimmed(body)
}

// joinTrimmed drops the blank lines at the end, so a section that runs to
// the end of the file reads the same as one followed by a heading.
func joinTrimmed(body []string) string {
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}
	return strings.Join(body, "\n")
}

// semverPattern is a version's numbers and a prerelease: dot-separated
// identifiers of ASCII letters, digits and hyphens, none empty
// (https://semver.org/#spec-item-9). An identifier of hyphens alone, which
// semver's grammar admits, is refused: it names no release.
const semverPattern = `\d+\.\d+\.\d+(?:-` + semverIdentifier + `(?:\.` + semverIdentifier + `)*)?`

// semverIdentifier is one prerelease identifier holding a letter or a digit.
const semverIdentifier = `[0-9A-Za-z-]*[0-9A-Za-z][0-9A-Za-z-]*`

var (
	// changelogVersion is a release's version, a prerelease suffix allowed.
	changelogVersion = regexp.MustCompile(`^` + semverPattern + `$`)
	// changelogFooter is a compare-link footer line: Unreleased's or a
	// version's, with or without a v, its URL written or not, the label in
	// any case, as a label matches (https://spec.commonmark.org/0.31.2/#matches).
	// Any other link definition sits in a section's prose.
	changelogFooter = regexp.MustCompile(`^\[(?i:unreleased|v?` + semverPattern + `)\]:(?:[ \t]|$)`)
)

// changelogDoc is a CHANGELOG read once, fenced code as an example: a
// heading or a link reference inside a fence is neither. Every reader of
// the CHANGELOG reads it through this, so the release note, the link
// check, the docs gate and the changelog gate agree on where a section
// ends.
type changelogDoc struct {
	// lines are the file's lines, CRLF read as LF, and prose the same
	// lines with fenced code blanked.
	lines, prose []string
	// headings are the `## [label]` headings, in file order.
	headings []changelogHeading
}

// changelogHeading is a `## [label]` heading and the index of its line.
type changelogHeading struct {
	label string
	line  int
}

func readChangelog(text string) changelogDoc {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	c := changelogDoc{lines: strings.Split(text, "\n"), prose: docsProse(text)}
	for i, line := range c.prose {
		if label, ok := changelogLabel(line); ok {
			c.headings = append(c.headings, changelogHeading{label, i})
		}
	}
	return c
}

// changelogLabel is the label of a `## [label]` heading line.
func changelogLabel(line string) (string, bool) {
	if docsHeadingLevel(line) != 2 {
		return "", false
	}
	title := docsATXHeading.FindStringSubmatch(line)[1]
	label, _, ok := strings.Cut(strings.TrimPrefix(title, "["), "]")
	return label, ok && strings.HasPrefix(title, "[")
}

// section is the range of lines under `## [label]`, empty when there is
// no such heading. It ends at the next heading of its level or above and
// at the compare-link footer, which follows the oldest section with no
// heading in between.
func (c changelogDoc) section(label string) (from, to int) {
	i := slices.IndexFunc(c.headings, func(h changelogHeading) bool { return h.label == label })
	if i < 0 {
		return 0, 0
	}
	from = c.headings[i].line + 1
	for to = from; to < len(c.prose); to++ {
		if level := docsHeadingLevel(c.prose[to]); (level > 0 && level <= 2) || changelogFooter.MatchString(c.prose[to]) {
			break
		}
	}
	return from, to
}

// versions are the headings that name a release, in file order.
func (c changelogDoc) versions() []changelogHeading {
	var out []changelogHeading
	for _, h := range c.headings {
		if changelogVersion.MatchString(h.label) {
			out = append(out, h)
		}
	}
	return out
}

// lastTag is the newest release rev reaches: of the tags on its history,
// the highest vX.Y.Z by semver, a prerelease included, so neither a newer
// release on another line nor a nearer tag that names no release is rev's.
// It is empty when rev reaches no release, which is a state, not a
// failure; a git error is one.
func lastTag(rev string) (string, error) {
	tags, err := git("tag", "--merged", rev)
	if err != nil {
		return "", err
	}
	newest := ""
	for _, tag := range strings.Fields(tags) {
		v, ok := strings.CutPrefix(tag, "v")
		if ok && changelogVersion.MatchString(v) && (newest == "" || changelogCompare(v, newest) > 0) {
			newest = v
		}
	}
	if newest == "" {
		return "", nil
	}
	return "v" + newest, nil
}

// changelogCompare orders two versions changelogVersion matches by semver
// precedence: by number, a prerelease before its release, and prerelease
// fields compared in turn, numbers below words (https://semver.org/#spec-item-11).
func changelogCompare(a, b string) int {
	coreA, preA, _ := strings.Cut(a, "-")
	coreB, preB, _ := strings.Cut(b, "-")
	if c := slices.CompareFunc(strings.Split(coreA, "."), strings.Split(coreB, "."), changelogCompareNumbers); c != 0 {
		return c
	}
	switch {
	case preA == preB:
		return 0
	case preA == "":
		return 1
	case preB == "":
		return -1
	}
	return slices.CompareFunc(strings.Split(preA, "."), strings.Split(preB, "."), func(x, y string) int {
		xNum, yNum := changelogDigits(x), changelogDigits(y)
		switch {
		case xNum && yNum:
			return changelogCompareNumbers(x, y)
		case xNum:
			return -1
		case yNum:
			return 1
		}
		return strings.Compare(x, y)
	})
}

// changelogCompareNumbers orders two runs of digits by the number they
// write, however long. Semver allows no leading zero.
func changelogCompareNumbers(x, y string) int {
	return cmp.Or(cmp.Compare(len(x), len(y)), strings.Compare(x, y))
}

// changelogDigits reports whether a prerelease field is a number.
func changelogDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

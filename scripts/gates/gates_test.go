package main

import (
	"strings"
	"testing"
)

const changelogFixture = `# Changelog

## [Unreleased]

## [1.1.0] - 2026-02-02

### Added
- A thing.

` + "```bash\n# not a heading\n### also not a heading\n```" + `

## [1.0.0] - 2026-01-01

### Added
- The first thing.

[Unreleased]: https://example.com/compare/v1.1.0...HEAD
[1.0.0]: https://example.com/releases/v1.0.0
`

func TestSectionForTakesOnlyItsOwnVersion(t *testing.T) {
	got := sectionFor(changelogFixture, "1.1.0")
	if !strings.Contains(got, "- A thing.") {
		t.Errorf("the section lost its own body:\n%s", got)
	}
	if strings.Contains(got, "The first thing") {
		t.Errorf("the section ran into the one below it:\n%s", got)
	}
}

// The compare-link footer follows the oldest section with no heading in
// between. The shell version this replaced published it as part of that
// release's notes.
func TestSectionForStopsAtTheLinkFooter(t *testing.T) {
	got := sectionFor(changelogFixture, "1.0.0")
	if strings.Contains(got, "example.com/releases") {
		t.Errorf("the link footer was published as release notes:\n%s", got)
	}
}

func TestSectionForIsEmptyForAnAbsentVersion(t *testing.T) {
	if got := sectionFor(changelogFixture, "9.9.9"); got != "" {
		t.Errorf("a version with no section returned %q", got)
	}
}

// GitHub renders the tag name as the page's h1, so a section published
// unaltered starts at h3 and skips a rank.
func TestPromoteHeadingsLiftsOneLevelAndSparesFencedCode(t *testing.T) {
	got := promoteHeadings(sectionFor(changelogFixture, "1.1.0"))
	if !strings.Contains(got, "## Added") || strings.Contains(got, "### Added") {
		t.Errorf("headings were not lifted:\n%s", got)
	}
	for _, want := range []string{"# not a heading", "### also not a heading"} {
		if !strings.Contains(got, want) {
			t.Errorf("fenced code was rewritten; %q is missing:\n%s", want, got)
		}
	}
	// Outside a fence only: a `#` comment in a code block is not an h1,
	// and the first version of this assertion said it was.
	fenced := false
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if !fenced && strings.HasPrefix(line, "# ") {
			t.Errorf("emitted an h1, duplicating the tag heading: %q", line)
		}
	}
}

func TestTouchesWatchedReadsThePaths(t *testing.T) {
	for _, tc := range []struct {
		files string
		want  bool
	}{
		{"internal/tools/deals.go", true},
		{"cmd/pipedrive-mcp/main.go", true},
		{"internal/pipedrive/client.go", true},
		{"README.md\ndocs/architecture.md", false},
		{"", false},
	} {
		if got := touchesWatched(tc.files); got != tc.want {
			t.Errorf("touchesWatched(%q) = %t, want %t", tc.files, got, tc.want)
		}
	}
}

// An entry added under an already-released heading is invisible to
// anyone reading the release it belongs to — and now that the release
// page is the changelog section, invisible on the page too.
func TestAddedUnderUnreleasedIgnoresOtherSections(t *testing.T) {
	const diff = `@@ -1,10 +1,12 @@
 # Changelog
 
 ## [Unreleased]
+- the new entry
 
 ## [1.0.0] - 2026-01-01
+- snuck in under a released heading
 - the old entry
`
	if got := addedUnderUnreleased(diff); got != 1 {
		t.Errorf("counted %d added lines under [Unreleased], want 1", got)
	}
}

func TestAddedUnderUnreleasedCountsNothingWhenTheSectionIsUntouched(t *testing.T) {
	const diff = `@@ -1,6 +1,7 @@
 ## [1.0.0] - 2026-01-01
+- only an old section changed
`
	if got := addedUnderUnreleased(diff); got != 0 {
		t.Errorf("counted %d, want 0", got)
	}
}

// Cutting a release renames [Unreleased] to the version, so the entries
// being released sit under a heading this gate would otherwise read as
// already published. It failed exactly that way on the v0.5.0 cut. The
// cut excuses only the heading it cut: entries parked under an older
// released heading in the same diff stay invisible to a reader of that
// release, so they must not be what carries the gate.
func TestAddedUnderUnreleasedCountsTheCutSectionOnly(t *testing.T) {
	const diff = `@@ -1,8 +1,10 @@
 # Changelog
 
-## [Unreleased]
+## [0.5.0] - 2026-09-18
+- the entry being released
 
 ## [0.4.0] - 2026-09-17
+- parked under a shipped release
 - the old entry
`
	if got := addedUnderUnreleased(diff); got != 1 {
		t.Errorf("counted %d, want 1: only the cut section counts", got)
	}
}

// Without the cut, a version heading is just a released section, and
// this is the case the gate exists for. It also pins that the cut is
// read off the diff rather than assumed: hard-coding it true passes
// every other test in this file.
func TestAddedUnderUnreleasedIgnoresAnUncutVersionHeading(t *testing.T) {
	const diff = `@@ -1,8 +1,10 @@
 # Changelog
 
 ## [Unreleased]
 
+## [0.5.0] - 2026-09-18
+- added under a heading this diff did not cut
 
 ## [0.4.0] - 2026-09-17
 - the old entry
`
	if got := addedUnderUnreleased(diff); got != 0 {
		t.Errorf("counted %d, want 0: no [Unreleased] heading was cut", got)
	}
}

// The hint exists for the one failure that looks wrong: entries were
// added, under a version heading, and the gate still refused. It must
// not fire on a cut, where the gate passes and the hint would confuse.
func TestCutHintOnlyFiresWithoutACut(t *testing.T) {
	const parked = `@@ -1,4 +1,6 @@
 ## [Unreleased]
 
+## [0.5.0] - 2026-09-18
+- added under a heading this diff did not cut
`
	if cutHint(parked) == "" {
		t.Error("a new section that was not cut from [Unreleased] got no hint")
	}
	const cut = `@@ -1,4 +1,5 @@
-## [Unreleased]
+## [0.5.0] - 2026-09-18
+- the entry being released
`
	if got := cutHint(cut); got != "" {
		t.Errorf("a release cut got a hint it cannot act on: %q", got)
	}
}

func TestPinnedWithReasonNeedsTheReason(t *testing.T) {
	const gomod = `module example.com/x

require (
	example.com/held v1.0.0 // pinned: upstream dropped the API we use
	example.com/bare v1.0.0
)
`
	if !pinnedWithReason(gomod, "example.com/held") {
		t.Error("a pin with a reason was not recognised")
	}
	if pinnedWithReason(gomod, "example.com/bare") {
		t.Error("a bare pin counted as pinned; the reason is the point")
	}
}

// grep '"tools"' passes on a line that merely mentions the word, which
// is why this decodes the frame instead.
func TestToolsListReplyReadsTheFrame(t *testing.T) {
	const good = `{"jsonrpc":"2.0","id":1,"result":{}}
{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"a"},{"name":"b"}]}}`
	n, err := toolsListReply(good)
	if err != nil || n != 2 {
		t.Errorf("toolsListReply = %d, %v; want 2, nil", n, err)
	}

	for _, tc := range []struct{ name, out string }{
		{"no output", ""},
		{"no reply to id 2", `{"jsonrpc":"2.0","id":1,"result":{}}`},
		{"a reply with no tools array", `{"jsonrpc":"2.0","id":2,"result":{}}`},
		{"the word tools in prose", `an error mentioning "tools" and nothing else`},
	} {
		if _, err := toolsListReply(tc.out); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
}

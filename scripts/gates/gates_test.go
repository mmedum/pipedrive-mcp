package main

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// A section runs to the compare-link footer: a definition in the first
// column whose label is Unreleased, in any case, or a version, with or
// without a v. A footnote is no definition, an indented line continues a
// list item, and a definition of another label is prose: each belongs to
// the section it is in.
func TestASectionEndsOnlyAtTheLinkFooter(t *testing.T) {
	const head = "## [1.0.0] - 2026-09-13\n\n- The thing.[^1]\n"
	for _, tc := range []struct {
		name, tail, want string
	}{
		{"a footnote", "\n[^1]: Only on Linux.\n", "- The thing.[^1]\n\n[^1]: Only on Linux."},
		{"a definition in the first column", "\n[1.0.0]: https://example.com/v1.0.0\n", "- The thing.[^1]"},
		{"a version's definition with its URL not yet written", "\n[1.0.0]:\n", "- The thing.[^1]"},
		{"a version's definition with a v", "\n[v1.0.0]: https://example.com/v1.0.0\n", "- The thing.[^1]"},
		{"a version's definition with a capital V", "\n[V1.0.0]: https://example.com/v1.0.0\n", "- The thing.[^1]"},
		{"a prerelease's definition with a v", "\n[v1.1.0-rc.1]: https://example.com/v1.1.0-rc.1\n", "- The thing.[^1]"},
		{"Unreleased's definition", "\n[Unreleased]: https://example.com/compare/v1.0.0...HEAD\n", "- The thing.[^1]"},
		{"Unreleased's definition in lower case", "\n[unreleased]: https://example.com/compare/v1.0.0...HEAD\n",
			"- The thing.[^1]"},
		{"Unreleased's definition in upper case", "\n[UNRELEASED]: https://example.com/compare/v1.0.0...HEAD\n",
			"- The thing.[^1]"},
		{"a definition continuing a list item", "  [1]: https://example.com/issue/1\n",
			"- The thing.[^1]\n  [1]: https://example.com/issue/1"},
		{"a version's definition continuing a list item", "  [v1.0.0]: https://example.com/v1.0.0\n",
			"- The thing.[^1]\n  [v1.0.0]: https://example.com/v1.0.0"},
		{"a definition of another label", "\n[issue]: https://example.com/issue/1\n",
			"- The thing.[^1]\n\n[issue]: https://example.com/issue/1"},
		{"a definition of a label that is no version", "\n[v1]: https://example.com/v1\n",
			"- The thing.[^1]\n\n[v1]: https://example.com/v1"},
		{"a definition of a label that only starts as Unreleased", "\n[unreleased-notes]: https://example.com/notes\n",
			"- The thing.[^1]\n\n[unreleased-notes]: https://example.com/notes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sectionFor(head+tc.tail, "1.0.0"); got != tc.want {
				t.Errorf("section = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadChangelog(t *testing.T) {
	doc := readChangelog(strings.ReplaceAll("# Changelog\n\n## [Unreleased]\n\n- a\n### [8.8.8]\n\n~~~markdown\n- in a fence\n## [9.9.9]\n"+
		"[9.9.9]: https://example.com/x\n~~~\n\n- b\n\n## [2.0.0-rc.1] - 2026-01-01\n\n- c\n##\tLater\n- d\n\n"+
		"[Unreleased]: https://example.com/u\n[2.0.0-rc.1]:\n", "\n", "\r\n"))
	if want := []changelogHeading{{"Unreleased", 2}, {"2.0.0-rc.1", 15}}; !slices.Equal(doc.headings, want) {
		t.Errorf("headings = %v, want %v", doc.headings, want)
	}
	if want := []changelogHeading{{"2.0.0-rc.1", 15}}; !slices.Equal(doc.versions(), want) {
		t.Errorf("versions = %v, want %v", doc.versions(), want)
	}
	if from, to := readChangelog("## [1.0.0]\n- a\n# Notes\n- b\n").section("1.0.0"); from != 1 || to != 2 {
		t.Errorf("a section ends at an h1 too: lines [%d, %d), want [1, 2)", from, to)
	}
	// The footer's own labels end a section, its URL written or not: a link
	// definition with any other label is prose.
	for line, ends := range map[string]bool{
		"[Unreleased]:":                       true,
		"[Unreleased]: https://example.com/u": true,
		"[2.0.0-rc.1]:\thttps://example.com":  true,
		"[note]: see below":                   false,
		"[1.0.0]:x":                           false,
		"[v1.0.0]: https://example.com":       true,
		"[1.0]: https://example.com":          false,
		"[1.0.0--]: https://example.com":      false,
		"[1.0.0-rc..1]: https://example.com":  false,
	} {
		if _, to := readChangelog("## [1.0.0]\n- a\n" + line + "\n- b\n").section("1.0.0"); (to == 2) != ends {
			t.Errorf("%q ends the section: %v, want %v", line, to == 2, ends)
		}
	}
	// A fence closes only at a line of its own character, as many or more,
	// and a # with no blank after it starts no heading.
	inner := readChangelog("##\t[1.0.0]\n````\n```bash\n## [9.9.9]\n```\n````\n#hashtag\n####### seven\n- a\n")
	if want := []changelogHeading{{"1.0.0", 0}}; !slices.Equal(inner.versions(), want) {
		t.Errorf("versions = %v, want %v: a tab after ## starts a heading", inner.versions(), want)
	}
	if from, to := inner.section("1.0.0"); from != 1 || to != 10 {
		t.Errorf("section 1.0.0 = lines [%d, %d), want [1, 10)", from, to)
	}
	for label, want := range map[string]string{
		"Unreleased": "\n- a\n### [8.8.8]\n\n~~~markdown\n- in a fence\n## [9.9.9]\n[9.9.9]: https://example.com/x\n~~~\n\n- b\n",
		"2.0.0-rc.1": "\n- c",
		"9.9.9":      "",
	} {
		if from, to := doc.section(label); strings.Join(doc.lines[from:to], "\n") != want {
			t.Errorf("section %s = %q, want %q", label, strings.Join(doc.lines[from:to], "\n"), want)
		}
	}
}

// The release note is the section the docs gate scans, fenced code kept
// and never cut at, and a fenced heading is not lifted.
func TestReleaseNotesReadTheFence(t *testing.T) {
	path := filepath.Join(t.TempDir(), changelogFile)
	if err := os.WriteFile(path, []byte("## [Unreleased]\n\n## [1.0.0]\n\n### Added\n\n- a\n\n~~~bash\n## not a heading\n### nor this\n~~~\n\n- b\n\n[1.0.0]: https://example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := releaseNotes(&out, []string{"1.0.0", path}); err != nil {
		t.Fatal(err)
	}
	if want := "## Added\n\n- a\n\n~~~bash\n## not a heading\n### nor this\n~~~\n\n- b\n"; out.String() != want {
		t.Errorf("notes = %q, want %q", out.String(), want)
	}
}

// Versions order as semver orders them, so a release candidate is a
// release and v1.10.0 comes after v1.9.0.
func TestChangelogCompareOrdersAsSemver(t *testing.T) {
	chain := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2",
		"1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.9.0", "1.10.0", "10.0.0"}
	for i := range chain {
		for j := range chain {
			if got, want := changelogCompare(chain[i], chain[j]), cmp.Compare(i, j); got != want {
				t.Errorf("changelogCompare(%s, %s) = %d, want %d", chain[i], chain[j], got, want)
			}
		}
	}
}

// The link check reads the CHANGELOG as the release note does: a fenced
// heading is an example, a heading's link is the definition of its own
// label, written as the heading writes it, and a [v1.2.3]: definition
// links no ## [1.2.3].
func TestChangelogLinksReadTheFooter(t *testing.T) {
	const good = "# Changelog\n\n## [Unreleased]\n\n## [1.1.0-RC.1]\n\n- b\n\n```text\n## [9.9.9]\n```\n\n## [1.0.0]\n\n- a\n\n" +
		"[Unreleased]: https://example.com/compare/v1.1.0-RC.1...HEAD\n[1.1.0-RC.1]: https://example.com/v1.1.0-RC.1\n[1.0.0]: https://example.com/v1.0.0\n"
	for _, tc := range []struct {
		name, old, new, want string
	}{
		{"the footer, a prerelease and a fenced heading", "", "", ""},
		{"Unreleased's label in another case", "[Unreleased]:", "[unreleased]:",
			"CHANGELOG.md:\n  [unreleased]: is the label of ## [Unreleased] in another case; write it as the heading does"},
		{"a prerelease's label in another case", "[1.1.0-RC.1]:", "[1.1.0-rc.1]:",
			"CHANGELOG.md:\n  ## [1.1.0-RC.1] has no [1.1.0-RC.1]: link definition, so the heading renders as literal brackets\n" +
				"  [1.1.0-rc.1]: is the label of ## [1.1.0-RC.1] in another case; write it as the heading does"},
		{"a definition with a v", "[1.0.0]:", "[v1.0.0]:",
			"CHANGELOG.md:\n  ## [1.0.0] has no [1.0.0]: link definition, so the heading renders as literal brackets"},
		{"Unreleased compared from an older release", "compare/v1.1.0-RC.1...HEAD", "compare/v1.0.0...HEAD",
			"CHANGELOG.md:\n  [Unreleased] compares from \"https://example.com/compare/v1.0.0...HEAD\"; the newest release is 1.1.0-RC.1, so it should compare from v1.1.0-RC.1..."},
		{"the footer inside a fence", "\n[Unreleased]:", "\n```\n[Unreleased]:",
			"CHANGELOG.md:\n  ## [1.0.0] has no [1.0.0]: link definition, so the heading renders as literal brackets\n" +
				"  ## [1.1.0-RC.1] has no [1.1.0-RC.1]: link definition, so the heading renders as literal brackets"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if err := os.WriteFile(changelogFile, []byte(strings.Replace(good, tc.old, tc.new, 1)), 0o600); err != nil {
				t.Fatal(err)
			}
			err := changelogLinksGate(&strings.Builder{})
			if got := fmt.Sprint(err); tc.want == "" && err != nil || tc.want != "" && got != tc.want {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}

// The last release is the newest release tag by semver, a prerelease
// included, of those the revision reaches. A newer release on another line
// of history is not the revision's, a nearer tag that is no release or an
// older one is not its newest, and a branch that reaches none has no
// release, although the repository has tags.
func TestLastTagIsOneTheRevisionReaches(t *testing.T) {
	initRepo(t)
	empty := func(message string) { mustGit(t, "commit", "--quiet", "--allow-empty", "-m", message) }
	empty("Start")
	mustGit(t, "branch", "before-any-release")
	empty("Release")
	mustGit(t, "tag", "v1.0.0")
	mustGit(t, "switch", "--quiet", "-c", "maint")
	empty("Fix")
	mustGit(t, "tag", "v1.0.1")
	mustGit(t, "switch", "--quiet", "main")
	empty("Next")
	mustGit(t, "tag", "v2.0.0")
	// main moves on before the maintenance release merges in, so the merge
	// is nearer v1.0.1 than v2.0.0.
	for _, m := range []string{"More", "And more", "And still more"} {
		empty(m)
	}
	mustGit(t, "switch", "--quiet", "-c", "merged")
	mustGit(t, "merge", "--quiet", "--no-ff", "-m", "Merge maint", "maint")
	// git's version sort and the tags' names both put v3.0.0-rc.1 after
	// v3.0.0; semver puts it before.
	mustGit(t, "switch", "--quiet", "-c", "next", "main")
	empty("Candidate")
	mustGit(t, "tag", "v3.0.0-rc.1")
	mustGit(t, "branch", "candidate")
	empty("Final")
	mustGit(t, "tag", "v3.0.0")
	mustGit(t, "switch", "--quiet", "main")
	empty("Weekly")
	mustGit(t, "tag", "weekly")
	mustGit(t, "tag", "v9.9.9--")
	for rev, want := range map[string]string{
		"main":               "v2.0.0",
		"maint":              "v1.0.1",
		"merged":             "v2.0.0",
		"candidate":          "v3.0.0-rc.1",
		"next":               "v3.0.0",
		"before-any-release": "",
	} {
		if got, err := lastTag(rev); err != nil || got != want {
			t.Errorf("lastTag(%s) = %q, %v; want %q", rev, got, err, want)
		}
	}
	if _, err := lastTag("no-such-rev"); err == nil {
		t.Error("lastTag of a revision git cannot read returned no error")
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

// What users get is any file but a test in a package the binary imports,
// a file such a package embeds, or the bundle's manifest, which a person
// reads when installing it. Test data is left out even where a package
// embeds it.
func TestTouchesWatchedReadsThePaths(t *testing.T) {
	shipped := &shippedSource{
		dirs:   map[string]bool{"cmd/pipedrive-mcp": true, "internal/pipedrive": true, "internal/server": true},
		embeds: map[string]bool{"internal/server/instructions.md": true, "internal/pipedrive/testdata/fields.json": true},
	}
	for _, tc := range []struct {
		files string
		want  bool
	}{
		{"cmd/pipedrive-mcp/main.go", true},
		{"internal/pipedrive/client.go", true},
		{"internal/server/server.go", true},
		{"internal/server/instructions.md", true},
		{"internal/pipedrive/client_test.go", false},
		// The go command builds a package from more than its Go files.
		{"internal/pipedrive/probe_amd64.s", true},
		{"internal/pipedrive/rsrc_windows_amd64.syso", true},
		{"cmd/pipedrive-mcp/notes.txt", true},
		{"internal/pipedrive/testdata/probe_amd64.s", false},
		{"internal/pipedrive/sub/probe_amd64.s", false},
		{"internal/pipedrive/testdata/v2-response-fields.json", false},
		{"internal/pipedrive/testdata/fields.json", false},
		{"internal/pipedrive/testdata/v2-response-fields.json\ninternal/pipedrive/client.go", true},
		{"internal/pipedrive/sub/x.go", false},
		{"internal/integration/read_test.go\nscripts/gates/pins.go", false},
		{"README.md\ndocs/architecture.md", false},
		{"packaging/mcpb/manifest.json", true},
		{"packaging/mcpb/mcpb-manifest-v0.3.schema.json", false},
		{"", false},
	} {
		if got := touchesWatched(tc.files, shipped); got != tc.want {
			t.Errorf("touchesWatched(%q) = %t, want %t", tc.files, got, tc.want)
		}
	}
}

// The binary is built from every package of this module ./cmd/...
// imports, so a package no hand list names is held too.
func TestTheShippedSetIsWhatTheBinaryImports(t *testing.T) {
	shipped, err := listShipped(parityRepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"cmd/pipedrive-mcp", "internal/server", "internal/config", "internal/version", "internal/tools"} {
		if !shipped.dirs[dir] {
			t.Errorf("the shipped set lacks %s", dir)
		}
	}
	for _, dir := range []string{"scripts/gates", "internal/integration"} {
		if shipped.dirs[dir] {
			t.Errorf("the shipped set holds %s, which the binary does not import", dir)
		}
	}
}

// A go, toolchain or replace line changes what the binary is built with;
// a dependency's version is go.sum's and Dependabot's.
func TestGoModBuildLinesLeaveOutTheRequires(t *testing.T) {
	const base = "module example.com/x\n\ngo 1.27.3\n\nrequire example.com/a v1.0.0\n\nrequire (\n\texample.com/b v1.0.0\n)\n"
	for _, tc := range []struct {
		name, gomod string
		same        bool
	}{
		{"a require bumped", strings.Replace(base, "a v1.0.0", "a v1.1.0", 1), true},
		{"a block's require bumped", strings.Replace(base, "b v1.0.0", "b v1.1.0", 1), true},
		{"a tool added", base + "tool example.com/c\n", true},
		{"a require block written require(", strings.Replace(base, "require (", "require(", 1), true},
		{"the go line", strings.Replace(base, "go 1.27.3", "go 1.28.0", 1), false},
		{"a toolchain", base + "toolchain go1.28.0\n", false},
		{"a replace", base + "replace example.com/a => ../a\n", false},
	} {
		if same := goModBuildLines(tc.gomod) == goModBuildLines(base); same != tc.same {
			t.Errorf("%s: unchanged = %t, want %t", tc.name, same, tc.same)
		}
	}
}

// The gate's git and go calls run in a checkout. One that fails reached
// no verdict, so the gate says so and exits 2.
func TestTheChangelogGateNeedsAGitCheckout(t *testing.T) {
	initRepo(t)
	putAll(t, ".", map[string]string{"go.mod": "module example.com/x\n\ngo 1.27.3\n", "cmd/x/main.go": "package main\n\nfunc main() {}\n"})
	mustGit(t, "add", "-A")
	mustGit(t, "commit", "-q", "-m", "first")
	err := changelogGate(&strings.Builder{}, []string{"no-such-base", "HEAD"})
	if !errors.As(err, new(couldNotRun)) || exitCode(err) != 2 || !strings.Contains(err.Error(), "git diff --name-only --no-renames no-such-base HEAD") {
		t.Errorf("got %v, want a git diff that could not run", err)
	}
}

// The gate end to end: a change to a shipped package or the bundle's
// manifest with no entry is refused, one with an entry passes, and a
// change outside what users get needs none.
func TestTheChangelogGateReadsWhatTheBinaryIsBuiltFrom(t *testing.T) {
	const changelog = "# Changelog\n\n## [Unreleased]\n\n## [1.0.0]\n\n- First.\n"
	initRepo(t)
	putAll(t, ".", map[string]string{
		"go.mod":            "module example.com/x\n\ngo 1.27.3\n",
		"cmd/x/main.go":     "package main\n\nimport _ \"example.com/x/internal/a\"\n\nfunc main() {}\n",
		"internal/a/a.go":   "package a\n",
		"internal/b/b.go":   "package b\n",
		changelogFile:       changelog,
		"scripts/s/main.go": "package main\n\nfunc main() {}\n",
		manifestPath:        "{}\n",
	})
	mustGit(t, "add", "-A")
	mustGit(t, "commit", "-q", "-m", "base")
	mustGit(t, "tag", "base")
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"a package the binary imports, with no entry", map[string]string{"internal/a/a.go": "package a\n\nvar X = 1\n"},
			"what users get changed but CHANGELOG.md did not; the release notes are CHANGELOG.md's section, so the change would ship unannounced"},
		{"a replace line, with no entry", map[string]string{"go.mod": "module example.com/x\n\ngo 1.27.3\n\nreplace example.com/y => ./y\n"},
			"what users get changed but CHANGELOG.md did not; the release notes are CHANGELOG.md's section, so the change would ship unannounced"},
		{"the bundle's manifest, with no entry", map[string]string{manifestPath: "{\"name\": \"x\"}\n"},
			"what users get changed but CHANGELOG.md did not; the release notes are CHANGELOG.md's section, so the change would ship unannounced"},
		{"a package the binary imports, with a blank line under [Unreleased]", map[string]string{
			"internal/a/a.go": "package a\n\nvar X = 1\n", changelogFile: strings.Replace(changelog, "## [Unreleased]\n", "## [Unreleased]\n\n\n", 1)},
			"CHANGELOG.md changed but no entry was added under [Unreleased], the section the next release's notes are cut from"},
		{"a package the binary imports, with an entry", map[string]string{
			"internal/a/a.go": "package a\n\nvar X = 1\n", changelogFile: strings.Replace(changelog, "## [Unreleased]\n", "## [Unreleased]\n\n- A change.\n", 1)}, ""},
		{"a package the binary does not import, a test and a script", map[string]string{
			"internal/b/b.go": "package b\n\nvar X = 1\n", "internal/a/a_test.go": "package a\n", "scripts/s/main.go": "package main\n\nfunc main() { _ = 1 }\n"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mustGit(t, "checkout", "-q", "base")
			putAll(t, ".", tc.files)
			mustGit(t, "add", "-A")
			mustGit(t, "commit", "-q", "-m", tc.name)
			err := changelogGate(&strings.Builder{}, []string{"base", "HEAD"})
			if got := fmt.Sprint(err); tc.want == "" && err != nil || tc.want != "" && got != tc.want {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
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
	if got := addedUnderUnreleased(readChangelogDiff(diff)); got != 1 {
		t.Errorf("counted %d added lines under [Unreleased], want 1", got)
	}
}

func TestAddedUnderUnreleasedCountsNothingWhenTheSectionIsUntouched(t *testing.T) {
	const diff = `@@ -1,6 +1,7 @@
 ## [1.0.0] - 2026-01-01
+- only an old section changed
`
	if got := addedUnderUnreleased(readChangelogDiff(diff)); got != 0 {
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
	if got := addedUnderUnreleased(readChangelogDiff(diff)); got != 1 {
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
	if got := addedUnderUnreleased(readChangelogDiff(diff)); got != 0 {
		t.Errorf("counted %d, want 0: no [Unreleased] heading was cut", got)
	}
}

// An entry is a bullet at the margin, outside fenced code, as the release
// note reads one: a blank line, a heading or prose added under
// [Unreleased] announces nothing, and a fenced heading starts no section.
func TestAddedUnderUnreleasedCountsOnlyEntries(t *testing.T) {
	for _, tc := range []struct {
		name, added string
		want        int
	}{
		{"a blank line", "+\n", 0},
		{"a heading alone", "+### Fixed\n", 0},
		{"a line of prose", "+Nothing yet.\n", 0},
		{"an indented bullet", "+  - a detail\n", 0},
		{"a bullet in a fence", "+```text\n+- not an entry\n+```\n", 0},
		{"a bullet", "+### Fixed\n+- the entry\n", 1},
	} {
		diff := "@@ -1,6 +1,8 @@\n # Changelog\n \n ## [Unreleased]\n" + tc.added + " \n ## [1.0.0] - 2026-01-01\n - the old entry\n"
		if got := addedUnderUnreleased(readChangelogDiff(diff)); got != tc.want {
			t.Errorf("%s: counted %d, want %d", tc.name, got, tc.want)
		}
	}
	// A fenced [Unreleased] heading in a released section starts nothing.
	const fenced = "@@ -1,8 +1,9 @@\n # Changelog\n \n ## [1.0.0] - 2026-01-01\n ```text\n ## [Unreleased]\n+- under a fenced heading\n ```\n"
	if got := addedUnderUnreleased(readChangelogDiff(fenced)); got != 0 {
		t.Errorf("a bullet under a fenced heading counted %d, want 0", got)
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
	if cutHint(readChangelogDiff(parked)) == "" {
		t.Error("a new section that was not cut from [Unreleased] got no hint")
	}
	const cut = `@@ -1,4 +1,5 @@
-## [Unreleased]
+## [0.5.0] - 2026-09-18
+- the entry being released
`
	if got := cutHint(readChangelogDiff(cut)); got != "" {
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
		t.Error("a pin with a reason was not recognized")
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

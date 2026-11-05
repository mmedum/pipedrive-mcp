package main

import (
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The fixture checkout's paths, named once because most rows edit them.
const (
	fxArch        = "docs/architecture.md"
	fxRunbook     = "docs/runbook.md"
	fxReadme      = "README.md"
	fxAgent       = "CLAUDE.md"
	fxChangelog   = "CHANGELOG.md"
	fxClient      = "internal/pipedrive/client.go"
	fxClientTest  = "internal/pipedrive/client_test.go"
	fxIntegration = "internal/integration/write_test.go"
	fxServer      = "cmd/server/main.go"
	fxGoMod       = "go.mod"
	fxEvals       = "scripts/evals/tasks.go"
	fxGolangci    = ".golangci.yml"
	fxMakefile    = "Makefile"
	fxGitignore   = ".gitignore"
	fxGates       = "scripts/gates/main.go"
)

// textLines is a file of these lines, so a fixture's line numbers can be
// read off its source: the nth argument is line n.
func textLines(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

// The index rows, named because several rows edit them whole.
const (
	fxRow01 = "| D-01 | Retries back off | accepted | TestRetry, smoke |"
	fxRow02 = "| D-02 | Retry forever | superseded by D-03 | — |"
	fxRow03 = "| D-03 | Retry three times | accepted | forbidigo, lint, review |"
)

// The ends of the problems several tests expect, written once.
const (
	notAHolder = "which is not review, a make target, a gates command, an enabled linter or a test or fuzz function"
	bySlug     = "cite a D-ID, docs/<file>.md#<slug> or a source link"
	notTagged  = ", which no _test.go file built only with -tags=integration declares"
	notListed  = ", which scripts/evals/tasks.go does not list"
)

// docsFixture is a checkout the docs gate accepts as it stands. Between
// them its files hold one of everything the gate must stay quiet about:
// each kind of Held by item, a superseded decision cited only where that
// is allowed, a repeated heading, links to a file and a directory, a
// codemap naming each package, integration tests and their subtests
// cited across comment lines, eval tasks, config comments, go.mod's pin
// reason, and citations in a fence, a string literal, a testdata page and
// a released changelog section.
func docsFixture() map[string]string {
	return map[string]string{
		fxMakefile: textLines(
			"# The checks CI runs (D-03).",
			"check: lint docs",
			"",
			".PHONY: lint",
			"lint:",
			"\tgolangci-lint run",
			"",
			".PHONY: docs",
			"docs:",
			"\t@go run ./scripts/gates docs",
			"",
			".PHONY: integration",
			"integration:",
			"\tgo test -tags=integration ./internal/integration/",
		),
		fxGolangci: textLines(
			`version: "2"`,
			"linters:",
			"  default: none",
			"  enable:",
			"    - errcheck",
			"    - forbidigo # stdout carries only frames",
			"  settings:",
			"    govet:",
			"      enable:",
			"        - nilness",
			"    misspell:",
			"      locale: US # American English, as D-01 says",
			"formatters:",
			"  enable:",
			"    - gofmt",
		),
		fxGitignore: textLines("internal/generated/", "# Generated, as docs/runbook.md#steps says (D-01)."),
		fxGoMod: textLines(
			"module example.com/fixture",
			"",
			"go 1.21",
			"",
			"require example.com/retry v1.0.0 // pinned: v1.1 retries forever, which D-03 rules out, as the integration test TestWrite_Retry/twice shows",
		),
		fxGates: textLines("package main", "", "func main() {}"),
		fxClient: textLines(
			"package pipedrive",
			"",
			"// Retries back off (D-01), as measured live.",
			"// A redirect is followed once, as RFC 9110 §15.4 allows.",
			`func Retry() string { return "D-99 and §4 in a string are data" }`,
			"",
			"// Deals read back. Checked by the integration",
			"// tests TestRead_Deal/deal and /person, and by the integration test",
			"// TestWrite_Retry/gives_up.",
			"func Deals() {}",
			"",
			"// Retries stop. Checked by the integration tests TestWrite_Retry/once,",
			"// TestWrite_Retry/twice or `TestRead_Deal`; no integration test sends one.",
			"func Stop() {}",
			"",
			`// Picked by the eval tasks "find-by-name" and`,
			"// `won-then-reopen`, and by the eval task \"find-by-name\".",
			"func Pick() {}",
		),
		fxClientTest: textLines(
			"package pipedrive",
			"",
			`import "testing"`,
			"",
			"func TestRetry(t *testing.T) { _ = Retry() }",
			"",
			"func FuzzRetry(f *testing.F) { f.Fuzz(func(*testing.T, string) { _ = Retry() }) }",
			"",
			"func BenchmarkRetry(b *testing.B) { _ = Retry() }",
			"",
			"func ExampleRetry() { _ = Retry() }",
		),
		fxIntegration: textLines(
			"//go:build integration",
			"",
			"package integration",
			"",
			`import "testing"`,
			"",
			"type row struct{ tool, key string }",
			"",
			"func TestRead_Deal(t *testing.T) {",
			`	for _, tc := range []row{{tool: "get_deal", key: "deal"}, {key: "person", tool: "get_person"}} {`,
			"		t.Run(tc.key, func(*testing.T) {})",
			"	}",
			"}",
			"",
			"func TestWrite_Retry(t *testing.T) {",
			"	cases := []struct{ name, want string }{",
			`		{"once", "ok"},`,
			`		{"twice", "ok"},`,
			"	}",
			"	for _, tc := range cases {",
			"		t.Run(tc.name, func(*testing.T) {})",
			"	}",
			`	t.Run("gives up", func(*testing.T) {})`,
			"}",
		),
		fxEvals: textLines(
			"package main",
			"",
			"type Task struct{ Name, Prompt string }",
			"",
			"var Tasks = []Task{",
			`	{Name: "find-by-name", Prompt: "Find {org}."},`,
			`	{Prompt: "Win it, then reopen it.", Name: "won-then-reopen"},`,
			"}",
		),
		"internal/pipedrive/testdata/pg.md": textLines("A fixture page citing E-777 and §5, and `internal/gone`."),
		fxServer:                            textLines("package main", "", "func main() {}"),
		fxChangelog: textLines(
			"# Changelog",
			"",
			"## [Unreleased]",
			"",
			"- Retries back off (D-01), as `docs/architecture.md#codemap` says.",
			"",
			"## [1.0.0]",
			"",
			"- Cites D-99, `internal/gone` and the integration test TestGone, true when written.",
			"",
			"[Unreleased]: https://example.com/compare/v1.0.0...HEAD",
			"[1.0.0]: https://example.com/releases/v1.0.0",
		),
		fxArch: textLines(
			"# Architecture",
			"",
			"## Codemap",
			"",
			"- `internal/pipedrive` owns the REST client, in `internal/pipedrive/client.go`.",
			"- `cmd/server` is the binary, and `internal/integration` drives it live.",
			"",
			"## Decisions",
			"",
			"| ID | Decision | Status | Held by |",
			"| --- | --- | --- | --- |",
			fxRow01,
			fxRow02,
			fxRow03,
			"",
			"### D-01 Retries back off",
			"",
			"Pipedrive answers a burst with 429, so a retry waits.",
			"",
			"```markdown",
			"### D-09 An example entry, not a decision",
			"It has no Rejected or Cost line, and cites the integration test TestGone.",
			"```",
			"",
			"Rejected: retrying at once, which meets the same 429.",
			"Cost: a burst takes longer to drain.",
			"",
			"### D-02 Retry forever",
			"",
			"Rejected: giving up, which drops the write.",
			"Cost: a write can wait forever.",
			"",
			"### D-03 Retry three times",
			"",
			"Three tries replace D-02, since a write that waits forever never reports.",
			"",
			"Rejected: retrying forever.",
			"Cost: a fourth 429 fails the call.",
			"",
			"Next ID: D-04",
		),
		fxRunbook: textLines(
			"# Runbook",
			"",
			"## Steps",
			"",
			"See [the codemap](architecture.md#codemap) and [the readme](../README.md).",
			"The retry is in `internal/pipedrive/client.go`, linked as [the client](../internal/pipedrive/client.go).",
			"",
			"## Steps",
			"",
			"Follow the old runbook, in [the server](/cmd/server).",
			"",
			"Back to [the first steps](#steps).",
			"",
			"[map]: architecture.md#codemap",
		),
		fxReadme: textLines(
			"# Fixture",
			"",
			"Read the [decisions](docs/architecture.md#decisions) and [the second steps](docs/runbook.md#steps-1).",
			"Code cites `docs/architecture.md#d-01-retries-back-off`.",
			"A redirect is followed once, as RFC 9110 §15.4 allows.",
			"The [spec](https://example.com/spec#§2) says so too.",
			"",
			"```text",
			"§3, D-99 and [a link](nowhere.md) in a fence are an example.",
			"```",
		),
		fxAgent: textLines(
			"# CLAUDE.md",
			"",
			"Read `docs/architecture.md` and `docs/architecture.md#codemap` first.",
			"The client is in `internal/pipedrive/`, and `go.mod` names the module.",
			"Run `make check`, or `go run ./scripts/gates docs` alone; `scripts/gates smoke` needs a binary.",
			"A made-up id says so: `persons/SyntheticPersonAAAA`. Streams are named in `main`, never in `cmd/*/x.go`.",
		),
	}
}

// docsEdit changes one fixture file: old replaced by new, the whole file
// written when old is empty, or the file removed when both are.
type docsEdit struct{ file, old, new string }

func replace(file, from, to string) docsEdit { return docsEdit{file, from, to} }
func add(file, body string) docsEdit         { return docsEdit{file, "", body} }
func drop(file string) docsEdit              { return docsEdit{file: file} }

// docsEdited is the fixture with the edits applied. An edit whose old
// text is not there fails the test, so a fixture change cannot quietly
// turn a reject row into a copy of the accepted checkout.
func docsEdited(t *testing.T, edits ...docsEdit) map[string]string {
	t.Helper()
	files := docsFixture()
	for _, e := range edits {
		switch body := files[e.file]; {
		case e.old == "" && e.new == "":
			delete(files, e.file)
		case e.old == "":
			files[e.file] = e.new
		case !strings.Contains(body, e.old):
			t.Fatalf("the fixture's %s does not contain %q", e.file, e.old)
		default:
			files[e.file] = strings.Replace(body, e.old, e.new, 1)
		}
	}
	return files
}

// codemapNames adds a line naming pkg to the fixture's codemap, for a
// row that adds a package.
func codemapNames(pkg string) docsEdit {
	return replace(fxArch, "drives it live.\n", "drives it live.\n- `"+pkg+"` is a package.\n")
}

// putAll writes each file's body to its path under root.
func putAll(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		put(t, root, rel, body)
	}
}

// docsFixtureFloors keeps the floors out of the rows whose subject is not a
// floor. The fixture is far smaller than the repository, and a pass that
// reads nothing still fails.
var docsFixtureFloors = docsFloors{links: 1, spanPaths: 1, packages: 1, integration: 1, evalTasks: 1}

// docsRun runs every check over files, listed as git lists them, held to
// floors. Listing a checkout is docsFiles' part, and the tests that need
// git test it.
func docsRun(t *testing.T, files map[string]string, floors docsFloors) *docsCheck {
	t.Helper()
	root := t.TempDir()
	putAll(t, root, files)
	g := newDocsCheck(root, slices.Sorted(maps.Keys(files)), floors)
	g.run()
	return g
}

// docsCase is one checkout and every problem the gate reports for it. A
// nil want means the gate accepts it.
type docsCase struct {
	name  string
	edits []docsEdit
	want  []string
}

// checkDocsCases runs each row against its own checkout, in parallel,
// and compares the whole problem list, so a row also fails on a problem
// it did not expect.
func checkDocsCases(t *testing.T, cases []docsCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkDocs(t, docsFixtureFloors, tc.edits, tc.want)
		})
	}
}

// checkDocs runs the checks, held to floors, over the fixture with edits,
// and wants exactly the problems want, or none when want is nil.
func checkDocs(t *testing.T, floors docsFloors, edits []docsEdit, want []string) {
	t.Helper()
	if got := docsRun(t, docsEdited(t, edits...), floors).problems; !slices.Equal(got, want) {
		t.Errorf("problems\n got %q\nwant %q", got, want)
	}
}

func TestDocsGateAcceptsAConsistentCheckout(t *testing.T) {
	t.Parallel()
	checkDocsCases(t, []docsCase{
		{name: "the fixture as it stands"},
		{"a Go file under testdata is neither parsed nor read for citations", []docsEdit{
			add("internal/pipedrive/testdata/fixture.go", "package fixture\n\n// Cites D-99.\nfunc {\n"),
		}, nil},
		{"footnotes, and a link out of the repository", []docsEdit{
			replace(fxReadme, "says so too.", "says so too.[^spec] See [issue 51](../../issues/51)."),
			replace(fxReadme, "```text", "[^spec]: The spec's own words.\n\n```text"),
		}, nil},
		{"a nested doc's anchor in a Go comment, and nested names that are not references", []docsEdit{
			add("internal/pipedrive/NOTES.md", textLines("# Notes", "", "## Setup")),
			replace(fxClient, "as measured live.", "as measured live; ./internal/pipedrive/NOTES.md#setup says how.\n"+
				"// Each run writes out/summary.md, as https://example.com/x/blob/main/MANIFEST.md says."),
		}, nil},
		{"a doc in a Go comment read from the file's directory, up and out of it", []docsEdit{
			replace(fxClient, "as measured live.", "as measured live; testdata/pg.md, ../../README.md#fixture and ../../../outside.md say so."),
		}, nil},
		{"a doc path inside a URL or a query string in a Go comment", []docsEdit{
			replace(fxClient, "as measured live.", "as measured live, at https://pkg.go.dev/example.com/fixture@v1.0.0/RUNBOOK.md#setup\n"+
				"// and at /render?page=docs/gone.md."),
		}, nil},
		{"a root doc's anchor, and a lowercase file name, in a Go comment", []docsEdit{
			replace(fxClient, "§15.4 allows.", "§15.4 allows, as README.md#fixture says.\n// Each retry is logged to retries.md."),
		}, nil},
		{"a doc path in a string of a test-helper package, which is fixture content", []docsEdit{
			codemapNames("internal/pipedrive/pipedrivetest"),
			add("internal/pipedrive/pipedrivetest/fake.go", textLines(
				"package pipedrivetest",
				"",
				`var files = map[string]string{"docs/guide.md": "# Guide"}`,
			)),
		}, nil},
		{"a bare file name in a Go string, and a doc path in a test's string", []docsEdit{
			replace(fxClient, "D-99 and §4", "D-99, §4 and NOTES.md"),
			replace(fxClientTest, "_ = Retry() }\n\nfunc Fuzz", `_ = Retry() + "docs/gone.md" }`+"\n\nfunc Fuzz"),
		}, nil},
		{"an escaped pipe in an index cell, unescaped to match the entry", []docsEdit{
			replace(fxArch, "| D-03 | Retry three times |", "| D-03 | Retry `a \\| b` times |"),
			replace(fxArch, "### D-03 Retry three times", "### D-03 Retry `a | b` times"),
		}, nil},
		{"a link written in a code span", []docsEdit{
			replace(fxReadme, "says so too.", "says so too. Write `[x](docs/example.md#slug)` for a link."),
		}, nil},
		{"a placeholder or a wildcard where a make target, a gates command or a path goes", []docsEdit{
			replace(fxAgent, "needs a binary.", "needs a binary; `make <target>` and `go run ./scripts/gates <name>` run one,\n"+
				"`make ...` and `scripts/gates ...` run more, and `go test ./internal/gone/...` tests them."),
		}, nil},
		{"a gates command that runs by hand, in no pipeline", []docsEdit{
			replace(fxAgent, "`scripts/gates smoke` needs a binary.", "`scripts/gates smoke` needs a binary and `scripts/gates release-notes` a tag."),
			replace(fxArch, "| forbidigo, lint, review |", "| forbidigo, release-notes, review |"),
		}, nil},
		{"a workflow by its bare name, and an archive's file, which is not a path here", []docsEdit{
			add(".github/workflows/ci.yml", textLines("name: ci")),
			replace(fxAgent, "`go.mod` names the module", "`go.mod` names the module, `ci.yml` checks it and `checksums.txt` lists an archive"),
		}, nil},
		{"a bare file name in a nested doc, read from its directory, then the root, then as a workflow", []docsEdit{
			add(".github/workflows/ci.yml", textLines("name: ci")),
			replace(fxRunbook, "The retry is in", "As `architecture.md#codemap`, `go.mod` and `ci.yml` say, the retry is in"),
		}, nil},
		{"a generated table under a package's testdata", []docsEdit{
			add("internal/pipedrive/testdata/golden.tsv", textLines("method\tnote", "deals.get\tD-99 and E-12")),
		}, nil},
		{"IDs in a YAML value, after a # with no space before it, and in a Makefile recipe", []docsEdit{
			add(".github/workflows/ci.yml", textLines(
				"name: ci",
				"jobs:",
				"  test:",
				"    steps:",
				"      - run: echo D-99 §4 https://example.com/#E-12",
			)),
			replace(fxMakefile, "\tgolangci-lint run", "\tgolangci-lint run\n\t@echo D-99"),
		}, nil},
	})
}

// What a pass reads is in its summary, so "found nothing" and "looked at
// nothing" cannot read the same. A failure counts its problems and prints
// nothing.
func TestDocsGateSaysWhatItRead(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	if err := docsRun(t, docsFixture(), docsFixtureFloors).verdict(&out); err != nil {
		t.Fatalf("the fixture failed: %v", err)
	}
	want := "docs ok: 3 decisions, 3 packages, 2 integration tests, 2 eval tasks, 8 links and 14 code-span paths, in 16 files\n"
	if out.String() != want {
		t.Errorf("printed %q, want %q", out.String(), want)
	}

	out.Reset()
	edited := docsEdited(t, replace(fxClient, "(D-01)", "(D-07)"), replace(fxReadme, "says so too.", "says so too, as D-08 did."))
	err := docsRun(t, edited, docsFixtureFloors).verdict(&out)
	want = "2 problem(s):\n  README.md:6: cites D-08, which is not a decision\n  internal/pipedrive/client.go:3: cites D-07, which is not a decision"
	if err == nil || err.Error() != want || out.Len() != 0 {
		t.Errorf("failed with %v after printing %q, want %q and nothing printed", err, out.String(), want)
	}
}

// The gate reads what git would commit, as it is on disk: an untracked
// doc is read and an ignored file is not. A file deleted in the worktree
// stays in the index until the deletion is staged, and has nothing to
// read.
func TestDocsGateReadsWhatGitWouldCommit(t *testing.T) {
	r := newFakeRepo(t)
	putAll(t, r.root, docsEdited(t,
		add("internal/old/old.go", "package old\n"),
		add("internal/generated/broken.go", "this is not Go\n"),
	))
	r.git("add", "-A")
	if err := os.Remove(filepath.Join(r.root, "internal", "old", "old.go")); err != nil {
		t.Fatal(err)
	}
	put(t, r.root, "docs/new.md", "A new page cites D-07.\n")
	err := docsGateAt(r.root, docsFixtureFloors, io.Discard)
	if want := "1 problem(s):\n  docs/new.md:1: cites D-07, which is not a decision"; err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
}

// Outside a git checkout the gate has no file list. It reached no
// verdict, so it says so and exits 2, as a gate that could not run does.
func TestDocsGateNeedsAGitCheckout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Setenv("LC_ALL", "C") // git's own words, untranslated
	err := docsGateAt(dir, docsFixtureFloors, io.Discard)
	if !errors.As(err, new(couldNotRun)) || exitCode(err) != 2 ||
		!strings.HasPrefix(err.Error(), "could not run: git -C "+dir+" ls-files -z --cached --others --exclude-standard: exit status 128: ") ||
		!strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("got %v; want a git ls-files failure that could not run and says why", err)
	}
}

func TestDocsGateHoldsDecisionIntegrity(t *testing.T) {
	t.Parallel()
	checkDocsCases(t, []docsCase{
		{"an ID in the index twice", []docsEdit{
			replace(fxArch, fxRow03, fxRow03+"\n"+fxRow03),
		}, []string{"docs/architecture.md:15: D-03 is in the index twice"}},
		{"an ID with two entries", []docsEdit{
			replace(fxArch, "Next ID: D-04", "### D-03 Retry three times\n\nRejected: none.\nCost: none.\n\nNext ID: D-04"),
		}, []string{"docs/architecture.md:40: D-03 has two entries"}},
		{"an ID at Next ID", []docsEdit{
			replace(fxArch, "Next ID: D-04", "Next ID: D-03"),
		}, []string{"docs/architecture.md:14: D-03 is not below Next ID D-03"}},
		{"an ID below Next ID that is gone", []docsEdit{
			replace(fxArch, "Next ID: D-04", "Next ID: D-05"),
		}, []string{"docs/architecture.md:40: D-04 is below Next ID D-05 and is missing; an ID is never deleted"}},
		{"no Next ID line", []docsEdit{
			replace(fxArch, "\n\nNext ID: D-04", ""),
		}, []string{`docs/architecture.md:38: the last line is not "Next ID: D-NN"`}},
		{"a status outside the set", []docsEdit{
			replace(fxArch, "| D-01 | Retries back off | accepted |", "| D-01 | Retries back off | proposed |"),
		}, []string{`docs/architecture.md:12: D-01 has status "proposed", want accepted, rejected, deprecated or superseded by D-NN`}},
		{"a superseded status that names no decision ID", []docsEdit{
			replace(fxArch, "| superseded by D-03 |", "| superseded by D-3 |"),
		}, []string{
			`docs/architecture.md:13: D-02 has status "superseded by D-3", want accepted, rejected, deprecated or superseded by D-NN`,
			`docs/architecture.md:13: "D-3" is not written as a decision ID like D-03`,
		}},
		{"a superseded status naming a decision that does not exist", []docsEdit{
			replace(fxArch, "| superseded by D-03 |", "| superseded by D-07 |"),
		}, []string{"docs/architecture.md:13: cites D-07, which is not a decision"}},
		{"an entry titled unlike its index row", []docsEdit{
			replace(fxArch, "| D-01 | Retries back off |", "| D-01 | Retries back off fast |"),
		}, []string{`docs/architecture.md:16: D-01's entry is titled "Retries back off" and its index row "Retries back off fast"`}},
		{"an entry with no Rejected line", []docsEdit{
			replace(fxArch, "Rejected: giving up, which drops the write.\n", ""),
		}, []string{`docs/architecture.md:28: D-02 has no "Rejected:" line`}},
		{"an entry with no Cost line", []docsEdit{
			replace(fxArch, "Cost: a fourth 429 fails the call.\n", ""),
		}, []string{`docs/architecture.md:33: D-03 has no "Cost:" line`}},
		{"an index row with no entry", []docsEdit{
			replace(fxArch, fxRow03, fxRow03+"\n| D-04 | Log every retry | accepted | integration |"),
			replace(fxArch, "Next ID: D-04", "Next ID: D-05"),
		}, []string{"docs/architecture.md:15: D-04 has no entry"}},
		{"an entry with no index row", []docsEdit{
			replace(fxArch, "Next ID: D-04", "### D-04 Log every retry\n\nRejected: none.\nCost: none.\n\nNext ID: D-05"),
		}, []string{"docs/architecture.md:40: D-04 has no index row"}},
		{"an ID with one digit, which the citation scan also reads, reported once", []docsEdit{
			replace(fxArch, "| D-03 | Retry three", "| D-3 | Retry three"),
		}, []string{
			`docs/architecture.md:14: "D-3" is not written as a decision ID like D-03`,
			"docs/architecture.md:33: D-03 has no index row",
		}},
		{"a pipe left unescaped in an index cell, even in a code span", []docsEdit{
			replace(fxArch, "| D-03 | Retry three times |", "| D-03 | Retry `a | b` times |"),
		}, []string{
			"docs/architecture.md:14: an index row has 5 cells, want 4",
			"docs/architecture.md:33: D-03 has no index row",
		}},
		{"an index with a column too many", []docsEdit{
			replace(fxArch, "| Held by |\n| --- | --- | --- | --- |", "| Held by | Links |\n| --- | --- | --- | --- | --- |"),
		}, []string{
			`docs/architecture.md:10: the decision index has the columns ["ID" "Decision" "Status" "Held by" "Links"], ` +
				`want ["ID" "Decision" "Status" "Held by"]`,
			"docs/architecture.md: no decisions in the index: this gate would be looking at nothing",
			"docs/architecture.md:16: D-01 has no index row",
			"docs/architecture.md:28: D-02 has no index row",
			"docs/architecture.md:33: D-03 has no index row",
		}},
		{"an index that is not directly under its heading", []docsEdit{
			replace(fxArch, "## Decisions\n\n|", "## Decisions\n\nThe index.\n\n|"),
		}, []string{
			`docs/architecture.md:8: the decision index is not a table directly under "## Decisions"`,
			"docs/architecture.md: no decisions in the index: this gate would be looking at nothing",
			"docs/architecture.md:18: D-01 has no index row",
			"docs/architecture.md:30: D-02 has no index row",
			"docs/architecture.md:35: D-03 has no index row",
		}},
	})
}

func TestDocsGateHoldsHeldByToWhatExists(t *testing.T) {
	t.Parallel()
	checkDocsCases(t, []docsCase{
		{"a fuzz target, whose seeds go test runs, and an integration test", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| FuzzRetry, TestWrite_Retry, smoke |"),
		}, nil},
		{"live, which names no check", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| live, smoke |"),
		}, []string{`docs/architecture.md:12: D-01 is held by "live", ` + notAHolder}},
		{"a test function that does not exist", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| TestRetryLater, smoke |"),
		}, []string{`docs/architecture.md:12: D-01 is held by "TestRetryLater", ` + notAHolder}},
		{"a benchmark or an example, which go test does not run by name", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| BenchmarkRetry, ExampleRetry, smoke |"),
		}, []string{
			`docs/architecture.md:12: D-01 is held by "BenchmarkRetry", ` + notAHolder,
			`docs/architecture.md:12: D-01 is held by "ExampleRetry", ` + notAHolder,
		}},
		{"a make target beside a quoted variable is the only target", []docsEdit{
			replace(fxAgent, "`make check`", "`make verify LIVE_ARGS=\"-run Word\"`"),
		}, []string{"CLAUDE.md:5: names make verify, which is not a Makefile target"}},
		{"a gates command that does not exist", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| TestRetry, smok |"),
		}, []string{`docs/architecture.md:12: D-01 is held by "smok", ` + notAHolder}},
		{"a linter configured but not enabled", []docsEdit{
			replace(fxArch, "| forbidigo, lint, review |", "| misspell, lint, review |"),
		}, []string{`docs/architecture.md:14: D-03 is held by "misspell", ` + notAHolder}},
		{"a linter list whose keys carry a comment", []docsEdit{
			replace(fxGolangci, "linters:\n  default: none\n  enable:\n", "linters: # the checks\n  default: none\n  enable: # the list\n"),
		}, nil},
		{"a linter enabled only inside another linter's settings", []docsEdit{
			replace(fxArch, "| forbidigo, lint, review |", "| nilness, lint, review |"),
		}, []string{`docs/architecture.md:14: D-03 is held by "nilness", ` + notAHolder}},
		{"an accepted decision held by a dash", []docsEdit{
			replace(fxArch, "| forbidigo, lint, review |", "| — |"),
		}, []string{"docs/architecture.md:14: D-03 is accepted and held by —; an accepted decision names what holds it"}},
		{"a decision held by nothing", []docsEdit{
			replace(fxArch, "| TestRetry, smoke |", "| |"),
		}, []string{"docs/architecture.md:12: D-01 is held by nothing"}},
	})
}

// Evidence has no IDs: what was verified upstream is stated at the line
// that depends on it. An E- citation fails wherever another citation is read.
func TestDocsGateRefusesEvidenceIDs(t *testing.T) {
	t.Parallel()
	const noIDs = "; evidence has no IDs: state the behavior at the line that depends on it, with its source link"
	checkDocsCases(t, []docsCase{
		{"an evidence ID in a Go comment", []docsEdit{
			replace(fxClient, "as measured live.", "as measured in E-001."),
		}, []string{"internal/pipedrive/client.go:3: cites E-001" + noIDs}},
		{"an evidence ID in a doc, at any width", []docsEdit{
			replace(fxArch, "Pipedrive answers a burst with 429, so", "Pipedrive answers a burst with 429 (E-12), so"),
		}, []string{"docs/architecture.md:18: cites E-12" + noIDs}},
		{"an evidence ID in go.mod's pin reason", []docsEdit{
			replace(fxGoMod, "which D-03 rules out", "which E-12 found"),
		}, []string{"go.mod:5: cites E-12" + noIDs}},
		{"an evidence ID in a fenced block", []docsEdit{
			replace(fxReadme, "§3, D-99 and", "§3, D-99, E-001 and"),
		}, nil},
		{"an evidence ID in a string literal", []docsEdit{
			replace(fxClient, "D-99 and §4", "D-99, E-001 and §4"),
		}, nil},
	})
}

// A comment, a doc or go.mod's pin reason names the integration test that
// checks it, and the name has to be a test a file built only with
// -tags=integration declares; a /sub has to be a subtest that test gives
// t.Run, by a literal, by a map's key or by a field of the table its loop
// ranges over.
func TestDocsGateHoldsIntegrationTestsToTheSuite(t *testing.T) {
	t.Parallel()
	cites := "Code cites `docs/architecture.md#d-01-retries-back-off`."
	tables := add("internal/integration/tables_test.go", textLines(
		"//go:build integration",
		"",
		"package integration",
		"",
		`import "testing"`,
		"",
		"func TestByKey(t *testing.T) {",
		"	cases := map[string]struct{ want string }{",
		`		"first key": {want: "a"},`,
		`		"second":    {"b"},`,
		"	}",
		"	for name, tc := range cases {",
		"		t.Run(name, func(*testing.T) { _ = tc })",
		"	}",
		"}",
		"",
		"func TestByValue(t *testing.T) {",
		`	for _, tc := range map[string]struct{ key string }{"a": {key: "valued"}} {`,
		"		t.Run(tc.key, func(*testing.T) {})",
		"	}",
		"}",
		"",
		"func TestTwoTables(t *testing.T) {",
		`	unrelated := []struct{ name string }{{"ghost"}}`,
		"	_ = unrelated",
		`	cases := []struct{ name string }{{"real"}}`,
		"	for _, tc := range cases {",
		"		t.Run(tc.name, func(*testing.T) {})",
		"	}",
		"}",
	))
	checkDocsCases(t, []docsCase{
		{"a map's keys and a field of its values, and the table a loop ranges over", []docsEdit{
			tables,
			replace(fxReadme, cites, cites+" The integration tests `TestByKey/first_key`, `TestByKey/second`, "+
				"`TestByValue/valued` and `TestTwoTables/real` check it."),
		}, nil},
		{"a key the map does not have", []docsEdit{
			tables,
			replace(fxReadme, cites, cites+" The integration test `TestByKey/third` checks it."),
		}, []string{`README.md:4: names the integration test "TestByKey/third", and TestByKey runs no subtest named "third"`}},
		{"a row of a table the loop does not range over", []docsEdit{
			tables,
			replace(fxReadme, cites, cites+" The integration test `TestTwoTables/ghost` checks it."),
		}, []string{`README.md:4: names the integration test "TestTwoTables/ghost", and TestTwoTables runs no subtest named "ghost"`}},
		{"a test that does not exist, in a Go comment", []docsEdit{
			replace(fxClient, "// TestWrite_Retry/gives_up.", "// TestWrite_Retries/gives_up."),
		}, []string{`internal/pipedrive/client.go:9: names the integration test "TestWrite_Retries/gives_up"` + notTagged}},
		{"a subtest no table row names", []docsEdit{
			replace(fxClient, "TestRead_Deal/deal and", "TestRead_Deal/lead and"),
		}, []string{`internal/pipedrive/client.go:8: names the integration test "TestRead_Deal/lead", ` +
			`and TestRead_Deal runs no subtest named "lead"`}},
		{"a bare /sub, read against the test before it", []docsEdit{
			replace(fxClient, "and /person,", "and /organization,"),
		}, []string{`internal/pipedrive/client.go:8: names the integration test "TestRead_Deal/organization", ` +
			`and TestRead_Deal runs no subtest named "organization"`}},
		{"a subtest cited with the space t.Run was given, not the underscore go test prints", []docsEdit{
			replace(fxClient, "TestWrite_Retry/gives_up.", "TestWrite_Retry/gives up."),
		}, []string{`internal/pipedrive/client.go:9: names the integration test "TestWrite_Retry/gives", ` +
			`and TestWrite_Retry runs no subtest named "gives"`}},
		{"the second name of a list, in code quotes", []docsEdit{
			replace(fxClient, "or `TestRead_Deal`;", "or `TestRead_Deals`;"),
		}, []string{`internal/pipedrive/client.go:13: names the integration test "TestRead_Deals"` + notTagged}},
		{"a unit test, which no integration run reaches", []docsEdit{
			replace(fxClient, "or `TestRead_Deal`;", "or `TestRetry`;"),
		}, []string{`internal/pipedrive/client.go:13: names the integration test "TestRetry"` + notTagged}},
		{"a test in a file of the suite with no build tag", []docsEdit{
			add("internal/integration/plain_test.go", textLines(
				"package integration",
				"",
				`import "testing"`,
				"",
				"func TestPlain(*testing.T) {}",
			)),
			replace(fxClient, "or `TestRead_Deal`;", "or TestPlain;"),
		}, []string{`internal/pipedrive/client.go:13: names the integration test "TestPlain"` + notTagged}},
		{"a test in a file built with or without the tag", []docsEdit{
			add("internal/integration/either_test.go", textLines(
				"//go:build integration || !integration",
				"",
				"package integration",
				"",
				`import "testing"`,
				"",
				"func TestEither(*testing.T) {}",
			)),
			replace(fxClient, "or `TestRead_Deal`;", "or TestEither;"),
		}, []string{`internal/pipedrive/client.go:13: names the integration test "TestEither"` + notTagged}},
		{"a Markdown paragraph, across a line break", []docsEdit{
			replace(fxReadme, cites, cites+" The integration\ntest TestWrite_Retry/thrice checks it."),
		}, []string{`README.md:5: names the integration test "TestWrite_Retry/thrice", and TestWrite_Retry runs no subtest named "thrice"`}},
		{"a Markdown paragraph naming tests that exist", []docsEdit{
			replace(fxReadme, cites, cites+" The integration tests `TestWrite_Retry/once` and `TestRead_Deal/person` check it."),
		}, nil},
		{"go.mod's pin reason", []docsEdit{
			replace(fxGoMod, "TestWrite_Retry/twice shows", "TestWrite_Retry/twise shows"),
		}, []string{`go.mod:5: names the integration test "TestWrite_Retry/twise", and TestWrite_Retry runs no subtest named "twise"`}},
		{"a config comment", []docsEdit{
			add(".github/workflows/ci.yml", textLines("name: ci", "# Runs no integration test, not even", "# the integration test TestGone.")),
		}, []string{`.github/workflows/ci.yml:3: names the integration test "TestGone"` + notTagged}},
		{"placeholders showing the form", []docsEdit{
			replace(fxReadme, cites, cites+" Cite it as `Checked by the integration test TestX/sub.`,\n"+
				"or the integration tests TestX and Test<Name>/<sub>."),
		}, nil},
		{"a released changelog section and a fence, which are not read", []docsEdit{
			replace(fxReadme, "§3, D-99 and", "§3, D-99, the integration test TestGone and"),
		}, nil},
	})
}

// A comment or a doc names the eval task that checks it, and the name has
// to be a row of the eval task table.
func TestDocsGateHoldsEvalTasksToTheTable(t *testing.T) {
	t.Parallel()
	cites := "Code cites `docs/architecture.md#d-01-retries-back-off`."
	checkDocsCases(t, []docsCase{
		{"a task the table does not list, in a Go comment", []docsEdit{
			replace(fxClient, `the eval task "find-by-name".`, `the eval task "find-by-names".`),
		}, []string{`internal/pipedrive/client.go:17: names the eval task "find-by-names"` + notListed}},
		{"the second name of a list, wrapped and in code quotes", []docsEdit{
			replace(fxClient, "// `won-then-reopen`, and", "// `won-and-reopen`, and"),
		}, []string{`internal/pipedrive/client.go:17: names the eval task "won-and-reopen"` + notListed}},
		{"a Markdown paragraph", []docsEdit{
			replace(fxReadme, cites, cites+" The eval task \"read-a-stage\" checks it."),
		}, []string{`README.md:4: names the eval task "read-a-stage"` + notListed}},
		{"a Markdown paragraph, in code quotes across a line break", []docsEdit{
			replace(fxReadme, cites, cites+" The eval task\n`read-a-stage` checks it."),
		}, []string{`README.md:5: names the eval task "read-a-stage"` + notListed}},
		{"a placeholder showing the form", []docsEdit{
			replace(fxReadme, cites, cites+" Cite it as the eval task \"<name>\"."),
		}, nil},
	})
}

func TestDocsGateResolvesCitations(t *testing.T) {
	t.Parallel()
	checkDocsCases(t, []docsCase{
		{"a superseded decision cited from a Go comment", []docsEdit{
			replace(fxClient, "(D-01)", "(D-02)"),
		}, []string{"internal/pipedrive/client.go:3: cites D-02, which is superseded by D-03"}},
		{"a deprecated decision cited from a doc", []docsEdit{
			replace(fxArch, fxRow03, fxRow03+"\n| D-04 | Log every retry | deprecated | — |"),
			replace(fxArch, "Next ID: D-04", "### D-04 Log every retry\n\nRejected: none.\nCost: none.\n\nNext ID: D-05"),
			replace(fxReadme, "#d-01-retries-back-off`.", "#d-01-retries-back-off`. Logging was D-04."),
		}, []string{"README.md:4: cites D-04, which is deprecated"}},
		{"an unknown decision cited twice on a line, reported once", []docsEdit{
			replace(fxClient, "(D-01)", "(D-07, and D-07 again)"),
		}, []string{"internal/pipedrive/client.go:3: cites D-07, which is not a decision"}},
		{"an unknown decision, and a section number, in go.mod's pin reason", []docsEdit{
			replace(fxGoMod, "which D-03 rules out", "which D-05 rules out, as §4 says"),
		}, []string{
			"go.mod:5: cites D-05, which is not a decision",
			"go.mod:5: cites a section by number (§4); " + bySlug,
		}},
		{"a link slug no heading has", []docsEdit{
			replace(fxReadme, "docs/architecture.md#decisions", "docs/architecture.md#decision"),
		}, []string{"README.md:3: links to docs/architecture.md#decision, and docs/architecture.md has no heading with that slug"}},
		{"a code span slug no heading has", []docsEdit{
			replace(fxReadme, "#d-01-retries-back-off`", "#d-01-retry`"),
		}, []string{"README.md:4: names docs/architecture.md#d-01-retry, and docs/architecture.md has no heading with that slug"}},
		{"a repeated heading's suffix past the last repeat", []docsEdit{
			replace(fxReadme, "#steps-1", "#steps-2"),
		}, []string{"README.md:3: links to docs/runbook.md#steps-2, and docs/runbook.md has no heading with that slug"}},
		{"a same-file anchor no heading has", []docsEdit{
			replace(fxRunbook, "(#steps)", "(#step)"),
		}, []string{"docs/runbook.md:12: links to docs/runbook.md#step, and docs/runbook.md has no heading with that slug"}},
		{"a reference definition no heading answers", []docsEdit{
			replace(fxRunbook, "[map]: architecture.md#codemap", "[map]: architecture.md#codemaps"),
		}, []string{"docs/runbook.md:14: links to docs/architecture.md#codemaps, and docs/architecture.md has no heading with that slug"}},
		{"a link to a doc that does not exist", []docsEdit{
			replace(fxReadme, "says so too.", "says so too, in [the guide](docs/gone.md)."),
		}, []string{"README.md:6: links to docs/gone.md, which does not exist"}},
		{"a link written from the root instead of the linking file", []docsEdit{
			replace(fxRunbook, "](architecture.md#codemap)", "](docs/architecture.md#codemap)"),
		}, []string{"docs/runbook.md:5: links to docs/docs/architecture.md, which does not exist"}},
		{"a docs citation in a Go comment to a file that does not exist", []docsEdit{
			replace(fxClient, "as measured live.", "as measured live and in docs/retries.md#backoff."),
		}, []string{"internal/pipedrive/client.go:3: cites docs/retries.md, which does not exist"}},
		{"a bare section number in a Go comment", []docsEdit{
			replace(fxClient, "as RFC 9110 §15.4 allows", "as §15.4 allows"),
		}, []string{"internal/pipedrive/client.go:4: cites a section by number (§15); " + bySlug}},
		{"a section number not right after an RFC or inside a URL on its line", []docsEdit{
			replace(fxClient, "RFC 9110 §15.4 allows", "RFC 9110 allows and §15.4 says"),
			replace(fxReadme, "RFC 9110 §15.4", "RFC 9110  §15.4"),
			replace(fxReadme, "spec#§2) says", "spec) §2 says"),
		}, []string{
			"README.md:5: cites a section by number (§15); " + bySlug,
			"README.md:6: cites a section by number (§2); " + bySlug,
			"internal/pipedrive/client.go:4: cites a section by number (§15); " + bySlug,
		}},
		{"a bare section number in the changelog's Unreleased section", []docsEdit{
			replace(fxChangelog, "#codemap` says.", "#codemap` and §3 say."),
		}, []string{"CHANGELOG.md:5: cites a section by number (§3); " + bySlug}},
		{"a bare section number in a released changelog section", []docsEdit{
			replace(fxChangelog, "Cites D-99,", "Cites D-99, §3,"),
		}, nil},
		{"IDs written at the wrong width, in a Go comment and a doc", []docsEdit{
			replace(fxClient, "(D-01)", "(D-1)"),
			replace(fxReadme, "says so too.", "says so too, as D-001 did."),
		}, []string{
			`README.md:6: "D-001" is not written as a decision ID like D-01`,
			`internal/pipedrive/client.go:3: "D-1" is not written as a decision ID like D-01`,
		}},
		{"a one-line code span in fence backticks opens no fence", []docsEdit{
			replace(fxReadme, "says so too.\n", "says so too.\n```go``` is a span, not a fence.\nAs D-07 said.\n"),
		}, []string{"README.md:8: cites D-07, which is not a decision"}},
		{"a nested doc in a Go comment that does not exist, or has no such heading", []docsEdit{
			add("internal/pipedrive/NOTES.md", textLines("# Notes", "", "## Setup")),
			replace(fxClient, "as measured live.", "as measured live, packaging/README.md and internal/pipedrive/NOTES.md#teardown."),
		}, []string{
			"internal/pipedrive/client.go:3: cites packaging/README.md, which does not exist",
			"internal/pipedrive/client.go:3: cites internal/pipedrive/NOTES.md#teardown, and internal/pipedrive/NOTES.md has no heading with that slug",
		}},
		{"a doc in a Go comment missing from the file's directory, or up from it", []docsEdit{
			replace(fxClient, "as measured live.", "as measured live, testdata/gone.md and ../gone.md."),
		}, []string{
			"internal/pipedrive/client.go:3: cites internal/pipedrive/testdata/gone.md, which does not exist",
			"internal/pipedrive/client.go:3: cites internal/gone.md, which does not exist",
		}},
		{"a root doc in a Go comment that does not exist, or has no such heading", []docsEdit{
			replace(fxClient, "as measured live.", "as measured live, RUNBOOK.md and README.md#retries."),
		}, []string{
			"internal/pipedrive/client.go:3: cites RUNBOOK.md, which does not exist",
			"internal/pipedrive/client.go:3: cites README.md#retries, and README.md has no heading with that slug",
		}},
		{"doc paths in a Go string that do not resolve", []docsEdit{
			replace(fxClient, "in a string are data", "in a string are data; docs/setup.md and README.md#setup are not"),
		}, []string{
			"internal/pipedrive/client.go:5: names docs/setup.md, which does not exist",
			"internal/pipedrive/client.go:5: names README.md#setup, and README.md has no heading with that slug",
		}},
		{"a doc path in an ordinary package's string, and in a test-helper package's comment", []docsEdit{
			codemapNames("internal/pipedrive/pipedriveutil"),
			codemapNames("internal/pipedrive/pipedrivetest"),
			add("internal/pipedrive/pipedriveutil/fake.go", textLines(
				"package pipedriveutil",
				"",
				`var files = map[string]string{"docs/guide.md": "# Guide"}`,
			)),
			add("internal/pipedrive/pipedrivetest/fake.go", textLines(
				"package pipedrivetest",
				"",
				"// The fake's files, as docs/fake.md lists them.",
				`var files = map[string]string{"docs/guide.md": "# Guide"}`,
			)),
		}, []string{
			"internal/pipedrive/pipedrivetest/fake.go:3: cites docs/fake.md, which does not exist",
			"internal/pipedrive/pipedriveutil/fake.go:3: names docs/guide.md, which does not exist",
		}},
		{"a comment line in each kind of config file", []docsEdit{
			replace(fxMakefile, "(D-03)", "(D-3)"),
			replace(fxGitignore, "#steps says", "#stairs says"),
			add(".github/workflows/ci.yml", textLines(
				"name: ci",
				"jobs:",
				"  test:",
				"    steps:",
				"      # Pinned, as E-12 found.",
				"      - run: go test ./...",
			)),
			add(".goreleaser.yaml", textLines("version: 2", "# Archives follow §3.")),
		}, []string{
			".github/workflows/ci.yml:5: cites E-12; evidence has no IDs: state the behavior at the line that depends on it, with its source link",
			".gitignore:2: cites docs/runbook.md#stairs, and docs/runbook.md has no heading with that slug",
			".goreleaser.yaml:2: cites a section by number (§3); " + bySlug,
			`Makefile:1: "D-3" is not written as a decision ID like D-03`,
		}},
		{"a trailing comment on a YAML line", []docsEdit{
			replace(fxGolangci, "as D-01 says", "as D-07 says"),
		}, []string{".golangci.yml:12: cites D-07, which is not a decision"}},
	})
}

func TestDocsGateHoldsPathsToTheRepository(t *testing.T) {
	t.Parallel()
	checkDocsCases(t, []docsCase{
		{"a path in CLAUDE.md that does not exist", []docsEdit{
			replace(fxAgent, "`internal/pipedrive/`", "`internal/api/`"),
		}, []string{"CLAUDE.md:4: names internal/api/, which does not exist"}},
		{"a path in another doc that does not exist", []docsEdit{
			replace(fxRunbook, "`internal/pipedrive/client.go`", "`internal/pipedrive/retry.go`"),
		}, []string{"docs/runbook.md:6: names internal/pipedrive/retry.go, which does not exist"}},
		{"a package in the codemap that does not exist", []docsEdit{
			replace(fxArch, "drives it live.", "drives it live.\n- `internal/gone` was removed."),
		}, []string{"docs/architecture.md:7: names internal/gone, which does not exist"}},
		{"a link to a file that does not exist", []docsEdit{
			replace(fxRunbook, "(../internal/pipedrive/client.go)", "(../internal/pipedrive/retry.go)"),
		}, []string{"docs/runbook.md:6: links to internal/pipedrive/retry.go, which does not exist"}},
		{"a link from the root to a directory that does not exist", []docsEdit{
			replace(fxRunbook, "(/cmd/server)", "(/cmd/client)"),
		}, []string{"docs/runbook.md:10: links to cmd/client, which does not exist"}},
		{"a file:line pointer, with punctuation after it", []docsEdit{
			replace(fxRunbook, "`internal/pipedrive/client.go`", "`(see internal/pipedrive/client.go:3:5),` and `internal/pipedrive/client.go:3`"),
		}, nil},
		{"a file:line pointer to a file that does not exist", []docsEdit{
			replace(fxRunbook, "`internal/pipedrive/client.go`", "`internal/pipedrive/retry.go:12`"),
		}, []string{"docs/runbook.md:6: names internal/pipedrive/retry.go:12, which does not exist"}},
		{"a Go name, plain or qualified, in a package that exists", []docsEdit{
			replace(fxRunbook, "`internal/pipedrive/client.go`", "`internal/pipedrive.Retry` and `internal/pipedrive.Client.Do`"),
		}, nil},
		{"a missing file whose name less its extension is a directory", []docsEdit{
			codemapNames("internal/tools/deals"),
			add("internal/tools/deals/main.go", "package main\n"),
			replace(fxRunbook, "`internal/pipedrive/client.go`", "`internal/tools/deals.go` and `scripts/gates.sh`"),
		}, []string{
			"docs/runbook.md:6: names internal/tools/deals.go, which does not exist",
			"docs/runbook.md:6: names scripts/gates.sh, which does not exist",
		}},
		{"a Go name in a package that does not exist", []docsEdit{
			replace(fxRunbook, "`internal/pipedrive/client.go`", "`internal/api.Retry`"),
		}, []string{"docs/runbook.md:6: names internal/api.Retry, which does not exist"}},
		{"a root file and a source directory that are gone, recognized by their shape", []docsEdit{
			replace(fxAgent, "`go.mod` names the module", "`go.mod` names the module, `.goreleaser.yaml` the archives and `packaging/mcpb/manifest.json` the bundle"),
		}, []string{
			"CLAUDE.md:4: names .goreleaser.yaml, which does not exist",
			"CLAUDE.md:4: names packaging/mcpb/manifest.json, which does not exist",
		}},
		{"a bare workflow name no workflow has", []docsEdit{
			replace(fxAgent, "`go.mod` names the module", "`go.mod` names the module, and `release.yml` releases it"),
		}, []string{"CLAUDE.md:4: names release.yml, which does not exist"}},
		{"a bare file name in a nested doc, with no such heading in its directory, or found nowhere", []docsEdit{
			replace(fxRunbook, "The retry is in", "As `architecture.md#codemaps` and `gone.md` say, the retry is in"),
		}, []string{
			"docs/runbook.md:6: names docs/architecture.md#codemaps, and docs/architecture.md has no heading with that slug",
			"docs/runbook.md:6: names gone.md, which does not exist",
		}},
		{"a file named as a directory", []docsEdit{
			replace(fxAgent, "`go.mod`", "`go.mod/`"),
		}, []string{"CLAUDE.md:4: names go.mod/, which is not a directory"}},
		{"a make target that does not exist", []docsEdit{
			replace(fxAgent, "`make check`", "`make verify`"),
		}, []string{"CLAUDE.md:5: names make verify, which is not a Makefile target"}},
		{"a gates command that does not exist", []docsEdit{
			replace(fxAgent, "`scripts/gates smoke`", "`scripts/gates smok`"),
		}, []string{"CLAUDE.md:5: names scripts/gates smok, which is not a gates command"}},
		{"no CLAUDE.md", []docsEdit{
			drop(fxAgent),
		}, []string{"CLAUDE.md is missing"}},
	})
}

// A gate that read nothing passes everything, so each input it depends on
// reading has a floor, and each row here sets one off.
func TestDocsGateRefusesToReadNothing(t *testing.T) {
	t.Parallel()
	checkDocsCases(t, []docsCase{
		{"no Go files", []docsEdit{
			drop(fxClient), drop(fxClientTest), drop(fxIntegration), drop(fxServer), drop(fxEvals), drop(fxGates),
		}, []string{
			"no .go files read: this gate would be looking at nothing",
			"0 integration tests read, fewer than 1: the files built with -tags=integration are not being read",
			"0 eval tasks read from scripts/evals/tasks.go, fewer than 1: the task table is not being read",
			`docs/architecture.md:12: D-01 is held by "TestRetry", ` + notAHolder,
			"CLAUDE.md:5: names ./scripts/gates, which does not exist",
			"CLAUDE.md:5: names scripts/gates, which does not exist",
			"docs/architecture.md:5: names internal/pipedrive/client.go, which does not exist",
			"docs/architecture.md:6: names cmd/server, which does not exist",
			"docs/architecture.md:6: names internal/integration, which does not exist",
			"docs/runbook.md:6: links to internal/pipedrive/client.go, which does not exist",
			"docs/runbook.md:6: names internal/pipedrive/client.go, which does not exist",
			"docs/runbook.md:10: links to cmd/server, which does not exist",
			`go.mod:5: names the integration test "TestWrite_Retry/twice"` + notTagged,
			"0 package directories under internal/ and cmd/, fewer than 1: the tree is not being read",
		}},
		{"an integration suite with no build tag", []docsEdit{
			replace(fxIntegration, "//go:build integration\n\n", ""),
		}, []string{
			"0 integration tests read, fewer than 1: the files built with -tags=integration are not being read",
			`go.mod:5: names the integration test "TestWrite_Retry/twice"` + notTagged,
			`internal/pipedrive/client.go:8: names the integration test "TestRead_Deal/deal"` + notTagged,
			`internal/pipedrive/client.go:8: names the integration test "TestRead_Deal/person"` + notTagged,
			`internal/pipedrive/client.go:9: names the integration test "TestWrite_Retry/gives_up"` + notTagged,
			`internal/pipedrive/client.go:12: names the integration test "TestWrite_Retry/once"` + notTagged,
			`internal/pipedrive/client.go:13: names the integration test "TestWrite_Retry/twice"` + notTagged,
			`internal/pipedrive/client.go:13: names the integration test "TestRead_Deal"` + notTagged,
		}},
		{"an eval table under another name", []docsEdit{
			replace(fxEvals, "var Tasks =", "var tasks ="),
		}, []string{
			"0 eval tasks read from scripts/evals/tasks.go, fewer than 1: the task table is not being read",
			`internal/pipedrive/client.go:16: names the eval task "find-by-name"` + notListed,
			`internal/pipedrive/client.go:17: names the eval task "won-then-reopen"` + notListed,
			`internal/pipedrive/client.go:17: names the eval task "find-by-name"` + notListed,
		}},
		{"an index with no rows", []docsEdit{
			replace(fxArch, fxRow01+"\n"+fxRow02+"\n"+fxRow03+"\n", ""),
		}, []string{
			"docs/architecture.md: no decisions in the index: this gate would be looking at nothing",
			"docs/architecture.md:13: D-01 has no index row",
			"docs/architecture.md:25: D-02 has no index row",
			"docs/architecture.md:30: D-03 has no index row",
		}},
		{"a config that enables no linter", []docsEdit{
			replace(fxGolangci, "  enable:\n    - errcheck\n    - forbidigo # stdout carries only frames\n", ""),
		}, []string{
			".golangci.yml: no enabled linters read: a Held by naming one would never match",
			`docs/architecture.md:14: D-03 is held by "forbidigo", ` + notAHolder,
		}},
		// The reader takes a block list only. A flow list is read as none,
		// and the floor says so rather than passing over it.
		{"linters listed in flow style", []docsEdit{
			replace(fxGolangci, "  enable:\n    - errcheck\n    - forbidigo # stdout carries only frames\n", "  enable: [errcheck, forbidigo]\n"),
		}, []string{
			".golangci.yml: no enabled linters read: a Held by naming one would never match",
			`docs/architecture.md:14: D-03 is held by "forbidigo", ` + notAHolder,
		}},
		{"a Makefile with no rules", []docsEdit{
			add(fxMakefile, "GO ?= go\n"),
		}, []string{
			"Makefile: no targets read: a Held by naming one would never match",
			`docs/architecture.md:14: D-03 is held by "lint", ` + notAHolder,
			"CLAUDE.md:5: names make check, which is not a Makefile target",
		}},
		{"no architecture document", []docsEdit{
			drop(fxArch),
		}, []string{
			"docs/architecture.md is missing",
			"CHANGELOG.md:5: names docs/architecture.md#codemap, which does not exist",
			"CLAUDE.md:3: names docs/architecture.md, which does not exist",
			"CLAUDE.md:3: names docs/architecture.md#codemap, which does not exist",
			"README.md:3: links to docs/architecture.md, which does not exist",
			"README.md:4: names docs/architecture.md#d-01-retries-back-off, which does not exist",
			"docs/runbook.md:5: links to docs/architecture.md, which does not exist",
			"docs/runbook.md:14: links to docs/architecture.md, which does not exist",
		}},
		{"a Go file that does not parse", []docsEdit{
			add("internal/pipedrive/broken.go", "package pipedrive\n\nfunc {\n"),
		}, []string{"gates cannot parse internal/pipedrive/broken.go: internal/pipedrive/broken.go:3:6: expected 'IDENT', found '{'"}},
	})
}

// Each pass has a floor of its own, so one that keeps reading cannot make
// up for another that stops. Each row raises one floor one past what the
// fixture holds.
func TestDocsGateHoldsEachPassToItsFloor(t *testing.T) {
	t.Parallel()
	atFloor := docsFloors{links: 8, spanPaths: 14, packages: 3, integration: 2, evalTasks: 2}
	for _, tc := range []struct {
		name  string
		raise func(*docsFloors)
		want  []string
	}{
		{"each pass at its floor", func(*docsFloors) {}, nil},
		{
			"links one under their floor", func(f *docsFloors) { f.links++ },
			[]string{"8 Markdown links checked, fewer than 9: the links are not being read"},
		},
		{
			"code-span paths one under their floor", func(f *docsFloors) { f.spanPaths++ },
			[]string{"14 code-span paths checked, fewer than 15: the code spans are not being read"},
		},
		{
			"packages one under their floor", func(f *docsFloors) { f.packages++ },
			[]string{"3 package directories under internal/ and cmd/, fewer than 4: the tree is not being read"},
		},
		{
			"integration tests one under their floor", func(f *docsFloors) { f.integration++ },
			[]string{"2 integration tests read, fewer than 3: the files built with -tags=integration are not being read"},
		},
		{
			"eval tasks one under their floor", func(f *docsFloors) { f.evalTasks++ },
			[]string{"2 eval tasks read from scripts/evals/tasks.go, fewer than 3: the task table is not being read"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			floors := atFloor
			tc.raise(&floors)
			checkDocs(t, floors, nil, tc.want)
		})
	}
}

// The codemap is held to the tree: every package directory under
// internal/ and cmd/, nested ones included, is named in a code span in
// the "## Codemap" section, and every directory it names holds Go.
func TestDocsGateHoldsTheCodemapToTheTree(t *testing.T) {
	t.Parallel()
	const redact = "internal/redact/redact.go"
	checkDocsCases(t, []docsCase{
		{"a package named under a subheading of the codemap", []docsEdit{
			add(redact, "package redact\n"),
			replace(fxArch, "drives it live.\n", "drives it live.\n\n### The logs\n\n`internal/redact` keeps the payload out.\n"),
		}, nil},
		{"a package the codemap leaves out", []docsEdit{
			add(redact, "package redact\n"),
		}, []string{`docs/architecture.md:3: "## Codemap" does not name internal/redact`}},
		{"a nested package the codemap leaves out", []docsEdit{
			add("internal/pipedrive/pipedrivetest/server.go", "package pipedrivetest\n"),
		}, []string{`docs/architecture.md:3: "## Codemap" does not name internal/pipedrive/pipedrivetest`}},
		{"a package named only by a file in it", []docsEdit{
			add(redact, "package redact\n"),
			codemapNames(redact),
		}, []string{`docs/architecture.md:3: "## Codemap" does not name internal/redact`}},
		{"a package named only in a fenced example", []docsEdit{
			add(redact, "package redact\n"),
			replace(fxArch, "drives it live.\n", "drives it live.\n\n```markdown\n- `internal/redact` keeps the payload out.\n```\n"),
		}, []string{`docs/architecture.md:3: "## Codemap" does not name internal/redact`}},
		{"a package named only after the section ends", []docsEdit{
			add(redact, "package redact\n"),
			replace(fxArch, "so a retry waits.", "so a retry waits, logged by `internal/redact`."),
		}, []string{`docs/architecture.md:3: "## Codemap" does not name internal/redact`}},
		{"a directory the codemap names that holds no .go file", []docsEdit{
			add("internal/assets/logo.txt", "a logo\n"),
			codemapNames("internal/assets"),
		}, []string{`docs/architecture.md:7: "## Codemap" names internal/assets, which holds no .go file`}},
		{"no codemap heading", []docsEdit{
			replace(fxArch, "## Codemap", "## Packages"),
		}, []string{
			"CHANGELOG.md:5: names docs/architecture.md#codemap, and docs/architecture.md has no heading with that slug",
			"CLAUDE.md:3: names docs/architecture.md#codemap, and docs/architecture.md has no heading with that slug",
			"docs/runbook.md:5: links to docs/architecture.md#codemap, and docs/architecture.md has no heading with that slug",
			"docs/runbook.md:14: links to docs/architecture.md#codemap, and docs/architecture.md has no heading with that slug",
			`docs/architecture.md: no "## Codemap" heading`,
		}},
	})
}

// A package directory holds a .go file of its own under internal/ or
// cmd/, at any depth, and outside testdata.
func TestDocsPackageDirs(t *testing.T) {
	files := []string{
		"cmd/pipedrive-mcp/main.go",
		"cmd/pipedrive-mcp/main_test.go",
		"internal/doc.go",
		"internal/client/client.go",
		"internal/client/clienttest/server.go",
		"internal/client/testdata/fixture.go",
		"internal/render/testdata/golden/card.txt",
		"internal/assets/logo.txt",
		"scripts/gates/main.go",
		"README.md",
	}
	want := []string{"cmd/pipedrive-mcp", "internal/client", "internal/client/clienttest"}
	if got := docsPackageDirs(files); !slices.Equal(got, want) {
		t.Errorf("docsPackageDirs = %q, want %q", got, want)
	}
}

// These slugs are what GitHub renders for the same headings.
func TestGitHubSlug(t *testing.T) {
	for _, tc := range []struct{ heading, want string }{
		{"Codemap", "codemap"},
		{"D-01 Stdout carries only MCP JSON-RPC frames", "d-01-stdout-carries-only-mcp-json-rpc-frames"},
		{"What a write may reach", "what-a-write-may-reach"},
		{"The `register` function", "the-register-function"},
		{"C# and F#?", "c-and-f"},
		{"snake_case stays", "snake_case-stays"},
		{"Two  spaces", "two--spaces"},
		{"Straße über alles", "straße-über-alles"},
		{"A [link](https://example.com) here", "a-link-here"},
	} {
		if got := docsGitHubSlug(tc.heading); got != tc.want {
			t.Errorf("docsGitHubSlug(%q) = %q, want %q", tc.heading, got, tc.want)
		}
	}
}

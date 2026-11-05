package main

import (
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The gate itself, run against this repository. It is what `make parity`
// runs, so a failure here is the failure a maintainer sees.
func TestParityGate(t *testing.T) {
	if err := parity(io.Discard, nil); err != nil {
		t.Errorf("parity gate: %v", err)
	}
}

// A Makefile and a workflow that agree, in this repository's shape: some
// prerequisites run a gate, some a pinned tool through `go run`, some a
// plain go command, and CI runs some through make and two through an
// action. Every case below is this pair with one thing moved, because
// each divergence the gate catches is one edit away from correct.
const (
	parityMakefile = `# Common dev tasks.
GO ?= go
GOLANGCI_LINT_VERSION ?= v2.14.0
GOVULNCHECK_VERSION ?= v1.8.0
GITLEAKS_VERSION ?= v8.30.1

.PHONY: fmt
fmt:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

.PHONY: vet
vet:
	$(GO) vet ./...
	$(GO) vet -tags=integration ./...

.PHONY: lint
lint:
	$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run

.PHONY: test
test:
	$(GO) test -race -coverprofile cov.out ./...

.PHONY: cover
cover: test
	@$(GO) run ./scripts/gates coverage cov.out

.PHONY: coverage
coverage: test
	$(GO) tool cover -html=cov.out

.PHONY: vuln
vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

.PHONY: licenses
licenses:
	$(GO) run github.com/google/go-licenses@v1.6.0 check ./... \
	  --allowed_licenses=Apache-2.0,MIT

.PHONY: gitleaks
gitleaks:
	$(GO) run github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION) dir . --redact --no-banner

.PHONY: staleness
staleness:
	go run ./scripts/gates deps

leaks:
	go run ./scripts/gates leaks

pins:
	go run ./scripts/gates pins

parity:
	@$(GO) run ./scripts/gates parity

schema-ack:
	go run ./scripts/gates schema-ack origin/main HEAD

.PHONY: check
check: fmt vet lint test cover vuln licenses gitleaks staleness leaks pins parity ## Every gate CI runs
`
	parityCI = `name: CI
jobs:
  test:
    steps:
      - name: Verify gofmt
        run: make fmt
      - run: go vet ./...
      - run: go vet -tags=integration ./...
      - run: go test -race -shuffle=on -coverprofile=cov.out ./...
      - name: Coverage floor
        run: go run ./scripts/gates coverage cov.out
      - uses: golangci/golangci-lint-action@0000000000000000000000000000000000000000 # v9
      - run: make vuln
      - run: make licenses
      - uses: gitleaks/gitleaks-action@0000000000000000000000000000000000000000 # v3
      - run: go run ./scripts/gates deps
      - run: go run ./scripts/gates leaks
      - run: go run ./scripts/gates pins
      - run: go run ./scripts/gates parity
  pr:
    steps:
      - run: go run ./scripts/gates schema-ack "$PR_BASE_REF" "$PR_HEAD"
`
)

// parityRegistry declares the fixture's gates, its pull-request gate, and
// a command that is neither.
var parityRegistry = map[string]command{
	"coverage": {gate: true}, "deps": {gate: true}, "leaks": {gate: true}, "parity": {gate: true}, "pins": {gate: true},
	"schema-ack":    {pr: true},
	"release-notes": {},
}

var parityTags = []string{"integration"}

// parityProblems runs the rule, which must reach a verdict.
func parityProblems(t *testing.T, makefile, ci string, registry map[string]command, tags []string) []string {
	t.Helper()
	problems, _, err := parityCheck(makefile, ci, registry, tags)
	if err != nil {
		t.Fatalf("parityCheck: %v", err)
	}
	return problems
}

func TestParityAcceptsAMatchedPair(t *testing.T) {
	problems, summary, err := parityCheck(parityMakefile, parityCI, parityRegistry, parityTags)
	if err != nil {
		t.Fatalf("parityCheck: %v", err)
	}
	if len(problems) > 0 {
		t.Errorf("a matched pair reported %d problems:\n%s", len(problems), strings.Join(problems, "\n"))
	}
	want := "parity ok: 5 gates in both, 1 pull-request gates in ci.yml only, " +
		"13 commands of make check in ci.yml, a vet pass for -tags=integration in both"
	if summary != want {
		t.Errorf("summary = %q, want %q", summary, want)
	}
}

// Both directions, and the ways a step can be present in the text and
// not run.
func TestParityCatchesDivergence(t *testing.T) {
	const pinsStep = "      - run: go run ./scripts/gates pins\n"
	cases := []struct {
		name     string
		makefile string
		ci       string
		want     string
	}{
		{
			name: "CI stopped running a gate",
			ci:   strings.Replace(parityCI, pinsStep, "", 1),
			want: `gate "pins" is declared in scripts/gates but no step in ci.yml runs it`,
		},
		{
			name: "the step is commented out",
			ci:   strings.Replace(parityCI, pinsStep, "      # - run: go run ./scripts/gates pins\n", 1),
			want: `gate "pins" is declared in scripts/gates but no step in ci.yml runs it`,
		},
		{
			name: "the gate is named but nothing runs it",
			ci:   strings.Replace(parityCI, pinsStep, "      - name: go run ./scripts/gates pins\n", 1),
			want: `gate "pins" is declared in scripts/gates but no step in ci.yml runs it`,
		},
		{
			// A gate CI runs inside a block scalar. A false alarm here
			// would get the gate edited out.
			name: "a step is a run: block and the gate is inside it",
			ci: strings.Replace(parityCI, pinsStep,
				"      - run: |\n          go run ./scripts/gates pins\n", 1),
			want: "",
		},
		{
			name: "the block ended before the gate was named",
			ci: strings.Replace(parityCI, pinsStep,
				"      - run: |\n          echo building\n      - name: go run ./scripts/gates pins\n", 1),
			want: `gate "pins" is declared in scripts/gates but no step in ci.yml runs it`,
		},
		{
			name:     "`make check` stopped depending on a gate",
			makefile: strings.Replace(parityMakefile, "leaks pins parity", "leaks parity", 1),
			want:     `gate "pins" is declared in scripts/gates but no ` + "`make check`" + ` prerequisite runs it`,
		},
		{
			name:     "a gate's recipe line is commented out",
			makefile: strings.Replace(parityMakefile, "\t@$(GO) run ./scripts/gates parity", "\t# @$(GO) run ./scripts/gates parity", 1),
			want:     `gate "parity" is declared in scripts/gates but no ` + "`make check`" + ` prerequisite runs it`,
		},
		{
			// `make coverage` opens the HTML report; the floor is `make
			// cover`, and a check naming the report must not satisfy it.
			name:     "the check names the report rather than the floor",
			makefile: strings.Replace(parityMakefile, "test cover vuln", "test coverage vuln", 1),
			want:     `gate "coverage" is declared in scripts/gates but no ` + "`make check`" + ` prerequisite runs it`,
		},
		{
			name: "CI reaches coverage only through go test",
			ci:   strings.Replace(parityCI, "      - name: Coverage floor\n        run: go run ./scripts/gates coverage cov.out\n", "", 1),
			want: `gate "coverage" is declared in scripts/gates but no step in ci.yml runs it`,
		},
		{
			name:     "a gate under its target's name",
			makefile: strings.Replace(parityMakefile, "gitleaks staleness leaks", "gitleaks leaks", 1),
			want:     `gate "deps" is declared in scripts/gates but no ` + "`make check`" + ` prerequisite runs it`,
		},
		{
			// The hole a hand list leaves: a prerequisite on no list.
			name: "a new prerequisite CI does not run",
			makefile: strings.Replace(parityMakefile, "pins parity ##", "pins parity tidy ##", 1) +
				"\ntidy:\n\t$(GO) mod tidy -diff\n",
			want: "`make tidy` runs \"go mod tidy\" and nothing in ci.yml does; `make check` claims to be what CI runs",
		},
		{
			name:     "a prerequisite with no recipe",
			makefile: strings.Replace(parityMakefile, "pins parity ##", "pins parity placeholder ##", 1) + "\nplaceholder:\n",
			want:     "`make check` depends on \"placeholder\" and the Makefile gives it no recipe; it checks nothing",
		},
		{
			name: "a tool CI ran through make was deleted from CI",
			ci:   strings.Replace(parityCI, "      - run: make vuln\n", "", 1),
			want: "`make vuln` runs \"govulncheck\" and nothing in ci.yml does; `make check` claims to be what CI runs",
		},
		{
			name:     "`make check` stopped running what CI runs through make",
			makefile: strings.Replace(parityMakefile, "cover vuln licenses", "cover licenses", 1),
			want:     "ci.yml runs `make vuln` and `make check` does not depend on it; a contributor cannot reproduce it",
		},
		{
			name: "CI stopped running gitleaks",
			ci: strings.Replace(parityCI,
				"      - uses: gitleaks/gitleaks-action@0000000000000000000000000000000000000000 # v3\n", "", 1),
			want: "`make gitleaks` runs \"gitleaks\" and nothing in ci.yml does; `make check` claims to be what CI runs",
		},
		{
			name:     "`make check` stopped running gitleaks",
			makefile: strings.Replace(parityMakefile, "licenses gitleaks staleness", "licenses staleness", 1),
			want: "ci.yml runs gitleaks through gitleaks/gitleaks-action and no `make check` prerequisite runs it; " +
				"a contributor cannot reproduce it",
		},
		{
			name: "the action is named only in a comment",
			ci: strings.Replace(parityCI,
				"      - uses: golangci/golangci-lint-action@", "      # - uses: golangci/golangci-lint-action@", 1),
			want: "`make lint` runs \"golangci-lint\" and nothing in ci.yml does",
		},
		{
			name: "a tagged vet pass runs only locally",
			ci:   strings.Replace(parityCI, "      - run: go vet -tags=integration ./...\n", "", 1),
			want: "a file builds only with -tags=integration and ci.yml vets no such pass; it compiles only on a maintainer's machine",
		},
		{
			name:     "a tagged vet pass runs only in CI",
			makefile: strings.Replace(parityMakefile, "\t$(GO) vet -tags=integration ./...\n", "", 1),
			want:     "a file builds only with -tags=integration and `make check` vets no such pass; a contributor cannot see it break",
		},
		{
			// CI's commands include the recipe of a target it runs.
			name: "CI vets through make",
			ci: strings.Replace(parityCI, "      - run: go vet ./...\n      - run: go vet -tags=integration ./...\n",
				"      - run: make vet\n", 1),
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			makefile, ci := c.makefile, c.ci
			if makefile == "" {
				makefile = parityMakefile
			}
			if ci == "" {
				ci = parityCI
			}
			if makefile == parityMakefile && ci == parityCI {
				t.Fatal("the case changed nothing; its replacement did not match")
			}
			got := parityProblems(t, makefile, ci, parityRegistry, parityTags)
			if c.want == "" {
				if len(got) > 0 {
					t.Errorf("expected no problem; got:\n%s", strings.Join(got, "\n"))
				}
				return
			}
			if !mentions(got, c.want) {
				t.Errorf("no problem containing %q; got:\n%s", c.want, strings.Join(got, "\n"))
			}
		})
	}
}

// Each tag the tree requires needs a vet pass on both sides, and a tag no
// file requires needs none.
func TestParityRequiresAVetPassPerBuildTag(t *testing.T) {
	got := parityProblems(t, parityMakefile, parityCI, parityRegistry, []string{"integration", "live"})
	want := []string{
		"a file builds only with -tags=live and `make check` vets no such pass; a contributor cannot see it break",
		"a file builds only with -tags=live and ci.yml vets no such pass; it compiles only on a maintainer's machine",
	}
	if !slices.Equal(got, want) {
		t.Errorf("problems\n got %q\nwant %q", got, want)
	}
}

// A pull-request gate runs in ci.yml and stays out of `make check`,
// which has no merge-base to measure from.
func TestParityHoldsThePullRequestGates(t *testing.T) {
	const ack = `      - run: go run ./scripts/gates schema-ack "$PR_BASE_REF" "$PR_HEAD"` + "\n"
	// changelog-links is a gate, and changelog a pull-request gate whose
	// name starts it.
	links := map[string]command{"changelog": {pr: true}, "changelog-links": {gate: true}}
	for name, c := range parityRegistry {
		links[name] = c
	}
	linksMakefile := strings.Replace(parityMakefile, "pins parity ##", "pins parity changelog-links ##", 1) +
		"\nchangelog-links:\n\tgo run ./scripts/gates changelog-links\n"
	linksCI := parityCI + "      - run: go run ./scripts/gates changelog-links\n"
	for _, tc := range []struct {
		name, makefile, ci string
		registry           map[string]command
		want               string
	}{
		{"in ci.yml only", linksMakefile, linksCI + "      - run: go run ./scripts/gates changelog BASE HEAD\n", links, ""},
		{"missing from ci.yml", parityMakefile, strings.Replace(parityCI, ack, "", 1), parityRegistry,
			`pull-request gate "schema-ack" is declared but no step in ci.yml runs it`},
		{"commented out", parityMakefile, strings.Replace(parityCI, ack, "      # "+strings.TrimLeft(ack, " "), 1), parityRegistry,
			`pull-request gate "schema-ack" is declared but no step in ci.yml runs it`},
		{"in make check", strings.Replace(parityMakefile, "check: fmt", "check: schema-ack fmt", 1), parityCI, parityRegistry,
			`pull-request gate "schema-ack" is in ` + "`make check`, where there is no merge-base"},
		{"named only as the start of another gate", linksMakefile, linksCI, links,
			`pull-request gate "changelog" is declared but no step in ci.yml runs it`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parityProblems(t, tc.makefile, tc.ci, tc.registry, parityTags)
			if tc.want == "" && len(got) > 0 || tc.want != "" && !mentions(got, tc.want) {
				t.Errorf("problems = %q, want %q", got, tc.want)
			}
		})
	}
}

// A gate that finds too little to be looking at anything says so rather
// than pass.
func TestParityRefusesToRunOnTooLittle(t *testing.T) {
	cases := []struct {
		name     string
		makefile string
		registry map[string]command
		tags     []string
		want     string
	}{
		{
			name:     "the registry is not being read",
			registry: map[string]command{"leaks": {gate: true}, "pins": {gate: true}, "schema-ack": {pr: true}},
			want:     "only 2 gates declared; the registry is not being read",
		},
		{
			name: "no pull-request gate is declared",
			registry: map[string]command{
				"coverage": {gate: true}, "deps": {gate: true}, "leaks": {gate: true}, "parity": {gate: true}, "pins": {gate: true},
			},
			want: "no pull-request gate is declared; schema-ack belongs there, and the registry is not being read",
		},
		{
			name:     "no check target",
			makefile: "build:\n\tgo build ./...\n",
			want:     "no `check:` target in the Makefile; the check is reading the wrong file",
		},
		{
			name:     "the check target is a stub",
			makefile: "check: fmt vet\n",
			want:     "the check target has 2 prerequisites; that is not the whole list",
		},
		{
			// Every recipe unreadable: no comparison ran, so none passed.
			name:     "no prerequisite has a recipe",
			makefile: "check: a b c d e f g h\n",
			want:     "only 0 recipe lines matched; the Makefile's shape has changed and this check is reading almost nothing",
		},
		{
			name: "no build tag was read",
			tags: []string{},
			want: "no //go:build line requires a tag; the tree is not being read",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			makefile, registry, tags := c.makefile, c.registry, c.tags
			if makefile == "" {
				makefile = parityMakefile
			}
			if registry == nil {
				registry = parityRegistry
			}
			if tags == nil {
				tags = parityTags
			}
			_, _, err := parityCheck(makefile, parityCI, registry, tags)
			if err == nil {
				t.Fatal("no error; the gate passed on a file it cannot be reading")
			}
			if err.Error() != c.want {
				t.Errorf("error = %q, want %q", err, c.want)
			}
		})
	}
}

// The tags come from the //go:build lines of what git would commit: a tag
// a file builds only with, outside testdata, less an OS, an architecture
// and the tags the go command sets.
func TestTreeBuildTags(t *testing.T) {
	r := newFakeRepo(t)
	for rel, body := range map[string]string{
		"a/a.go":           "//go:build integration\n\npackage a\n",
		"b/b.go":           "//go:build e2e || integration\n\npackage b\n",
		"c/c.go":           "//go:build !live\n\npackage c\n",
		"d/d.go":           "// A comment first.\n\n//go:build smoke\n\npackage d\n",
		"e/e.go":           "package e\n\n//go:build late\n",
		"f/f_windows.go":   "//go:build windows\n\npackage f\n",
		"g/g.go":           "//go:build linux || arm64 || unix || cgo && !gccgo\n\npackage g\n",
		"h/h.go":           "//go:build amd64.v3 || go1.22 || gc || ignore\n\npackage h\n",
		"j/j.go":           "//go:build e2e || wasip1\n\npackage j\n",
		"a/testdata/x.go":  "//go:build fixture\n\npackage x\n",
		"ignored/i.go":     "//go:build ignored\n\npackage i\n",
		".gitignore":       "ignored/\n",
		"notes/README.txt": "//go:build text\n",
	} {
		put(t, r.root, rel, body)
	}
	got, err := treeBuildTags(r.root)
	if err != nil {
		t.Fatalf("treeBuildTags: %v", err)
	}
	if want := []string{"e2e", "integration", "smoke"}; !slices.Equal(got, want) {
		t.Errorf("treeBuildTags = %q, want %q", got, want)
	}
}

// signature is what lets the two files spell the same job differently.
func TestSignatureNamesTheJob(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{"go run ./scripts/gates leaks", "./scripts/gates leaks"},
		{"go run ./scripts/gates coverage cov.out", "./scripts/gates coverage"},
		{"bin --dump-schemas | go run ./scripts/gates descriptions -", "./scripts/gates descriptions"},
		{"go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run", "golangci-lint"},
		{"go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...", "govulncheck"},
		{"go run github.com/zricethezav/gitleaks/v8@v8.30.1 dir .", "gitleaks"},
		{"go vet -tags=integration ./...", "go vet -tags=integration"},
		{"go mod tidy -diff", "go mod tidy"},
		{"go build", "go build"},
	} {
		if got := signature(tc.line); got != tc.want {
			t.Errorf("signature(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

// A recipe is read as make runs it: variables expanded, a continued line
// joined, the echo and error markers dropped, and blank lines skipped.
func TestRecipeOf(t *testing.T) {
	makefile := withoutCommentLines(parityMakefile)
	vars := makeVars(makefile)
	for _, tc := range []struct {
		target string
		want   []string
	}{
		{"licenses", []string{"go run github.com/google/go-licenses@v1.6.0 check ./... --allowed_licenses=Apache-2.0,MIT"}},
		{"vet", []string{"go vet ./...", "go vet -tags=integration ./..."}},
		{"parity", []string{"go run ./scripts/gates parity"}},
		{"cover", []string{"go run ./scripts/gates coverage cov.out"}},
		{"nosuchtarget", nil},
	} {
		if got := recipeOf(makefile, tc.target, vars); !slices.Equal(got, tc.want) {
			t.Errorf("recipeOf(%q) = %q, want %q", tc.target, got, tc.want)
		}
	}
}

// The parity gate reads ci.yml by its path, and the tree's tags from the
// checkout: with ci.yml's tagged vet passes removed, this repository
// fails.
func TestParityReadsThisRepository(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	tags, err := treeBuildTags(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"integration", "live"}; !slices.Equal(tags, want) {
		t.Errorf("this repository's build tags = %q, want %q", tags, want)
	}
	ci := string(repoFile(t, filepath.Join(".github", "workflows", "ci.yml")))
	without := strings.ReplaceAll(ci, "vet -tags=", "vet -tagz=")
	if without == ci {
		t.Fatal("ci.yml runs no tagged vet pass, so this test removes nothing")
	}
	got := parityProblems(t, string(repoFile(t, "Makefile")), without, commands, tags)
	if !mentions(got, "a file builds only with -tags=live and ci.yml vets no such pass") {
		t.Errorf("problems = %q, want one naming -tags=live", got)
	}
}

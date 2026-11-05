package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// pins is a command rather than a test, so a person reading the
// Makefile's target list can see that pinning is checked.

var (
	// usesLine is a step's `uses:`. Its spaces are [ \t], never \s, which
	// crosses a newline and would start a match on a blank line above the
	// step, lending the step the next step's keys.
	usesLine    = regexp.MustCompile(`(?m)^[ \t]*-?[ \t]*uses:[ \t]*([^\s#]+)`)
	fullSHA     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	exactSemver = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

	// versionLine matches any input that names a tool's version, derived
	// from the shape of the key rather than from a list of them. A list
	// names keys an action may not have: gitleaks-action reads
	// `GITLEAKS_VERSION` from the environment and has no
	// `gitleaks-version`, and golangci-lint-action takes plain `version`.
	// A key that can never match reads as coverage and provides none.
	//
	// `go-version-file` ends in `file`, so it is not matched, and it is
	// a pin by reference anyway; a bare `go-version` would be caught,
	// which is what we would want.
	// The value is either a bare token or a `${{ ... }}` expression,
	// which contains spaces and would otherwise be cut at the first one —
	// leaving the check judging the string "${{" and reporting it as an
	// unpinned version.
	versionLine = regexp.MustCompile(`(?mi)^\s*([A-Za-z0-9_-]*(?:version|release)):\s*(\$\{\{[^}]*\}\}|["']?[^"'\s#]+["']?)`)
)

// workflowFile is one file under .github/workflows.
type workflowFile struct {
	name string
	data string
}

// readWorkflows reads every workflow once and holds the floor itself
// rather than leaving each rule to remember one: zero files and zero
// findings are the same output.
func readWorkflows(root string) ([]workflowFile, error) {
	dir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var files []workflowFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := filepath.Ext(e.Name()); ext != ".yml" && ext != ".yaml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		files = append(files, workflowFile{name: e.Name(), data: string(data)})
	}
	if len(files) < 3 {
		return nil, fmt.Errorf("only %d workflows read; the check is not reading %s", len(files), dir)
	}
	return files, nil
}

func pins(w io.Writer, _ []string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	files, err := readWorkflows(root)
	if err != nil {
		return err
	}

	var problems []string
	actions, versions, installers := 0, 0, 0
	for _, f := range files {
		a, v, i, found := inspectWorkflow(f)
		actions += a
		versions += v
		installers += i
		problems = append(problems, found...)
	}

	// A checker that finds nothing passes for the wrong reason, and
	// either count can go to zero on its own.
	if actions < 5 {
		return fmt.Errorf("only %d action references found; the check is not reading the workflows", actions)
	}
	if installers < 3 {
		return fmt.Errorf("only %d tool installers found; the classification table has probably "+
			"drifted from the actions the workflows use", installers)
	}
	if versions < 3 {
		return fmt.Errorf("only %d tool versions found; the input names have probably changed", versions)
	}
	if agreeErr := toolPinsAgree(root); agreeErr != nil {
		problems = append(problems, agreeErr.Error())
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	_, err = fmt.Fprintf(w, "pins ok: %d actions by SHA, %d tool versions exact, %d installers name their tool's version, %d workflows pin the shell\n",
		actions, versions, installers, len(files))
	return err
}

// toolPinsAgree keeps every local run of a tool on the version CI runs.
// CI runs govulncheck and go-licenses through make, at the Makefile's
// version alone. It runs golangci-lint and gitleaks through actions, so
// the Makefile and ci.yml each name those two, and pre-commit names
// gitleaks too. Two versions means a commit can pass one and fail
// another, on rules that exist in one and not the other.
func toolPinsAgree(root string) error {
	read := func(rel string) (string, error) {
		data, err := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // a repository path from a constant
		if err != nil {
			return "", fmt.Errorf("read %s: %w", rel, err)
		}
		return string(data), nil
	}
	precommit, err := read(".pre-commit-config.yaml")
	if err != nil {
		return err
	}
	makefile, err := read("Makefile")
	if err != nil {
		return err
	}
	workflow, err := read(filepath.Join(".github", "workflows", "ci.yml"))
	if err != nil {
		return err
	}
	return pinsAgree(precommit, makefile, workflow)
}

// makePin matches a tool version the Makefile pins, such as
// GITLEAKS_VERSION ?= v8.30.1. Its spaces are [ \t], as usesLine's are:
// \s would read an empty value's version from the next line.
var makePin = regexp.MustCompile(`(?m)^([A-Z][A-Z0-9_]*_VERSION)[ \t]*\??=[ \t]*(\S*)`)

// actionPins are the Makefile's versions of the tools CI runs through an
// action, which ci.yml names too.
var actionPins = []string{"GOLANGCI_LINT_VERSION", "GITLEAKS_VERSION"}

// pinsAgree is toolPinsAgree over the three files' text. Every version
// the Makefile pins is exact, since `go run` resolves a partial one to the
// newest match. A version is compared without its `v`, since go wants it
// and gitleaks-action does not.
func pinsAgree(precommit, makefile, workflow string) error {
	ci := map[string]string{}
	for _, m := range versionLine.FindAllStringSubmatch(workflow, -1) {
		ci[m[1]] = strings.Trim(m[2], `"'`)
	}
	var problems []string
	local := map[string]string{}
	for _, m := range makePin.FindAllStringSubmatch(makefile, -1) {
		local[m[1]] = m[2]
		if !exactSemver.MatchString(m[2]) {
			problems = append(problems, fmt.Sprintf("the Makefile pins %s to %q, which is not exactly one version", m[1], m[2]))
		}
	}
	bare := func(v string) string { return strings.TrimPrefix(v, "v") }
	for _, name := range actionPins {
		version, pinned := local[name]
		want, inCI := ci[name]
		switch {
		case !pinned:
			problems = append(problems, fmt.Sprintf("the Makefile pins no %s; the check is reading the wrong file", name))
		case !inCI:
			problems = append(problems, fmt.Sprintf("the Makefile pins %s and ci.yml does not; "+
				"make check runs a version CI never does", name))
		case bare(version) != bare(want):
			problems = append(problems, fmt.Sprintf("%s is %s in the Makefile and %s in ci.yml; "+
				"a commit can pass one and fail the other", name, version, want))
		}
	}
	hook := gitleaksRev(precommit)
	switch want, ok := ci["GITLEAKS_VERSION"]; {
	case hook == "":
		problems = append(problems, "no rev under the gitleaks repo in .pre-commit-config.yaml; the check is reading the wrong file")
	case !ok:
		problems = append(problems, "no GITLEAKS_VERSION in ci.yml; the check is reading the wrong file")
	case bare(hook) != bare(want):
		problems = append(problems, fmt.Sprintf("gitleaks is %s in pre-commit and %s in ci.yml; "+
			"a commit can pass the hook and fail the job", hook, want))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return nil
}

// pinsShell reports whether a workflow sets bash as the default shell
// for the whole file: `defaults:` at column zero, `run:` under it, and
// `shell: bash` nested *under* that.
//
// Depth is the whole point. A first draft that only asked whether both
// keys had been seen said yes to a `shell: bash` sitting *beside*
// `run:`, which is `defaults.shell` — not a key GitHub has, so it pins
// nothing while reading to a maintainer as though it does. The only two
// things read here are "column zero" and "deeper than the `run:` it sits
// under", so four-space and tab-indented files are not false failures.
func pinsShell(data string) bool {
	lines := strings.Split(data, "\n")
	for i, line := range lines {
		if indentOf(line) != 0 || withoutComment(line) != "defaults:" {
			continue
		}
		runIndent := -1
		for _, l := range lines[i+1:] {
			key := withoutComment(l)
			if key == "" {
				continue
			}
			if indent := indentOf(l); indent == 0 || indent <= runIndent {
				break // the defaults block, or the run block inside it, ended
			} else if key == "run:" {
				runIndent = indent
			} else if runIndent >= 0 && isShellBash(key) {
				return true
			}
		}
		return false // YAML allows one `defaults:` per mapping, so there is no second chance
	}
	return false
}

// indentOf counts the leading whitespace, which is the only measure of
// depth this file needs. A space and a tab each count as one.
func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// withoutComment trims a trailing `#` comment and the surrounding space,
// so a block annotated the way this repository annotates things still
// reads as pinned. A gate that fails a workflow which does carry the
// block teaches people to distrust the gate.
func withoutComment(line string) string {
	if i := strings.Index(line, "#"); i >= 0 {
		line = line[:i]
	}
	return strings.TrimSpace(line)
}

// isShellBash accepts `shell: bash` however YAML lets it be written,
// quotes included. Comparing the whole line as a literal made `shell:
// "bash"` an unpinned workflow.
func isShellBash(entry string) bool {
	name, value, ok := strings.Cut(entry, ":")
	if !ok || strings.TrimSpace(name) != "shell" {
		return false
	}
	return strings.Trim(strings.TrimSpace(value), `"'`) == "bash"
}

// gitleaksRev returns the tag pre-commit installs gitleaks from, found
// by walking down from the gitleaks repository rather than by taking the
// first `rev:` in the file. Anchoring on position would let a second
// remote hook added above this one silently substitute that tool's tag —
// a pattern that matches, but not the thing it names, which is the
// failure this file exists to catch.
func gitleaksRev(config string) string {
	lines := strings.Split(config, "\n")
	for i, line := range lines {
		if withoutComment(line) != "- repo: https://github.com/gitleaks/gitleaks" {
			continue
		}
		for _, l := range lines[i+1:] {
			entry := withoutComment(l)
			if strings.HasPrefix(entry, "- repo:") {
				break // the next hook began and this one named no rev
			}
			if rev, ok := strings.CutPrefix(entry, "rev:"); ok {
				return strings.TrimSpace(rev)
			}
		}
	}
	return ""
}

// envRef matches a workflow expression naming one of the workflow's own
// env entries, which is how a version pinned in one place reaches the
// step that installs it.
var envRef = regexp.MustCompile(`^\$\{\{\s*env\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}$`)

// resolveEnv turns `${{ env.NAME }}` into the value the workflow's env
// block gives it, and returns the input unchanged for anything else. An
// expression naming an env entry that does not exist stays unresolved,
// so it fails the check rather than passing on a value nobody set.
func resolveEnv(value, workflow string) string {
	m := envRef.FindStringSubmatch(strings.TrimSpace(value))
	if m == nil {
		return value
	}
	decl := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(m[1]) + `:\s*"?([^"\n]+)"?\s*$`)
	if d := decl.FindStringSubmatch(workflow); d != nil {
		return strings.Trim(strings.TrimSpace(d[1]), `"'`)
	}
	return value
}

// inspectWorkflow reads one workflow: how many action references and
// tool versions it names, and what is wrong with them. Split out of pins
// so that function stays under the cyclomatic limit — the three loops
// are independent and read better named than nested.
func inspectWorkflow(f workflowFile) (actions, versions, installers int, problems []string) {
	for _, m := range usesLine.FindAllStringSubmatch(f.data, -1) {
		ref := m[1]
		if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "docker://") {
			continue // a local action carries no version of its own
		}
		actions++
		_, rev, ok := strings.Cut(ref, "@")
		if !ok || !fullSHA.MatchString(rev) {
			problems = append(problems, fmt.Sprintf(
				"%s: %s is not pinned to a full commit SHA (a tag is mutable)", f.name, ref))
		}
	}
	for _, m := range versionLine.FindAllStringSubmatch(f.data, -1) {
		versions++
		// `version: ${{ env.X }}` is still one exact version when the
		// workflow's own env block names one — and it is a better
		// pattern than repeating a literal, because the two copies
		// cannot then disagree. Resolve it before judging it.
		value := resolveEnv(strings.Trim(m[2], `"\'`), f.data)
		if !exactSemver.MatchString(value) {
			problems = append(problems, fmt.Sprintf(
				"%s: %s: %q is not exactly one version — a range, a bare major or `latest` "+
					"lets the tool change under a pinned action", f.name, m[1], m[2]))
		}
	}
	installers, toolProblems := unpinnedTools(f)
	problems = append(problems, toolProblems...)
	if !pinsShell(f.data) {
		problems = append(problems, fmt.Sprintf(
			"%s: no workflow-level `defaults: run: shell: bash`; without it the Windows "+
				"runner parses a `run` line as PowerShell", f.name))
	}
	return actions, versions, installers, problems
}

// A version check reads only the versions that are written. An action
// that installs a tool and names no version at all is an absence: a SHA
// pins the wrapper, not the tool, so `sigstore/cosign-installer` with no
// `cosign-release` installs whatever cosign is newest, default signing
// format and all.
//
// So every action is classified here, and an unknown one fails the gate
// rather than passing quietly.
var (
	// installerPins maps an action to the input keys that pin the tool
	// it installs. Any one of them satisfies the rule.
	installerPins = map[string][]string{
		"sigstore/cosign-installer":         {"cosign-release"},
		"anchore/sbom-action/download-syft": {"syft-version"},
		"goreleaser/goreleaser-action":      {"version"},
		"golangci/golangci-lint-action":     {"version"},
		// A file is a pin by reference, and a better one: go.mod cannot
		// disagree with itself the way two literals can.
		"actions/setup-go": {"go-version-file", "go-version"},
		// This wrapper has never had a version input; it reads the
		// scanner's version from the environment.
		"gitleaks/gitleaks-action": {"GITLEAKS_VERSION"},
	}

	// notInstallers are the actions that install no tool, each with the
	// reason, so that adding one is a decision rather than an omission.
	notInstallers = map[string]string{
		"actions/checkout":                "checks out the repository",
		"actions/upload-artifact":         "uploads, installs nothing",
		"actions/attest-build-provenance": "calls the attestation API",
		"github/codeql-action/init":       "CodeQL's bundle is GitHub's to manage",
		"github/codeql-action/analyze":    "CodeQL's bundle is GitHub's to manage",
	}
)

// stepBlock returns the lines belonging to the step whose `uses:` line
// starts at start, so that a `with:` or `env:` key is read from the step
// that owns it rather than from the next one down the file.
func stepBlock(data string, start int) string {
	lines := strings.Split(data[start:], "\n")
	base := indentOf(lines[0])
	var out []string
	for i, ln := range lines {
		if i > 0 && strings.TrimSpace(ln) != "" {
			in := indentOf(ln)
			if in < base || (in == base && strings.HasPrefix(strings.TrimSpace(ln), "- ")) {
				break
			}
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// unpinnedTools reports every action whose tool is left to float, and
// every action nobody has classified.
func unpinnedTools(f workflowFile) (installers int, problems []string) {
	for _, loc := range usesLine.FindAllStringSubmatchIndex(f.data, -1) {
		ref := f.data[loc[2]:loc[3]]
		if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "docker://") {
			continue
		}
		action, _, _ := strings.Cut(ref, "@")
		keys, isInstaller := installerPins[action]
		if !isInstaller {
			if _, known := notInstallers[action]; !known {
				problems = append(problems, fmt.Sprintf(
					"%s: %s is not classified in scripts/gates/pins.go — add it to installerPins "+
						"with the input that pins its tool, or to notInstallers with the reason. "+
						"A SHA pins the wrapper, not the tool", f.name, action))
			}
			continue
		}
		installers++
		block := stepBlock(f.data, loc[0])
		if !namesAnyKey(block, keys) {
			problems = append(problems, fmt.Sprintf(
				"%s: %s is pinned by SHA but the tool it installs is not — set %s. "+
					"A SHA pins the wrapper, and the tool floats to its newest release", f.name, action, strings.Join(keys, " or ")))
		}
	}
	return installers, problems
}

// namesAnyKey reports whether the step sets one of the given keys.
func namesAnyKey(block string, keys []string) bool {
	for _, k := range keys {
		if regexp.MustCompile(`(?mi)^\s*` + regexp.QuoteMeta(k) + `:\s*\S`).MatchString(block) {
			return true
		}
	}
	return false
}

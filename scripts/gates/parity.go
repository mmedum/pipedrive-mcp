package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var (
	// checkLine is the `check:` rule, whose prerequisites are what `make
	// check` runs.
	checkLine = regexp.MustCompile(`(?m)^check:([^\n]*)$`)
	// makeAssign is a `NAME ?= value`, `NAME := value` or `NAME = value`
	// line.
	makeAssign = regexp.MustCompile(`(?m)^([A-Z_][A-Z0-9_]*)[ \t]*[:?]?=[ \t]*(.*)$`)
	makeRef    = regexp.MustCompile(`\$\([A-Za-z_][A-Za-z0-9_]*\)`)
	// gatesCall is a gates invocation, in a recipe or a workflow step.
	gatesCall = regexp.MustCompile(`\./scripts/gates ([a-z-]+)`)
	// pinnedTool is a module run at an exact version. The command is its
	// last path element less a major-version suffix: `gitleaks/v8@v8.30.1`
	// runs gitleaks, and the "v8" it would otherwise take also sits in the
	// version string, where it matches anything.
	pinnedTool = regexp.MustCompile(`([A-Za-z0-9_-]+)(?:/v\d+)?@v\d+\.\d+\.\d+`)
	// makeCall is a `make` in a workflow step, with the targets it names.
	makeCall = regexp.MustCompile(`\bmake((?:[ \t]+[\w.-]+)+)`)
	// vetTags is a tagged vet pass, with its comma-separated tags.
	vetTags = regexp.MustCompile(`\bvet -tags=([\w.,]+)`)
)

// gatesPrefix starts the signature of a recipe line that runs a gate.
const gatesPrefix = "./scripts/gates "

// ciActions maps each tool `make check` runs with `go run` and CI runs
// through an action to that action, which stands in for the command.
// Every other command is found in what ci.yml runs. gitleaks-action
// scans the commits of the push or pull request, and `make gitleaks`
// the branch's commits not on origin/main, then the working tree.
var ciActions = map[string]string{
	"golangci-lint": "golangci/golangci-lint-action",
	"gitleaks":      "gitleaks/gitleaks-action",
}

// parity holds `make check` and the CI workflow to the same set of gates
// (D-84).
//
// The two lists live in different files and an edit touches one of
// them, so they drift silently: a gate CI skips can break on main with
// every pull request green, and a gate only CI runs fails a pull request
// that passed `make check`.
//
// The comparison is by command: each `check:` prerequisite's recipe is
// read, and what it runs is looked for in what ci.yml runs. A target and
// its CI step need not share a name, and a new prerequisite cannot pass
// for being on no list.
func parity(w io.Writer, _ []string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	makefile, err := os.ReadFile(filepath.Join(root, "Makefile")) //nolint:gosec // a repository path from a constant
	if err != nil {
		return err
	}
	ci, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml")) //nolint:gosec // a repository path from a constant
	if err != nil {
		return err
	}
	tags, err := treeBuildTags(root)
	if err != nil {
		return couldNotRun{err}
	}
	problems, summary, err := parityCheck(string(makefile), string(ci), commands, tags)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	_, err = fmt.Fprintln(w, summary)
	return err
}

// parityCheck is the rule itself, over the text of the two files, a
// registry and the tree's build tags, so a test can hand it a Makefile and
// a workflow that disagree without breaking the repository first.
//
// Both files are read without their whole-line comments, which both
// spell with a leading `#`: a step commented out to unblock a red build
// runs nothing.
func parityCheck(makefile, ci string, registry map[string]command, tags []string) (problems []string, summary string, err error) {
	makefile, ci = withoutCommentLines(makefile), withoutCommentLines(ci)
	prereqs, ok := checkPrereqs(makefile)
	if !ok {
		return nil, "", fmt.Errorf("no `check:` target in the Makefile; the check is reading the wrong file")
	}
	if len(prereqs) < 5 {
		return nil, "", fmt.Errorf("the check target has %d prerequisites; that is not the whole list", len(prereqs))
	}
	if len(tags) == 0 {
		return nil, "", fmt.Errorf("no //go:build line requires a tag; the tree is not being read")
	}

	vars := makeVars(makefile)
	remote := readCI(ci, makefile, vars)
	local, problems := readCheck(makefile, vars, prereqs, remote, registry)
	if local.matched < 8 {
		return nil, "", fmt.Errorf("only %d recipe lines matched; the Makefile's shape has changed and "+
			"this check is reading almost nothing", local.matched)
	}
	problems = append(problems, ciOnlyProblems(prereqs, local, remote)...)
	declared, gates, prOnly := registryProblems(registry, local, remote)
	if gates < 5 {
		return nil, "", fmt.Errorf("only %d gates declared; the registry is not being read", gates)
	}
	if prOnly == 0 {
		return nil, "", fmt.Errorf("no pull-request gate is declared; schema-ack belongs there, and the registry is not being read")
	}
	problems = append(problems, declared...)
	problems = append(problems, vetProblems(tags, local, remote)...)
	summary = fmt.Sprintf("parity ok: %d gates in both, %d pull-request gates in ci.yml only, "+
		"%d commands of make check in ci.yml, a vet pass for -tags=%s in both",
		gates, prOnly, local.matched, strings.Join(tags, ", -tags="))
	return problems, summary, nil
}

// ciSide is what ci.yml runs: its run lines with the recipe of each target
// a step runs with `make`, the gates in them, and the actions it uses.
type ciSide struct {
	targets []string
	runs    string
	gates   map[string]bool
	uses    map[string]bool
}

func readCI(ci, makefile string, vars map[string]string) ciSide {
	runs := runLines(ci)
	c := ciSide{targets: ciMakeTargets(runs, makefile), runs: runs, uses: actionsIn(ci)}
	for _, target := range c.targets {
		c.runs += "\n" + strings.Join(recipeOf(makefile, target, vars), "\n")
	}
	c.gates = gatesIn(c.runs)
	return c
}

// ran reports whether CI runs the command a recipe line's signature names.
func (c ciSide) ran(sig string) bool {
	if gate, ok := strings.CutPrefix(sig, gatesPrefix); ok {
		return c.gates[gate]
	}
	return c.uses[ciActions[sig]] || strings.Contains(c.runs, sig)
}

// checkSide is what `make check` runs: each recipe line's signature, the
// gates and vetted tags among them, and how many lines CI runs too.
type checkSide struct {
	sigs, gates, vetted map[string]bool
	matched             int
}

// readCheck reads each prerequisite's recipe, and reports each command in
// it CI does not run. A gate the registry declares is left to
// registryProblems.
func readCheck(makefile string, vars map[string]string, prereqs []string, remote ciSide,
	registry map[string]command,
) (local checkSide, problems []string) {
	local = checkSide{sigs: map[string]bool{}, gates: map[string]bool{}, vetted: map[string]bool{}}
	for _, target := range prereqs {
		recipe := recipeOf(makefile, target, vars)
		if len(recipe) == 0 {
			problems = append(problems, fmt.Sprintf(
				"`make check` depends on %q and the Makefile gives it no recipe; it checks nothing", target))
		}
		for _, line := range recipe {
			for _, tag := range tagsVetted(line) {
				local.vetted[tag] = true
			}
			sig := signature(line)
			local.sigs[sig] = true
			gate, isGate := strings.CutPrefix(sig, gatesPrefix)
			if isGate {
				local.gates[gate] = true
			}
			switch c := registry[gate]; {
			case remote.ran(sig):
				local.matched++
			case !isGate || !c.gate && !c.pr:
				problems = append(problems, fmt.Sprintf(
					"`make %s` runs %q and nothing in ci.yml does; `make check` claims to be what CI runs", target, sig))
			}
		}
	}
	return local, problems
}

// ciOnlyProblems is the other direction, for what CI runs through make or
// an action: `make check` runs it too.
func ciOnlyProblems(prereqs []string, local checkSide, remote ciSide) []string {
	var problems []string
	for _, target := range remote.targets {
		if !slices.Contains(prereqs, target) {
			problems = append(problems, fmt.Sprintf(
				"ci.yml runs `make %s` and `make check` does not depend on it; a contributor cannot reproduce it", target))
		}
	}
	for _, tool := range slices.Sorted(maps.Keys(ciActions)) {
		if action := ciActions[tool]; remote.uses[action] && !local.sigs[tool] {
			problems = append(problems, fmt.Sprintf(
				"ci.yml runs %s through %s and no `make check` prerequisite runs it; a contributor cannot reproduce it", tool, action))
		}
	}
	return problems
}

// registryProblems holds each declared gate to both sides, and each
// pull-request gate to CI alone, and counts each kind.
func registryProblems(registry map[string]command, local checkSide, remote ciSide) (problems []string, gates, prOnly int) {
	for _, name := range commandNames(registry) {
		switch c := registry[name]; {
		case c.gate:
			gates++
			if !remote.gates[name] {
				problems = append(problems, fmt.Sprintf("gate %q is declared in scripts/gates but no step in ci.yml runs it", name))
			}
			if !local.gates[name] {
				problems = append(problems, fmt.Sprintf("gate %q is declared in scripts/gates but no `make check` prerequisite runs it", name))
			}
		case c.pr:
			prOnly++
			if !remote.gates[name] {
				problems = append(problems, fmt.Sprintf("pull-request gate %q is declared but no step in ci.yml runs it", name))
			}
			if local.gates[name] {
				problems = append(problems, fmt.Sprintf("pull-request gate %q is in `make check`, where there is no merge-base", name))
			}
		}
	}
	return problems, gates, prOnly
}

// vetProblems holds each tag the tree requires to a vet pass on both
// sides: a file behind a build tag compiles only in a pass with that tag.
func vetProblems(tags []string, local checkSide, remote ciSide) []string {
	var problems []string
	ciVetted := tagsVetted(remote.runs)
	for _, tag := range tags {
		if !local.vetted[tag] {
			problems = append(problems, fmt.Sprintf(
				"a file builds only with -tags=%s and `make check` vets no such pass; a contributor cannot see it break", tag))
		}
		if !slices.Contains(ciVetted, tag) {
			problems = append(problems, fmt.Sprintf(
				"a file builds only with -tags=%s and ci.yml vets no such pass; it compiles only on a maintainer's machine", tag))
		}
	}
	return problems
}

// checkPrereqs is the targets `check:` depends on in a Makefile's text,
// less the trailing `## help text`, which documents the target for `make
// help`. ok is false when there is no `check:` target.
func checkPrereqs(makefile string) (prereqs []string, ok bool) {
	m := checkLine.FindStringSubmatch(makefile)
	if m == nil {
		return nil, false
	}
	line, _, _ := strings.Cut(m[1], "##")
	return strings.Fields(line), true
}

// makeVars reads the Makefile's simple variable assignments, so a recipe
// spelled `$(GO) run …@$(GITLEAKS_VERSION)` compares with a workflow that
// spells the same command literally.
func makeVars(makefile string) map[string]string {
	vars := map[string]string{}
	for _, m := range makeAssign.FindAllStringSubmatch(makefile, -1) {
		vars[m[1]] = strings.TrimSpace(m[2])
	}
	return vars
}

// expand substitutes $(NAME) until nothing changes, which is enough for
// assignments that refer to each other a few levels deep.
func expand(line string, vars map[string]string) string {
	for range 5 {
		next := makeRef.ReplaceAllStringFunc(line, func(ref string) string {
			if v, ok := vars[ref[2:len(ref)-1]]; ok {
				return v
			}
			return ref
		})
		if next == line {
			break
		}
		line = next
	}
	return line
}

// recipeOf is a target's recipe, one command per line, with a line a
// backslash continues joined to the next and variables expanded. A recipe
// line starts with a tab, and blank lines among them are ignored
// (https://www.gnu.org/software/make/manual/html_node/Recipe-Syntax.html).
func recipeOf(makefile, target string, vars map[string]string) []string {
	lines := strings.Split(makefile, "\n")
	start := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, target+":") })
	if start < 0 {
		return nil
	}
	var recipe []string
	joining := false
	for _, l := range lines[start+1:] {
		body, isRecipe := strings.CutPrefix(l, "\t")
		if !isRecipe {
			if strings.TrimSpace(l) == "" {
				continue
			}
			break
		}
		body = strings.TrimSpace(body)
		if !joining {
			recipe = append(recipe, "")
			body = strings.TrimLeft(body, "@-+")
		}
		body, joining = strings.CutSuffix(body, `\`)
		recipe[len(recipe)-1] += " " + strings.TrimSpace(body)
	}
	for i, line := range recipe {
		recipe[i] = expand(strings.TrimSpace(line), vars)
	}
	return slices.DeleteFunc(recipe, func(line string) bool { return line == "" })
}

// signature reduces a recipe line to the shortest string that names what
// it runs, so the same job spelled differently in a workflow still
// matches: a gate by its subcommand, a pinned tool by its command, and
// anything else by its first three words.
func signature(line string) string {
	if m := gatesCall.FindStringSubmatch(line); m != nil {
		return gatesPrefix + m[1]
	}
	if m := pinnedTool.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	fields := strings.Fields(line)
	return strings.Join(fields[:min(len(fields), 3)], " ")
}

// runLines keeps the lines of a workflow that run something: a `run:`
// line, and the body of a `run: |` block, which is every line indented
// past the `run:` that opened it. Block scalars are the one piece of
// YAML this needs to know, and indentation is all it takes to find the
// end of one. A step disabled by an `if:` that is never true is not
// seen; that needs a YAML parse.
func runLines(ci string) string {
	var kept []string
	body := -1
	for _, l := range strings.Split(ci, "\n") {
		indent := indentOf(l)
		if body >= 0 {
			if strings.TrimSpace(l) == "" || indent > body {
				kept = append(kept, l)
				continue
			}
			body = -1
		}
		i := strings.Index(l, "run:")
		if i < 0 {
			continue
		}
		kept = append(kept, l)
		switch strings.TrimSpace(l[i+len("run:"):]) {
		case "|", "|-", "|+", ">", ">-", ">+":
			body = indent
		}
	}
	return strings.Join(kept, "\n")
}

// ciMakeTargets is each Makefile target a `make` in a workflow's run
// lines names, once.
func ciMakeTargets(runs, makefile string) []string {
	rules := strings.Split(makefile, "\n")
	var targets []string
	for _, m := range makeCall.FindAllStringSubmatch(runs, -1) {
		for _, target := range strings.Fields(m[1]) {
			isRule := slices.ContainsFunc(rules, func(l string) bool { return strings.HasPrefix(l, target+":") })
			if isRule && !slices.Contains(targets, target) {
				targets = append(targets, target)
			}
		}
	}
	return targets
}

// gatesIn is every gate a text runs, by its exact name: changelog-links
// does not run changelog.
func gatesIn(text string) map[string]bool {
	in := map[string]bool{}
	for _, m := range gatesCall.FindAllStringSubmatch(text, -1) {
		in[m[1]] = true
	}
	return in
}

// actionsIn is every action a workflow uses, less its ref.
func actionsIn(workflow string) map[string]bool {
	in := map[string]bool{}
	for _, m := range usesLine.FindAllStringSubmatch(workflow, -1) {
		action, _, _ := strings.Cut(m[1], "@")
		in[action] = true
	}
	return in
}

// tagsVetted is every tag a tagged vet pass in text names.
func tagsVetted(text string) []string {
	matches := vetTags.FindAllStringSubmatch(text, -1)
	tags := make([]string, 0, len(matches))
	for _, m := range matches {
		tags = append(tags, strings.Split(m[1], ",")...)
	}
	return tags
}

// treeBuildTags is every tag a //go:build line in the tree requires, each
// once and sorted: a file that builds with the tag and not without it,
// which an untagged vet never compiles.
func treeBuildTags(root string) ([]string, error) {
	files, err := docsFiles(root)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var tags []string
	for _, rel := range files {
		if !strings.HasSuffix(rel, ".go") || docsInTestdata(rel) {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil,
			parser.PackageClauseOnly|parser.ParseComments)
		if err != nil {
			return nil, err
		}
		tags = append(tags, requiredTags(f)...)
	}
	slices.Sort(tags)
	return slices.Compact(tags), nil
}

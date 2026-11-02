package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// plain is a released surface: two tools and a resource template.
const plain = `{"header":{"binary_version":"v1.0.0","sdk_version":"v1.8.0","tool_count":2},"tools":[
	{"name":"manage_deal","annotations":{"destructiveHint":false},
	 "inputSchema":{"type":"object","properties":{"id":{"type":"integer"}}}},
	{"name":"get_deal","annotations":{"readOnlyHint":true},
	 "inputSchema":{"type":"object","properties":{"id":{"type":"integer"}}}}],
	"resource_templates":[{"uriTemplate":"pipedrive://deals/{id}","name":"deal"}]}`

// flipped is plain with manage_deal made destructive, and renamed is
// plain with manage_deal's id input renamed, which breaks it.
var (
	flipped = strings.Replace(plain, `"destructiveHint":false`, `"destructiveHint":true`, 1)
	renamed = strings.Replace(plain, `"properties":{"id"`, `"properties":{"deal_id"`, 1)
)

// renameBreak is the break renamed makes of plain.
var renameBreak = []change{{"manage_deal", "manage_deal: input field id removed", "input id"}}

// schema-ack fails a pull request unless its commits name every changed
// item in a footer, and every break of a release in a BREAKING CHANGE:
// footer.
func TestSchemaAckNeedsEveryItemNamed(t *testing.T) {
	const tmpl = "pipedrive://deals/{id}"
	relabeled := strings.Replace(plain, `"name":"deal"`, `"name":"a deal"`, 1)
	withoutTemplate := strings.Replace(plain, `{"uriTemplate":"pipedrive://deals/{id}","name":"deal"}`, ``, 1)
	reworded := strings.Replace(plain, `"tools":[`, `"instructions":"new","tools":[`, 1)
	const (
		wantUnnamed      = "no footer names manage_deal"
		wantUnnamedBreak = "no BREAKING CHANGE: footer names manage_deal"
	)
	for _, tc := range []struct {
		name, built, message, wantErr string
	}{
		{"an unchanged surface needs nothing", plain, "Tidy the README", ""},
		{"a change needs a footer", flipped, "Mark manage_deal destructive", wantUnnamed},
		{"a footer naming the tool acknowledges it", flipped, "Mark it\n\nSCHEMA-CHANGE: manage_deal: destructiveHint true", ""},
		{"a tab after the colon starts the description too", flipped, "Mark it\n\nSCHEMA-CHANGE: get_deal:\tdestructive, manage_deal", wantUnnamed},
		{"a value wrapped at the margin continues it", flipped, "Mark them\n\nSCHEMA-CHANGE: get_deal,\nmanage_deal: destructiveHint true", ""},
		{"prose after a bare list names nothing", flipped, "Mark it\n\nSCHEMA-CHANGE: get_deal\nmanage_deal is destructive now", wantUnnamed},
		{"a bare list ends at its line, and its name counts", flipped, "Mark it\n\nSCHEMA-CHANGE: manage_deal\nhints flipped", ""},
		{"prose after a trailing comma names nothing", flipped, "Mark them\n\nSCHEMA-CHANGE: get_deal,\nwhich manage_deal also reads", wantUnnamed},
		{"a list is split on commas, not spaces", flipped, "Mark them\n\nSCHEMA-CHANGE: get_deal manage_deal: hints", wantUnnamed},
		{"a Token #value footer is a footer", flipped, "Mark it\n\nSCHEMA-CHANGE: manage_deal: hints\n\nCloses #12", ""},
		{"a Token #value footer acknowledges nothing", flipped, "Mark it\n\nSCHEMA-CHANGE #manage_deal", wantUnnamed},
		{"nor does a BREAKING CHANGE #value one", renamed, "Rename it\n\nBREAKING CHANGE #manage_deal", wantUnnamedBreak},
		{"a footer ends the value before it", flipped, "Mark it\n\nSCHEMA-CHANGE: get_deal,\nRefs #12, manage_deal", wantUnnamed},
		{"a footer naming another tool does not", flipped, "Mark it\n\nSCHEMA-CHANGE: get_deal: destructiveHint true", wantUnnamed},
		{"a name in the description names nothing", flipped, "Mark it\n\nSCHEMA-CHANGE: get_deal: as manage_deal is now", wantUnnamed},
		{"a footer with no description names its list", flipped, "Mark it\n\nSCHEMA-CHANGE: manage_deal", ""},
		{"a name is a whole token", flipped, "Mark it\n\nSCHEMA-CHANGE: manage_deals: destructiveHint true", wantUnnamed},
		{"a continuation line can name it", flipped, "Mark them\n\nSCHEMA-CHANGE: get_deal,\n  manage_deal: destructiveHint true", ""},
		{"an empty footer says nothing", flipped, "Mark it\n\nSCHEMA-CHANGE:", wantUnnamed},
		{"the next line is not the footer's text", flipped, "Mark it\n\nSCHEMA-CHANGE:\nCo-Authored-By: manage_deal", wantUnnamed},
		{"a footer must open its line", flipped, "Mark it, see SCHEMA-CHANGE: manage_deal", wantUnnamed},
		{"a line of prose is not a footer", flipped, "Mark it\n\nThe gate wants a\nSCHEMA-CHANGE: manage_deal footer, which this is not.", wantUnnamed},
		{"a subject is not a footer", flipped, "SCHEMA-CHANGE: manage_deal", wantUnnamed},
		{"an indented block is not a footer block", flipped, "Mark it\n\nSCHEMA-CHANGE: manage_deal: hints\n\n    make schema-ack", wantUnnamed},
		{"a footer above the co-author trailer", flipped, "Mark it\n\nSCHEMA-CHANGE: manage_deal: hints\n\nCo-Authored-By: someone <x@example.com>", ""},
		{"a footer above three blank lines", flipped, "Mark it\n\nSCHEMA-CHANGE: manage_deal: hints\n\n\n\nCo-Authored-By: someone <x@example.com>", ""},
		{"a footer beside the co-author trailer", flipped, "Mark it\n\nCo-Authored-By: someone <x@example.com>\nSCHEMA-CHANGE: manage_deal: hints", ""},
		{"a resource template by its URI", relabeled, "Rename it\n\nSCHEMA-CHANGE: " + tmpl + ": renamed", ""},
		{"a resource template unnamed", relabeled, "Rename it\n\nSCHEMA-CHANGE: manage_deal: renamed", "no footer names " + tmpl},
		{"an instructions change needs a footer", reworded, "Reword them", "no footer names instructions"},
		{"the instructions by their key", reworded, "Reword them\n\nSCHEMA-CHANGE: instructions: reworded", ""},
		{"a break named only as a change", renamed, "Rename the input\n\nSCHEMA-CHANGE: manage_deal: id is deal_id", wantUnnamedBreak},
		{"BREAKING CHANGE: acknowledges a break", renamed, "Rename the input\n\nBREAKING CHANGE: manage_deal: id is deal_id", ""},
		{"BREAKING-CHANGE: is its synonym", renamed, "Rename the input\n\nBREAKING-CHANGE: manage_deal: id is deal_id", ""},
		{"SCHEMA-CHANGE: in any case", flipped, "Mark it\n\nSchema-Change: manage_deal: destructiveHint true", ""},
		{"BREAKING CHANGE: only in uppercase", renamed, "Rename the input\n\nBreaking-Change: manage_deal: id is deal_id", wantUnnamedBreak},
		{"a removed template needs BREAKING CHANGE:", withoutTemplate, "Drop it\n\nSCHEMA-CHANGE: " + tmpl + ": gone",
			"no BREAKING CHANGE: footer names " + tmpl},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := compareSurfaces(surfaceOf(t, plain), surfaceOf(t, tc.built))
			err := checkAck(io.Discard, c, []string{"Tidy the docs", tc.message}, "base")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("checkAck = %v, want it to pass", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("checkAck = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

// The fix a refusal names is the footer that fixes it: BREAKING CHANGE:
// for an unnamed break, SCHEMA-CHANGE: for the rest, each naming its
// items, and a message that ends with it passes.
func TestARefusalNamesTheFooterThatFixesIt(t *testing.T) {
	both := strings.Replace(renamed, `"name":"deal"`, `"name":"a deal"`, 1)
	for _, tc := range []struct{ name, built, wantFix string }{
		{"a change", flipped, "SCHEMA-CHANGE: manage_deal: what changed"},
		{"a break", renamed, "BREAKING CHANGE: manage_deal: what broke"},
		{"a break and a change", both, "BREAKING CHANGE: manage_deal: what broke\nSCHEMA-CHANGE: pipedrive://deals/{id}: what changed"},
		{
			"a break and a change to one tool", strings.Replace(renamed, `"destructiveHint":false`, `"destructiveHint":true`, 1),
			"BREAKING CHANGE: manage_deal: what broke",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := compareSurfaces(surfaceOf(t, plain), surfaceOf(t, tc.built))
			err := checkAck(io.Discard, c, []string{"Change it"}, "base")
			if err == nil {
				t.Fatal("checkAck passed with no footer")
			}
			_, fix, _ := strings.Cut(err.Error(), "\n\n")
			if fix != tc.wantFix {
				t.Errorf("the refusal's fix is %q, want %q", fix, tc.wantFix)
			}
			if err := checkAck(io.Discard, c, []string{"Change it", "Name it\n\n" + fix}, "base"); err != nil {
				t.Errorf("checkAck = %v after the fix it named", err)
			}
		})
	}
}

// A name in backticks is taken whole, so a URI with a comma in it can be
// named; split on its comma, it names nothing.
func TestAFooterCanNameAURIWithACommaInBackticks(t *testing.T) {
	const tmpl = "pipedrive://deals{?status,owner}"
	was := strings.Replace(plain, `"pipedrive://deals/{id}"`, `"`+tmpl+`"`, 1)
	now := strings.Replace(was, `"name":"deal"`, `"name":"a deal"`, 1)
	c := compareSurfaces(surfaceOf(t, was), surfaceOf(t, now))
	if err := checkAck(io.Discard, c, []string{"Rename\n\nSCHEMA-CHANGE: `" + tmpl + "`: renamed"}, "base"); err != nil {
		t.Errorf("checkAck = %v, want the backticked URI to name the template", err)
	}
	if err := checkAck(io.Discard, c, []string{"Rename\n\nSCHEMA-CHANGE: " + tmpl + ": renamed"}, "base"); err == nil {
		t.Error("checkAck passed with the URI split on its comma, which names nothing")
	}
}

// The fix a refusal prints puts a name the footer would split or trim in
// backticks, so the footer it prints is one that passes.
func TestARefusalQuotesANameTheFooterWouldSplit(t *testing.T) {
	for _, uri := range []string{"pipedrive://deals{?status,owner}", "pipedrive://deals/{id}."} {
		t.Run(uri, func(t *testing.T) {
			was := strings.Replace(plain, `"pipedrive://deals/{id}"`, `"`+uri+`"`, 1)
			c := compareSurfaces(surfaceOf(t, was), surfaceOf(t, strings.Replace(was, `"name":"deal"`, `"name":"a deal"`, 1)))
			err := checkAck(io.Discard, c, []string{"Rename it"}, "base")
			_, fix, _ := strings.Cut(fmt.Sprint(err), "\n\n")
			if want := "SCHEMA-CHANGE: `" + uri + "`: what changed"; fix != want {
				t.Fatalf("the refusal's fix is %q, want %q", fix, want)
			}
			if err := checkAck(io.Discard, c, []string{"Rename it", "Name it\n\n" + fix}, "base"); err != nil {
				t.Errorf("checkAck = %v after the fix it named", err)
			}
		})
	}
}

// A break no footer names is asked for once, as a break: the BREAKING
// CHANGE: footer that fixes it names it for both checks.
func TestARefusalAsksForAnUnnamedBreakOnce(t *testing.T) {
	err := checkAck(io.Discard, compareSurfaces(surfaceOf(t, plain), surfaceOf(t, renamed)), nil, "base")
	if want := "the tool surface changed since base, and no BREAKING CHANGE: footer names manage_deal. "; err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("checkAck = %v, want it to start %q", err, want)
	}
}

// A break of the released surface needs BREAKING CHANGE:; removing a tool
// added since the release breaks no caller, so it is a plain change, and
// the report lists it as one.
func TestABreakIsABreakOfARelease(t *testing.T) {
	release := surfaceOf(t, plain)
	base := surfaceOf(t, strings.Replace(plain, `"tools":[`, `"tools":[{"name":"draft_tool"},`, 1))
	head := surfaceOf(t, renamed)
	c := releaseBreaksOnly(compareSurfaces(base, head), compareSurfaces(release, head).breaking)
	if !slices.Equal(c.breaking, renameBreak) {
		t.Errorf("breaking = %v, want %v", c.breaking, renameBreak)
	}
	var report strings.Builder
	footer := "Drop and rename\n\nSCHEMA-CHANGE: draft_tool: never released\nBREAKING CHANGE: manage_deal: id is deal_id"
	if err := checkAck(&report, c, []string{footer}, "base"); err != nil {
		t.Errorf("checkAck = %v, want both acknowledged", err)
	}
	want := "against the merge-base base: 0 added, 2 changed, 1 breaking\n" +
		"  ~ tool manage_deal (inputSchema.properties)\n" +
		"  ~ tool draft_tool removed\n" +
		"  ! manage_deal: input field id removed  BREAKING\n" +
		"the change since base is acknowledged\n"
	if report.String() != want {
		t.Errorf("the report is\n%s\nwant\n%s", report.String(), want)
	}
}

// A tool the base branch already broke since the release, in one field,
// does not make a pull request's break of another field, one no release
// shipped, a break of the release.
func TestABreakOfTheReleaseIsMatchedByItsField(t *testing.T) {
	withInputs := func(props string) surface {
		return surfaceOf(t, strings.Replace(plain, `"properties":{"id":{"type":"integer"}}`, `"properties":{`+props+`}`, 1))
	}
	release := withInputs(`"id":{"type":"integer"},"note":{"type":"string"}`)
	base := withInputs(`"id":{"type":"integer"},"draft":{"type":"string"}`)
	head := withInputs(`"id":{"type":"integer"}`)
	c := releaseBreaksOnly(compareSurfaces(base, head), compareSurfaces(release, head).breaking)
	if len(c.breaking) != 0 {
		t.Errorf("breaking = %v, want draft, which no release shipped, as a change", c.breaking)
	}
	// Nor does a released tool of the name of a prompt the pull request
	// removes: they are two items, though a footer names both alike.
	// The release lists prompts, none: a list its dump lacked would be
	// read as the base has it (asReleased).
	release = surfaceOf(t, `{"tools":[{"name":"get_deal"},{"name":"summarize"}],"prompts":[]}`)
	base = surfaceOf(t, `{"tools":[{"name":"get_deal"}],"prompts":[{"name":"summarize"}]}`)
	head = surfaceOf(t, `{"tools":[{"name":"get_deal"}],"prompts":[]}`)
	if c := releaseBreaksOnly(compareSurfaces(base, head), compareSurfaces(release, head).breaking); len(c.breaking) != 0 {
		t.Errorf("breaking = %v, want the unreleased prompt's removal as a change", c.breaking)
	}
}

// The release is the build of the tag git describe finds for the base
// branch: not the working tree, not a later commit, and not an older tag
// a branch cut before the last release reaches. Before the first tag
// there is none, and nothing breaks one.
func TestReleasedBreaksReadTheBaseBranchTag(t *testing.T) {
	r := newFakeRepo(t)
	r.git("commit", "--quiet", "--allow-empty", "-m", "Start")
	r.git("branch", "before-any-release")
	base, head := surfaceOf(t, plain), surfaceOf(t, renamed)

	if breaks, err := releasedBreaks("HEAD", base, head); err != nil || len(breaks) != 0 {
		t.Fatalf("releasedBreaks with no tag = %v, %v; want none", breaks, err)
	}
	r.commit(plain, "Release 1")
	r.git("tag", "v1.0.0")
	r.git("branch", "feature")
	// The next release renamed the input already, so it breaks nothing.
	r.commit(renamed, "Release 2")
	r.git("tag", "v2.0.0")
	r.git("switch", "--quiet", "feature")
	// A later commit and the working tree are not the release.
	r.commit(`{"tools":[{"name":"other"}]}`, "After")
	r.write(`{"tools":[{"name":"another"}]}`)

	if breaks, err := releasedBreaks("main", base, head); err != nil || len(breaks) != 0 {
		t.Errorf("releasedBreaks against main = %v, %v; want none, as v2.0.0 shipped the rename", breaks, err)
	}
	if breaks, err := releasedBreaks("feature", base, head); err != nil || !slices.Equal(breaks, renameBreak) {
		t.Errorf("releasedBreaks against feature = %v, %v; want %v, from v1.0.0", breaks, err, renameBreak)
	}
	// A branch whose history reaches no tag has no release, although the
	// repository has tags.
	if breaks, err := releasedBreaks("before-any-release", base, head); err != nil || len(breaks) != 0 {
		t.Errorf("releasedBreaks against a branch that reaches no tag = %v, %v; want none", breaks, err)
	}
}

// A list the release's dump does not carry, as v1.3.0's carries tools
// alone, is read as the base has it, so removing a resource template the
// base has needs BREAKING CHANGE. A release whose dump carries the list,
// empty or not, is read as it says.
func TestAListTheReleaseDumpLacksIsReadAsTheBaseHasIt(t *testing.T) {
	const (
		toolsOnly    = `{"tools":[{"name":"get_deal"}]}`
		noTemplate   = `{"tools":[{"name":"get_deal"}],"resource_templates":[]}`
		withTemplate = `{"tools":[{"name":"get_deal"}],"resource_templates":[{"uriTemplate":"pipedrive://deals/{id}"}]}`
	)
	fixFor := func(branch string) string {
		t.Helper()
		r := newFakeRepo(t)
		r.commit(toolsOnly, "Release 1")
		r.git("tag", "v1.0.0")
		if branch == "after-release-2" {
			r.commit(noTemplate, "Release 2")
			r.git("tag", "v2.0.0")
		}
		r.commit(withTemplate, "Add a template")
		r.git("switch", "--quiet", "-c", branch)
		r.commit(noTemplate, "Drop it")
		_, fix, _ := strings.Cut(fmt.Sprint(schemaAck(io.Discard, []string{"main", "HEAD"})), "\n\n")
		return fix
	}
	if got, want := fixFor("after-release-1"), "BREAKING CHANGE: pipedrive://deals/{id}: what broke"; got != want {
		t.Errorf("against a release whose dump lists tools alone, the fix is %q, want %q", got, want)
	}
	if got, want := fixFor("after-release-2"), "SCHEMA-CHANGE: pipedrive://deals/{id}: what changed"; got != want {
		t.Errorf("against a release that served no template, the fix is %q, want %q", got, want)
	}
}

// Both sides are built from commits: the merge-base and the head. The
// checkout is neither, in CI the merge commit and here main with a change
// nobody committed, so what it would serve is not what is judged.
func TestSchemaAckBuildsBothSidesFromCommits(t *testing.T) {
	r := newFakeRepo(t)
	r.commit(plain, "Base")
	r.git("switch", "--quiet", "-c", "feature")
	r.commit(flipped, "Flip manage_deal")
	r.git("switch", "--quiet", "main")
	r.write(renamed)

	var out strings.Builder
	err := schemaAck(&out, []string{"main", "feature"})
	if err == nil || !strings.HasSuffix(err.Error(), "\n\nSCHEMA-CHANGE: manage_deal: what changed") {
		t.Fatalf("schemaAck = %v, want a refusal naming the flip alone", err)
	}
	if !strings.Contains(out.String(), "  ~ tool manage_deal (annotations.destructiveHint)\n") || strings.Contains(out.String(), "BREAKING") {
		t.Errorf("the report is\n%s\nwant the flip and no break", out.String())
	}
	r.git("checkout", "--", ".")
	r.git("switch", "--quiet", "feature")
	r.git("commit", "--quiet", "--allow-empty", "-m", "Acknowledge it\n\nSCHEMA-CHANGE: manage_deal: destructive")
	if err := schemaAck(io.Discard, []string{"main", "HEAD"}); err != nil {
		t.Fatalf("acknowledged: %v", err)
	}
	if n := r.worktrees(); n != 1 {
		t.Errorf("%d worktrees left, want the checkout alone", n)
	}
}

// Through the whole gate: a tool no release shipped, removed, is a plain
// change, and an input the last tag shipped, renamed, is a break.
func TestSchemaAckAsksForBreakingChangeOnlyForAReleasedBreak(t *testing.T) {
	r := newFakeRepo(t)
	r.commit(plain, "Release")
	r.git("tag", "v1.0.0")
	r.commit(strings.Replace(plain, `"tools":[`, `"tools":[{"name":"draft_tool"},`, 1), "Add draft_tool")
	r.git("switch", "--quiet", "-c", "feature")
	r.commit(renamed, "Drop draft_tool and rename id")
	err := schemaAck(io.Discard, []string{"main", "HEAD"})
	_, fix, _ := strings.Cut(fmt.Sprint(err), "\n\n")
	if want := "BREAKING CHANGE: manage_deal: what broke\nSCHEMA-CHANGE: draft_tool: what changed"; fix != want {
		t.Fatalf("schemaAck = %v, want the fix %q", err, want)
	}
	r.git("commit", "--quiet", "--allow-empty", "-m", "Acknowledge them\n\n"+fix)
	if err := schemaAck(io.Discard, []string{"main", "HEAD"}); err != nil {
		t.Fatalf("acknowledged: %v", err)
	}
}

// A gate that could not run says so and exits 2; a refusal exits 1.
// Under `go run`, which exits 1 for both, the message tells them apart.
func TestSchemaAckTellsARefusalFromAFailure(t *testing.T) {
	refused := checkAck(io.Discard, compareSurfaces(surfaceOf(t, plain), surfaceOf(t, flipped)), nil, "base")
	if refused == nil || exitCode(refused) != 1 || errors.As(refused, new(couldNotRun)) {
		t.Errorf("a refusal (%v) exits %d, want 1", refused, exitCode(refused))
	}
	r := newFakeRepo(t)
	r.git("commit", "--quiet", "--allow-empty", "-m", "Start")
	err := schemaAck(io.Discard, []string{"no-such-ref", "HEAD"})
	// What git says after it is git's, in its words.
	const want = "could not run: git merge-base no-such-ref HEAD: exit status 128: "
	if exitCode(err) != 2 || !strings.HasPrefix(fmt.Sprint(err), want) {
		t.Errorf("schemaAck with no base = %v, exit %d; want exit 2 and %q", err, exitCode(err), want)
	}
	if err := schemaAck(io.Discard, []string{"HEAD"}); err == nil || err.Error() != "usage: gates schema-ack BASE HEAD" {
		t.Errorf("schemaAck with one argument = %v, want its usage", err)
	}
}

// fakeRepo is a git repository, the working directory for the test,
// whose cmd/pipedrive-mcp prints a fixed dump, so the gate's worktree
// builds are exercised without building the real server. Its git reads
// none of the machine's configuration.
type fakeRepo struct {
	t    *testing.T
	root string
}

func newFakeRepo(t *testing.T) *fakeRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	r := &fakeRepo{t: t, root: t.TempDir()}
	t.Chdir(r.root)
	r.git("init", "--quiet", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(r.root, "go.mod"), []byte("module example.invalid/fake\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.git("add", "go.mod")
	return r
}

func (r *fakeRepo) git(args ...string) {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.root
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// commit commits a main that prints dump.
func (r *fakeRepo) commit(dump, message string) {
	r.t.Helper()
	r.write(dump)
	r.git("add", "-A")
	r.git("commit", "--quiet", "-m", message)
}

// write puts a main that prints dump in the working tree, uncommitted.
func (r *fakeRepo) write(dump string) {
	r.t.Helper()
	dir := filepath.Join(r.root, "cmd", "pipedrive-mcp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.t.Fatal(err)
	}
	src := fmt.Sprintf("package main\n\nimport \"os\"\n\nfunc main() { os.Stdout.WriteString(%q) }\n", dump)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

func (r *fakeRepo) worktrees() int {
	r.t.Helper()
	out, err := exec.Command("git", "-C", r.root, "worktree", "list", "--porcelain").Output()
	if err != nil {
		r.t.Fatal(err)
	}
	return strings.Count(string(out), "worktree ")
}

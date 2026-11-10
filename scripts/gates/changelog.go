package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// changelogGate fails when a pull request changes what users get, what the
// binary is built from or the bundle's manifest, without adding an entry
// under [Unreleased].
//
// The [Unreleased] part matters more than it looks: an entry added under
// an already-released heading is invisible to anyone reading the release
// it belongs to, and the release page IS the changelog section, so it is
// invisible on the page too.
//
// A git or go call that fails is a gate that could not run, and exits 2.
func changelogGate(w io.Writer, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: gates changelog BASE HEAD")
	}
	base, head := args[0], args[1]

	changed, err := git("diff", "--name-only", "--no-renames", base, head)
	if err != nil {
		return couldNotRun{err}
	}
	shipped, err := listShipped(".")
	if err != nil {
		return couldNotRun{err}
	}
	touched := touchesWatched(changed, shipped)
	if !touched && strings.Contains("\n"+changed, "\ngo.mod\n") {
		if touched, err = goModChangedBetween(base, head); err != nil {
			return couldNotRun{err}
		}
	}
	if !touched {
		_, _ = fmt.Fprintln(w, "nothing users get changed; skipping the CHANGELOG check")
		return nil
	}

	// A generous context window, so the [Unreleased] heading is in the
	// diff however far the entries sit below it. Without it the hunk for
	// a new entry arrives with no heading above it and every added line
	// looks like it belongs to whatever section came last.
	diff, err := git("diff", "--unified=99999", base, head, "--", changelogFile)
	if err != nil {
		return couldNotRun{err}
	}
	if strings.TrimSpace(diff) == "" {
		return fmt.Errorf("what users get changed but %s did not; the release notes are "+
			"%s's section, so the change would ship unannounced", changelogFile, changelogFile)
	}
	d := readChangelogDiff(diff)
	if n := addedUnderUnreleased(d); n == 0 {
		return fmt.Errorf("%s changed but no entry was added under [Unreleased], the section the next "+
			"release's notes are cut from%s", changelogFile, cutHint(d))
	} else {
		_, _ = fmt.Fprintf(w, "CHANGELOG entry under [Unreleased] confirmed, %d added\n", n)
	}
	return nil
}

// touchesWatched reports whether the diff changes a file the binary is
// built from. Test data is left out: a refreshed fixture, such as the spec
// snapshot each release takes, changes nothing a user sees.
func touchesWatched(nameOnlyDiff string, shipped *shippedSource) bool {
	for _, f := range strings.Split(nameOnlyDiff, "\n") {
		if strings.Contains("/"+f, "/testdata/") {
			continue
		}
		if shipped.has(f) {
			return true
		}
	}
	return false
}

// shippedSource is what the binary is built from in this module, on this
// platform: its package directories and the files they embed, each
// slash-separated from the module root.
type shippedSource struct{ dirs, embeds map[string]bool }

// has reports whether a file is one users get: any file but a test in one
// of the binary's package directories, an embedded file, or the bundle's
// manifest, which a person reads when installing the .mcpb. Every file of
// a package directory counts: the go command builds a package from
// assembly, a .syso and C as well as Go (`go help filetype`). A package
// counts by its directory, so a file of it another platform builds counts,
// and so does one removed from it. The packages are the host's, in the
// checkout: a package only another platform's files import, or a directory
// the head removes whole, is not one.
func (s *shippedSource) has(file string) bool {
	return file == manifestPath || s.embeds[file] || !strings.HasSuffix(file, "_test.go") && s.dirs[path.Dir(file)]
}

// listShipped asks the go command what ./cmd/... is built from in the
// module at dir. A package of another module is a dependency, which
// go.mod's require lines name.
func listShipped(dir string) (*shippedSource, error) {
	const what = "go list -deps ./cmd/..."
	cmd := exec.Command("go", "list", "-deps", "-json=Dir,EmbedFiles,Module", "./cmd/...")
	cmd.Dir = dir
	out, err := cmd.Output()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return nil, fmt.Errorf("%s: %s", what, strings.TrimSpace(string(exit.Stderr)))
	case err != nil:
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	s := &shippedSource{dirs: map[string]bool{}, embeds: map[string]bool{}}
	for dec := json.NewDecoder(bytes.NewReader(out)); dec.More(); {
		var p struct {
			Dir        string
			EmbedFiles []string
			Module     *struct {
				Main bool
				Dir  string
			}
		}
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		if p.Module == nil || !p.Module.Main {
			continue
		}
		rel, err := filepath.Rel(p.Module.Dir, p.Dir)
		if err != nil {
			return nil, err
		}
		pkg := filepath.ToSlash(rel)
		s.dirs[pkg] = true
		for _, f := range p.EmbedFiles {
			s.embeds[path.Join(pkg, f)] = true
		}
	}
	// An empty listing is not reading the binary, and would excuse every
	// change.
	if len(s.dirs) == 0 {
		return nil, fmt.Errorf("%s named no package of this module, so it is not reading the binary", what)
	}
	return s, nil
}

// goModChangedBetween reports whether go.mod at head differs from base's
// outside its require and tool lines: a go, toolchain or replace line
// changes what the binary is built with.
func goModChangedBetween(base, head string) (bool, error) {
	before, err := git("show", base+":go.mod")
	if err != nil {
		return false, err
	}
	after, err := git("show", head+":go.mod")
	if err != nil {
		return false, err
	}
	return goModBuildLines(before) != goModBuildLines(after), nil
}

// goModBuildLines is a go.mod's lines, each trimmed and the blank ones
// dropped, without its require and tool directives, a block's included
// (https://go.dev/ref/mod#go-mod-file-require). A tool directive adds a
// package `go tool` runs (https://go.dev/ref/mod#go-mod-file-tool). ( is a
// token of its own, so `require(` opens a block as `require (` does
// (https://go.dev/ref/mod#go-mod-file-lexical).
func goModBuildLines(text string) string {
	var kept []string
	inBlock := false
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		verb, paren := "", false
		if len(fields) > 0 {
			verb, _, paren = strings.Cut(fields[0], "(")
		}
		switch {
		case inBlock:
			inBlock = len(fields) == 0 || !strings.HasPrefix(fields[0], ")")
		case len(fields) == 0:
		case verb == "require" || verb == "tool":
			inBlock = paren || len(fields) > 1 && strings.HasPrefix(fields[1], "(")
		default:
			kept = append(kept, strings.Join(fields, " "))
		}
	}
	return strings.Join(kept, "\n")
}

// changelogDiff is a CHANGELOG diff read back into the file before and
// after it, each through the reader release-notes reads with, and which
// lines of each the diff removed and added. The gate diffs with the whole
// file as context, so each side is the whole file.
type changelogDiff struct {
	before, after  changelogDoc
	removed, added map[int]bool
}

func readChangelogDiff(diff string) changelogDiff {
	var before, after []string
	d := changelogDiff{removed: map[int]bool{}, added: map[int]bool{}}
	inHunk := false
	for _, line := range strings.Split(strings.ReplaceAll(diff, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case !inHunk || strings.HasPrefix(line, `\`):
		case line == "":
			// A blank context line whose leading space was trimmed.
			before, after = append(before, ""), append(after, "")
		case line[0] == '+':
			d.added[len(after)] = true
			after = append(after, line[1:])
		case line[0] == '-':
			d.removed[len(before)] = true
			before = append(before, line[1:])
		default:
			before, after = append(before, line[1:]), append(after, line[1:])
		}
	}
	d.before, d.after = readChangelog(strings.Join(before, "\n")), readChangelog(strings.Join(after, "\n"))
	return d
}

// isReleaseCut reports whether the diff removes the [Unreleased] heading,
// which is what a release-prep change does and nothing else does.
func (d changelogDiff) isReleaseCut() bool {
	for _, h := range d.before.headings {
		if h.label == "Unreleased" && d.removed[h.line] {
			return true
		}
	}
	return false
}

// addedHeadings are the labels of the headings the diff adds.
func (d changelogDiff) addedHeadings() []string {
	var out []string
	for _, h := range d.after.headings {
		if d.added[h.line] {
			out = append(out, h.label)
		}
	}
	return out
}

// addedUnderUnreleased counts the entries a CHANGELOG diff adds under the
// [Unreleased] heading: the bullets at the margin, outside fenced code, on
// added lines. A blank line, a heading or a line of prose is no entry.
//
// A RELEASE CUT is the one case where the entries do not sit under
// [Unreleased] and are still in the right place: cutting a release
// renames that heading to the version, so a pull request that both cuts
// the release and touches watched source could never satisfy this gate.
// When the diff removes the [Unreleased] heading, a heading it adds is
// treated as the unreleased section — which is what it was, one commit
// ago, and the entries under it are the ones being released.
func addedUnderUnreleased(d changelogDiff) int {
	labels := []string{"Unreleased"}
	if d.isReleaseCut() {
		labels = append(labels, d.addedHeadings()...)
	}
	count := 0
	for _, label := range labels {
		from, to := d.after.section(label)
		for i := from; i < to; i++ {
			if d.added[i] && strings.HasPrefix(d.after.prose[i], "- ") {
				count++
			}
		}
	}
	return count
}

// cutHint explains the one confusing way this gate fails: entries exist,
// but they are under a version heading that was not cut from
// [Unreleased] in this diff, so they are invisible to a reader of the
// release they belong to.
func cutHint(d changelogDiff) string {
	if !d.isReleaseCut() && len(d.addedHeadings()) > 0 {
		return " — entries were added under a released heading; " +
			"a new section only counts when this diff also cuts it from [Unreleased]"
	}
	return ""
}

// git runs git in the working directory. A failure carries what git
// said, not only its exit status.
func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(bytes.TrimSpace(ee.Stderr)) > 0 {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(ee.Stderr))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

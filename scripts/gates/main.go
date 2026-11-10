// Command gates is this repository's own checks and release helpers.
//
// They are Go rather than shell for the reasons the sibling MCP servers
// converted theirs: a shell script is held to no gofmt, vet, lint or
// test; `make check` has to run on whatever a maintainer has installed,
// where bash and jq are a dependency rather than a given; and a script
// that parses JSON with grep is how a quote ends up inside a string.
//
//	go run ./scripts/gates changelog BASE HEAD
//	go run ./scripts/gates deps
//	go run ./scripts/gates docs
//	go run ./scripts/gates parity
//	go run ./scripts/gates release-notes VERSION [CHANGELOG]
//	go run ./scripts/gates schema-ack BASE HEAD
//	go run ./scripts/gates smoke binary TARGET
package main

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// command is one thing this program can do.
type command struct {
	run  func(io.Writer, []string) error
	args string
	doc  string
	// runs says where the command has to run, and is what parity reads:
	// it ties the Makefile and the workflows to one list.
	runs where
}

// where is the pipeline a command has to run in, which parity holds.
type where int

const (
	// manual is a maintainer's command, run by a Makefile target or a
	// pre-commit hook and never by `make check`.
	manual where = iota
	// inCheck has to run in BOTH `make check` and the CI workflow.
	inCheck
	// inPR has to run in CI on a pull request and NOT in `make check`:
	// it measures from the merge-base, which only a pull request has.
	inPR
	// inRelease runs in the release workflows or the goreleaser config,
	// and never in `make check`.
	inRelease
)

// commands is the one list of what this program does: the usage text,
// the dispatch and the parity gate all read it, so they cannot drift
// from each other.
var commands map[string]command

// init fills the registry rather than a var literal, because parity reads
// it and Go will not let a map initializer refer to a function that
// refers back to the map.
func init() {
	commands = map[string]command{
		"changelog": {
			run: changelogGate, args: "BASE HEAD", runs: inPR,
			doc: "a change to watched source needs an entry under [Unreleased]",
		},
		"changelog-links": {
			run:  func(out io.Writer, _ []string) error { return changelogLinksGate(out) },
			runs: inCheck,
			doc:  "every CHANGELOG version heading has its link definition",
		},
		"mcpb": {
			run: mcpbCmd, args: "", runs: inCheck,
			doc: "the bundle manifest describes the bundle the packer stages, and the release its six archives",
		},
		"mcpb-pack": {
			run: mcpbPackCmd, args: "DIST VERSION OUT", runs: inRelease,
			doc: "pack the .mcpb from a built dist tree; runs at release time",
		},
		"registry-publish": {
			run: registryPublishCmd, args: "VERSION CHECKSUMS", runs: inRelease,
			doc: "the MCP registry entry, with the bundle's hash from the published checksums",
		},
		"leaks": {
			run: leaks, args: "[history]", runs: inCheck,
			doc: "nothing from a real Pipedrive account is in the tree, or with history anywhere in the history",
		},
		"pins": {
			run: pins, args: "", runs: inCheck,
			doc: "every action is a commit and every tool version is exact",
		},
		"deps": {
			run: depsGate, args: "", runs: inCheck,
			doc: "no direct dependency is more than six months behind without a reason",
		},
		"release-notes": {
			run: releaseNotes, args: "VERSION [CHANGELOG]", runs: inRelease,
			doc: "one version's CHANGELOG section, which is the release note",
		},
		"schema-ack": {
			run: schemaAck, args: "BASE HEAD", runs: inPR,
			doc: "a commit footer names each item of the tool surface a pull request changes",
		},
		"smoke": {
			run: smokeGate, args: "binary TARGET", runs: inCheck,
			doc: "drive an MCP handshake over stdio and read the reply",
		},
		"descriptions": {
			run: descriptionsGate, args: "SCHEMA_DUMP|-", runs: inCheck,
			doc: "hold the tool descriptions against the mechanical house-style rules",
		},
		"coverage": {
			run: coverageGate, args: "PROFILE", runs: inCheck,
			doc: "the statement-coverage floor on the packages it holds",
		},
		"parity": {
			run: parity, args: "", runs: inCheck,
			doc: "`make check` and CI run the same gates",
		},
		"docs": {
			run: docs, args: "", runs: inCheck,
			doc: "decision IDs, citations, links, paths, the codemap and the integration tests the docs and code name all resolve",
		},
	}
}

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}
	c, ok := commands[os.Args[1]]
	if !ok {
		_, _ = fmt.Fprintf(os.Stderr, "gates: unknown command %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
	// Every command reads the repository from its root, so it runs the same
	// from any directory under it.
	root, err := moduleRoot()
	if err == nil {
		err = os.Chdir(root)
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "gates: "+couldNotRun{err}.Error())
		os.Exit(2)
	}
	if err := c.run(os.Stdout, os.Args[2:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "gates: "+err.Error())
		os.Exit(exitCode(err))
	}
}

// couldNotRun is a gate that reached no verdict: a build or a git call
// failed. Its message says so, and it exits 2, where a refusal exits 1.
type couldNotRun struct{ err error }

func (e couldNotRun) Error() string { return "could not run: " + e.err.Error() }
func (e couldNotRun) Unwrap() error { return e.err }

// floorProblems is a problem when a gate read fewer of what than least:
// found nothing and looked at nothing print the same sentence otherwise.
func floorProblems(what string, read, least int) []string {
	if read >= least {
		return nil
	}
	return []string{fmt.Sprintf("read %d %s, want at least %d: the reader is not seeing its input", read, what, least)}
}

// exitCode is the status a gate's error exits with.
func exitCode(err error) int {
	if errors.As(err, new(couldNotRun)) {
		return 2
	}
	return 1
}

func usage(w io.Writer) {
	var b strings.Builder
	b.WriteString("usage: gates COMMAND [ARGS]\n\n")
	for _, name := range commandNames(commands) {
		c := commands[name]
		mark := " "
		if c.runs == inCheck {
			mark = "*"
		}
		_, _ = fmt.Fprintf(&b, "  %s %-16s %-24s %s\n", mark, name, c.args, c.doc)
	}
	b.WriteString("\n  * runs in both `make check` and CI; the parity gate checks that.\n")
	b.WriteString("  Every command runs from the module root, and reads a path argument from there.\n")
	_, _ = io.WriteString(w, b.String())
}

// commandNames is a registry's command names, sorted.
func commandNames(registry map[string]command) []string {
	return slices.Sorted(maps.Keys(registry))
}

// moduleRoot walks up from the working directory to the directory
// holding go.mod, which main runs every command from.
func moduleRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		if filepath.Dir(dir) == dir {
			return "", fmt.Errorf("no go.mod in %s or above it", cwd)
		}
	}
}

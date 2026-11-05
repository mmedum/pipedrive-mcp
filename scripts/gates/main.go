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
	// gate marks a command that runs in BOTH `make check` and ci.yml.
	// The parity gate reads it, which ties the Makefile and the workflow
	// to this one list.
	gate bool
	// pr marks a command only a pull request can run, because it measures
	// from the merge-base. Parity holds it to a `run:` step in ci.yml and
	// keeps it out of `make check`.
	pr bool
}

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
			run: changelogGate, args: "BASE HEAD", pr: true,
			doc: "a change to watched source needs an entry under [Unreleased]",
		},
		"changelog-links": {
			run:  func(out io.Writer, _ []string) error { return changelogLinksGate(out) },
			gate: true,
			doc:  "every CHANGELOG version heading has its link definition",
		},
		"mcpb": {
			run: mcpbCmd, args: "", gate: true,
			doc: "the bundle manifest describes the bundle the packer stages, and the release its six archives",
		},
		"mcpb-pack": {
			run: mcpbPackCmd, args: "DIST VERSION OUT",
			doc: "pack the .mcpb from a built dist tree; runs at release time",
		},
		"registry-publish": {
			run: registryPublishCmd, args: "VERSION CHECKSUMS",
			doc: "the MCP registry entry, with the bundle's hash from the published checksums",
		},
		"leaks": {
			run: leaks, args: "[history]", gate: true,
			doc: "nothing from a real Pipedrive account is in the tree, or with history anywhere in the history",
		},
		"pins": {
			run: pins, args: "", gate: true,
			doc: "every action is a commit and every tool version is exact",
		},
		"deps": {
			run: depsGate, args: "", gate: true,
			doc: "no direct dependency is more than six months behind without a reason",
		},
		"release-notes": {
			run: releaseNotes, args: "VERSION [CHANGELOG]",
			doc: "one version's CHANGELOG section, which is the release note",
		},
		"schema-ack": {
			run: schemaAck, args: "BASE HEAD", pr: true,
			doc: "a commit footer names each item of the tool surface a pull request changes",
		},
		"smoke": {
			run: smokeGate, args: "binary TARGET", gate: true,
			doc: "drive an MCP handshake over stdio and read the reply",
		},
		"descriptions": {
			run: descriptionsGate, args: "SCHEMA_DUMP|-", gate: true,
			doc: "hold the tool descriptions against the mechanical house-style rules",
		},
		"coverage": {
			run: coverageGate, args: "PROFILE", gate: true,
			doc: "the statement-coverage floor on the packages it holds",
		},
		"parity": {
			run: parity, args: "", gate: true,
			doc: "`make check` and CI run the same gates",
		},
		"docs": {
			run: docs, args: "", gate: true,
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
		if c.gate {
			mark = "*"
		}
		_, _ = fmt.Fprintf(&b, "  %s %-16s %-24s %s\n", mark, name, c.args, c.doc)
	}
	b.WriteString("\n  * runs in both `make check` and CI; the parity gate checks that.\n")
	_, _ = io.WriteString(w, b.String())
}

// commandNames is a registry's command names, sorted.
func commandNames(registry map[string]command) []string {
	return slices.Sorted(maps.Keys(registry))
}

// moduleRoot finds the directory holding go.mod, so a gate reads the
// repository rather than whatever directory it was invoked from.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

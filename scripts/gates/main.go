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
//	go run ./scripts/gates release-notes VERSION [CHANGELOG]
//	go run ./scripts/gates smoke binary TARGET
package main

import (
	"fmt"
	"io"
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
}

// commands is the one list of what this program does: the usage text and
// the dispatch both read it, so they cannot drift from each other.
var commands map[string]command

func init() {
	commands = map[string]command{
		"changelog": {
			run: changelogGate, args: "BASE HEAD",
			doc: "a change to watched source needs an entry under [Unreleased]",
		},
		"changelog-links": {
			run: func(out io.Writer, _ []string) error { return changelogLinksGate(out) },
			doc: "every CHANGELOG version heading has its link definition",
		},
		"mcpb": {
			run: mcpbCmd, args: "",
			doc: "the bundle manifest describes the bundle the packer stages",
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
			run: leaks, args: "",
			doc: "nothing from a real Pipedrive account is in the tree",
		},
		"pins": {
			run: pins, args: "",
			doc: "every action is a commit and every tool version is exact",
		},
		"deps": {
			run: depsGate, args: "",
			doc: "no direct dependency is more than six months behind without a reason",
		},
		"release-notes": {
			run: releaseNotes, args: "VERSION [CHANGELOG]",
			doc: "one version's CHANGELOG section, which is the release note",
		},
		"smoke": {
			run: smokeGate, args: "binary TARGET",
			doc: "drive an MCP handshake over stdio and read the reply",
		},
		"checklist": {
			run: checklistGate, args: "[MAKEFILE CLAUDE_MD]",
			doc: "CLAUDE.md's make check list against the Makefile's check: target",
		},
		"descriptions": {
			run: descriptionsGate, args: "SCHEMA_DUMP|-",
			doc: "hold the tool descriptions against the mechanical house-style rules",
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
		os.Exit(1)
	}
}

func usage(w io.Writer) {
	var b strings.Builder
	b.WriteString("usage: gates COMMAND [ARGS]\n\n")
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		c := commands[name]
		_, _ = fmt.Fprintf(&b, "  %-14s %-24s %s\n", name, c.args, c.doc)
	}
	_, _ = io.WriteString(w, b.String())
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

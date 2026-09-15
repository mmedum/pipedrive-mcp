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
//	go run ./scripts/gates smoke (binary|docker) TARGET
package main

import (
	"fmt"
	"io"
	"os"
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
		"deps": {
			run: depsGate, args: "",
			doc: "no direct dependency is more than six months behind without a reason",
		},
		"release-notes": {
			run: releaseNotes, args: "VERSION [CHANGELOG]",
			doc: "one version's CHANGELOG section, which is the release note",
		},
		"smoke": {
			run: smokeGate, args: "(binary|docker) TARGET",
			doc: "drive an MCP handshake over stdio and read the reply",
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

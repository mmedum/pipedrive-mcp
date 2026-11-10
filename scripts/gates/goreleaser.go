package main

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// One reader for .goreleaser.yaml, shared by the pins, parity and mcpb
// gates. It records every value the config sets by its key path,
// with its line, in the block style this config is written in, and
// refuses by name a shape it does not read, so a config written another
// way fails rather than yielding nothing to check. Like the workflow
// reader, it is not a YAML parser, and is not grown into one.

// goreleaserValue is one scalar the config sets: a key's value, or an item
// of a sequence.
type goreleaserValue struct {
	// keys is the path of keys down to the value, sequence items left
	// out: builds.goos.
	keys string
	// items is the index of each sequence item on the path, outermost
	// first, so the second goos of the first build is [0 1].
	items []int
	line  int
	value string
}

// goreleaserFile is what the reader takes from the goreleaser config.
type goreleaserFile struct {
	values []goreleaserValue
	// runs are the command lines the release runs, a block scalar's as one
	// line, and words are the words of a sign's or an SBOM's command.
	runs, words []workflowRun
	refused     []string
}

// goreleaserRole is what a key path's value is to the release.
type goreleaserRole int

const (
	// goreleaserHolds is a key with one under it that runs.
	goreleaserHolds goreleaserRole = iota + 1
	// goreleaserLine is a command line the release runs.
	goreleaserLine
	// goreleaserWord is one word of a sign's or an SBOM's command.
	goreleaserWord
)

// goreleaserChecked is the goreleaser version whose source the reader's
// key paths and template rule are checked against. The pins gate holds it
// to the version release.yml runs.
const goreleaserChecked = "v2.13.1"

// goreleaserKeys are, by key path, the keys whose values the release
// runs, and the keys above them: a hook, as a string or its cmd:, a
// publisher's cmd, and the cmd and args of a sign or an SBOM, as
// goreleaser v2.13.1 reads them
// (https://github.com/goreleaser/goreleaser/blob/v2.13.1/pkg/config/config.go;
// a hook's string forms at marshaler.go#L30-L45 and #L64-L80 there).
var goreleaserKeys = func() map[string]goreleaserRole {
	keys := map[string]goreleaserRole{}
	add := func(role goreleaserRole, paths ...string) {
		for _, path := range paths {
			keys[path] = role
			for at := strings.LastIndexByte(path, '.'); at > 0; at = strings.LastIndexByte(path[:at], '.') {
				if _, ok := keys[path[:at]]; !ok {
					keys[path[:at]] = goreleaserHolds
				}
			}
		}
	}
	add(goreleaserLine, "before.hooks", "publishers.cmd")
	for _, at := range []string{"builds", "universal_binaries"} {
		for _, when := range []string{".hooks.pre", ".hooks.post"} {
			add(goreleaserLine, at+when, at+when+".cmd")
		}
	}
	for _, at := range []string{"signs", "docker_signs", "binary_signs", "sboms"} {
		add(goreleaserWord, at+".cmd", at+".args")
	}
	return keys
}()

// goreleaserAbsent are key paths a later goreleaser reads and v2.13.1 does
// not have: v2.13.1 loads the config strictly and fails on an unknown key
// (https://github.com/goreleaser/goreleaser/blob/v2.13.1/pkg/config/load.go#L59),
// and its DockerV2 has no hooks (config.go#L1064-L1079 there).
var goreleaserAbsent = map[string]bool{"dockers_v2.hooks": true}

// readGoreleaser parses the goreleaser config under root.
func readGoreleaser(root string) (goreleaserFile, error) {
	data, err := os.ReadFile(filepath.Join(root, parityGoreleaser))
	if err != nil {
		return goreleaserFile{}, err
	}
	return parseGoreleaser(string(data)), nil
}

// goreleaserFrame is one open key, by the column it starts at, with the
// sequence items read under it so far. A refused key takes the lines
// under it with it.
type goreleaserFrame struct {
	indent, items int
	key           string
	refused       bool
}

// goreleaserParser is parseGoreleaser's state: the open keys, and the key
// of a value read on its own line just before, which a line continuing
// that value is refused by.
type goreleaserParser struct {
	g     goreleaserFile
	stack []goreleaserFrame
	last  string
}

// parseGoreleaser reads the goreleaser config text.
func parseGoreleaser(text string) goreleaserFile {
	var p goreleaserParser
	yamlStrayBreaks(text, p.refuse)
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		if t := strings.TrimSpace(lines[i]); t != "" && !strings.HasPrefix(t, "#") && t != "---" {
			i = p.line(lines, i, t)
		}
	}
	for _, v := range p.g.values {
		switch goreleaserKeys[v.keys] {
		case goreleaserLine:
			line := strings.ReplaceAll(v.value, "\n", " ")
			if !goreleaserWrapped(line) {
				p.refuse(v.line, "%s: a template control action; goreleaser runs one branch, and the gates read only "+
					"a command wrapped whole in one {{ if }}...{{ end }}", v.keys[strings.LastIndexByte(v.keys, '.')+1:])
				continue
			}
			if text := goreleaserCommand(line); text != "" {
				p.g.runs = append(p.g.runs, workflowRun{line: v.line, text: text})
			}
		case goreleaserWord:
			p.g.words = append(p.g.words, workflowRun{line: v.line, text: v.value})
		}
	}
	return p.g
}

// line reads lines[i], trimmed, and returns the index of the last line it
// took: a block scalar takes the lines under it.
func (p *goreleaserParser) line(lines []string, i int, trimmed string) int {
	n, col := i+1, indent(lines[i])
	item := trimmed == "-" || strings.HasPrefix(trimmed, "- ")
	p.pop(col, item)
	if len(p.stack) > 0 && p.stack[len(p.stack)-1].refused {
		return i
	}
	last := p.last
	p.last = ""
	rest, keyCol := trimmed, col
	if item {
		if len(p.stack) == 0 {
			p.refuse(n, "an item outside any key; the config is a mapping")
			return i
		}
		p.stack[len(p.stack)-1].items++
		rest = strings.TrimLeft(trimmed[1:], " \t")
		keyCol = col + len(trimmed) - len(rest)
	}
	if quoted := workflowQuotedKey.FindStringSubmatch(trimmed); quoted != nil {
		p.refuse(n, "%s: a quoted key; write the key plain", quoted[1])
		p.stack = append(p.stack, goreleaserFrame{indent: keyCol, key: quoted[1], refused: true})
		return i
	}
	m := workflowKeyLine.FindStringSubmatch(trimmed)
	if m == nil {
		return p.notKey(lines, i, item, rest, last, col)
	}
	key, raw := m[2], strings.TrimSpace(m[3])
	if key == "<<" {
		p.refuse(n, "<<: a merge key; write the mapping out")
		p.stack = append(p.stack, goreleaserFrame{indent: keyCol, key: key, refused: true})
		return i
	}
	if path := strings.Join(append(p.keys(), key), "."); goreleaserAbsent[path] {
		p.refuse(n, "%s: goreleaser %s, which %s runs, has no such key", path, goreleaserChecked, releaseWorkflow)
		p.stack = append(p.stack, goreleaserFrame{indent: keyCol, key: key, refused: true})
		return i
	}
	if value, _ := workflowValue(raw); value == "" {
		p.stack = append(p.stack, goreleaserFrame{indent: keyCol, key: key})
		return i
	}
	return p.value(lines, i, append(p.keys(), key), raw, keyCol)
}

// notKey reads lines[i], a line holding no key, with rest the line after
// an item's dash and last the key of a value read on the line before: a
// scalar item, the value of the key it is an item of, or a line refused.
func (p *goreleaserParser) notKey(lines []string, i int, item bool, rest, last string, col int) int {
	switch {
	case !item:
		name := last
		if name == "" && len(p.stack) > 0 {
			name = p.stack[len(p.stack)-1].key
		}
		p.refuse(i+1, "%s: a value continued on the next line; write it on one line or as a block scalar", cmp.Or(name, "a line"))
	case rest == "" || rest == "-" || strings.HasPrefix(rest, "- "):
		p.refuse(i+1, "%s: an empty item or an item holding a sequence; write the item's value on its dash",
			p.stack[len(p.stack)-1].key)
	default:
		return p.value(lines, i, p.keys(), rest, col)
	}
	return i
}

// value reads raw, the value on line i of the key path keys, and returns
// the index of the last line it took. col is the column the value's key,
// or its item's dash, starts at, past which a block scalar's lines sit.
// It refuses a value it would misread: an anchor, an alias or a tag, a
// flow mapping, a flow sequence of anything but plain words or one where
// a command goes, since a command may hold a comma, and a quoted value
// with an escape in it.
func (p *goreleaserParser) value(lines []string, i int, keys []string, raw string, col int) int {
	n, path, name := i+1, strings.Join(keys, "."), keys[len(keys)-1]
	value, _ := workflowValue(raw)
	items := p.items()
	switch {
	case strings.ContainsRune("&*!", rune(raw[0])):
		p.refuse(n, "%s: an anchor, an alias or a tag; write the value out", name)
	case raw[0] == '{' || (raw[0] == '[' && (goreleaserKeys[path] != 0 || !goreleaserPlainList.MatchString(value))):
		p.refuse(n, "%s: a flow collection; write it as a block, one key or item a line", name)
	case workflowQuoteEscape(raw):
		p.refuse(n, "%s: a quoted value with an escape in it; write it unquoted or as a block", name)
	case raw[0] == '[':
		for j, word := range strings.FieldsFunc(value[1:len(value)-1], func(r rune) bool { return r == ',' }) {
			p.record(path, append(items, j), n, strings.TrimSpace(word))
		}
	case workflowBlockScalar.MatchString(value):
		body, end := workflowScalarBody(lines, i, col)
		text := workflowJoin(body)
		if strings.HasPrefix(value, ">") {
			text = strings.ReplaceAll(text, "\n", " ")
		}
		p.record(path, items, n+1, text)
		return end
	default:
		p.record(path, items, n, value)
		p.last = name
	}
	return i
}

// goreleaserPlainList is a one-line flow sequence of plain words, such as
// `ids: [pipedrive-mcp]`.
var goreleaserPlainList = regexp.MustCompile(`^\[[^\[\]{}"'#]*\]$`)

func (p *goreleaserParser) record(keys string, items []int, line int, value string) {
	p.g.values = append(p.g.values, goreleaserValue{keys: keys, items: slices.Clone(items), line: line, value: value})
}

// pop closes the frames a line at col ends: each deeper, and one at col
// unless the line is an item of it. A sequence may sit at its key's own
// column (https://yaml.org/spec/1.2.2/#823-block-nodes).
func (p *goreleaserParser) pop(col int, item bool) {
	for n := len(p.stack); n > 0 && (p.stack[n-1].indent > col || p.stack[n-1].indent == col && !item); n-- {
		p.stack = p.stack[:n-1]
	}
}

// keys is the open keys' path.
func (p *goreleaserParser) keys() []string {
	out := make([]string, 0, len(p.stack)+1)
	for _, f := range p.stack {
		out = append(out, f.key)
	}
	return out
}

// items is the index of the item open under each sequence on the path.
func (p *goreleaserParser) items() []int {
	var out []int
	for _, f := range p.stack {
		if f.items > 0 {
			out = append(out, f.items-1)
		}
	}
	return out
}

func (p *goreleaserParser) refuse(n int, format string, a ...any) {
	p.g.refused = append(p.g.refused, fmt.Sprintf("%s:%d: %s", parityGoreleaser, n, fmt.Sprintf(format, a...)))
}

// goreleaserTemplate is a template action, which goreleaser renders before
// it splits a command line into words
// (https://github.com/goreleaser/goreleaser/blob/v2.13.1/internal/pipe/universalbinary/universalbinary.go#L108-L114;
// a before hook at internal/pipe/before/before.go#L29-L33, a build's at
// internal/pipe/build/build.go#L191-L200 and a publisher's at
// internal/exec/exec.go#L176-L184 there).
var goreleaserTemplate = regexp.MustCompile(`\{\{.*?\}\}`)

// goreleaserControl is an action that renders no text of its own: a
// wrapper's if, else, range, with or end, a break, a continue or a
// comment (https://pkg.go.dev/text/template#hdr-Actions).
var goreleaserControl = regexp.MustCompile(`^\{\{-?\s*(?:(if|else|end|range|with|break|continue)\b|/\*)`)

// goreleaserWrapped reports whether a command line's control actions are
// none, or one if or with that opens the line and the end that closes it.
// goreleaserCommand reads every branch as one line, and goreleaser runs
// only the branch it renders, so any other control action hides what runs.
func goreleaserWrapped(line string) bool {
	line = strings.TrimSpace(line)
	opened := false
	for _, at := range goreleaserTemplate.FindAllStringIndex(line, -1) {
		m := goreleaserControl.FindStringSubmatch(line[at[0]:at[1]])
		switch {
		case m == nil:
		case !opened && at[0] == 0 && (m[1] == "if" || m[1] == "with"):
			opened = true
		case opened && at[1] == len(line) && m[1] == "end":
			return true
		default:
			return false
		}
	}
	return !opened
}

// goreleaserPlaceholder is the word an action that is a word of its own
// reads as.
const goreleaserPlaceholder = "{{}}"

// goreleaserCommand is a command with its template actions rendered
// without a context, its words kept whole. A wrapper's actions disappear,
// so `{{ if not .Prerelease }}go run x{{ end }}` runs go. Other actions in
// a row that is a word of its own read as goreleaserPlaceholder, and
// inside a word as nothing: what they render is no word a gate reads.
func goreleaserCommand(text string) string {
	text = goreleaserTemplate.ReplaceAllStringFunc(text, func(action string) string {
		if goreleaserControl.MatchString(action) {
			return ""
		}
		return action
	})
	var b strings.Builder
	last := 0
	actions := goreleaserTemplate.FindAllStringIndex(text, -1)
	for i := 0; i < len(actions); i++ {
		start, end := actions[i][0], actions[i][1]
		for i+1 < len(actions) && actions[i+1][0] == end {
			i++
			end = actions[i][1]
		}
		b.WriteString(text[last:start])
		if workflowBlankAt(text, start-1) && workflowBlankAt(text, end) {
			b.WriteString(goreleaserPlaceholder)
		}
		last = end
	}
	b.WriteString(text[last:])
	return strings.TrimSpace(b.String())
}

// at is every value at one of the key paths, in file order.
func (g goreleaserFile) at(keys ...string) []goreleaserValue {
	var out []goreleaserValue
	for _, v := range g.values {
		if slices.Contains(keys, v.keys) {
			out = append(out, v)
		}
	}
	return out
}

// first is the first value at the key path, or "".
func (g goreleaserFile) first(keys string) string {
	if found := g.at(keys); len(found) > 0 {
		return found[0].value
	}
	return ""
}

// has reports whether the config sets a value at or under the key path.
func (g goreleaserFile) has(keys string) bool {
	return slices.ContainsFunc(g.values, func(v goreleaserValue) bool {
		return v.keys == keys || strings.HasPrefix(v.keys, keys+".")
	})
}

// builds is each build's values, by build, in file order.
func (g goreleaserFile) builds() [][]goreleaserValue {
	var out [][]goreleaserValue
	index := map[int]int{}
	for _, v := range g.values {
		if !strings.HasPrefix(v.keys, "builds.") || len(v.items) == 0 {
			continue
		}
		i, ok := index[v.items[0]]
		if !ok {
			i = len(out)
			index[v.items[0]] = i
			out = append(out, nil)
		}
		out[i] = append(out[i], v)
	}
	return out
}

// goreleaserMatrix is every goos-goarch pair the builds build, sorted:
// for each build, the cross product of its goos and goarch lists, less its
// ignore entries. A build that leaves either list to goreleaser's default,
// sets targets, or ignores by any key but goos and goarch is refused:
// this reads none of those.
func goreleaserMatrix(g goreleaserFile) ([]string, error) {
	var pairs, problems []string
	for _, build := range g.builds() {
		built, refused := goreleaserBuildPairs(build)
		pairs, problems = append(pairs, built...), append(problems, refused...)
	}
	if len(problems) == 0 && len(pairs) == 0 {
		problems = append(problems, parityGoreleaser+" builds no goos-goarch pair; this check is reading the wrong shape")
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		return nil, fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	slices.Sort(pairs)
	return slices.Compact(pairs), nil
}

// goreleaserBuildPairs is the goos-goarch pairs one build's values build,
// and what in them goreleaserMatrix refuses.
func goreleaserBuildPairs(build []goreleaserValue) (pairs, problems []string) {
	var oses, arches []string
	// ignored is each ignore entry's goos and goarch, by entry.
	ignored := map[int][2]string{}
	for _, v := range build {
		switch {
		case v.keys == "builds.goos":
			oses = append(oses, v.value)
		case v.keys == "builds.goarch":
			arches = append(arches, v.value)
		case v.keys == "builds.ignore.goos" && len(v.items) > 1:
			ignored[v.items[1]] = [2]string{v.value, ignored[v.items[1]][1]}
		case v.keys == "builds.ignore.goarch" && len(v.items) > 1:
			ignored[v.items[1]] = [2]string{ignored[v.items[1]][0], v.value}
		case v.keys == "builds.targets" || strings.HasPrefix(v.keys, "builds.ignore."):
			problems = append(problems, fmt.Sprintf("%s:%d: %s is set, which this gate does not read; "+
				"list the build's goos and goarch, and ignore a pair by its goos and goarch", parityGoreleaser, v.line, v.keys))
		}
	}
	if len(oses) == 0 || len(arches) == 0 {
		problems = append(problems, fmt.Sprintf("%s:%d: a build that leaves goos or goarch to goreleaser's default, "+
			"which this gate does not read", parityGoreleaser, build[0].line))
	}
	skip := map[string]bool{}
	for _, pair := range ignored {
		if pair[0] == "" || pair[1] == "" {
			problems = append(problems, fmt.Sprintf("%s:%d: an ignore entry without both goos and goarch, "+
				"which this gate does not read", parityGoreleaser, build[0].line))
		}
		skip[pair[0]+"-"+pair[1]] = true
	}
	return goreleaserCross(oses, arches, skip), problems
}

// goreleaserCross is each goos by each goarch, less the pairs skip holds.
func goreleaserCross(oses, arches []string, skip map[string]bool) []string {
	var pairs []string
	for _, o := range oses {
		for _, a := range arches {
			if !skip[o+"-"+a] {
				pairs = append(pairs, o+"-"+a)
			}
		}
	}
	return pairs
}

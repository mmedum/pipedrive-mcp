package main

import (
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/template"
)

// The release's archives: which pairs .goreleaser.yaml builds, what the
// archives are called, and every place that names them.
//
// `gates mcpb` runs these beside the manifest checks. The packer and the
// registry entry do not, so a release is packed and published as before.

// goreleaserPath is the release configuration these checks read.
const goreleaserPath = ".goreleaser.yaml"

// docVersion is the version the docs write an archive name with.
const docVersion = "X.Y.Z"

// releaseMatrix is the family's release: every goos-goarch pair
// .goreleaser.yaml builds, an archive each, and, for a pair the bundle
// stages no binary of its own, the pair whose binary runs there. Windows
// on arm64 runs the amd64 binary under emulation; its archive is for
// someone installing from the release page.
var releaseMatrix = []struct{ pair, runs string }{
	{pair: "darwin-amd64"},
	{pair: "darwin-arm64"},
	{pair: "linux-amd64"},
	{pair: "linux-arm64"},
	{pair: "windows-amd64"},
	{pair: "windows-arm64", runs: "windows-amd64"},
}

// releasePairs is every pair releaseMatrix builds, sorted.
func releasePairs() []string {
	out := make([]string, 0, len(releaseMatrix))
	for _, p := range releaseMatrix {
		out = append(out, p.pair)
	}
	slices.Sort(out)
	return out
}

// releaseProblems holds .goreleaser.yaml under root to the family's
// archives and the bundle, and every file that names an archive to the
// names it renders. archives is how many archives the release carries.
func releaseProblems(root string) (problems []string, archives int, err error) {
	raw, err := os.ReadFile(filepath.Join(root, goreleaserPath)) //nolint:gosec // a repository path from a constant
	if err != nil {
		return nil, 0, err
	}
	blocks, err := goreleaserBlocks(withoutCommentLines(string(raw)))
	if err != nil {
		return nil, 0, err
	}
	built, err := goreleaserMatrix(blocks["builds"])
	if err != nil {
		return nil, 0, err
	}
	project, names, err := releaseArchives(blocks, built, docVersion)
	if err != nil {
		return nil, 0, err
	}
	module, err := modulePath(root)
	if err != nil {
		return nil, 0, err
	}

	problems = releaseArchiveProblems(built)
	problems = append(problems, mcpbMatrixProblems(bundleFiles)...)
	_, universal := blocks["universal_binaries"]
	problems = append(problems, stagedBuildProblems(bundleFiles, built, universal)...)
	problems = append(problems, familyNameProblems(names, moduleName(module), docVersion)...)

	named, err := archiveNamingFiles(root, raw)
	if err != nil {
		return nil, 0, err
	}
	mentions := 0
	for _, f := range named {
		found := archiveMentions(f.data, project)
		mentions += len(found)
		problems = append(problems, staleNameProblems(f.name, found, names)...)
		problems = append(problems, oldFormProblems(f.name, f.data, project, found)...)
	}
	// The floor. docs/release.md must name every archive, so a scan
	// finding fewer mentions than that is not reading the names at all,
	// and would pass every stale one.
	if mentions < len(names) {
		return nil, 0, fmt.Errorf("found %d archive names across %d files, fewer than the %d archives "+
			"docs/release.md must name; the scan is reading nothing", mentions, len(named), len(names))
	}
	// The two files that must name every archive, each by its own rule.
	for file, check := range map[string]func(string, map[string]string) []string{
		"docs/release.md":               releaseDocProblems,
		".github/workflows/release.yml": attestationProblems,
	} {
		i := slices.IndexFunc(named, func(f namedFile) bool { return f.name == file })
		if i < 0 {
			problems = append(problems, fmt.Sprintf("%s is missing, and it is what names every archive", file))
			continue
		}
		problems = append(problems, check(named[i].data, names)...)
	}
	slices.Sort(problems)
	return problems, len(names), nil
}

// withoutCommentLines drops every line that is only a YAML comment, so a
// key named in a comment is not read as the key.
func withoutCommentLines(text string) string {
	var kept []string
	for _, l := range strings.Split(text, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

var (
	goosKey   = regexp.MustCompile(`(?m)^\s*goos:\s*$`)
	goarchKey = regexp.MustCompile(`(?m)^\s*goarch:\s*$`)
	ignoreKey = regexp.MustCompile(`(?m)^\s*ignore:`)
	listItem  = regexp.MustCompile(`^-\s*([a-z0-9]+)$`)
)

// goreleaserMatrix is every goos-goarch pair the release builds, read
// from builds, the builds block of .goreleaser.yaml without its comment
// lines: the cross product of its two lists, sorted.
//
// Regular expressions rather than a YAML parser: a parser is a
// dependency this program does not otherwise need, and the file it reads
// is one this repository writes. So it refuses what it does not read
// rather than read it wrong: a second build, and an ignore list, which
// the family's matrix, every pair of its lists, never needs. The floor
// is what keeps a changed layout from reading as an empty matrix.
func goreleaserMatrix(builds string) ([]string, error) {
	if n := len(goosKey.FindAllStringIndex(builds, -1)); n > 1 {
		return nil, fmt.Errorf("%s has %d builds; this check reads one", goreleaserPath, n)
	}
	if ignoreKey.MatchString(builds) {
		return nil, fmt.Errorf("%s's build has an ignore list; the family builds every pair of its goos "+
			"and goarch lists, and this check reads no ignore", goreleaserPath)
	}
	oses := listAfter(builds, goosKey)
	arches := listAfter(builds, goarchKey)
	if len(oses) < 2 || len(arches) < 2 {
		return nil, fmt.Errorf("%s parsed as %d platforms and %d architectures; the build block's shape "+
			"has changed and this check is reading nothing", goreleaserPath, len(oses), len(arches))
	}
	var out []string
	for _, o := range oses {
		for _, a := range arches {
			out = append(out, o+"-"+a)
		}
	}
	slices.Sort(out)
	return out, nil
}

// listAfter is the YAML list right after the first match of key: the
// run of `- value` items, stopping at the first line that is not one.
func listAfter(text string, key *regexp.Regexp) []string {
	loc := key.FindStringIndex(text)
	if loc == nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(text[loc[1]:], "\n") {
		// withoutComment, because listItem anchors on the end of the
		// line: a trailing comment on an entry would otherwise end the
		// list early.
		entry := withoutComment(line)
		if entry == "" {
			continue
		}
		m := listItem.FindStringSubmatch(entry)
		if m == nil {
			break
		}
		out = append(out, m[1])
	}
	return out
}

// topLevelKey matches a top-level block's header.
var topLevelKey = regexp.MustCompile(`(?m)^([a-z_]+):`)

// goreleaserBlocks splits text, .goreleaser.yaml without its comment
// lines, into its top-level blocks, each after its key. It refuses a
// file missing a block these checks read, so a changed layout cannot
// read as a release with nothing to hold.
func goreleaserBlocks(text string) (map[string]string, error) {
	locs := topLevelKey.FindAllStringSubmatchIndex(text, -1)
	blocks := map[string]string{}
	for i, loc := range locs {
		end := len(text)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		blocks[text[loc[2]:loc[3]]] = text[loc[1]:end]
	}
	for _, key := range []string{"project_name", "builds", "archives"} {
		if _, ok := blocks[key]; !ok {
			return nil, fmt.Errorf("%s has no top-level %s block; its shape has changed and these "+
				"checks are reading nothing", goreleaserPath, key)
		}
	}
	return blocks, nil
}

var (
	formatOverride = regexp.MustCompile(`-\s*goos:\s*([a-z0-9]+)\s*\n\s*formats:\s*\[\s*([a-z0-9.]+)\s*\]`)
	formatsList    = regexp.MustCompile(`formats:\s*\[\s*([a-z0-9.]+)\s*\]`)
)

// releaseArchives is the file name of every archive the release carries
// for version, keyed by pair: the archives block's name_template,
// rendered for each built pair, and its format, the default or a goos
// override. project is the config's project_name.
func releaseArchives(blocks map[string]string, built []string, version string) (
	project string, names map[string]string, err error,
) {
	project = strings.Trim(strings.TrimSpace(blocks["project_name"]), `"'`)
	archives := blocks["archives"]
	name := strings.Join(blockScalar(archives, "name_template"), " ")
	if name == "" {
		return "", nil, fmt.Errorf("%s's archives block has no name_template; goreleaser's default adds "+
			"suffixes this check does not render", goreleaserPath)
	}
	tmpl, err := template.New("name_template").Parse(name)
	if err != nil {
		return "", nil, fmt.Errorf("%s's archive name_template: %w", goreleaserPath, err)
	}
	overrides := map[string]string{}
	for _, m := range formatOverride.FindAllStringSubmatch(archives, -1) {
		overrides[m[1]] = m[2]
	}
	defaults := formatsList.FindAllStringSubmatch(formatOverride.ReplaceAllString(archives, ""), -1)
	if len(defaults) != 1 {
		return "", nil, fmt.Errorf("%s's archives block names %d default formats, not one", goreleaserPath,
			len(defaults))
	}
	names = map[string]string{}
	for _, pair := range built {
		goos, goarch, _ := strings.Cut(pair, "-")
		var b strings.Builder
		fields := struct{ ProjectName, Version, Os, Arch string }{project, version, goos, goarch}
		if err := tmpl.Execute(&b, fields); err != nil {
			return "", nil, fmt.Errorf("%s's archive name_template: %w", goreleaserPath, err)
		}
		format := defaults[0][1]
		if o, ok := overrides[goos]; ok {
			format = o
		}
		names[pair] = b.String() + "." + format
	}
	return project, names, nil
}

// blockScalar is the value of the first `key:` in text: the inline value
// without its quotes, or the lines of a block scalar (`>-`, `|` and the
// like) indented under it, trimmed. Nil when the key is absent.
func blockScalar(text, key string) []string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		name, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.TrimPrefix(name, "- ") != key {
			continue
		}
		value = strings.TrimSpace(value)
		if value != "" && !strings.ContainsAny(value[:1], ">|") {
			return []string{strings.Trim(value, `"'`)}
		}
		var out []string
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" {
				continue
			}
			if indentOf(next) <= indentOf(line) {
				break
			}
			out = append(out, strings.TrimSpace(next))
		}
		return out
	}
	return nil
}

// familyNameProblems holds the archive names to the family's,
// <name>_<version>_<os>_<arch>, a zip on Windows and a tar.gz elsewhere,
// where name is the module's.
func familyNameProblems(names map[string]string, name, version string) []string {
	var problems []string
	for _, pair := range slices.Sorted(maps.Keys(names)) {
		goos, goarch, _ := strings.Cut(pair, "-")
		format := "tar.gz"
		if goos == "windows" {
			format = "zip"
		}
		want := fmt.Sprintf("%s_%s_%s_%s.%s", name, version, goos, goarch, format)
		if names[pair] != want {
			problems = append(problems, fmt.Sprintf("%s names the %s archive %s; the family's name is %s",
				goreleaserPath, pair, names[pair], want))
		}
	}
	return problems
}

// majorSuffix is a module path's /vN.
var majorSuffix = regexp.MustCompile(`/v\d+$`)

// moduleName is the last element of a module path, less any /vN.
func moduleName(module string) string {
	return path.Base(majorSuffix.ReplaceAllString(module, ""))
}

// releaseArchiveProblems holds the build matrix to the family's
// archives, releaseMatrix, both ways. A pair left out is a machine whose
// owner has nothing to download; a bundle reaching it under emulation
// does not help someone installing from an archive. A pair beyond it is
// an archive no sibling's release carries.
func releaseArchiveProblems(built []string) []string {
	family := releasePairs()
	var problems []string
	for _, pair := range family {
		if !slices.Contains(built, pair) {
			problems = append(problems, fmt.Sprintf("%s does not build %s, so the release carries no %s "+
				"archive; the family's releases carry %s", goreleaserPath, pair, strings.ReplaceAll(pair, "-", "_"),
				strings.Join(family, ", ")))
		}
	}
	for _, pair := range built {
		if !slices.Contains(family, pair) {
			problems = append(problems, fmt.Sprintf("%s builds %s, which is not one of the family's "+
				"archives, %s", goreleaserPath, pair, strings.Join(family, ", ")))
		}
	}
	return problems
}

// buildDir matches the build a staged binary's glob reads: goreleaser
// names a build's directory <id>_<goos>_<goarch>[_<variant>].
var buildDir = regexp.MustCompile(`\*([a-z]+)_([a-z0-9]+)\*/`)

// universalBuild is what build reports for the macOS universal binary,
// which is not in the goos × goarch matrix.
const universalBuild = "darwin-all"

// build is the goreleaser goos-goarch pair a staged binary is copied
// from, read off its glob; universalBuild for the universal binary,
// whose directory is <id>_darwin_all, and "" for a file this
// repository ships itself or a glob that names no build.
func (f staged) build() string {
	switch {
	case f.glob == "":
		return ""
	case strings.Contains(f.glob, "*universal*"):
		return universalBuild
	}
	if m := buildDir.FindStringSubmatch(f.glob); m != nil {
		return m[1] + "-" + m[2]
	}
	return ""
}

// mcpbMatrixProblems holds the Desktop bundle to releaseMatrix: every
// pair the release builds runs a binary the bundle stages, its own, the
// universal one on macOS, or the one releaseMatrix says it runs under
// emulation.
func mcpbMatrixProblems(files []staged) []string {
	staged := func(pair string) bool {
		goos, _, _ := strings.Cut(pair, "-")
		return slices.ContainsFunc(files, func(f staged) bool {
			b := f.build()
			return b == pair || (b == universalBuild && goos == "darwin")
		})
	}
	var problems []string
	for _, p := range releaseMatrix {
		if !staged(p.pair) && (p.runs == "" || !staged(p.runs)) {
			problems = append(problems, fmt.Sprintf("the release builds %s and the Desktop bundle stages no "+
				"binary that runs there", p.pair))
		}
	}
	return problems
}

// stagedBuildProblems holds every binary the bundle stages to a build
// .goreleaser.yaml makes: a pair it builds, or its universal binary.
func stagedBuildProblems(files []staged, built []string, universal bool) []string {
	var problems []string
	for _, f := range files {
		if f.glob == "" {
			continue
		}
		switch b := f.build(); {
		case b == "":
			problems = append(problems, fmt.Sprintf("the bundle stages %s from %s, which names no goreleaser "+
				"build", f.path, f.glob))
		case b == universalBuild:
			if !universal {
				problems = append(problems, fmt.Sprintf("the bundle stages %s from the universal binary, and "+
					"%s has no universal_binaries block", f.path, goreleaserPath))
			}
		case !slices.Contains(built, b):
			problems = append(problems, fmt.Sprintf("the bundle stages %s from %s, which %s does not build",
				f.path, b, goreleaserPath))
		}
	}
	return problems
}

// namedFile is one file that may name an archive.
type namedFile struct{ name, data string }

// versionTemplate is the version in a goreleaser template, which a file
// name in the release footer is written with.
var versionTemplate = regexp.MustCompile(`\{\{\s*\.Version\s*\}\}`)

// archiveNamingFiles is every file that names an archive for someone to
// use: the Markdown at the root, under docs/ and under .github/, the
// workflows, and
// .goreleaser.yaml itself, raw, with its version template written as
// docVersion. CHANGELOG.md and audit/ are left out: they record the
// names releases shipped with.
func archiveNamingFiles(root string, goreleaser []byte) ([]namedFile, error) {
	var docs []string
	for _, pattern := range []string{"*.md", "docs/*.md", ".github/*.md"} {
		found, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			return nil, err
		}
		docs = append(docs, found...)
	}
	var out []namedFile
	for _, doc := range docs {
		rel, err := filepath.Rel(root, doc)
		if err != nil {
			return nil, err
		}
		if rel == "CHANGELOG.md" {
			continue
		}
		data, err := os.ReadFile(doc) //nolint:gosec // a path under the repository, from a glob
		if err != nil {
			return nil, err
		}
		out = append(out, namedFile{name: filepath.ToSlash(rel), data: string(data)})
	}
	workflows, err := readWorkflows(root)
	if err != nil {
		return nil, err
	}
	for _, w := range workflows {
		out = append(out, namedFile{name: ".github/workflows/" + w.name, data: w.data})
	}
	return append(out, namedFile{
		name: goreleaserPath,
		data: versionTemplate.ReplaceAllString(string(goreleaser), docVersion),
	}), nil
}

// archiveMentions is every name in text that starts with the project's
// name and ends the way an archive does, in the shape it has now or any
// it has had: a glob, a dash or an underscore after the project's name.
func archiveMentions(text, project string) []string {
	mention := regexp.MustCompile(regexp.QuoteMeta(project) + `[-_][A-Za-z0-9._*-]*?\.(?:tar\.gz|zip)\b`)
	return mention.FindAllString(text, -1)
}

// oldForm is the shape the archives had before 1.3.0: a dash and the
// version after the project's name, the version written as one, as
// X.Y.Z or as <version>, with or without an extension.
func oldForm(project string) *regexp.Regexp {
	return regexp.MustCompile(regexp.QuoteMeta(project) + `-(?:X\.Y\.Z|<version>|v?\d+\.\d+\.\d+)[A-Za-z0-9._<>-]*`)
}

// oldFormProblems is each name in text, file's contents, written in
// oldForm that archiveMentions has not already found, such as a name in
// prose with no extension.
func oldFormProblems(file, text, project string, found []string) []string {
	var problems []string
	for _, m := range oldForm(project).FindAllString(text, -1) {
		if slices.ContainsFunc(found, func(a string) bool { return strings.HasPrefix(m, a) }) {
			continue
		}
		problems = append(problems, fmt.Sprintf("%s names %s, the archives' old form; the family's names are "+
			"%s_<version>_<os>_<arch>", file, m, project))
	}
	return problems
}

// staleNameProblems is each of found, the archive names file mentions,
// that is not an archive the release carries: a name, or a glob, that
// matches none of names, rendered for docVersion.
func staleNameProblems(file string, found []string, names map[string]string) []string {
	var problems []string
	for _, m := range found {
		matches := false
		for _, name := range names {
			if ok, _ := path.Match(m, name); ok {
				matches = true
				break
			}
		}
		if !matches {
			problems = append(problems, fmt.Sprintf("%s names %s, which is no archive the release carries; "+
				"they are %s", file, m, strings.Join(slices.Sorted(maps.Values(names)), ", ")))
		}
	}
	return problems
}

// releaseDocProblems holds docs/release.md's text to naming every
// archive, so the after-the-workflow check covers each one the release
// should carry.
func releaseDocProblems(text string, names map[string]string) []string {
	var problems []string
	for _, name := range slices.Sorted(maps.Values(names)) {
		if !strings.Contains(text, name) {
			problems = append(problems, fmt.Sprintf("docs/release.md does not name %s, so the release check "+
				"never looks for it", name))
		}
	}
	return problems
}

// attestationProblems holds release.yml's text to attesting every
// archive: each name matches a subject-path glob, so a renamed archive
// cannot ship with no build provenance.
func attestationProblems(text string, names map[string]string) []string {
	globs := blockScalar(text, "subject-path")
	if len(globs) == 0 {
		return []string{"release.yml has no subject-path, so no archive is attested"}
	}
	var problems []string
	for _, name := range slices.Sorted(maps.Values(names)) {
		attested := slices.ContainsFunc(globs, func(g string) bool {
			ok, _ := path.Match(strings.TrimPrefix(g, "dist/"), name)
			return ok
		})
		if !attested {
			problems = append(problems, fmt.Sprintf("release.yml's subject-path matches no %s, so that "+
				"archive ships with no build provenance", name))
		}
	}
	return problems
}

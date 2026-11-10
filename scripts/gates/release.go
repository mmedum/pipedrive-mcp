package main

import (
	"cmp"
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

// docVersion is the version the docs write an archive name with.
const docVersion = "X.Y.Z"

// releaseMatrix is the release: every goos-goarch pair
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

// releaseProblems holds .goreleaser.yaml under root to releaseMatrix's
// archives and the bundle, every file that names an archive to the names
// it renders, and the release's build provenance to every file it
// publishes. archives is how many archives the release carries.
func releaseProblems(root string) (problems []string, archives int, err error) {
	raw, err := os.ReadFile(filepath.Join(root, parityGoreleaser)) //nolint:gosec // a repository path from a constant
	if err != nil {
		return nil, 0, err
	}
	flows, err := workflowsUnder(root)
	if err != nil {
		return nil, 0, err
	}
	cfg := parseGoreleaser(string(raw))
	built, err := goreleaserMatrix(cfg)
	if err != nil {
		return nil, 0, err
	}
	project, names, err := releaseArchives(cfg, built, docVersion)
	if err != nil {
		return nil, 0, err
	}
	module, err := modulePath(root)
	if err != nil {
		return nil, 0, err
	}

	problems = slices.Clone(cfg.refused)
	problems = append(problems, releaseArchiveProblems(built)...)
	problems = append(problems, mcpbMatrixProblems(bundleFiles)...)
	problems = append(problems, stagedBuildProblems(bundleFiles, built, cfg.has("universal_binaries"))...)
	problems = append(problems, archiveNameProblems(names, moduleName(module), docVersion)...)

	named, err := archiveNamingFiles(root, raw, flows)
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
			"%s must name; the scan is reading nothing", mentions, len(named), len(names), releaseDoc)
	}
	// docs/release.md must name every archive.
	if i := slices.IndexFunc(named, func(f namedFile) bool { return f.name == releaseDoc }); i < 0 {
		problems = append(problems, fmt.Sprintf("%s is missing, and it is what names every archive", releaseDoc))
	} else {
		problems = append(problems, releaseDocProblems(named[i].data, names)...)
	}
	subjects, wiring := releaseSubjects(cfg, names)
	problems = append(problems, wiring...)
	problems = append(problems, attestationProblems(flows, subjects)...)
	slices.Sort(problems)
	return problems, len(names), nil
}

// releaseArchives is the file name of every archive the release carries
// for version, keyed by pair: the one archive's name_template, rendered
// for each built pair, and its format, the default or a goos override's.
// project is the config's project_name. It refuses an archive it cannot
// render one name a pair for: a second archive, a name_template left to
// goreleaser's default, which adds suffixes, a format list of more than
// one, and the singular format keys, which it does not read.
func releaseArchives(cfg goreleaserFile, built []string, version string) (
	project string, names map[string]string, err error,
) {
	project = cfg.first("project_name")
	if project == "" {
		return "", nil, fmt.Errorf("%s sets no project_name; its shape has changed and these checks are reading nothing",
			parityGoreleaser)
	}
	format, overrides, err := releaseFormats(cfg)
	if err != nil {
		return "", nil, err
	}
	templates := cfg.at("archives.name_template")
	if len(templates) != 1 {
		return "", nil, fmt.Errorf("%s sets %d archive name_templates, not one; goreleaser's default adds "+
			"suffixes this check does not render", parityGoreleaser, len(templates))
	}
	tmpl, err := template.New("name_template").Parse(templates[0].value)
	if err != nil {
		return "", nil, fmt.Errorf("%s's archive name_template: %w", parityGoreleaser, err)
	}
	names = map[string]string{}
	for _, pair := range built {
		goos, goarch, _ := strings.Cut(pair, "-")
		var b strings.Builder
		fields := struct{ ProjectName, Version, Os, Arch string }{project, version, goos, goarch}
		if err := tmpl.Execute(&b, fields); err != nil {
			return "", nil, fmt.Errorf("%s's archive name_template: %w", parityGoreleaser, err)
		}
		names[pair] = b.String() + "." + releaseFormat(goos, format, overrides)
	}
	return project, names, nil
}

// releaseOverride is one format_overrides item: its goos and its format.
type releaseOverride struct{ goos, format string }

// releaseFormat is the format goreleaser v2.13.1 packs goos in: the first
// override whose goos goos starts with, or the default
// (https://github.com/goreleaser/goreleaser/blob/v2.13.1/internal/pipe/archive/archive.go#L321-L337).
func releaseFormat(goos, format string, overrides []releaseOverride) string {
	for _, o := range overrides {
		if strings.HasPrefix(goos, o.goos) {
			return o.format
		}
	}
	return format
}

// releaseFormats is the one archive's one default format and its
// overrides, in order, each with one goos and one format. goreleaser
// ignores an override with no goos or no formats, so each is refused.
func releaseFormats(cfg goreleaserFile) (format string, overrides []releaseOverride, err error) {
	for _, v := range cfg.values {
		switch {
		case v.keys == "archives.format" || v.keys == "archives.format_overrides.format":
			return "", nil, fmt.Errorf("%s:%d: %s is the singular key, which this check does not read; write formats",
				parityGoreleaser, v.line, v.keys)
		case strings.HasPrefix(v.keys, "archives.") && len(v.items) > 0 && v.items[0] > 0:
			return "", nil, fmt.Errorf("%s:%d: a second archive; this check reads one", parityGoreleaser, v.line)
		}
	}
	defaults := cfg.at("archives.formats")
	if len(defaults) != 1 {
		return "", nil, fmt.Errorf("%s's archive names %d default formats, not one", parityGoreleaser, len(defaults))
	}
	// Each override's goos, formats and first line, by the override's index.
	goos, formats, lines := map[int]string{}, map[int][]string{}, map[int]int{}
	for _, v := range cfg.at("archives.format_overrides.goos", "archives.format_overrides.formats") {
		if len(v.items) < 2 {
			return "", nil, fmt.Errorf("%s:%d: %s is not an item of a list of overrides", parityGoreleaser, v.line, v.keys)
		}
		i := v.items[1]
		if _, seen := lines[i]; !seen {
			lines[i] = v.line
		}
		if v.keys == "archives.format_overrides.goos" {
			goos[i] = v.value
		} else {
			formats[i] = append(formats[i], v.value)
		}
	}
	for _, i := range slices.Sorted(maps.Keys(lines)) {
		switch {
		case goos[i] == "":
			return "", nil, fmt.Errorf("%s:%d: a format_overrides item sets formats and no goos, which goreleaser ignores; "+
				"name its goos or drop it", parityGoreleaser, lines[i])
		case len(formats[i]) != 1:
			return "", nil, fmt.Errorf("%s's archive overrides %s with %d formats, not one", parityGoreleaser, goos[i], len(formats[i]))
		}
		overrides = append(overrides, releaseOverride{goos[i], formats[i][0]})
	}
	return defaults[0].value, overrides, nil
}

// archiveNameProblems holds the archive names to
// <name>_<version>_<os>_<arch>, a zip on Windows and a tar.gz elsewhere,
// where name is the module's: goreleaser's own form, which the sibling
// servers' releases (google-chat-mcp, google-docs-mcp) also use.
func archiveNameProblems(names map[string]string, name, version string) []string {
	var problems []string
	for _, pair := range slices.Sorted(maps.Keys(names)) {
		goos, goarch, _ := strings.Cut(pair, "-")
		format := "tar.gz"
		if goos == "windows" {
			format = "zip"
		}
		want := fmt.Sprintf("%s_%s_%s_%s.%s", name, version, goos, goarch, format)
		if names[pair] != want {
			problems = append(problems, fmt.Sprintf("%s names the %s archive %s; archives are named <name>_<version>_<os>_<arch>, so this one is %s",
				parityGoreleaser, pair, names[pair], want))
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

// releaseArchiveProblems holds the build matrix to releaseMatrix's
// archives, both ways. A pair left out is a machine whose
// owner has nothing to download; a bundle reaching it under emulation
// does not help someone installing from an archive. A pair beyond it is
// an archive no sibling server's release carries.
func releaseArchiveProblems(built []string) []string {
	matrix := releasePairs()
	var problems []string
	for _, pair := range matrix {
		if !slices.Contains(built, pair) {
			problems = append(problems, fmt.Sprintf("%s does not build %s, so the release carries no %s "+
				"archive; a release carries %s", parityGoreleaser, pair, strings.ReplaceAll(pair, "-", "_"),
				strings.Join(matrix, ", ")))
		}
	}
	for _, pair := range built {
		if !slices.Contains(matrix, pair) {
			problems = append(problems, fmt.Sprintf("%s builds %s, which is not one of the archives "+
				"a release carries, %s", parityGoreleaser, pair, strings.Join(matrix, ", ")))
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
					"%s has no universal_binaries block", f.path, parityGoreleaser))
			}
		case !slices.Contains(built, b):
			problems = append(problems, fmt.Sprintf("the bundle stages %s from %s, which %s does not build",
				f.path, b, parityGoreleaser))
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
// docVersion. CHANGELOG.md is left out: it records the names releases
// shipped with.
func archiveNamingFiles(root string, goreleaser []byte, flows []workflowFile) ([]namedFile, error) {
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
		if rel == changelogFile {
			continue
		}
		data, err := os.ReadFile(doc) //nolint:gosec // a path under the repository, from a glob
		if err != nil {
			return nil, err
		}
		out = append(out, namedFile{name: filepath.ToSlash(rel), data: string(data)})
	}
	for i := range flows {
		out = append(out, namedFile{name: flows[i].path, data: flows[i].text})
	}
	return append(out, namedFile{
		name: parityGoreleaser,
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
	return regexp.MustCompile(regexp.QuoteMeta(project) + `-(?:X\.Y\.Z|<version>|v?` + semverPattern + `)[A-Za-z0-9._<>-]*`)
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
		problems = append(problems, fmt.Sprintf("%s names %s, the archives' old form; they are named "+
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
			problems = append(problems, fmt.Sprintf("%s does not name %s, so the release check "+
				"never looks for it", releaseDoc, name))
		}
	}
	return problems
}

// releaseDoc is the release checklist, which names every archive.
const releaseDoc = "docs/release.md"

// releaseSubjects is every file the release publishes that its build
// provenance names, under goreleaser's dist directory: each archive, the
// checksum file, and the bundle the mcpb-pack hook writes. The SBOMs and
// the checksum file's signature are published and not held. problems are
// the config's, where it names no such file, does not upload the bundle,
// or uploads by a release.extra_files glob that matches none of them.
func releaseSubjects(cfg goreleaserFile, names map[string]string) (subjects, problems []string) {
	dist := cmp.Or(cfg.first("dist"), "dist")
	for _, name := range slices.Sorted(maps.Values(names)) {
		subjects = append(subjects, path.Join(dist, name))
	}
	if sums := cfg.first("checksum.name_template"); sums != "" {
		subjects = append(subjects, path.Join(dist, sums))
	} else {
		problems = append(problems, parityGoreleaser+" names no checksum.name_template, so the checksum file "+
			"the provenance names is unknown")
	}
	// The bundle is the file the hook's OUT names, not release.extra_files'
	// glob: release.yml attests dist/pipedrive-mcp_*.mcpb, narrower than
	// ./dist/*.mcpb, and a subject is matched by its whole name.
	bundle, uploads := releaseBundle(cfg), cfg.at("release.extra_files.glob")
	switch {
	case bundle == "":
		problems = append(problems, parityGoreleaser+" runs no `gates mcpb-pack DIST VERSION OUT`, so the bundle the "+
			"release publishes is unknown")
	case !slices.ContainsFunc(uploads, func(g goreleaserValue) bool { return releaseGlobMatches(g.value, bundle) }):
		problems = append(problems, fmt.Sprintf("%s's release.extra_files uploads no %s, the bundle mcpb-pack writes",
			parityGoreleaser, bundle))
	default:
		subjects = append(subjects, bundle)
	}
	for _, g := range uploads {
		if !slices.ContainsFunc(subjects, func(s string) bool { return releaseGlobMatches(g.value, s) }) {
			problems = append(problems, fmt.Sprintf("%s:%d: release.extra_files uploads %s, which matches no file "+
				"the release writes: an archive, the checksum file or the bundle", parityGoreleaser, g.line, g.value))
		}
	}
	return subjects, problems
}

// releaseBundle is the file a release command running mcpb-pack writes,
// its OUT with the version template written as docVersion, or "". It
// reads the command as written, not a run: a run renders the version
// inside a word as nothing, and a subject-path glob may need its shape.
func releaseBundle(cfg goreleaserFile) string {
	bundle := ""
	for _, v := range cfg.values {
		if goreleaserKeys[v.keys] != goreleaserLine {
			continue
		}
		command := goreleaserCommand(versionTemplate.ReplaceAllString(strings.ReplaceAll(v.value, "\n", " "), docVersion))
		for _, c := range parityGateCalls(shellSplit(command)) {
			if c.name == "mcpb-pack" && len(c.args) == 3 {
				bundle = path.Clean(c.args[2])
			}
		}
	}
	return bundle
}

// releaseGlobMatches reports whether a goreleaser file glob, such as
// ./dist/*.mcpb, matches file, a slash-separated path.
func releaseGlobMatches(glob, file string) bool {
	ok, _ := path.Match(path.Clean(glob), file)
	return ok
}

// attestationProblems holds release.yml's build provenance to naming every
// subject. The attest step sits in the job that runs goreleaser release,
// whose dist/ holds the files: another job, or a workflow it calls, can be
// skipped while the release ships, as publish-mcp.yml is on a prerelease.
// That job's token needs id-token: write and attestations: write to sign
// and store the provenance
// (https://github.com/actions/attest/blob/508db95dd578ae2727ebd6217d5ba78e4fbda05d/README.md#L64-L76).
// A subject-path entry is a glob matched against the whole path, so
// dist/unused/*.mcpb names no bundle in dist. actions/attest v4.2.1, which
// attest-build-provenance v4.2.2 runs, splits subject-path at commas and
// newlines and trims each glob
// (https://github.com/actions/attest/blob/508db95dd578ae2727ebd6217d5ba78e4fbda05d/src/subject.ts#L303),
// and fails only when no glob matches at all, so an entry that matches
// nothing is silent there
// (https://github.com/actions/attest/blob/508db95dd578ae2727ebd6217d5ba78e4fbda05d/src/subject.ts#L150).
func attestationProblems(flows []workflowFile, subjects []string) []string {
	w, ok := workflowNamed(flows, releaseWorkflow)
	if !ok {
		return []string{"no " + releaseWorkflow + ", so nothing attests what the release publishes"}
	}
	jobs := releaseJobs(w)
	if len(jobs) == 0 {
		return []string{"no job of " + releaseWorkflow + " runs goreleaser-action with args starting `release`, " +
			"so no attest step is held to the files the release publishes"}
	}
	var entries, problems []string
	attested := false
	for _, job := range jobs {
		steps := slices.DeleteFunc(slices.Clone(w.steps), func(s workflowStep) bool {
			return s.job != job || s.action() != "actions/attest-build-provenance"
		})
		if len(steps) == 0 {
			problems = append(problems, fmt.Sprintf("%s: job %s runs goreleaser release and has no attest step, "+
				"so no file the release publishes carries build provenance", w.path, job))
			continue
		}
		attested = true
		for _, scope := range []string{"id-token", "attestations"} {
			if !w.grantsWrite(job, scope) {
				problems = append(problems, fmt.Sprintf("%s: job %s attests without %s: write, which actions/attest needs "+
					"to sign and store the provenance; grant it in the job's permissions, or the workflow's when the job sets none",
					w.path, job, scope))
			}
		}
		for _, s := range steps {
			found, wrong := attestEntries(w.path, s)
			entries, problems = append(entries, found...), append(problems, wrong...)
		}
	}
	if !attested {
		return problems
	}
	for _, subject := range subjects {
		if !slices.ContainsFunc(entries, func(e string) bool { ok, _ := path.Match(e, subject); return ok }) {
			problems = append(problems, fmt.Sprintf("%s's subject-path matches no %s, so it ships with no build provenance",
				releaseWorkflow, subject))
		}
	}
	return problems
}

// releaseJobs are the jobs of w that publish the release, in file order:
// each runs goreleaser-action with args starting `release`.
func releaseJobs(w workflowFile) []string {
	var jobs []string
	for _, s := range w.steps {
		args, _ := s.input("args")
		words := strings.Fields(args.value)
		if s.action() == "goreleaser/goreleaser-action" && len(words) > 0 && words[0] == "release" && !slices.Contains(jobs, s.job) {
			jobs = append(jobs, s.job)
		}
	}
	return jobs
}

// attestEntries is each subject-path entry of the attest step s in file,
// and the entries it cannot take: a step with no subject-path, and an
// entry holding a space.
func attestEntries(file string, s workflowStep) (entries, problems []string) {
	v, ok := s.input("subject-path")
	if !ok {
		return nil, []string{fmt.Sprintf("%s:%d: attests no subject-path", file, s.line)}
	}
	for _, entry := range strings.FieldsFunc(v.value, func(r rune) bool { return r == '\n' || r == ',' }) {
		entry = strings.TrimSpace(entry)
		switch {
		case entry == "":
		case strings.ContainsAny(entry, " \t"):
			problems = append(problems, fmt.Sprintf("%s:%d: subject-path entry %q holds a space, which the "+
				"action reads as part of one path; write one path a line", file, v.line, entry))
		default:
			entries = append(entries, entry)
		}
	}
	return entries, problems
}

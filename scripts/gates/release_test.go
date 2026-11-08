package main

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The six archives under their <name>_<version>_<os>_<arch> names. Every
// case breaks one thing and asserts the sentence that names it.

// sixArchives is what the release carries, written out: the names the
// docs give, for version X.Y.Z.
var sixArchives = map[string]string{
	"darwin-amd64":  "pipedrive-mcp_X.Y.Z_darwin_amd64.tar.gz",
	"darwin-arm64":  "pipedrive-mcp_X.Y.Z_darwin_arm64.tar.gz",
	"linux-amd64":   "pipedrive-mcp_X.Y.Z_linux_amd64.tar.gz",
	"linux-arm64":   "pipedrive-mcp_X.Y.Z_linux_arm64.tar.gz",
	"windows-amd64": "pipedrive-mcp_X.Y.Z_windows_amd64.zip",
	"windows-arm64": "pipedrive-mcp_X.Y.Z_windows_arm64.zip",
}

func repoGoreleaser(t *testing.T) goreleaserFile {
	t.Helper()
	return parseGoreleaser(string(repoFile(t, parityGoreleaser)))
}

func TestTheReleaseMatrixIsTheSix(t *testing.T) {
	want := []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64", "windows-arm64"}
	if got := releasePairs(); !slices.Equal(got, want) {
		t.Errorf("releasePairs = %v, want %v", got, want)
	}
}

func TestGoreleaserBuildsTheSix(t *testing.T) {
	got, err := goreleaserMatrix(repoGoreleaser(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64", "windows-arm64"}
	if !slices.Equal(got, want) {
		t.Errorf("the matrix parsed as %v, want %v", got, want)
	}
}

func TestReleaseArchiveProblems(t *testing.T) {
	const matrix = "darwin-amd64, darwin-arm64, linux-amd64, linux-arm64, windows-amd64, windows-arm64"
	for _, tc := range []struct {
		name  string
		built []string
		want  []string
	}{
		{"the six", releasePairs(), nil},
		{
			"windows-arm64 left out",
			[]string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64"},
			[]string{".goreleaser.yaml does not build windows-arm64, so the release carries no windows_arm64 " +
				"archive; a release carries " + matrix},
		},
		{
			"a seventh",
			append(releasePairs(), "windows-386"),
			[]string{".goreleaser.yaml builds windows-386, which is not one of the archives a release carries, " + matrix},
		},
	} {
		if got := releaseArchiveProblems(tc.built); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestTheArchivesAreNamedNameVersionOSArch(t *testing.T) {
	cfg := repoGoreleaser(t)
	built, err := goreleaserMatrix(cfg)
	if err != nil {
		t.Fatal(err)
	}
	project, names, err := releaseArchives(cfg, built, docVersion)
	if err != nil {
		t.Fatal(err)
	}
	if project != "pipedrive-mcp" {
		t.Errorf("project = %q, want pipedrive-mcp", project)
	}
	if !maps.Equal(names, sixArchives) {
		t.Errorf("the archives render as %v, want %v", names, sixArchives)
	}
	if got := archiveNameProblems(names, "pipedrive-mcp", docVersion); len(got) > 0 {
		t.Errorf("the committed names are refused: %q", got)
	}
}

// archivesText is a goreleaser config with this repository's archive, the
// name template given, and extra appended under the archive.
func archivesText(name, extra string) string {
	return "project_name: pipedrive-mcp\narchives:\n  - id: default\n    formats: [tar.gz]\n" +
		"    format_overrides:\n      - goos: windows\n        formats: [zip]\n    name_template: >-\n      " + name +
		"\n    files:\n      - LICENSE\n" + extra
}

func archivesConfig(name, extra string) goreleaserFile {
	return parseGoreleaser(archivesText(name, extra))
}

func TestReleaseArchivesRendersTheTemplate(t *testing.T) {
	_, names, err := releaseArchives(archivesConfig("{{ .ProjectName }}-{{ .Version }}-{{ .Os }}-{{ .Arch }}", ""),
		[]string{"linux-arm64", "windows-amd64"}, "1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"linux-arm64":   "pipedrive-mcp-1.2.0-linux-arm64.tar.gz",
		"windows-amd64": "pipedrive-mcp-1.2.0-windows-amd64.zip",
	}
	if !maps.Equal(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
}

// A goos takes the format of the first override whose goos it starts
// with, as goreleaser v2.13.1 picks one, and the default otherwise.
func TestReleaseArchivesTakeTheFirstOverrideTheGoosStartsWith(t *testing.T) {
	cfg := parseGoreleaser(textLines(
		"project_name: x",
		"archives:",
		"  - formats: [tar.gz]",
		"    format_overrides:",
		"      - goos: win",
		"        formats: [zip]",
		"      - goos: windows",
		"        formats: [tar.xz]",
		"    name_template: '{{ .Os }}'",
	))
	_, names, err := releaseArchives(cfg, []string{"linux-amd64", "windows-amd64"}, "1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"linux-amd64": "linux.tar.gz", "windows-amd64": "windows.zip"}; !maps.Equal(names, want) {
		t.Errorf("names = %v, want %v", names, want)
	}
}

// An archive the check cannot render one name a pair for is refused by
// name, never rendered wrong.
func TestReleaseArchivesRefusesWhatItCannotRender(t *testing.T) {
	const name = "{{ .ProjectName }}"
	for _, tc := range []struct {
		name string
		cfg  goreleaserFile
		want string
	}{
		{"no project_name", parseGoreleaser("archives:\n  - formats: [tar.gz]\n    name_template: x\n"),
			".goreleaser.yaml sets no project_name; its shape has changed and these checks are reading nothing"},
		{"no name_template", parseGoreleaser("project_name: x\narchives:\n  - formats: [tar.gz]\n"),
			".goreleaser.yaml sets 0 archive name_templates, not one; goreleaser's default adds suffixes this " +
				"check does not render"},
		{"a field this does not render", archivesConfig("{{ .ProjectName }}_{{ .Arm }}", ""),
			"can't evaluate field Arm"},
		{"two default formats", archivesConfig(name, "    formats: [zip]\n"),
			".goreleaser.yaml's archive names 2 default formats, not one"},
		{"an override with two formats", parseGoreleaser(strings.Replace(archivesText(name, ""), "        formats: [zip]\n",
			"        formats: [zip]\n      - goos: darwin\n        formats: [zip, tar.gz]\n", 1)),
			".goreleaser.yaml's archive overrides darwin with 2 formats, not one"},
		{"an override with a goos and no formats", parseGoreleaser(strings.Replace(archivesText(name, ""), "        formats: [zip]\n",
			"        formats: [zip]\n      - goos: darwin\n", 1)),
			".goreleaser.yaml's archive overrides darwin with 0 formats, not one"},
		{"an override with formats and no goos", parseGoreleaser(strings.Replace(archivesText(name, ""), "        formats: [zip]\n",
			"        formats: [zip]\n      - formats: [tar.xz]\n", 1)),
			".goreleaser.yaml:8: a format_overrides item sets formats and no goos, which goreleaser ignores; name its goos or drop it"},
		{"the singular format key", archivesConfig(name, "    format: zip\n"),
			".goreleaser.yaml:12: archives.format is the singular key, which this check does not read; write formats"},
		{"a second archive", archivesConfig(name, "  - id: source\n    meta: true\n"),
			".goreleaser.yaml:12: a second archive; this check reads one"},
	} {
		_, _, err := releaseArchives(tc.cfg, []string{"linux-amd64"}, "1.2.0")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error saying %q", tc.name, err, tc.want)
		}
	}
}

func TestArchiveNameProblems(t *testing.T) {
	got := archiveNameProblems(map[string]string{
		"linux-amd64":   "pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz",
		"windows-arm64": "pipedrive-mcp_X.Y.Z_windows_arm64.tar.gz",
		"darwin-arm64":  "pipedrive-mcp_X.Y.Z_darwin_arm64.tar.gz",
	}, "pipedrive-mcp", docVersion)
	want := []string{
		".goreleaser.yaml names the linux-amd64 archive pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz; " +
			"archives are named <name>_<version>_<os>_<arch>, so this one is pipedrive-mcp_X.Y.Z_linux_amd64.tar.gz",
		".goreleaser.yaml names the windows-arm64 archive pipedrive-mcp_X.Y.Z_windows_arm64.tar.gz; " +
			"archives are named <name>_<version>_<os>_<arch>, so this one is pipedrive-mcp_X.Y.Z_windows_arm64.zip",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestModuleNameDropsTheMajorVersion(t *testing.T) {
	for module, want := range map[string]string{
		"github.com/mmedum/pipedrive-mcp":    "pipedrive-mcp",
		"github.com/mmedum/pipedrive-mcp/v2": "pipedrive-mcp",
	} {
		if got := moduleName(module); got != want {
			t.Errorf("moduleName(%q) = %q, want %q", module, got, want)
		}
	}
}

func TestArchiveMentions(t *testing.T) {
	text := "an SBOM per archive (e.g.\n`pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz.cdx.json`), the bundle " +
		"`pipedrive-mcp_X.Y.Z.mcpb`, the server `pipedrive-mcp.exe`,\n" +
		"            dist/pipedrive-mcp_*.zip\n" +
		"gh attestation verify pipedrive-mcp_X.Y.Z_windows_arm64.zip --owner mmedum\n"
	want := []string{"pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz", "pipedrive-mcp_*.zip", "pipedrive-mcp_X.Y.Z_windows_arm64.zip"}
	if got := archiveMentions(text, "pipedrive-mcp"); !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStaleNameProblems(t *testing.T) {
	got := staleNameProblems("README.md", []string{
		"pipedrive-mcp_X.Y.Z_linux_amd64.tar.gz",
		"pipedrive-mcp_*.zip",
		"pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz",
		"pipedrive-mcp-*.tar.gz",
		"pipedrive-mcp_1.2.0_linux_amd64.tar.gz",
	}, sixArchives)
	const they = "they are pipedrive-mcp_X.Y.Z_darwin_amd64.tar.gz, pipedrive-mcp_X.Y.Z_darwin_arm64.tar.gz, " +
		"pipedrive-mcp_X.Y.Z_linux_amd64.tar.gz, pipedrive-mcp_X.Y.Z_linux_arm64.tar.gz, " +
		"pipedrive-mcp_X.Y.Z_windows_amd64.zip, pipedrive-mcp_X.Y.Z_windows_arm64.zip"
	want := []string{
		"README.md names pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz, which is no archive the release carries; " + they,
		"README.md names pipedrive-mcp-*.tar.gz, which is no archive the release carries; " + they,
		"README.md names pipedrive-mcp_1.2.0_linux_amd64.tar.gz, which is no archive the release carries; " + they,
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestReleaseDocProblems(t *testing.T) {
	all := slices.Collect(maps.Values(sixArchives))
	if got := releaseDocProblems(strings.Join(all, "\n"), sixArchives); len(got) > 0 {
		t.Errorf("a doc naming all six: %q", got)
	}
	doc := strings.ReplaceAll(strings.Join(all, "\n"), "pipedrive-mcp_X.Y.Z_windows_arm64.zip", "")
	want := []string{"docs/release.md does not name pipedrive-mcp_X.Y.Z_windows_arm64.zip, so the release " +
		"check never looks for it"}
	if got := releaseDocProblems(doc, sixArchives); !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// sixSubjects is what the release's provenance names: the six archives,
// the checksum file and the bundle, each under dist.
var sixSubjects = func() []string {
	out := []string{}
	for _, name := range slices.Sorted(maps.Values(sixArchives)) {
		out = append(out, "dist/"+name)
	}
	return append(out, "dist/checksums.txt", "dist/pipedrive-mcp_X.Y.Z.mcpb")
}()

// Every file the release publishes beside its SBOMs and signature is a
// subject of its build provenance, read from the config: the archives
// under goreleaser's dist, the checksum file it names, and the bundle the
// mcpb-pack hook writes, which release.extra_files uploads.
func TestReleaseSubjectsAreWhatTheReleasePublishes(t *testing.T) {
	committed := string(repoFile(t, parityGoreleaser))
	subjects, problems := releaseSubjects(parseGoreleaser(committed), sixArchives)
	if !slices.Equal(subjects, sixSubjects) || problems != nil {
		t.Errorf("the committed config: subjects %q, problems %q; want %q and none", subjects, problems, sixSubjects)
	}
	for _, tc := range []struct {
		name, old, new string
		subjects       []string
		problems       []string
	}{
		{"another dist directory", "project_name: pipedrive-mcp\n", "project_name: pipedrive-mcp\ndist: out\n",
			[]string{"out/pipedrive-mcp_X.Y.Z_darwin_amd64.tar.gz", "out/pipedrive-mcp_X.Y.Z_darwin_arm64.tar.gz",
				"out/pipedrive-mcp_X.Y.Z_linux_amd64.tar.gz", "out/pipedrive-mcp_X.Y.Z_linux_arm64.tar.gz",
				"out/pipedrive-mcp_X.Y.Z_windows_amd64.zip", "out/pipedrive-mcp_X.Y.Z_windows_arm64.zip",
				"out/checksums.txt", "dist/pipedrive-mcp_X.Y.Z.mcpb"}, nil},
		{"another checksum file", "  name_template: checksums.txt\n", "  name_template: SHA256SUMS\n",
			append(slices.Clone(sixSubjects[:6]), "dist/SHA256SUMS", "dist/pipedrive-mcp_X.Y.Z.mcpb"), nil},
		{"no checksum file named", "  name_template: checksums.txt\n", "",
			append(slices.Clone(sixSubjects[:6]), "dist/pipedrive-mcp_X.Y.Z.mcpb"),
			[]string{".goreleaser.yaml names no checksum.name_template, so the checksum file the provenance names is unknown"}},
		{"no bundle packed", "post: go run ./scripts/gates mcpb-pack dist {{ .Version }} dist/pipedrive-mcp_{{ .Version }}.mcpb", "post: true",
			sixSubjects[:7],
			[]string{".goreleaser.yaml runs no `gates mcpb-pack DIST VERSION OUT`, so the bundle the release publishes is unknown",
				".goreleaser.yaml:158: release.extra_files uploads ./dist/*.mcpb, which matches no file the release writes: " +
					"an archive, the checksum file or the bundle"}},
		{"an upload that matches nothing the release writes", "    - glob: ./dist/*.mcpb\n  # The notes", "    - glob: ./dist/*.mcpb\n    - glob: ./dist/*.sbom\n  # The notes",
			sixSubjects,
			[]string{".goreleaser.yaml:159: release.extra_files uploads ./dist/*.sbom, which matches no file the release writes: " +
				"an archive, the checksum file or the bundle"}},
		// A wrapper's actions disappear, so a bundle packed only on a
		// release is the bundle.
		{"a bundle packed only when the release is not a snapshot",
			"post: go run ./scripts/gates mcpb-pack dist {{ .Version }} dist/pipedrive-mcp_{{ .Version }}.mcpb",
			"post: \"{{ if not .IsSnapshot }}go run ./scripts/gates mcpb-pack dist {{ .Version }} dist/pipedrive-mcp_{{ .Version }}.mcpb{{ end }}\"",
			sixSubjects, nil},
		{"a bundle the release does not upload", "release:\n  draft: false\n  prerelease: auto\n  mode: replace\n", "release:\n  draft: false\n  prerelease: auto\n  mode: replace\n  extra_files:\n    - glob: ./dist/*.zip\n",
			sixSubjects[:7],
			[]string{".goreleaser.yaml's release.extra_files uploads no dist/pipedrive-mcp_X.Y.Z.mcpb, the bundle mcpb-pack writes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(committed, tc.old) {
				t.Fatalf("the config does not say %q", tc.old)
			}
			cfg := strings.Replace(committed, tc.old, tc.new, 1)
			if tc.name == "a bundle the release does not upload" {
				cfg = strings.Replace(cfg, "  extra_files:\n    - glob: ./dist/*.mcpb\n  # The notes", "  # The notes", 1)
			}
			subjects, problems := releaseSubjects(parseGoreleaser(cfg), sixArchives)
			if !slices.Equal(subjects, tc.subjects) || !slices.Equal(problems, tc.problems) {
				t.Errorf("subjects %q, problems %q; want %q, %q", subjects, problems, tc.subjects, tc.problems)
			}
		})
	}
}

// The provenance names every subject by a subject-path entry matched
// against the whole path, entries split as actions/attest splits them.
func TestAttestationProblems(t *testing.T) {
	committed, err := workflowsUnder(parityRepoRoot)
	if err != nil {
		t.Fatal(err)
	}
	release, ok := workflowNamed(committed, releaseWorkflow)
	if !ok {
		t.Fatal("no release.yml")
	}
	if got := attestationProblems([]workflowFile{release}, sixSubjects); got != nil {
		t.Errorf("the committed workflow: %q", got)
	}
	// head is release.yml up to its goreleaser job's steps, which publish
	// the release, with the token the attest step needs.
	const head = "permissions:\n  id-token: write\n  attestations: write\njobs:\n  r:\n    steps:\n" +
		"      - uses: goreleaser/goreleaser-action@x\n        with:\n          args: release --clean\n"
	const attest = "      - uses: actions/attest-build-provenance@x\n"
	step := func(with string) []workflowFile {
		return []workflowFile{parseWorkflow("release.yml", head+attest+with)}
	}
	const all = "dist/pipedrive-mcp_*.tar.gz\n            dist/pipedrive-mcp_*.zip\n            dist/pipedrive-mcp_*.mcpb\n            dist/checksums.txt\n"
	full := attest + "        with:\n          subject-path: |\n            " + all
	const noAttest = "release.yml: job r runs goreleaser release and has no attest step, so no file the release publishes carries build provenance"
	const needs = ", which actions/attest needs to sign and store the provenance; grant it in the job's permissions, or the workflow's when the job sets none"
	for _, tc := range []struct {
		name  string
		flows []workflowFile
		want  []string
	}{
		{"one entry a line", step("        with:\n          subject-path: |\n            " + all), nil},
		{"entries split at commas", step("        with:\n          subject-path: dist/pipedrive-mcp_*.tar.gz,dist/pipedrive-mcp_*.zip, dist/*.mcpb,dist/checksums.txt\n"), nil},
		{"the bundle in another folder", step("        with:\n          subject-path: |\n            " +
			strings.Replace(all, "dist/pipedrive-mcp_*.mcpb", "dist/unused/*.mcpb", 1)),
			[]string{"release.yml's subject-path matches no dist/pipedrive-mcp_X.Y.Z.mcpb, so it ships with no build provenance"}},
		{"the zips outside dist", step("        with:\n          subject-path: |\n            " +
			strings.Replace(all, "dist/pipedrive-mcp_*.zip", "pipedrive-mcp_*.zip", 1)),
			[]string{
				"release.yml's subject-path matches no dist/pipedrive-mcp_X.Y.Z_windows_amd64.zip, so it ships with no build provenance",
				"release.yml's subject-path matches no dist/pipedrive-mcp_X.Y.Z_windows_arm64.zip, so it ships with no build provenance",
			}},
		{"a signature where the checksum file goes", step("        with:\n          subject-path: |\n            " +
			strings.Replace(all, "dist/checksums.txt", "dist/checksums.txt.bundle", 1)),
			[]string{"release.yml's subject-path matches no dist/checksums.txt, so it ships with no build provenance"}},
		{"two entries a space apart", step("        with:\n          subject-path: dist/pipedrive-mcp_*.tar.gz dist/pipedrive-mcp_*.zip\n"),
			[]string{
				`release.yml:12: subject-path entry "dist/pipedrive-mcp_*.tar.gz dist/pipedrive-mcp_*.zip" holds a space, which the action reads as part of one path; write one path a line`,
				"release.yml's subject-path matches no dist/checksums.txt, so it ships with no build provenance",
				"release.yml's subject-path matches no dist/pipedrive-mcp_X.Y.Z.mcpb, so it ships with no build provenance",
				"release.yml's subject-path matches no dist/pipedrive-mcp_X.Y.Z_darwin_amd64.tar.gz, so it ships with no build provenance",
				"release.yml's subject-path matches no dist/pipedrive-mcp_X.Y.Z_darwin_arm64.tar.gz, so it ships with no build provenance",
				"release.yml's subject-path matches no dist/pipedrive-mcp_X.Y.Z_linux_amd64.tar.gz, so it ships with no build provenance",
				"release.yml's subject-path matches no dist/pipedrive-mcp_X.Y.Z_linux_arm64.tar.gz, so it ships with no build provenance",
				"release.yml's subject-path matches no dist/pipedrive-mcp_X.Y.Z_windows_amd64.zip, so it ships with no build provenance",
				"release.yml's subject-path matches no dist/pipedrive-mcp_X.Y.Z_windows_arm64.zip, so it ships with no build provenance",
			}},
		{"subject-path only in a comment", step("        with:\n          # subject-path: dist/*\n          show-summary: true\n"),
			append([]string{"release.yml:10: attests no subject-path"}, func() []string {
				var out []string
				for _, s := range []string{"dist/checksums.txt", "dist/pipedrive-mcp_X.Y.Z.mcpb", "dist/pipedrive-mcp_X.Y.Z_darwin_amd64.tar.gz",
					"dist/pipedrive-mcp_X.Y.Z_darwin_arm64.tar.gz", "dist/pipedrive-mcp_X.Y.Z_linux_amd64.tar.gz", "dist/pipedrive-mcp_X.Y.Z_linux_arm64.tar.gz",
					"dist/pipedrive-mcp_X.Y.Z_windows_amd64.zip", "dist/pipedrive-mcp_X.Y.Z_windows_arm64.zip"} {
					out = append(out, "release.yml's subject-path matches no "+s+", so it ships with no build provenance")
				}
				return out
			}()...)},
		{"no attestation at all", []workflowFile{parseWorkflow("release.yml", head+"      - run: true\n")}, []string{noAttest}},
		// The attest step reads goreleaser's dist/, which only the job that
		// ran goreleaser holds, and a called workflow can be skipped.
		{"the attest step in another job", []workflowFile{parseWorkflow("release.yml",
			head+"  provenance:\n    needs: r\n    steps:\n"+full)}, []string{noAttest}},
		{"the attest step in a workflow release.yml calls", []workflowFile{
			parseWorkflow("release.yml", head+"  registry:\n    uses: ./.github/workflows/publish-mcp.yml\n"),
			parseWorkflow("publish-mcp.yml", "permissions:\n  id-token: write\n  attestations: write\njobs:\n  p:\n    steps:\n"+full),
		}, []string{noAttest}},
		{"no job runs goreleaser release", []workflowFile{parseWorkflow("release.yml", strings.Replace(head, "release --clean", "build --snapshot", 1)+full)},
			[]string{"no job of release.yml runs goreleaser-action with args starting `release`, so no attest step is held to the files the release publishes"}},
		{"no release.yml", nil, []string{"no release.yml, so nothing attests what the release publishes"}},
		// A job's permissions replace the workflow's: a scope it leaves out
		// is none.
		{"the job's own permissions leave attestations out", []workflowFile{parseWorkflow("release.yml",
			strings.Replace(head, "    steps:\n", "    permissions:\n      contents: write\n      id-token: write\n    steps:\n", 1)+full)},
			[]string{"release.yml: job r attests without attestations: write" + needs}},
		{"the job's permissions empty", []workflowFile{parseWorkflow("release.yml",
			strings.Replace(head, "    steps:\n", "    permissions: {}\n    steps:\n", 1)+full)},
			[]string{"release.yml: job r attests without id-token: write" + needs, "release.yml: job r attests without attestations: write" + needs}},
		{"no permissions anywhere", []workflowFile{parseWorkflow("release.yml",
			strings.Replace(head, "permissions:\n  id-token: write\n  attestations: write\n", "", 1)+full)},
			[]string{"release.yml: job r attests without id-token: write" + needs, "release.yml: job r attests without attestations: write" + needs}},
		{"write-all for the workflow", []workflowFile{parseWorkflow("release.yml",
			strings.Replace(head, "permissions:\n  id-token: write\n  attestations: write\n", "permissions: write-all\n", 1)+full)}, nil},
		{"read-all for the workflow, write-all for the job", []workflowFile{parseWorkflow("release.yml",
			strings.Replace(strings.Replace(head, "permissions:\n  id-token: write\n  attestations: write\n", "permissions: read-all\n", 1),
				"    steps:\n", "    permissions: write-all\n    steps:\n", 1)+full)}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := attestationProblems(tc.flows, sixSubjects)
			slices.Sort(got)
			want := slices.Sorted(slices.Values(tc.want))
			if !slices.Equal(got, want) {
				t.Errorf("problems:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
			}
		})
	}
}

// withoutFile is files less the row staged at path.
func withoutFile(files []staged, path string) []staged {
	return slices.DeleteFunc(slices.Clone(files), func(f staged) bool { return f.path == path })
}

func TestTheBundleReachesEveryPair(t *testing.T) {
	if got := mcpbMatrixProblems(bundleFiles); len(got) > 0 {
		t.Errorf("the committed bundle: %q", got)
	}
	for _, tc := range []struct {
		without string
		want    []string
	}{
		{"server/pipedrive-mcp.exe", []string{
			"the release builds windows-amd64 and the Desktop bundle stages no binary that runs there",
			"the release builds windows-arm64 and the Desktop bundle stages no binary that runs there",
		}},
		{"server/pipedrive-mcp-darwin", []string{
			"the release builds darwin-amd64 and the Desktop bundle stages no binary that runs there",
			"the release builds darwin-arm64 and the Desktop bundle stages no binary that runs there",
		}},
		{"server/pipedrive-mcp-linux-arm64", []string{
			"the release builds linux-arm64 and the Desktop bundle stages no binary that runs there",
		}},
	} {
		if got := mcpbMatrixProblems(withoutFile(bundleFiles, tc.without)); !slices.Equal(got, tc.want) {
			t.Errorf("without %s: %q, want %q", tc.without, got, tc.want)
		}
	}
}

func TestStagedBuildProblems(t *testing.T) {
	if got := stagedBuildProblems(bundleFiles, releasePairs(), true); len(got) > 0 {
		t.Errorf("the committed bundle: %q", got)
	}
	files := append(slices.Clone(bundleFiles),
		staged{path: "server/pipedrive-mcp-386.exe", glob: "dist/*windows_386*/pipedrive-mcp.exe"},
		staged{path: "server/pipedrive-mcp-any", glob: "dist/pipedrive-mcp"},
	)
	want := []string{
		"the bundle stages server/pipedrive-mcp-darwin from the universal binary, and .goreleaser.yaml has " +
			"no universal_binaries block",
		"the bundle stages server/pipedrive-mcp-386.exe from windows-386, which .goreleaser.yaml does not build",
		"the bundle stages server/pipedrive-mcp-any from dist/pipedrive-mcp, which names no goreleaser build",
	}
	if got := stagedBuildProblems(files, releasePairs(), false); !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// releaseRoot copies every file releaseProblems reads into a temporary
// repository, so a case can break one of them.
func releaseRoot(t *testing.T) string {
	t.Helper()
	root := copiedRoot(t)
	repo := parityRepoRoot
	var rels []string
	for _, pattern := range []string{"*.md", "docs/*.md", ".github/*.md", ".github/workflows/*.yml", ".github/workflows/*.yaml"} {
		found, err := filepath.Glob(filepath.Join(repo, pattern))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range found {
			rel, err := filepath.Rel(repo, f)
			if err != nil {
				t.Fatal(err)
			}
			rels = append(rels, filepath.ToSlash(rel))
		}
	}
	for _, rel := range append(rels, parityGoreleaser, "go.mod") {
		put(t, root, rel, string(repoFile(t, rel)))
	}
	return root
}

// edit replaces old with replacement in rel under root, every time, and
// fails when old is not there to replace.
func edit(t *testing.T, root, rel, old, replacement string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // a test's own temporary file
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), old) {
		t.Fatalf("%s does not say %q", rel, old)
	}
	put(t, root, rel, strings.ReplaceAll(string(body), old, replacement))
}

func TestReleaseProblemsRunsEveryCheck(t *testing.T) {
	const they = "they are pipedrive-mcp_X.Y.Z_darwin_amd64.tar.gz, pipedrive-mcp_X.Y.Z_darwin_arm64.tar.gz, " +
		"pipedrive-mcp_X.Y.Z_linux_amd64.tar.gz, pipedrive-mcp_X.Y.Z_linux_arm64.tar.gz, " +
		"pipedrive-mcp_X.Y.Z_windows_amd64.zip, pipedrive-mcp_X.Y.Z_windows_arm64.zip"
	for _, tc := range []struct {
		name             string
		file             string
		old, replacement string
		// add is the content of file, a new file, where the case needs a
		// file that names an archive rather than an edit to one that does.
		add string
		// only keeps the problems naming it, where one break sets off
		// several rules and the case is about one of them.
		only         string
		want         []string
		wantArchives int
	}{
		{name: "the committed release", wantArchives: 6},
		{
			name: "the CHANGELOG keeps the names releases shipped with", file: "CHANGELOG.md",
			old: "## [Unreleased]\n", replacement: "## [Unreleased]\n\n- pipedrive-mcp-1.2.0-linux-amd64.tar.gz\n",
			wantArchives: 6,
		},
		{
			name: "windows/arm64 left out", file: parityGoreleaser,
			old: "      - windows\n", replacement: "",
			only: "so the release carries no",
			want: []string{
				".goreleaser.yaml does not build windows-amd64, so the release carries no windows_amd64 archive; " +
					"a release carries darwin-amd64, darwin-arm64, linux-amd64, linux-arm64, windows-amd64, windows-arm64",
				".goreleaser.yaml does not build windows-arm64, so the release carries no windows_arm64 archive; " +
					"a release carries darwin-amd64, darwin-arm64, linux-amd64, linux-arm64, windows-amd64, windows-arm64",
			},
			wantArchives: 4,
		},
		{
			name: "the ignore list 1.2.0 shipped with", file: parityGoreleaser,
			old: "      - arm64\n", replacement: "      - arm64\n    ignore:\n      - goos: windows\n        goarch: arm64\n",
			only: "so the release carries no",
			want: []string{
				".goreleaser.yaml does not build windows-arm64, so the release carries no windows_arm64 archive; " +
					"a release carries darwin-amd64, darwin-arm64, linux-amd64, linux-arm64, windows-amd64, windows-arm64",
			},
			wantArchives: 5,
		},
		{
			name: "a shape the config reader refuses", file: parityGoreleaser,
			old: "  algorithm: sha256\n", replacement: "  'algorithm': sha256\n",
			want:         []string{".goreleaser.yaml:81: 'algorithm': a quoted key; write the key plain"},
			wantArchives: 6,
		},
		{
			name: "the old form in prose", file: "docs/fixture.md",
			add: "Unpack `pipedrive-mcp-<version>-<os>-<arch>`.\n",
			want: []string{
				"docs/fixture.md names pipedrive-mcp-<version>-<os>-<arch>, the archives' old form; " +
					"they are named pipedrive-mcp_<version>_<os>_<arch>",
			},
			wantArchives: 6,
		},
		{
			name: "a stale name under .github", file: ".github/fixture.md",
			add: "Attach pipedrive-mcp-X.Y.Z-linux-amd64 to the report.\n",
			want: []string{
				".github/fixture.md names pipedrive-mcp-X.Y.Z-linux-amd64, the archives' old form; " +
					"they are named pipedrive-mcp_<version>_<os>_<arch>",
			},
			wantArchives: 6,
		},
		{
			name: "a stale name at the root", file: "fixture.md",
			add: "Download pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz.\n",
			want: []string{
				"fixture.md names pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz, which is no archive the release carries; " + they,
			},
			wantArchives: 6,
		},
		{
			name: "a stale name in the release footer", file: parityGoreleaser,
			old: "pipedrive-mcp_{{ .Version }}_linux_amd64.tar.gz", replacement: "pipedrive-mcp-{{ .Version }}-linux-amd64.tar.gz",
			want: []string{
				".goreleaser.yaml names pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz, which is no archive the release carries; " + they,
			},
			wantArchives: 6,
		},
		{
			name: "the release check leaves an archive out", file: "docs/release.md",
			old: "pipedrive-mcp_X.Y.Z_windows_arm64.zip", replacement: "windows_arm64",
			want: []string{
				"docs/release.md does not name pipedrive-mcp_X.Y.Z_windows_arm64.zip, so the release check never looks for it",
			},
			wantArchives: 6,
		},
		{
			name: "the attestation misses the zips", file: ".github/workflows/release.yml",
			old: "            dist/pipedrive-mcp_*.zip\n", replacement: "",
			want: []string{
				"release.yml's subject-path matches no dist/pipedrive-mcp_X.Y.Z_windows_amd64.zip, so it ships with no build provenance",
				"release.yml's subject-path matches no dist/pipedrive-mcp_X.Y.Z_windows_arm64.zip, so it ships with no build provenance",
			},
			wantArchives: 6,
		},
		{
			name: "the old template", file: parityGoreleaser, only: "so this one is",
			old: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}", replacement: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}-{{ .Arch }}",
			want: []string{
				".goreleaser.yaml names the darwin-amd64 archive pipedrive-mcp_X.Y.Z_darwin-amd64.tar.gz; archives are named <name>_<version>_<os>_<arch>, so this one is pipedrive-mcp_X.Y.Z_darwin_amd64.tar.gz",
				".goreleaser.yaml names the darwin-arm64 archive pipedrive-mcp_X.Y.Z_darwin-arm64.tar.gz; archives are named <name>_<version>_<os>_<arch>, so this one is pipedrive-mcp_X.Y.Z_darwin_arm64.tar.gz",
				".goreleaser.yaml names the linux-amd64 archive pipedrive-mcp_X.Y.Z_linux-amd64.tar.gz; archives are named <name>_<version>_<os>_<arch>, so this one is pipedrive-mcp_X.Y.Z_linux_amd64.tar.gz",
				".goreleaser.yaml names the linux-arm64 archive pipedrive-mcp_X.Y.Z_linux-arm64.tar.gz; archives are named <name>_<version>_<os>_<arch>, so this one is pipedrive-mcp_X.Y.Z_linux_arm64.tar.gz",
				".goreleaser.yaml names the windows-amd64 archive pipedrive-mcp_X.Y.Z_windows-amd64.zip; archives are named <name>_<version>_<os>_<arch>, so this one is pipedrive-mcp_X.Y.Z_windows_amd64.zip",
				".goreleaser.yaml names the windows-arm64 archive pipedrive-mcp_X.Y.Z_windows-arm64.zip; archives are named <name>_<version>_<os>_<arch>, so this one is pipedrive-mcp_X.Y.Z_windows_arm64.zip",
			},
			wantArchives: 6,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := releaseRoot(t)
			switch {
			case tc.add != "":
				put(t, root, tc.file, tc.add)
			case tc.file != "":
				edit(t, root, tc.file, tc.old, tc.replacement)
			}
			problems, archives, err := releaseProblems(root)
			if err != nil {
				t.Fatal(err)
			}
			if tc.only != "" {
				problems = slices.DeleteFunc(problems, func(p string) bool { return !strings.Contains(p, tc.only) })
			}
			if !slices.Equal(problems, tc.want) || archives != tc.wantArchives {
				t.Errorf("problems %q, %d archives; want %q, %d", problems, archives, tc.want, tc.wantArchives)
			}
		})
	}
}

// The floor: a repository whose files name fewer archives than
// docs/release.md has to is one the scan is not reading.
func TestReleaseProblemsRefusesToReadNothing(t *testing.T) {
	root := releaseRoot(t)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(path) //nolint:gosec // a test's own temporary file
		if err != nil {
			return err
		}
		return os.WriteFile(path, []byte(strings.ReplaceAll(string(body), "pipedrive-mcp_", "server_")), 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = releaseProblems(root)
	const want = "found 0 archive names across "
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("got %v, want an error starting %q", err, want)
	}
}

// The gate runs the release checks, not only the manifest's.
func TestTheBundleGateHoldsTheRelease(t *testing.T) {
	root := releaseRoot(t)
	if err := mcpbGate(root); err != nil {
		t.Fatalf("the committed repository: %v", err)
	}
	edit(t, root, ".github/workflows/release.yml", "dist/pipedrive-mcp_*.zip", "dist/pipedrive-mcp-*.zip")
	err := mcpbGate(root)
	const want = ".github/workflows/release.yml names pipedrive-mcp-*.zip, which is no archive the release carries"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("got %v, want an error naming %q", err, want)
	}
}

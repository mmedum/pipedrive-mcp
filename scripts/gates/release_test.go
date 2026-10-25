package main

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The family's six archives under the family's names. Every case breaks
// one thing and asserts the sentence that names it.

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

func repoGoreleaser(t *testing.T) string {
	t.Helper()
	return withoutCommentLines(string(repoFile(t, goreleaserPath)))
}

func TestTheReleaseMatrixIsTheFamilysSix(t *testing.T) {
	want := []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64", "windows-arm64"}
	if got := releasePairs(); !slices.Equal(got, want) {
		t.Errorf("releasePairs = %v, want %v", got, want)
	}
}

func TestGoreleaserBuildsTheFamilysSix(t *testing.T) {
	blocks, err := goreleaserBlocks(repoGoreleaser(t))
	if err != nil {
		t.Fatal(err)
	}
	got, err := goreleaserMatrix(blocks["builds"])
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64", "windows-arm64"}
	if !slices.Equal(got, want) {
		t.Errorf("the matrix parsed as %v, want %v", got, want)
	}
}

func TestGoreleaserMatrixReadsTheCrossProduct(t *testing.T) {
	text := "  - id: x\n    goos:\n      - linux\n      - windows # the zip one\n    goarch:\n      - amd64\n      - arm64\n"
	got, err := goreleaserMatrix(text)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"linux-amd64", "linux-arm64", "windows-amd64", "windows-arm64"}; !slices.Equal(got, want) {
		t.Errorf("the matrix parsed as %v, want %v", got, want)
	}
}

// What the matrix does not read it refuses, rather than read it wrong:
// the ignore block 1.2.0 shipped with, which is how windows/arm64 was
// left out, in either key order, and a second build.
func TestGoreleaserMatrixRefusesWhatItDoesNotRead(t *testing.T) {
	const lists = "    goos:\n      - linux\n      - windows\n    goarch:\n      - amd64\n      - arm64\n"
	const ignored = ".goreleaser.yaml's build has an ignore list; the family builds every pair of its goos and " +
		"goarch lists, and this check reads no ignore"
	for text, want := range map[string]string{
		"  - id: x\n" + lists + "    ignore:\n      - goos: windows\n        goarch: arm64\n": ignored,
		"  - id: x\n" + lists + "    ignore:\n      - goarch: arm64\n        goos: windows\n": ignored,
		"  - id: x\n" + lists + "  - id: y\n" + lists:                                         ".goreleaser.yaml has 2 builds; this check reads one",
	} {
		if got, err := goreleaserMatrix(text); err == nil || err.Error() != want {
			t.Errorf("%q parsed as %v, %v; want the error %q", text, got, err, want)
		}
	}
}

func TestGoreleaserMatrixRefusesAFileItCannotRead(t *testing.T) {
	for _, text := range []string{
		"version: 2\n",
		"builds:\n  - goos: [linux, darwin, windows]\n    goarch: [amd64, arm64]\n",
	} {
		if got, err := goreleaserMatrix(text); err == nil {
			t.Errorf("%q parsed as %v; a layout this cannot read should fail rather than report no platforms", text, got)
		}
	}
}

func TestGoreleaserBlocksRefusesAFileMissingOne(t *testing.T) {
	_, err := goreleaserBlocks("version: 2\nproject_name: x\nbuilds:\n  - id: x\n")
	const want = ".goreleaser.yaml has no top-level archives block; its shape has changed and these checks are reading nothing"
	if err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
}

func TestBlockScalar(t *testing.T) {
	const text = "  - id: default\n    name_template: \"{{ .Os }}\"\n    footer: >-\n      one\n\n      two\n    files:\n" +
		"      - LICENSE\n"
	for key, want := range map[string][]string{
		"name_template": {"{{ .Os }}"},
		"footer":        {"one", "two"},
		"id":            {"default"},
		"absent":        nil,
	} {
		if got := blockScalar(text, key); !slices.Equal(got, want) {
			t.Errorf("blockScalar(%s) = %q, want %q", key, got, want)
		}
	}
}

func TestReleaseArchiveProblems(t *testing.T) {
	const family = "darwin-amd64, darwin-arm64, linux-amd64, linux-arm64, windows-amd64, windows-arm64"
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
				"archive; the family's releases carry " + family},
		},
		{
			"a seventh",
			append(releasePairs(), "windows-386"),
			[]string{".goreleaser.yaml builds windows-386, which is not one of the family's archives, " + family},
		},
	} {
		if got := releaseArchiveProblems(tc.built); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestTheArchivesAreTheFamilysNames(t *testing.T) {
	text := repoGoreleaser(t)
	built, err := goreleaserMatrix(text)
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := goreleaserBlocks(text)
	if err != nil {
		t.Fatal(err)
	}
	project, names, err := releaseArchives(blocks, built, docVersion)
	if err != nil {
		t.Fatal(err)
	}
	if project != "pipedrive-mcp" {
		t.Errorf("project = %q, want pipedrive-mcp", project)
	}
	if !maps.Equal(names, sixArchives) {
		t.Errorf("the archives render as %v, want %v", names, sixArchives)
	}
	if got := familyNameProblems(names, "pipedrive-mcp", docVersion); len(got) > 0 {
		t.Errorf("the committed names are refused: %q", got)
	}
}

// archivesBlock is an archives block in this repository's shape, with
// the name template given.
func archivesBlock(name string) map[string]string {
	return map[string]string{
		"project_name": " pipedrive-mcp\n",
		"archives": "\n  - id: default\n    formats: [tar.gz]\n    format_overrides:\n      - goos: windows\n" +
			"        formats: [zip]\n    name_template: >-\n      " + name + "\n    files:\n      - LICENSE\n",
	}
}

func TestReleaseArchivesRendersTheTemplate(t *testing.T) {
	_, names, err := releaseArchives(archivesBlock("{{ .ProjectName }}-{{ .Version }}-{{ .Os }}-{{ .Arch }}"),
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

func TestReleaseArchivesRefusesWhatItCannotRender(t *testing.T) {
	twoDefaults := archivesBlock("{{ .ProjectName }}")
	twoDefaults["archives"] += "    formats: [zip]\n"
	for _, tc := range []struct {
		name   string
		blocks map[string]string
		want   string
	}{
		{"no name_template", map[string]string{"project_name": "x", "archives": "\n  - formats: [tar.gz]\n"},
			".goreleaser.yaml's archives block has no name_template; goreleaser's default adds suffixes this " +
				"check does not render"},
		{"a field this does not render", archivesBlock("{{ .ProjectName }}_{{ .Arm }}"),
			"can't evaluate field Arm"},
		{"two default formats", twoDefaults, ".goreleaser.yaml's archives block names 2 default formats, not one"},
	} {
		_, _, err := releaseArchives(tc.blocks, []string{"linux-amd64"}, "1.2.0")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error saying %q", tc.name, err, tc.want)
		}
	}
}

func TestFamilyNameProblems(t *testing.T) {
	got := familyNameProblems(map[string]string{
		"linux-amd64":   "pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz",
		"windows-arm64": "pipedrive-mcp_X.Y.Z_windows_arm64.tar.gz",
		"darwin-arm64":  "pipedrive-mcp_X.Y.Z_darwin_arm64.tar.gz",
	}, "pipedrive-mcp", docVersion)
	want := []string{
		".goreleaser.yaml names the linux-amd64 archive pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz; " +
			"the family's name is pipedrive-mcp_X.Y.Z_linux_amd64.tar.gz",
		".goreleaser.yaml names the windows-arm64 archive pipedrive-mcp_X.Y.Z_windows_arm64.tar.gz; " +
			"the family's name is pipedrive-mcp_X.Y.Z_windows_arm64.zip",
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

func TestAttestationProblems(t *testing.T) {
	const step = "      - uses: actions/attest-build-provenance@x\n        with:\n" +
		"          # subject-path: dist/pipedrive-mcp_*.zip, in a comment\n" +
		"          subject-path: |\n"
	if got := attestationProblems(string(repoFile(t, ".github/workflows/release.yml")), sixArchives); len(got) > 0 {
		t.Errorf("the committed workflow: %q", got)
	}
	got := attestationProblems(step+"            dist/pipedrive-mcp-*.tar.gz\n            dist/pipedrive-mcp_*.zip\n"+
		"            dist/checksums.txt\n\n  registry:\n    needs: archives\n", sixArchives)
	want := []string{
		"release.yml's subject-path matches no pipedrive-mcp_X.Y.Z_darwin_amd64.tar.gz, so that archive ships with no build provenance",
		"release.yml's subject-path matches no pipedrive-mcp_X.Y.Z_darwin_arm64.tar.gz, so that archive ships with no build provenance",
		"release.yml's subject-path matches no pipedrive-mcp_X.Y.Z_linux_amd64.tar.gz, so that archive ships with no build provenance",
		"release.yml's subject-path matches no pipedrive-mcp_X.Y.Z_linux_arm64.tar.gz, so that archive ships with no build provenance",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	want = []string{"release.yml has no subject-path, so no archive is attested"}
	if got := attestationProblems(step[:strings.Index(step, "          subject-path: |")], sixArchives); !slices.Equal(got, want) {
		t.Errorf("a step that names subject-path only in a comment: %q, want %q", got, want)
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
	repo := repoRoot(t)
	var rels []string
	for _, pattern := range []string{"*.md", "docs/*.md", ".github/*.md", ".github/workflows/*.yml"} {
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
	for _, rel := range append(rels, goreleaserPath, "go.mod") {
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
			name: "windows/arm64 left out", file: goreleaserPath,
			old: "      - windows\n", replacement: "",
			only: "so the release carries no",
			want: []string{
				".goreleaser.yaml does not build windows-amd64, so the release carries no windows_amd64 archive; " +
					"the family's releases carry darwin-amd64, darwin-arm64, linux-amd64, linux-arm64, windows-amd64, windows-arm64",
				".goreleaser.yaml does not build windows-arm64, so the release carries no windows_arm64 archive; " +
					"the family's releases carry darwin-amd64, darwin-arm64, linux-amd64, linux-arm64, windows-amd64, windows-arm64",
			},
			wantArchives: 4,
		},
		{
			name: "the old form in prose", file: "docs/architecture.md",
			old: "`pipedrive-mcp_<version>_<os>_<arch>`", replacement: "`pipedrive-mcp-<version>-<os>-<arch>`",
			want: []string{
				"docs/architecture.md names pipedrive-mcp-<version>-<os>-<arch>, the archives' old form; " +
					"the family's names are pipedrive-mcp_<version>_<os>_<arch>",
			},
			wantArchives: 6,
		},
		{
			name: "a stale name in the pull request template", file: ".github/pull_request_template.md",
			old: "Thanks for contributing.", replacement: "Thanks for contributing. pipedrive-mcp-X.Y.Z-linux-amd64 ",
			want: []string{
				".github/pull_request_template.md names pipedrive-mcp-X.Y.Z-linux-amd64, the archives' old form; " +
					"the family's names are pipedrive-mcp_<version>_<os>_<arch>",
			},
			wantArchives: 6,
		},
		{
			name: "a stale name in the README", file: "README.md",
			old: "pipedrive-mcp_X.Y.Z_linux_amd64.tar.gz", replacement: "pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz",
			want: []string{
				"README.md names pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz, which is no archive the release carries; " + they,
			},
			wantArchives: 6,
		},
		{
			name: "a stale name in the release footer", file: goreleaserPath,
			old: "pipedrive-mcp_{{ .Version }}_linux_amd64.tar.gz", replacement: "pipedrive-mcp-{{ .Version }}-linux-amd64.tar.gz",
			want: []string{
				".goreleaser.yaml names pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz, which is no archive the release carries; " + they,
			},
			wantArchives: 6,
		},
		{
			name: "the release check leaves an archive out", file: "docs/release.md",
			old: "`pipedrive-mcp_X.Y.Z_windows_arm64.zip`", replacement: "`windows_arm64`",
			want: []string{
				"docs/release.md does not name pipedrive-mcp_X.Y.Z_windows_arm64.zip, so the release check never looks for it",
			},
			wantArchives: 6,
		},
		{
			name: "the attestation misses the zips", file: ".github/workflows/release.yml",
			old: "            dist/pipedrive-mcp_*.zip\n", replacement: "",
			want: []string{
				"release.yml's subject-path matches no pipedrive-mcp_X.Y.Z_windows_amd64.zip, so that archive ships with no build provenance",
				"release.yml's subject-path matches no pipedrive-mcp_X.Y.Z_windows_arm64.zip, so that archive ships with no build provenance",
			},
			wantArchives: 6,
		},
		{
			name: "the old template", file: goreleaserPath, only: "family's name",
			old: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}", replacement: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}-{{ .Arch }}",
			want: []string{
				".goreleaser.yaml names the darwin-amd64 archive pipedrive-mcp_X.Y.Z_darwin-amd64.tar.gz; the family's name is pipedrive-mcp_X.Y.Z_darwin_amd64.tar.gz",
				".goreleaser.yaml names the darwin-arm64 archive pipedrive-mcp_X.Y.Z_darwin-arm64.tar.gz; the family's name is pipedrive-mcp_X.Y.Z_darwin_arm64.tar.gz",
				".goreleaser.yaml names the linux-amd64 archive pipedrive-mcp_X.Y.Z_linux-amd64.tar.gz; the family's name is pipedrive-mcp_X.Y.Z_linux_amd64.tar.gz",
				".goreleaser.yaml names the linux-arm64 archive pipedrive-mcp_X.Y.Z_linux-arm64.tar.gz; the family's name is pipedrive-mcp_X.Y.Z_linux_arm64.tar.gz",
				".goreleaser.yaml names the windows-amd64 archive pipedrive-mcp_X.Y.Z_windows-amd64.zip; the family's name is pipedrive-mcp_X.Y.Z_windows_amd64.zip",
				".goreleaser.yaml names the windows-arm64 archive pipedrive-mcp_X.Y.Z_windows-arm64.zip; the family's name is pipedrive-mcp_X.Y.Z_windows_arm64.zip",
			},
			wantArchives: 6,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := releaseRoot(t)
			if tc.file != "" {
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
	for _, rel := range []string{"README.md", "SECURITY.md", "docs/release.md", goreleaserPath, ".github/workflows/release.yml"} {
		body, err := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // a test's own temporary file
		if err != nil {
			t.Fatal(err)
		}
		put(t, root, rel, strings.ReplaceAll(string(body), "pipedrive-mcp_", "server_"))
	}
	_, _, err := releaseProblems(root)
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

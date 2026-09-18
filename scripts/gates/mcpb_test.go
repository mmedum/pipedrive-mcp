package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Break the manifest every way it breaks and watch each be refused
// before believing the check. A check nobody has watched fail is a
// check nobody knows the shape of — doing this in a sibling repository
// found a packer that verified the entry point and the Linux files and
// never the win32 override's command, so a typo in the .exe path packed
// cleanly, installed cleanly, and was caught by nothing.
//
// Each case below breaks exactly one thing in a manifest that is
// otherwise correct, and asserts the sentence that names it.

// repoPath resolves a repository-relative path from wherever the test is
// running. The gates chdir to the root themselves; a test runs from its
// own package directory.
func repoPath(t *testing.T, path string) string {
	t.Helper()
	if _, err := os.Stat("../../" + path); err == nil {
		return "../../" + path
	}
	return path
}

func repoFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(repoPath(t, path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// good is the committed manifest, which every case starts from.
func good(t *testing.T) manifest {
	t.Helper()
	var m manifest
	if err := json.Unmarshal(repoFile(t, manifestPath), &m); err != nil {
		t.Fatalf("the committed manifest is not valid JSON: %v", err)
	}
	return m
}

func mentions(problems []string, want string) bool {
	for _, p := range problems {
		if strings.Contains(p, want) {
			return true
		}
	}
	return false
}

func TestTheCommittedManifestIsValid(t *testing.T) {
	if problems := validateManifest(good(t), bundleFiles, launcherNames()); len(problems) > 0 {
		t.Fatalf("the committed manifest does not describe the bundle:\n%s",
			strings.Join(problems, "\n"))
	}
}

func TestTheWaysAManifestBreaks(t *testing.T) {
	cases := []struct {
		name   string
		breaks func(m *manifest)
		want   string
	}{
		{
			name:   "an entry point nobody stages",
			breaks: func(m *manifest) { m.Server.EntryPoint = "server/pipedrive-mcp-mac" },
			want:   "entry_point",
		},
		{
			name: "a platform command nobody stages",
			breaks: func(m *manifest) {
				over := m.Server.MCPConfig.PlatformOverrides["win32"]
				over.Command = "${__dirname}/server/pipedrive-mcp-windows.exe"
				m.Server.MCPConfig.PlatformOverrides["win32"] = over
			},
			want: "platform_overrides.win32.command",
		},
		{
			name: "an env value spending a key nobody declared",
			breaks: func(m *manifest) {
				// Composed, not the whole value: a check that only looked
				// at values that ARE a reference would miss this one, and
				// the server would start with the text unsubstituted.
				m.Server.MCPConfig.Env["PIPEDRIVE_COMPANY_DOMAIN"] = "${user_config.workspace}.example"
			},
			want: "user_config does not declare",
		},
		{
			name: "an override for a platform the bundle does not claim",
			breaks: func(m *manifest) {
				m.Server.MCPConfig.PlatformOverrides["freebsd"] = m.Server.MCPConfig.PlatformOverrides["linux"]
			},
			want: "compatibility.platforms does not claim",
		},
		{
			name: "a platform running another platform's binary",
			breaks: func(m *manifest) {
				// Deleting the override passes every check above: win32
				// then runs the default command, which is the macOS
				// universal binary, and that file really is in the bundle.
				delete(m.Server.MCPConfig.PlatformOverrides, "win32")
			},
			want: `platform "win32" runs`,
		},
		{
			name:   "a $schema that disagrees with the declared version",
			breaks: func(m *manifest) { m.ManifestVersion = "0.2" },
			want:   "manifest_version",
		},
		{
			name:   "no $schema at all",
			breaks: func(m *manifest) { m.Schema = "" },
			want:   "no $schema",
		},
		{
			name: "the unpinned schema URL",
			breaks: func(m *manifest) {
				m.Schema = "https://raw.githubusercontent.com/anthropics/mcpb/main/dist/mcpb-manifest.schema.json"
			},
			want: "$schema is",
		},
		{
			name:   "nowhere to report a problem",
			breaks: func(m *manifest) { m.Support = "  " },
			want:   "no support URL",
		},
		{
			// The case the $schema check alone cannot see: both fields
			// old together, agreeing with each other, which is exactly
			// what three sibling repositories were shipping.
			name: "a stale version that agrees with its own schema",
			breaks: func(m *manifest) {
				m.ManifestVersion = "0.2"
				m.Schema = schemaFor("0.2")
			},
			want: "or newer",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := good(t)
			c.breaks(&m)
			problems := validateManifest(m, bundleFiles, launcherNames())
			if !mentions(problems, c.want) {
				t.Fatalf("breaking %s was not refused; problems: %v", c.name, problems)
			}
		})
	}
}

// TestTheLauncherNamesAreThePackers is the way no manifest can see: the
// names live in a shell script nothing else reads.
func TestTheLauncherNamesAreThePackers(t *testing.T) {
	problems := validateManifest(good(t), bundleFiles, []string{"pipedrive-mcp-linux-amd64"})
	if !mentions(problems, "the launcher runs") {
		t.Fatalf("a launcher naming a binary nobody stages was not refused; problems: %v", problems)
	}
}

// TestTheGeneratedLauncherCoversTheTable is what replaced reading a
// committed script. The generator is the only thing that can now put a
// wrong name in the launcher, so this is the check that it cannot.
func TestTheGeneratedLauncherCoversTheTable(t *testing.T) {
	script := launcherScript()
	launched := 0
	for _, f := range bundleFiles {
		if !f.launched {
			continue
		}
		launched++
		if !strings.Contains(script, `exec "$dir/`+filepath.Base(f.path)+`"`) {
			t.Errorf("the launcher does not dispatch to %s", f.path)
		}
		if len(f.uname) == 0 {
			t.Errorf("%s is launched but names no uname value, so nothing reaches it", f.path)
		}
		for _, u := range f.uname {
			if !strings.Contains(script, u) {
				t.Errorf("the launcher recognises no machine reporting %s", u)
			}
		}
	}
	if launched == 0 {
		t.Fatal("no row is launched; this test is reading nothing")
	}
}

// TestTheGeneratedLauncherRefusesToStdout: stdout is the JSON-RPC
// channel, so the one path that gives up has to write to stderr. A line
// of English on stdout corrupts the session before the client's first
// request completes, and the client reports a protocol error rather
// than this.
func TestTheGeneratedLauncherRefusesToStdout(t *testing.T) {
	script := launcherScript()
	if !strings.Contains(script, ">&2") {
		t.Error("the launcher's refusal does not go to stderr")
	}
	if !strings.Contains(script, "exit 1") {
		t.Error("the launcher does not exit non-zero when it finds no binary")
	}
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "echo ") && !strings.Contains(trimmed, ">&2") {
			t.Errorf("this line writes to stdout: %s", trimmed)
		}
	}
}

func TestTheCommittedManifestCarriesThePlaceholder(t *testing.T) {
	// Written out rather than compared to placeholderVersion. The point
	// is that no real version is ever committed, and moving both the
	// constant and the manifest to a real one together would satisfy a
	// comparison between them while doing precisely that.
	const want = "0.0.0-dev"
	if v := good(t).Version; v != want {
		t.Fatalf("the committed manifest says version %q; it must say %q", v, want)
	}
	if placeholderVersion != want {
		t.Errorf("placeholderVersion is %q; the packer would accept %q as a version to ship",
			placeholderVersion, want)
	}
}

// TestTheManifestSaysWhereTheTokenComesFrom pins the deliberate
// difference from the Google servers. Theirs cannot log anybody in and
// say so; this one can be configured entirely from the install dialog,
// so what a user needs is where the token comes from. Copying their
// sentence across would have been a false statement passing a check.
func TestTheManifestSaysWhereTheTokenComesFrom(t *testing.T) {
	if !strings.Contains(strings.ToLower(good(t).LongDescription), "api token") {
		t.Fatal("long_description does not tell the user they need a Pipedrive API token")
	}
}

// TestTheSchemaRefIsATagNotABranch: the version in the path pins the
// FORMAT, the ref pins the BYTES. main's bytes can change under a path
// that still reads as pinned, which is the same failure the pins gate
// exists for — and the two were byte-identical when this was written,
// so nothing would have shown it drifting.
func TestTheSchemaRefIsATagNotABranch(t *testing.T) {
	// The whole ref, not "is it main". A partial version like v2.1 is a
	// branch with a version number in its name and moves exactly like
	// main does, and blacklisting one name says nothing about it. This
	// asserts the SHAPE of schemaRef rather than comparing the URL to
	// the constant it was built from — that comparison passes whenever
	// the two are changed together, which is the failure this test
	// exists to catch.
	if !immutableRef.MatchString(schemaRef) {
		t.Errorf("schemaRef %q is not a complete release tag; a partial ref moves like a branch", schemaRef)
	}
	url := schemaFor(minManifestVersion)
	if !strings.Contains(url, "/"+schemaRef+"/") {
		t.Errorf("the schema URL does not carry the pinned ref %s: %s", schemaRef, url)
	}
	// The committed manifest cites the same URL the gate builds.
	if got := good(t).Schema; got != schemaFor(good(t).ManifestVersion) {
		t.Errorf("the manifest cites %s; the gate builds %s", got, schemaFor(good(t).ManifestVersion))
	}
}

// immutableRef is a complete release tag: vMAJOR.MINOR.PATCH and
// nothing shorter.
var immutableRef = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

func TestOlderThanComparesNumerically(t *testing.T) {
	cases := []struct {
		got, floor string
		want       bool
	}{
		{"0.2", "0.3", true},
		{"0.3", "0.3", false},
		{"0.4", "0.3", false},
		// The reason this is not a string comparison: "0.10" sorts
		// before "0.9" as text and is newer as a version.
		{"0.10", "0.9", false},
		{"0.9", "0.10", true},
		{"1.0", "0.3", false},
		{"0.3.1", "0.3", false},
		{"0.3", "0.3.1", true},
	}
	for _, c := range cases {
		if got := olderThan(c.got, c.floor); got != c.want {
			t.Errorf("olderThan(%q, %q) = %v; want %v", c.got, c.floor, got, c.want)
		}
	}
}

// ------------------------------------------------ schema conformance
//
// The referential checks above ask whether the manifest's names resolve
// to things this repository stages. These ask the other question: does
// the document satisfy the schema it cites. Each case below is watched
// failing, for the same reason the rest of this file is.

// goodDocument is the committed manifest as a plain JSON value; see
// readManifestDocument for why the typed struct will not do.
func goodDocument(t *testing.T) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(repoFile(t, manifestPath), &document); err != nil {
		t.Fatalf("the committed manifest is not valid JSON: %v", err)
	}
	return document
}

func vendoredSchema(t *testing.T) []byte {
	t.Helper()
	version, _ := goodDocument(t)["manifest_version"].(string)
	return repoFile(t, vendoredSchemaPath(version))
}

// The committed manifest satisfies the schema it points at. This is the
// assertion the whole vendored copy exists for, so it goes through the
// composition the gate actually runs — path, read, hash, validate —
// rather than assembling the schema bytes itself and testing only the
// pure half.
func TestCommittedManifestSatisfiesItsSchema(t *testing.T) {
	t.Chdir(repoRoot(t))
	if err := checkAgainstSchema(goodDocument(t)); err != nil {
		t.Errorf("the committed manifest does not satisfy its own schema: %v", err)
	}
}

// The vendored bytes are the bytes the hash names. checkAgainstSchema
// enforces this too; here it gets its own test for the sharper message.
func TestVendoredSchemaMatchesItsRecordedHash(t *testing.T) {
	version, _ := goodDocument(t)["manifest_version"].(string)
	want, ok := vendoredSchemaSHA256[version]
	if !ok {
		t.Fatalf("manifest_version %q has no recorded schema hash", version)
	}
	if got := schemaSHA256(vendoredSchema(t)); got != want {
		t.Errorf("%s hashes to %s; vendoredSchemaSHA256 says %s", vendoredSchemaPath(version), got, want)
	}
}

func TestSchemaConformanceCatchesABrokenDocument(t *testing.T) {
	schema := vendoredSchema(t)

	cases := []struct {
		name   string
		breaks func(map[string]any)
		want   string
	}{
		// The root closes with additionalProperties: false, so a
		// misspelled or invented key is caught here and nowhere else —
		// the referential checks never look at keys they do not know.
		{"an unknown top-level key", func(d map[string]any) { d["totally_unknown_key"] = 1 },
			"unexpected additional properties"},
		{"a field of the wrong type", func(d map[string]any) { d["keywords"] = "not-an-array" },
			"keywords"},
		{"a missing required field", func(d map[string]any) { delete(d, "author") },
			"missing properties"},
		// server.type is a closed enum, and "binary" is what this bundle
		// is. A typo here packs and installs and then does nothing.
		{"a server type off the enum", func(d map[string]any) {
			d["server"].(map[string]any)["type"] = "not-a-real-type"
		}, "server"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			document := goodDocument(t)
			tc.breaks(document)
			err := validateDocument(document, schema)
			if err == nil {
				t.Fatal("a broken document satisfied the schema")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
		})
	}
}

// A manifest_version with no vendored schema is refused with the two
// things needed to fix it: where to get the schema and where to put it.
func TestSchemaConformanceRefusesAnUnvendoredVersion(t *testing.T) {
	t.Chdir(repoRoot(t))

	document := goodDocument(t)
	document["manifest_version"] = "9.9"
	err := checkAgainstSchema(document)
	if err == nil {
		t.Fatal("a manifest_version with no vendored schema was accepted")
	}
	for _, want := range []string{"no copy of its schema is vendored", schemaFor("9.9"), vendoredSchemaPath("9.9")} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

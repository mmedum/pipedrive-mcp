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
	if v := good(t).Version; v != placeholderVersion {
		t.Fatalf("the committed manifest says version %q; it must say %q", v, placeholderVersion)
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

package main

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/pipedrive-mcp/internal/config"
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

// editedRaw is the committed manifest with one edit applied to its
// decoded document, encoded again.
func editedRaw(t *testing.T, edit func(map[string]any)) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(repoFile(t, manifestPath), &doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// edited is the committed manifest with one edit applied, read back the
// way the gate reads it.
func edited(t *testing.T, edit func(map[string]any)) manifest {
	t.Helper()
	var m manifest
	if err := json.Unmarshal(editedRaw(t, edit), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// dig is the object at a path in a decoded manifest.
func dig(doc map[string]any, path ...string) map[string]any {
	for _, key := range path {
		doc = doc[key].(map[string]any)
	}
	return doc
}

func mentions(problems []string, want string) bool {
	for _, p := range problems {
		if strings.Contains(p, want) {
			return true
		}
	}
	return false
}

// The committed manifest passes every check the gate and the packer
// run, read from the repository root. The one pass of it here: every
// other test breaks it.
func TestTheCommittedManifestIsValid(t *testing.T) {
	_, _, problems, err := committedManifest(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) > 0 {
		t.Fatalf("the committed manifest fails the gate:\n%s", strings.Join(problems, "\n"))
	}
}

// The bundle's layout is the family's: Go's architecture names, as in
// the release archives, and the license and the README at the root.
func TestTheLayoutIsTheFamilys(t *testing.T) {
	want := []string{
		"LICENSE", "README.md", "server/launch-linux.sh", "server/pipedrive-mcp-darwin",
		"server/pipedrive-mcp-linux-amd64", "server/pipedrive-mcp-linux-arm64", "server/pipedrive-mcp.exe",
	}
	got := make([]string, 0, len(bundleFiles))
	for _, f := range bundleFiles {
		got = append(got, f.path)
	}
	if slices.Sort(got); !slices.Equal(got, want) {
		t.Errorf("the bundle stages %v, want %v", got, want)
	}
}

func TestTheWaysAManifestBreaks(t *testing.T) {
	overrides := []string{"server", "mcp_config", "platform_overrides"}
	cases := []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{
			name: "an entry point nobody stages",
			edit: func(d map[string]any) { dig(d, "server")["entry_point"] = "server/pipedrive-mcp-mac" },
			want: `entry_point "server/pipedrive-mcp-mac" is not a program the packer stages`,
		},
		{
			name: "no entry point at all",
			edit: func(d map[string]any) { delete(dig(d, "server"), "entry_point") },
			want: "the manifest declares no entry_point",
		},
		{
			// A staged program, and not the one the default command runs:
			// the bundle names the Windows binary as the server and spawns
			// the macOS one.
			name: "an entry point the default command does not run",
			edit: func(d map[string]any) { dig(d, "server")["entry_point"] = "server/pipedrive-mcp.exe" },
			want: `entry_point is "server/pipedrive-mcp.exe", and mcp_config.command runs "server/pipedrive-mcp-darwin"`,
		},
		{
			// Packed, and not a program: the license is there to be read.
			name: "an entry point naming the license",
			edit: func(d map[string]any) { dig(d, "server")["entry_point"] = "LICENSE" },
			want: `entry_point "LICENSE" is not a program the packer stages`,
		},
		{
			name: "a platform command nobody stages",
			edit: func(d map[string]any) {
				dig(d, append(overrides, "win32")...)["command"] = "${__dirname}/server/pipedrive-mcp-windows.exe"
			},
			want: `platform_overrides.win32.command is "${__dirname}/server/pipedrive-mcp-windows.exe", ` +
				"which is not a program the packer stages",
		},
		{
			name: "a command outside the bundle",
			edit: func(d map[string]any) {
				dig(d, "server", "mcp_config")["command"] = "/usr/local/bin/pipedrive-mcp"
			},
			want: `mcp_config.command is "/usr/local/bin/pipedrive-mcp"; a command starts with ${__dirname}/`,
		},
		{
			// The specification shows this form once and never says what
			// it is resolved against.
			name: "a relative command",
			edit: func(d map[string]any) {
				dig(d, "server", "mcp_config")["command"] = "server/pipedrive-mcp-darwin"
			},
			want: `mcp_config.command is "server/pipedrive-mcp-darwin"; a command starts with ${__dirname}/`,
		},
		{
			// The old check stripped a leading slash and read this as
			// staged.
			name: "an override's absolute command",
			edit: func(d map[string]any) {
				dig(d, append(overrides, "win32")...)["command"] = "/server/pipedrive-mcp.exe"
			},
			want: `platform_overrides.win32.command is "/server/pipedrive-mcp.exe"; a command starts with ${__dirname}/`,
		},
		{
			name: "a command naming the README",
			edit: func(d map[string]any) { dig(d, "server", "mcp_config")["command"] = "${__dirname}/README.md" },
			want: `mcp_config.command is "${__dirname}/README.md", which is not a program the packer stages`,
		},
		{
			name: "an override naming the license",
			edit: func(d map[string]any) { dig(d, append(overrides, "linux")...)["command"] = "${__dirname}/LICENSE" },
			want: `platform_overrides.linux.command is "${__dirname}/LICENSE", which is not a program the packer stages`,
		},
		{
			// Staged, and a program, and the file staged for another
			// platform: macOS would spawn a Windows binary. entry_point
			// follows the command, so that only this is wrong.
			name: "macOS spawning the Windows binary",
			edit: func(d map[string]any) {
				dig(d, "server")["entry_point"] = "server/pipedrive-mcp.exe"
				dig(d, "server", "mcp_config")["command"] = "${__dirname}/server/pipedrive-mcp.exe"
			},
			want: `platform "darwin" runs "server/pipedrive-mcp.exe", and the file staged for it is ` +
				`"server/pipedrive-mcp-darwin"`,
		},
		{
			// Deleting the override passes every check above: win32 then
			// runs the default command, which is the macOS universal
			// binary, and that file really is in the bundle.
			name: "no win32 override",
			edit: func(d map[string]any) { delete(dig(d, overrides...), "win32") },
			want: `platform "win32" runs "server/pipedrive-mcp-darwin", and the file staged for it is ` +
				`"server/pipedrive-mcp.exe"`,
		},
		{
			name: "no linux override",
			edit: func(d map[string]any) { delete(dig(d, overrides...), "linux") },
			want: `platform "linux" runs "server/pipedrive-mcp-darwin", and the file staged for it is ` +
				`"server/launch-linux.sh"`,
		},
		{
			// Right for one of the two architectures Claude Desktop for
			// Linux ships.
			name: "one Linux binary for both architectures",
			edit: func(d map[string]any) {
				dig(d, append(overrides, "linux")...)["command"] = "${__dirname}/server/pipedrive-mcp-linux-amd64"
			},
			want: `platform "linux" runs "server/pipedrive-mcp-linux-amd64", and the file staged for it is ` +
				`"server/launch-linux.sh"`,
		},
		{
			name: "an override for a platform the bundle does not claim",
			edit: func(d map[string]any) {
				dig(d, overrides...)["freebsd"] = map[string]any{"command": "${__dirname}/server/launch-linux.sh"}
			},
			want: `platform_overrides has "freebsd", which compatibility.platforms does not claim`,
		},
		{
			// Composed, not the whole value: a check that only looked at
			// values that ARE a reference would miss this one, and the
			// server would start with the text unsubstituted.
			name: "an env value spending a key nobody declared",
			edit: func(d map[string]any) {
				dig(d, "server", "mcp_config", "env")["PIPEDRIVE_COMPANY_DOMAIN"] = "${user_config.workspace}.example"
			},
			want: "mcp_config.env.PIPEDRIVE_COMPANY_DOMAIN spends ${user_config.workspace}, which user_config does not declare",
		},
		{
			name: "an argument spending a key nobody declared",
			edit: func(d map[string]any) { dig(d, "server", "mcp_config")["args"] = []any{"${user_config.verbose}"} },
			want: "mcp_config.args spends ${user_config.verbose}, which user_config does not declare",
		},
		{
			name: "an override's argument spending a key nobody declared",
			edit: func(d map[string]any) {
				dig(d, append(overrides, "win32")...)["args"] = []any{"--domain=${user_config.workspace}"}
			},
			want: "platform_overrides.win32.args spends ${user_config.workspace}, which user_config does not declare",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			problems := validateManifest(edited(t, c.edit), bundleFiles)
			if len(problems) != 1 || !strings.Contains(problems[0], c.want) {
				t.Fatalf("problems:\n%s\nwant the one containing %q", strings.Join(problems, "\n"), c.want)
			}
		})
	}
}

// copiedRoot is a repository holding the files the gate and the packer
// read, copied from this one, so that one can be broken.
func copiedRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{manifestPath, vendoredSchemaPath(minManifestVersion), "LICENSE", "README.md"} {
		put(t, root, rel, string(repoFile(t, rel)))
	}
	return root
}

// put writes body to rel under root.
func put(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// committedManifest runs every check the gate has, on the files under
// the root it is given, not the working directory: a check broken in a
// copy of the repository is refused, and refused alone.
func TestManifestProblemsRunsEveryCheck(t *testing.T) {
	for _, tc := range []struct {
		name string
		// file is the one broken; edit applies to the manifest, and
		// old and new, every time old appears, to any other file.
		file     string
		edit     func(map[string]any)
		old, new string
		want     string
	}{
		{name: "a version in the tree", file: manifestPath, edit: func(d map[string]any) { d["version"] = "1.2.3" },
			want: `the committed manifest says version "1.2.3"`},
		{name: "a schema at a branch", file: manifestPath, edit: func(d map[string]any) {
			d["$schema"] = "https://raw.githubusercontent.com/anthropics/mcpb/main/schemas/mcpb-manifest-v0.3.schema.json"
		}, want: "$schema is"},
		{name: "a document the schema refuses", file: manifestPath, edit: func(d map[string]any) { d["keywords"] = "crm" },
			want: "the manifest does not satisfy the schema it cites"},
		{name: "no word of the API token", file: manifestPath, edit: func(d map[string]any) {
			d["long_description"] = "Pipedrive CRM over MCP."
		}, want: "long_description does not say the user needs a Pipedrive API token"},
		// Every check before identityProblems passed this: no platform
		// was left to spawn the wrong file.
		{name: "Linux dropped with its override", file: manifestPath, edit: func(d map[string]any) {
			dig(d, "compatibility")["platforms"] = []any{"darwin", "win32"}
			delete(dig(d, "server", "mcp_config", "platform_overrides"), "linux")
		}, want: "compatibility.platforms is [darwin win32]; the packer stages entry points for [darwin linux win32]"},
		// The decode into manifest merges the two, so every check sees
		// the env and the overrides; the schema and the host keep the
		// second, which has neither.
		{name: "a second server object", file: manifestPath, old: `  "user_config": {`,
			new: `  "server": {"type": "binary", "entry_point": "server/pipedrive-mcp-darwin", ` +
				`"mcp_config": {"command": "${__dirname}/server/pipedrive-mcp-darwin", "args": []}},` +
				"\n" + `  "user_config": {`,
			want: "the manifest has server more than once"},
		{name: "a variable mapped twice", file: manifestPath, old: `"PIPEDRIVE_DRY_RUN": "${user_config.dry_run}"`,
			new:  `"PIPEDRIVE_DRY_RUN": "${user_config.dry_run}", "PIPEDRIVE_DRY_RUN": "${user_config.dry_run}"`,
			want: "the manifest has server.mcp_config.env.PIPEDRIVE_DRY_RUN more than once"},
		// The schema and the license are read under the root too, not
		// from wherever the gate runs.
		{name: "a vendored schema somebody edited", file: vendoredSchemaPath(minManifestVersion),
			old: `"privacy_policies"`, new: `"privacy_policy"`,
			want: "it is no longer the reviewed copy of " + schemaFor(minManifestVersion)},
		{name: "a license of another kind", file: "LICENSE", old: "Apache License", new: "MIT License",
			want: `the manifest says the license is "Apache-2.0" and LICENSE is MIT`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := copiedRoot(t)
			if tc.edit != nil {
				put(t, root, tc.file, string(editedRaw(t, tc.edit)))
			} else {
				body := string(repoFile(t, tc.file))
				if !strings.Contains(body, tc.old) {
					t.Fatalf("%s does not say %q", tc.file, tc.old)
				}
				put(t, root, tc.file, strings.ReplaceAll(body, tc.old, tc.new))
			}
			_, _, problems, err := committedManifest(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(problems) != 1 || !strings.Contains(problems[0], tc.want) {
				t.Errorf("problems:\n%s\nwant the one containing %q", strings.Join(problems, "\n"), tc.want)
			}
		})
	}
}

// A key is repeated only within one object, and each repeated key is
// named once, by its path, a list's elements and a repeated object's
// own keys included. Two keys that differ only in case are one key
// repeated, since the gate's decode matches either, and a key holding a
// dot or a bracket is quoted, so its path is not another key's.
func TestRepeatedKeysNameEachPathOnce(t *testing.T) {
	for _, tc := range []struct {
		doc  string
		want []string
	}{
		{`{"a": [{"b": 1, "b": 2, "b": 3}], "c": {"d": 1}, "d": 1}`, []string{"a[0].b"}},
		{`{"x": {"y": 1}, "x": {"y": 1, "y": 2}}`, []string{"x", "x.y"}},
		{`{"server": {"type": "binary"}, "user_config": {"type": {"type": "boolean"}}}`, nil},
		{`{"command": "a", "Command": "b", "COMMAND": "c"}`, []string{"Command"}},
		// The Kelvin sign folds to k under Unicode, as the decode folds it.
		{`{"env": {"kind": 1, "\u212aind": 2}}`, []string{"env.\u212aind"}},
		{`{"env": {"a.b": 1, "a": {"b": 1}, "a.b": 2}}`, []string{`env["a.b"]`}},
		{`{"env": {"a[0]": 1, "a[0]": 2}, "a": [{"x": 1}]}`, []string{`env["a[0]"]`}},
	} {
		got, err := repeatedKeys([]byte(tc.doc))
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("repeatedKeys(%s) = %q, %v; want %q", tc.doc, got, err, tc.want)
		}
	}
	// A walk that cannot finish is an error, not a document with no
	// repeats.
	if got, err := repeatedKeys([]byte(`{"a": 1,`)); err == nil {
		t.Errorf("an unfinished document gave %q and no error", got)
	}
}

// Keys that differ only in case are refused by the gate itself: the
// decode reads the second "Server" into the struct's server, and the host
// keeps both.
func TestTheManifestRefusesAKeyInAnotherCase(t *testing.T) {
	root := copiedRoot(t)
	body := string(repoFile(t, manifestPath))
	const at = `"PIPEDRIVE_DRY_RUN": "${user_config.dry_run}"`
	if !strings.Contains(body, at) {
		t.Fatalf("the manifest does not say %s", at)
	}
	put(t, root, manifestPath, strings.Replace(body, at, at+`, "pipedrive_dry_run": "true"`, 1))
	_, _, problems, err := committedManifest(root)
	const want = "the manifest has server.mcp_config.env.pipedrive_dry_run more than once, case aside"
	if err != nil || len(problems) != 1 || !strings.HasPrefix(problems[0], want) {
		t.Errorf("problems %q, %v; want the one starting %q", problems, err, want)
	}
}

// TestTheLauncherNamesAreThePackers is the way no manifest can see: the
// names live in a shell script nothing else reads. The names here are
// the ones 1.1.3 shipped; the family's layout is -linux-amd64.
func TestTheLauncherNamesAreThePackers(t *testing.T) {
	want := []string{"the launcher runs [pipedrive-mcp-linux-arm64 pipedrive-mcp-linux-x64]; " +
		"the packer stages [pipedrive-mcp-linux-amd64 pipedrive-mcp-linux-arm64]"}
	got := checkLauncherNames(bundleFiles, []string{"pipedrive-mcp-linux-x64", "pipedrive-mcp-linux-arm64"})
	if !slices.Equal(got, want) {
		t.Fatalf("a launcher naming a binary nobody stages gives %q, want %q", got, want)
	}
	if got := checkLauncherNames(bundleFiles, launcherNames()); len(got) > 0 {
		t.Errorf("the generated launcher disagrees with the packer: %q", got)
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
				t.Errorf("the launcher recognizes no machine reporting %s", u)
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

// The manifest's declaration of its own format, broken once each.
func TestTheWaysAManifestMisdeclaresItsVersion(t *testing.T) {
	cases := []struct {
		name   string
		breaks func(m *manifest)
		want   string
	}{
		{
			name:   "a $schema that disagrees with the declared version",
			breaks: func(m *manifest) { m.ManifestVersion = "0.4" },
			want:   `$schema is "` + schemaFor("0.3") + `" but manifest_version "0.4"`,
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
			if problems := checkManifestShape(m); len(problems) != 1 || !strings.Contains(problems[0], c.want) {
				t.Fatalf("problems:\n%s\nwant the one containing %q", strings.Join(problems, "\n"), c.want)
			}
		})
	}
}

// Each rule the bundle's settings are held to, broken once in the
// committed manifest.
func TestTheBundleSettingsRefuseEachBreak(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"a switch the bundle turns on", func(d map[string]any) {
			dig(d, "user_config", "dry_run")["default"] = true
		}, "the bundle's defaults configure the server otherwise than no environment does, in DryRun;"},
		{"a required switch the bundle turns on, which Claude Desktop fills in", func(d map[string]any) {
			dig(d, "user_config", "dry_run")["default"] = true
			dig(d, "user_config", "dry_run")["required"] = true
		}, "the bundle's defaults configure the server otherwise than no environment does, in DryRun;"},
		{"a default the server refuses", func(d map[string]any) {
			dig(d, "user_config", "dry_run")["default"] = "maybe"
		}, `the environment the bundle's defaults give the server is refused: config: PIPEDRIVE_DRY_RUN "maybe"`},
		{"a field with a list for a default", func(d map[string]any) {
			dig(d, "user_config", "company_domain")["default"] = []any{"acme"}
		}, "user_config.company_domain defaults to a list, which the defaults check cannot build an environment from"},
		{"a switch asked as text", func(d map[string]any) {
			dig(d, "user_config", "dry_run")["type"] = "string"
			dig(d, "user_config", "dry_run")["default"] = "false"
		}, "user_config.dry_run is a string; PIPEDRIVE_DRY_RUN is a switch, so ask for a boolean"},
		{"a switch with no default", func(d map[string]any) {
			delete(dig(d, "user_config", "dry_run"), "default")
		}, `the environment the bundle's defaults give the server is refused: config: PIPEDRIVE_DRY_RUN ` +
			`"${user_config.dry_run}"`},
		{"an optional key with no default", func(d map[string]any) {
			dig(d, "user_config", "company_domain")["required"] = false
		}, "user_config.company_domain is optional with no default"},
		{"an override's env, which replaces the base", func(d map[string]any) {
			dig(d, "server", "mcp_config", "platform_overrides", "win32")["env"] = map[string]any{
				"PIPEDRIVE_DRY_RUN": "false",
			}
		}, "platform_overrides.win32.env is set"},
		{"an empty override env, which replaces the base too", func(d map[string]any) {
			dig(d, "server", "mcp_config", "platform_overrides", "linux")["env"] = map[string]any{}
		}, "platform_overrides.linux.env is set"},
		{"no argument list in mcp_config", func(d map[string]any) {
			delete(dig(d, "server", "mcp_config"), "args")
		}, "mcp_config.args is missing; keep it as []"},
		{"a null argument list in mcp_config", func(d map[string]any) {
			dig(d, "server", "mcp_config")["args"] = nil
		}, "mcp_config.args is null; keep it as []"},
		{"an argument that skips the startup probe", func(d map[string]any) {
			dig(d, "server", "mcp_config")["args"] = []any{"--skip-probe"}
		}, `mcp_config.args is ["--skip-probe"]`},
		{"an override's argument, which replaces the base", func(d map[string]any) {
			dig(d, "server", "mcp_config", "platform_overrides", "linux")["args"] = []any{"--skip-probe"}
		}, "platform_overrides.linux.args is set"},
		{"an empty override argument list, which replaces the base too", func(d map[string]any) {
			dig(d, "server", "mcp_config", "platform_overrides", "win32")["args"] = []any{}
		}, "platform_overrides.win32.args is set"},
		{"a setting the bundle no longer asks for", func(d map[string]any) {
			delete(dig(d, "user_config"), "dry_run")
			delete(dig(d, "server", "mcp_config", "env"), "PIPEDRIVE_DRY_RUN")
		}, "user_config does not ask for dry_run, which sets PIPEDRIVE_DRY_RUN"},
		{"a setting spent under another key", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["PIPEDRIVE_COMPANY_DOMAIN"] = "${user_config.api_token}"
		}, `mcp_config.env.PIPEDRIVE_COMPANY_DOMAIN is "${user_config.api_token}", want "${user_config.company_domain}"`},
		{"a key no setting has", func(d map[string]any) {
			dig(d, "user_config")["verbose"] = map[string]any{
				"type": "boolean", "title": "Verbose", "description": "d", "default": false}
		}, "user_config asks for verbose, which is no setting's key"},
		{"a setting the bundle leaves out", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["LOG_LEVEL"] = "debug"
		}, "mcp_config.env sets LOG_LEVEL, which the bundle leaves out: diagnostics"},
		{"the retired destructive switch", func(d map[string]any) {
			dig(d, "server", "mcp_config", "env")["PIPEDRIVE_ENABLE_DESTRUCTIVE"] = "true"
		}, "mcp_config.env sets PIPEDRIVE_ENABLE_DESTRUCTIVE, which the server does not read"},
		// config.Load reads neither the token nor the domain, so starting
		// it from the defaults cannot see a default on either.
		{"a default on the API token, which Load does not read", func(d map[string]any) {
			dig(d, "user_config", "api_token")["required"] = false
			dig(d, "user_config", "api_token")["default"] = "a-token"
		}, "user_config.api_token has a default, and config.Load does not read PIPEDRIVE_API_TOKEN"},
		{"a default on the required company domain", func(d map[string]any) {
			dig(d, "user_config", "company_domain")["default"] = "acme"
		}, "user_config.company_domain is required and has a default"},
		{"a default the host would expand", func(d map[string]any) {
			dig(d, "user_config", "dry_run")["default"] = "${__dirname}"
		}, "user_config.dry_run defaults to a value holding ${"},
		{"a token field shown in the clear", func(d map[string]any) {
			delete(dig(d, "user_config", "api_token"), "sensitive")
		}, "user_config.api_token is not sensitive; PIPEDRIVE_API_TOKEN is a credential"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if problems := settingProblems(edited(t, tc.edit), bundleLeftOut); !mentions(problems, tc.want) {
				t.Errorf("problems:\n%s\nwant one containing %q", strings.Join(problems, "\n"), tc.want)
			}
		})
	}
	if problems := settingProblems(good(t), bundleLeftOut); len(problems) > 0 {
		t.Errorf("the committed settings give %q", problems)
	}
}

// What the bundle says it is, broken once each: the server's own name, a
// binary, the family's floor, a privacy policy, and exactly the
// platforms the packer stages an entry point for.
func TestTheIdentityRefusesEachBreak(t *testing.T) {
	const entryPoints = "; the packer stages entry points for [darwin linux win32]"
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"another name", func(d map[string]any) { d["name"] = "pipedrive" },
			`the manifest's name is "pipedrive" and the server's is "pipedrive-mcp"`},
		{"a node server", func(d map[string]any) { dig(d, "server")["type"] = "node" },
			`server.type is "node"; the bundle carries a binary`},
		{"no Claude Desktop floor", func(d map[string]any) { delete(dig(d, "compatibility"), "claude_desktop") },
			`compatibility.claude_desktop is ""; the family's bundles claim ">=0.10.0"`},
		{"no Linux", func(d map[string]any) { dig(d, "compatibility")["platforms"] = []any{"darwin", "win32"} },
			"compatibility.platforms is [darwin win32]" + entryPoints},
		{"a platform the packer stages nothing for", func(d map[string]any) {
			dig(d, "compatibility")["platforms"] = []any{"darwin", "win32", "linux", "freebsd"}
		}, "compatibility.platforms is [darwin freebsd linux win32]" + entryPoints},
		{"a platform claimed twice", func(d map[string]any) {
			dig(d, "compatibility")["platforms"] = []any{"darwin", "win32", "linux", "linux"}
		}, "compatibility.platforms is [darwin linux linux win32]" + entryPoints},
		{"no privacy policy", func(d map[string]any) { delete(d, "privacy_policies") }, "privacy_policies is empty"},
		{"an empty privacy policy list", func(d map[string]any) { d["privacy_policies"] = []any{} },
			"privacy_policies is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if problems := identityProblems(edited(t, tc.edit), bundleFiles); len(problems) != 1 ||
				!strings.HasPrefix(problems[0], tc.want) {
				t.Errorf("problems:\n%s\nwant the one starting %q", strings.Join(problems, "\n"), tc.want)
			}
		})
	}
	// The platforms come from what is staged, not from a list of its own.
	files := slices.DeleteFunc(slices.Clone(bundleFiles), func(f staged) bool { return f.platform == "linux" })
	want := []string{"compatibility.platforms is [darwin linux win32]; the packer stages entry points for [darwin win32]"}
	if got := identityProblems(good(t), files); !slices.Equal(got, want) {
		t.Errorf("nothing staged for Linux gives %q, want %q", got, want)
	}
}

// Every default the check cannot read is reported, in key order, and
// the comparison still runs over the rest, so one run names everything
// wrong with the defaults.
func TestDefaultsProblemsReportsEveryUnreadableDefaultAndTheRest(t *testing.T) {
	m := edited(t, func(d map[string]any) {
		uc := dig(d, "user_config")
		dig(uc, "company_domain")["default"] = map[string]any{"name": "acme"}
		dig(uc, "api_token")["default"] = []any{"token"}
		dig(uc, "dry_run")["default"] = true
	})
	const cannot = ", which the defaults check cannot build an environment from: give it a boolean, a string or a number"
	want := []string{
		"user_config.api_token defaults to a list" + cannot,
		"user_config.company_domain defaults to an object" + cannot,
	}
	got, err := defaultsProblems(m)
	if err != nil || len(got) != 3 || !slices.Equal(got[:2], want) ||
		!strings.HasPrefix(got[2], "the bundle's defaults configure the server otherwise than no environment does, in DryRun;") {
		t.Errorf("defaultsProblems = %v,\n%s\nwant\n%s\nand then the comparison", err, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The bundle asks for every variable the server reads, less each one
// left out with a reason, so a setting added later is a decision about
// the bundle rather than a gap in it. An exclusion the server has no use
// for is refused.
func TestBundleVarsAreDerivedFromTheSettings(t *testing.T) {
	asked, problems := bundleVarsFrom(
		[]config.Var{{Name: "PIPEDRIVE_ASKED"}, {Name: "PIPEDRIVE_LEFT_OUT"}, {Name: "PIPEDRIVE_ADDED_LATER", Switch: true}},
		map[string]string{"PIPEDRIVE_LEFT_OUT": "a reason", "PIPEDRIVE_GONE": "a reason"},
	)
	if want := []config.Var{{Name: "PIPEDRIVE_ASKED"}, {Name: "PIPEDRIVE_ADDED_LATER", Switch: true}}; !slices.Equal(asked, want) {
		t.Errorf("asked = %v, want %v", asked, want)
	}
	if want := []string{"bundleLeftOut names PIPEDRIVE_GONE, which the server does not read"}; !slices.Equal(problems, want) {
		t.Errorf("problems = %q, want %q", problems, want)
	}
}

// The sensitive check reads the secrets the registry marks, so a bundle
// asking for none of them is refused rather than passed with nothing
// checked.
func TestSettingProblemsRefuseABundleWithNoSecret(t *testing.T) {
	leftOut := maps.Clone(bundleLeftOut)
	leftOut[config.EnvAPIToken] = "a reason"
	const want = "the bundle asks for no variable config.Vars marks secret"
	if got := settingProblems(good(t), leftOut); !mentions(got, want) {
		t.Errorf("settingProblems = %q, want one containing %q", got, want)
	}
}

// A stale exclusion fails the gate itself, not only the derivation, and
// a variable added later is asked for until the bundle asks or a reason
// is written.
func TestSettingProblemsRefuseAStaleExclusion(t *testing.T) {
	leftOut := maps.Clone(bundleLeftOut)
	leftOut["PIPEDRIVE_GONE"] = "a reason"
	want := "bundleLeftOut names PIPEDRIVE_GONE, which the server does not read"
	if got := settingProblems(good(t), leftOut); !slices.Contains(got, want) {
		t.Errorf("settingProblems = %q, want %q among them", got, want)
	}
	leftOut = maps.Clone(bundleLeftOut)
	delete(leftOut, "PIPEDRIVE_HTTP_TIMEOUT")
	want = "user_config does not ask for http_timeout, which sets PIPEDRIVE_HTTP_TIMEOUT"
	if got := settingProblems(good(t), leftOut); !slices.Contains(got, want) {
		t.Errorf("settingProblems = %q, want %q among them", got, want)
	}
}

// A field the defaults change is named, an unexported one included
// where its type compares; an unexported one that does not is reported
// unnamed, rather than panicking.
func TestTheConfigDiffNamesTheFieldsThatDiffer(t *testing.T) {
	type cfg struct {
		Shown  int
		hidden int
		rest   []string
		held   any
	}
	for _, tc := range []struct {
		b    cfg
		want []string
	}{
		{cfg{2, 1, nil, nil}, []string{"Shown"}},
		{cfg{1, 2, nil, nil}, []string{"hidden"}},
		{cfg{1, 1, []string{"x"}, nil}, []string{"an unexported field"}},
		{cfg{1, 1, nil, nil}, nil},
	} {
		if got := fieldDiff(cfg{1, 1, nil, nil}, tc.b); !slices.Equal(got, tc.want) {
			t.Errorf("fieldDiff against %+v = %q, want %q", tc.b, got, tc.want)
		}
	}
	// An interface holding a slice on both sides compares as a type and
	// panics in Value.Equal; it is reported unnamed instead.
	if got := fieldDiff(cfg{held: []string{"y"}}, cfg{held: []string{"x"}}); !slices.Equal(got,
		[]string{"an unexported field"}) {
		t.Errorf("an interface field differing gives %q, want [an unexported field]", got)
	}
	if got := fieldDiff(config.Config{CompanyDomain: "a", DryRun: true}, config.Config{CompanyDomain: "a"}); !slices.Equal(got,
		[]string{"DryRun"}) {
		t.Errorf("fieldDiff on the config = %q, want [DryRun]", got)
	}
	// An unexported field that does not compare is reported whenever it
	// differs, beside a named field that differs too.
	if got := fieldDiff(cfg{1, 1, nil, nil}, cfg{2, 1, []string{"x"}, nil}); !slices.Equal(got,
		[]string{"Shown", "an unexported field"}) {
		t.Errorf("a named and an unnamed field differing give %q, want [Shown an unexported field]", got)
	}
}

// A pointer is followed rather than compared by address, so two
// configurations reaching equal values are equal, and two reaching
// different ones are not.
func TestTheConfigDiffFollowsPointers(t *testing.T) {
	type cfg struct {
		at    *int
		inner struct{ at *int }
		run   func()
	}
	one, alsoOne, two := 1, 1, 2
	same := func(p *int) cfg {
		c := cfg{at: p}
		c.inner.at = p
		return c
	}
	if got := fieldDiff(same(&one), same(&alsoOne)); len(got) != 0 {
		t.Errorf("pointers to equal values give %q, want nothing", got)
	}
	if got := fieldDiff(same(&one), same(&two)); !slices.Equal(got, []string{"an unexported field"}) {
		t.Errorf("pointers to different values give %q, want [an unexported field]", got)
	}
	if got := fieldDiff(same(nil), same(&one)); !slices.Equal(got, []string{"an unexported field"}) {
		t.Errorf("a nil pointer and a set one give %q, want [an unexported field]", got)
	}
	if got := fieldDiff(cfg{run: func() {}}, cfg{}); !slices.Equal(got, []string{"an unexported field"}) {
		t.Errorf("a func and none give %q, want [an unexported field]", got)
	}
}

// Load refuses no set of the switches, and no setting it reads can be
// made to refuse one, so the refusal check is made to fire through its
// start: refusing is a start that refuses the server whenever the
// variable it names is set to the value given.
func refusing(name, value string) func(map[string]string) error {
	return func(env map[string]string) error {
		if env[name] == value {
			return errors.New("config: refused " + name + "=" + value)
		}
		return nil
	}
}

// withExtra is the committed manifest with a second switch, extra,
// defaulting to def, spent as PIPEDRIVE_EXTRA, so a set of two can be
// started.
func withExtra(t *testing.T, def bool, edit func(map[string]any)) manifest {
	t.Helper()
	return edited(t, func(d map[string]any) {
		dig(d, "user_config")["extra"] = map[string]any{
			"type": "boolean", "title": "Extra", "description": "Another switch.", "default": def}
		dig(d, "server", "mcp_config", "env")["PIPEDRIVE_EXTRA"] = "${user_config.extra}"
		edit(d)
	})
}

// undeclaredDryRun is the problem a start refusing dry_run on gives with
// nothing declared.
const undeclaredDryRun = "the server refuses [dry_run on], and bundleRefused does not declare it"

// Every set of the switches is started, and the refused ones are held to
// the declared list both ways, each defect reported once, for the
// smallest set that shows it.
func TestTheBundleRefusalsAreDeclared(t *testing.T) {
	committed := good(t)
	one := []string{"dry_run"}
	refused := refusing("PIPEDRIVE_DRY_RUN", "true")
	if got := refusalProblems(committed, one, nil, nil, refused); len(got) != 1 ||
		!strings.HasPrefix(got[0], undeclaredDryRun) {
		t.Errorf("an undeclared refusal gives %q, want the one problem %q", got, undeclaredDryRun)
	}
	declared := [][]string{{"dry_run"}}
	if got := refusalProblems(committed, one, declared, nil, refused); len(got) != 0 {
		t.Errorf("a declared refusal gives %q, want nothing", got)
	}
	const starts = "bundleRefused declares that the server refuses [dry_run on], and it starts with them"
	if got := refusalProblems(committed, one, declared, nil, startEnv); !slices.Equal(got, []string{starts}) {
		t.Errorf("a declared refusal the server starts with gives %q, want %q once", got, starts)
	}
	// When the defaults do not start, no set is started: the defaults
	// check reports it.
	if got := refusalProblems(committed, one, declared, errors.New("the defaults do not start"), startEnv); len(got) != 0 {
		t.Errorf("a set was started beside defaults that do not start: %q", got)
	}
	if got := refusalProblems(committed, one, [][]string{{"api_token", "dry_run"}}, nil, refused); !slices.Contains(got,
		"bundleRefused names api_token, which is not a switch the bundle asks for") {
		t.Errorf("a declared key that is not a switch gives %q", got)
	}
	// dry_run on is refused alone, so the pair with extra, which is
	// refused too, is not reported again.
	two := []string{"dry_run", "extra"}
	if got := refusalProblems(withExtra(t, false, func(map[string]any) {}), two, nil, nil, refused); len(got) != 1 ||
		!strings.HasPrefix(got[0], undeclaredDryRun) {
		t.Errorf("a refusal and its superset give %q, want the one problem %q", got, undeclaredDryRun)
	}
}

// Each field in a declared set names the others' titles, trimmed, and a
// key with no title is reported once, however many sets name it.
func TestTheDeclaredSetsNameEachOther(t *testing.T) {
	two := []string{"dry_run", "extra"}
	pair := [][]string{{"dry_run", "extra"}}
	named := withExtra(t, false, func(d map[string]any) {
		dig(d, "user_config", "dry_run")["description"] = "Refused with Extra on."
		dig(d, "user_config", "extra")["description"] = "Refused with Dry run on."
		dig(d, "user_config", "extra")["title"] = " Extra "
	})
	if got := declaredProblems(named, two, pair); len(got) != 0 {
		t.Errorf("fields that name each other give %q, want nothing", got)
	}
	want := []string{
		`user_config.dry_run does not name "Extra", which the server refuses it with`,
		`user_config.extra does not name "Dry run", which the server refuses it with`,
	}
	if got := declaredProblems(withExtra(t, false, func(map[string]any) {}), two, pair); !slices.Equal(got, want) {
		t.Errorf("fields that do not name each other give %q, want %q", got, want)
	}
	untitled := withExtra(t, false, func(d map[string]any) { dig(d, "user_config", "dry_run")["title"] = " " })
	twice := [][]string{{"dry_run", "extra"}, {"dry_run"}}
	for _, defaultsErr := range []error{nil, errors.New("the defaults do not start")} {
		got := refusalProblems(untitled, two, twice, defaultsErr, startEnv)
		if n := len(slices.DeleteFunc(got, func(p string) bool {
			return !strings.HasPrefix(p, "user_config.dry_run has no title")
		})); n != 1 {
			t.Errorf("defaults error %v: an untitled dry_run is reported %d times, want 1", defaultsErr, n)
		}
	}
}

// A switch in a set is on unless its default is true: a required one
// with no default, or one with a list, is turned on rather than left
// out, which would hide a refusal. One that defaults to true is turned
// off.
func TestTheRefusalsTurnEachSwitchFromItsDefault(t *testing.T) {
	refused := refusing("PIPEDRIVE_DRY_RUN", "true")
	for name, edit := range map[string]func(map[string]any){
		"required with no default": func(d map[string]any) {
			dig(d, "user_config", "dry_run")["required"] = true
			delete(dig(d, "user_config", "dry_run"), "default")
		},
		"a list default": func(d map[string]any) { dig(d, "user_config", "dry_run")["default"] = []any{"true"} },
	} {
		got := refusalProblems(edited(t, edit), []string{"dry_run"}, nil, nil, refused)
		if !slices.ContainsFunc(got, func(p string) bool { return strings.HasPrefix(p, undeclaredDryRun) }) {
			t.Errorf("%s: got %q, want one starting %q", name, got, undeclaredDryRun)
		}
	}
	const off = "the server refuses [extra off], and bundleRefused does not declare it"
	m := withExtra(t, true, func(map[string]any) {})
	if got := refusalProblems(m, []string{"extra"}, nil, nil, refusing("PIPEDRIVE_EXTRA", "false")); len(got) != 1 ||
		!strings.HasPrefix(got[0], off) {
		t.Errorf("a refusal of a default-on switch turned off gives %q, want the one problem %q", got, off)
	}
}

// settingProblems runs the refusal check over the switches the bundle
// asks for, against the server's own start, so a refusal Load adds, or
// one declared that Load does not make, fails the gate itself.
func TestSettingProblemsHoldTheRefusals(t *testing.T) {
	saved := bundleRefused
	t.Cleanup(func() { bundleRefused = saved })
	bundleRefused = [][]string{{"dry_run"}}
	const starts = "bundleRefused declares that the server refuses [dry_run on], and it starts with them"
	if got := settingProblems(good(t), bundleLeftOut); !slices.Equal(got, []string{starts}) {
		t.Errorf("settingProblems = %q, want %q", got, starts)
	}
	bundleRefused = saved
	// Defaults the server refuses start no set, which would each fail
	// with them: the defaults problem is the one report.
	got := settingProblems(edited(t, func(d map[string]any) {
		dig(d, "user_config", "dry_run")["default"] = "maybe"
	}), bundleLeftOut)
	if len(got) != 1 || !strings.HasPrefix(got[0], "the environment the bundle's defaults give the server is refused") {
		t.Errorf("defaults the server refuses give %q, want that one problem", got)
	}
}

// ------------------------------------------------ schema conformance
//
// The referential checks above ask whether the manifest's names resolve
// to things this repository stages. These ask the other question: does
// the document satisfy the schema it cites. Each case below is watched
// failing, for the same reason the rest of this file is.

// goodDocument is the committed manifest as a plain JSON value: decoding
// into the typed struct drops every key it does not declare, and those
// keys are most of what a schema is there to check.
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
	if err := checkAgainstSchema(repoRoot(t), goodDocument(t)); err != nil {
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
	document := goodDocument(t)
	document["manifest_version"] = "9.9"
	err := checkAgainstSchema(repoRoot(t), document)
	if err == nil {
		t.Fatal("a manifest_version with no vendored schema was accepted")
	}
	for _, want := range []string{"no copy of its schema is vendored", schemaFor("9.9"), vendoredSchemaPath("9.9")} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

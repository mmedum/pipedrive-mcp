package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The packer is the one gate whose output nobody reads before it ships:
// the bundle goes into a release and the next person to open it is
// somebody installing it. These tests pack a real bundle from a fake
// dist and read it back.

// fakeDist writes the four binaries the globs in bundleFiles expect,
// laid out the way goreleaser lays them out, and returns the dist path.
func fakeDist(t *testing.T) string {
	t.Helper()
	dist := t.TempDir()
	for path, body := range map[string]string{
		"pipedrive-mcp-universal_darwin_all/pipedrive-mcp": "macho-universal",
		"pipedrive-mcp_windows_amd64_v1/pipedrive-mcp.exe": "pe-amd64",
		"pipedrive-mcp_linux_amd64_v1/pipedrive-mcp":       "elf-amd64",
		"pipedrive-mcp_linux_arm64_v8.0/pipedrive-mcp":     "elf-arm64",
	} {
		full := filepath.Join(dist, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dist
}

// packed reads a bundle back as name -> contents, with the mode.
type entry struct {
	body string
	mode os.FileMode
}

func packed(t *testing.T, path string) map[string]entry {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open bundle: %v", err)
	}
	defer func() { _ = r.Close() }()

	out := map[string]entry{}
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		out[f.Name] = entry{body: string(body), mode: f.Mode()}
	}
	return out
}

// packHere packs version 1.2.3 from a fake dist and reads it back.
func packHere(t *testing.T) map[string]entry {
	t.Helper()
	t.Chdir(repoRoot(t))
	out := filepath.Join(t.TempDir(), "out.mcpb")
	if err := packMCPB(fakeDist(t), "1.2.3", out); err != nil {
		t.Fatalf("pack: %v", err)
	}
	return packed(t, out)
}

// repoRoot is where the gates run from. The test runs in its own package
// directory, and the packer reads repository-relative paths.
func repoRoot(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat("../../" + manifestPath); err == nil {
		return "../.."
	}
	return "."
}

func TestPackRefusesThePlaceholderAsAVersion(t *testing.T) {
	t.Chdir(repoRoot(t))
	err := packMCPB(fakeDist(t), placeholderVersion, filepath.Join(t.TempDir(), "out.mcpb"))
	if err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Fatalf("packing with the placeholder version gave %v", err)
	}
}

// The packer starts where the gate does, so the release packs no
// manifest the gate would refuse.
func TestPackRefusesAManifestTheGateRefuses(t *testing.T) {
	dist := fakeDist(t)
	root := copiedRoot(t)
	put(t, root, manifestPath, string(editedRaw(t, func(d map[string]any) { d["version"] = "1.2.3" })))
	t.Chdir(root)
	err := packMCPB(dist, "1.2.3", filepath.Join(t.TempDir(), "out.mcpb"))
	if err == nil || !strings.Contains(err.Error(), `the committed manifest says version "1.2.3"`) {
		t.Fatalf("packing a manifest the gate refuses gave %v", err)
	}
}

// TestThePackerStampsTheVersionThroughJSON: the version goes in through
// a decode and an encode rather than a substitution over text, so this
// reads the version back out of the packed manifest by parsing it.
func TestThePackerStampsTheVersionThroughJSON(t *testing.T) {
	files := packHere(t)

	manifestEntry, ok := files["manifest.json"]
	if !ok {
		t.Fatal("the bundle has no manifest.json at its root")
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(manifestEntry.body), &document); err != nil {
		t.Fatalf("the packed manifest is not valid JSON: %v", err)
	}
	if document["version"] != "1.2.3" {
		t.Errorf("the packed manifest says version %v; want 1.2.3", document["version"])
	}
	// The committed file is untouched: the stamp happens in memory.
	if v := good(t).Version; v != "0.0.0-dev" {
		t.Errorf("packing rewrote the committed manifest to %q", v)
	}
}

// TestEveryStagedFileIsInTheBundle holds the archive to the table rather
// than to a list written twice.
func TestEveryStagedFileIsInTheBundle(t *testing.T) {
	files := packHere(t)
	for _, f := range bundleFiles {
		if _, ok := files[f.path]; !ok {
			t.Errorf("%s is staged but not in the bundle", f.path)
		}
	}
	if len(files) != len(bundleFiles)+1 { // +1 for the manifest
		t.Errorf("the bundle holds %d entries; the table stages %d plus a manifest",
			len(files), len(bundleFiles))
	}
}

// What runs is packed 0755, because the desktop spawns it with the mode
// the zip gives it, and what is there to be read is packed 0644 with
// this repository's own bytes.
func TestEachFileIsPackedWithItsMode(t *testing.T) {
	files := packHere(t)
	for path, want := range map[string]os.FileMode{
		"server/pipedrive-mcp-darwin":      0o755,
		"server/pipedrive-mcp.exe":         0o755,
		"server/launch-linux.sh":           0o755,
		"server/pipedrive-mcp-linux-amd64": 0o755,
		"server/pipedrive-mcp-linux-arm64": 0o755,
		"LICENSE":                          0o644,
		"README.md":                        0o644,
	} {
		if got := files[path].mode.Perm(); got != want {
			t.Errorf("%s is packed %v, want %v", path, got, want)
		}
	}
	for _, path := range []string{"LICENSE", "README.md"} {
		if files[path].body != string(repoFile(t, path)) {
			t.Errorf("the packed %s is not this repository's", path)
		}
	}
}

// failingClose is a bundle file whose close fails, as a full disk's
// last write does.
type failingClose struct{ bytes.Buffer }

var errClose = errors.New("the last write did not reach the disk")

func (*failingClose) Close() error { return errClose }

// A bundle whose close fails is not packed: the end of the archive
// reaches the disk only there.
func TestAFailedCloseFailsThePack(t *testing.T) {
	t.Chdir(repoRoot(t))
	stamped, err := stampedManifest("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	sources, err := stagedSources(fakeDist(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeBundle(&failingClose{}, stamped, sources, generatedFiles()); !errors.Is(err, errClose) {
		t.Errorf("a bundle whose close failed gave %v, want %v", err, errClose)
	}
}

// TestTheGeneratedLauncherIsPackedExecutable is the pair to deleting the
// committed script. It is now written by the packer, so nothing on disk
// carries its mode — and a launcher that arrives without the execute bit
// is a bundle that fails for every Linux user at first run.
func TestTheGeneratedLauncherIsPackedExecutable(t *testing.T) {
	files := packHere(t)

	launcher, ok := files[launcherName]
	if !ok {
		t.Fatalf("%s is not in the bundle", launcherName)
	}
	if launcher.mode&0o111 == 0 {
		t.Errorf("the launcher is packed %v, which is not executable", launcher.mode)
	}
	if launcher.body != launcherScript() {
		t.Error("the packed launcher is not what launcherScript generates")
	}
	if !strings.HasPrefix(launcher.body, "#!/bin/sh") {
		t.Error("the packed launcher has no shebang")
	}
}

// TestEveryEntryCarriesTheFixedTimestamp is the half of determinism
// that comparing two archives cannot see.
//
// Zip stores DOS timestamps at two-second granularity, so two packs a
// millisecond apart are byte-identical whatever the clock says —
// TestTwoPacksOfTheSameInputsAgree below passes with zipTime replaced
// by time.Now(), which is how this hole was found. The expected instant
// is written out here rather than read from zipTime, because a test
// asserting that a value equals itself is the shape of the bug.
func TestEveryEntryCarriesTheFixedTimestamp(t *testing.T) {
	want := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

	t.Chdir(repoRoot(t))
	out := filepath.Join(t.TempDir(), "out.mcpb")
	if err := packMCPB(fakeDist(t), "1.2.3", out); err != nil {
		t.Fatalf("pack: %v", err)
	}
	r, err := zip.OpenReader(out)
	if err != nil {
		t.Fatalf("open bundle: %v", err)
	}
	defer func() { _ = r.Close() }()

	if len(r.File) == 0 {
		t.Fatal("the bundle is empty; this test is reading nothing")
	}
	for _, f := range r.File {
		if !f.Modified.UTC().Equal(want) {
			t.Errorf("%s is stamped %s; every entry must carry %s",
				f.Name, f.Modified.UTC(), want)
		}
	}
}

// TestTwoPacksOfTheSameInputsAgree: the bundle is named in checksums.txt
// and the signature is over that file, so a packer that wrote a
// different archive each time would break the one thing the signature
// is for.
//
// This catches ordering — a map walked twice — rather than the clock;
// the timestamp is the test above.
func TestTwoPacksOfTheSameInputsAgree(t *testing.T) {
	t.Chdir(repoRoot(t))
	dist := fakeDist(t)
	dir := t.TempDir()

	first := filepath.Join(dir, "first.mcpb")
	second := filepath.Join(dir, "second.mcpb")
	for _, out := range []string{first, second} {
		if err := packMCPB(dist, "1.2.3", out); err != nil {
			t.Fatalf("pack: %v", err)
		}
	}
	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Error("two packs of the same inputs wrote different bytes")
	}
}

// TestPackRefusesADistMissingABinary: a glob matching nothing must stop
// the pack. A bundle short one platform's binary installs fine and fails
// only for the people on that platform.
func TestPackRefusesADistMissingABinary(t *testing.T) {
	t.Chdir(repoRoot(t))
	err := packMCPB(t.TempDir(), "1.2.3", filepath.Join(t.TempDir(), "out.mcpb"))
	if err == nil {
		t.Fatal("packing from an empty dist was allowed")
	}
}

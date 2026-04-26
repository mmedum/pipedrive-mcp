package userconfig_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mmedum/pipedrive-mcp/internal/userconfig"
)

func TestLoad_MissingFileReturnsZero(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nope.json")
	f, err := userconfig.Load(p)
	if err != nil {
		t.Fatalf("Load on missing file: unexpected error %v", err)
	}
	if f.DefaultDomain != "" {
		t.Errorf("missing file: DefaultDomain = %q, want empty", f.DefaultDomain)
	}
}

func TestLoad_ParseErrorSurfaced(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(p, []byte("not valid json"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := userconfig.Load(p); err == nil {
		t.Fatal("Load on malformed file: want error, got nil")
	}
}

func TestSaveLoad_Roundtrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "subdir", "config.json")
	if err := userconfig.Save(p, userconfig.File{DefaultDomain: "partisia"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := userconfig.Load(p)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if got.DefaultDomain != "partisia" {
		t.Errorf("DefaultDomain = %q, want partisia", got.DefaultDomain)
	}
}

func TestSave_FilePermsAre0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission semantics don't apply on Windows")
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if err := userconfig.Save(p, userconfig.File{DefaultDomain: "x"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file perm = %o, want 0600", perm)
	}
}

func TestSave_DirPermsAre0700(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission semantics don't apply on Windows")
	}
	root := t.TempDir()
	p := filepath.Join(root, "fresh", "config.json")
	if err := userconfig.Save(p, userconfig.File{DefaultDomain: "x"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(filepath.Dir(p))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir perm = %o, want 0700", perm)
	}
}

func TestSetDefaultDomain_CreatesAndOverwrites(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := userconfig.SetDefaultDomain(p, "first"); err != nil {
		t.Fatalf("Set first: %v", err)
	}
	if err := userconfig.SetDefaultDomain(p, "second"); err != nil {
		t.Fatalf("Set second: %v", err)
	}
	got, err := userconfig.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.DefaultDomain != "second" {
		t.Errorf("DefaultDomain = %q, want second", got.DefaultDomain)
	}
}

func TestClearDefaultDomainIfMatches_OnlyMatching(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := userconfig.SetDefaultDomain(p, "partisia"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Clearing for a different domain is a no-op.
	if err := userconfig.ClearDefaultDomainIfMatches(p, "other"); err != nil {
		t.Fatalf("Clear non-matching: %v", err)
	}
	got, _ := userconfig.Load(p)
	if got.DefaultDomain != "partisia" {
		t.Errorf("non-matching clear changed pointer: %q", got.DefaultDomain)
	}

	// Clearing for the matching domain wipes it.
	if err := userconfig.ClearDefaultDomainIfMatches(p, "partisia"); err != nil {
		t.Fatalf("Clear matching: %v", err)
	}
	got, _ = userconfig.Load(p)
	if got.DefaultDomain != "" {
		t.Errorf("matching clear left pointer = %q, want empty", got.DefaultDomain)
	}
}

func TestClearDefaultDomainIfMatches_MissingFile(t *testing.T) {
	// Should be a clean no-op when the file doesn't exist (logout
	// running before any login should not error).
	p := filepath.Join(t.TempDir(), "nope.json")
	if err := userconfig.ClearDefaultDomainIfMatches(p, "anything"); err != nil {
		t.Errorf("Clear on missing file: unexpected error %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("Clear on missing file created it: stat err = %v", err)
	}
}

func TestDefaultPath_NonEmpty(t *testing.T) {
	got, err := userconfig.DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if got == "" {
		t.Fatal("DefaultPath returned empty string")
	}
	if filepath.Base(got) != "config.json" {
		t.Errorf("DefaultPath = %q, want it to end in config.json", got)
	}
}

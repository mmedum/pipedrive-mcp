package version

import (
	"strings"
	"testing"
)

func TestInfo_String(t *testing.T) {
	b := BuildInfo{
		Version:   "v1.2.3",
		Commit:    "abcdef1234567890",
		Time:      "2026-04-26T01:00:00Z",
		Dirty:     true,
		GoVersion: "go1.26.2",
	}
	got := b.String()
	for _, want := range []string{"v1.2.3", "abcdef123456", "-dirty", "2026-04-26", "go1.26.2"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q; missing %q", got, want)
		}
	}
}

func TestInfo_StringFallbacks(t *testing.T) {
	// Empty Commit/Time/Dirty=false should render with "unknown" placeholders
	// and no -dirty suffix.
	got := BuildInfo{Version: "dev"}.String()
	if !strings.Contains(got, "unknown") {
		t.Errorf("String() = %q; expected 'unknown' fallback", got)
	}
	if strings.Contains(got, "-dirty") {
		t.Errorf("String() = %q; should not include -dirty when Dirty=false", got)
	}
}

func TestInfo_ReadsBuildSettings(t *testing.T) {
	// Info() reads from debug.ReadBuildInfo, which during `go test` populates
	// vcs.* settings if -buildvcs=auto is in effect. The test only asserts
	// that the function returns a value with the package-level Version
	// fallback; the VCS fields are environment-dependent.
	got := Info()
	if got.Version != Version {
		t.Errorf("Info().Version = %q, want %q", got.Version, Version)
	}
}

// TestCanonicalOneSpellingPerRelease holds the v prefix across both
// sources. goreleaser stamps "0.3.1" and `go install` reports "v0.3.1"
// for the same release; anything parsing --version must not get a
// different answer per install method.
func TestCanonicalOneSpellingPerRelease(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"0.3.1", "v0.3.1"},
		{"v0.3.1", "v0.3.1"},
		{"dev", "dev"},
		{"", ""},
	} {
		if got := canonical(tc.in); got != tc.want {
			t.Errorf("canonical(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestLdflagsStampWins keeps the linker as the source of truth for a
// release build, so the build-info fallback cannot override it.
func TestLdflagsStampWins(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })

	Version = "0.9.9"
	if got := Info().Version; got != "v0.9.9" {
		t.Errorf("stamped build reported %q, want v0.9.9", got)
	}
}

// TestUnstampedBuildDoesNotSayDevWhenGoKnowsBetter is the bug this
// fallback exists for: `go install module@v0.3.1` applies no ldflags, so
// without it the installed binary calls itself "dev" forever and cannot
// say which release it came from. Under `go test` the main module
// version is "(devel)", so the honest answer here is still "dev" — what
// this asserts is that Info consults the build info at all rather than
// returning the package variable untouched.
func TestUnstampedBuildDoesNotSayDevWhenGoKnowsBetter(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })

	Version = "dev"
	info := Info()
	if info.GoVersion == "" {
		t.Fatal("Info did not read debug.BuildInfo at all")
	}
	if info.Version != "dev" {
		t.Errorf("under `go test` the module version is %q; want the untouched %q", info.Version, "dev")
	}
}

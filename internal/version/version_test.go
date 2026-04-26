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

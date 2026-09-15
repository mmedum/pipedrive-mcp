// Package version exposes build identity for the binary.
//
// The release workflow stamps Version via -ldflags:
//
//	go build -ldflags="-X github.com/mmedum/pipedrive-mcp/internal/version.Version=v1.2.3" ...
//
// VCS metadata (commit SHA, build time, dirty flag) is read from
// runtime/debug.BuildInfo. Go's default -buildvcs=auto populates these
// for any module-rooted build, so they're free.
//
// BuildInfo.Main.Version is "(devel)" for a plain `go build`, so it is
// not a source for the tag — but it is exactly right for
// `go install module@version`, which applies no ldflags at all. It is
// used as the fallback there, and only there (golang/go#29228).
package version

import (
	"fmt"
	"runtime/debug"
)

// Version is set at build time. Default "dev" for local builds.
var Version = "dev"

// BuildInfo bundles the human-readable identity of the running binary.
type BuildInfo struct {
	Version   string // tag (from -ldflags) or "dev"
	Commit    string // VCS revision; empty if unavailable
	Time      string // build time in RFC3339; empty if unavailable
	Dirty     bool   // true if VCS reported uncommitted changes
	GoVersion string
}

// Info returns the current build identity. debug.ReadBuildInfo is
// already runtime-cached; no further caching needed.
func Info() BuildInfo {
	i := BuildInfo{Version: canonical(Version)}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return i
	}
	i.GoVersion = bi.GoVersion
	// `go install module@v1.2.3` applies no ldflags, so a binary
	// installed the way the README suggests would call itself "dev"
	// forever. Go records the module version it was built from, which
	// is the honest answer in that case.
	if Version == "dev" {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			i.Version = canonical(v)
		}
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			i.Commit = s.Value
		case "vcs.time":
			i.Time = s.Value
		case "vcs.modified":
			i.Dirty = s.Value == "true"
		}
	}
	return i
}

// canonical is the one spelling of a release, whichever way the binary
// was built.
//
// There are two sources and they disagree. goreleaser stamps Version
// with its own {{.Version}}, which has the leading v stripped, so a
// release archive said "0.3.1"; `go install` stamps nothing and the
// fallback reads the module version out of the build info, which is
// "v0.3.1". The same release therefore reported two different strings
// depending on how somebody installed it, and anything parsing
// --version got a different answer per install method. Reported from
// outside, by a reader comparing the servers side by side, and fixed in
// the four sibling servers before this one.
//
// The v stays, because that is how the tag, the module version and the
// release are all named; "dev" and any other non-release string are
// left exactly as they are.
func canonical(v string) string {
	if v == "" || v == "dev" {
		return v
	}
	if v[0] >= '0' && v[0] <= '9' {
		return "v" + v
	}
	return v
}

// String renders Info as a single line suitable for `--version` output.
func (b BuildInfo) String() string {
	commit := b.Commit
	if commit == "" {
		commit = "unknown"
	} else if len(commit) > 12 {
		commit = commit[:12]
	}
	dirty := ""
	if b.Dirty {
		dirty = "-dirty"
	}
	t := b.Time
	if t == "" {
		t = "unknown"
	}
	return fmt.Sprintf("%s (%s%s, built %s, %s)", b.Version, commit, dirty, t, b.GoVersion)
}

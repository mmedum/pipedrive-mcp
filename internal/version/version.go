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
// Note: we deliberately do NOT use BuildInfo.Main.Version for the tag —
// that field is "(devel)" for `go build` and only populates correctly
// under `go install module@version` (golang/go#29228). Tags must come
// from the linker.
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
	i := BuildInfo{Version: Version}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return i
	}
	i.GoVersion = bi.GoVersion
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

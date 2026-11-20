package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// crossMinTargets is the floor on how many targets the gate builds: the
// six archives the release ships. Fewer means it read the wrong shape.
const crossMinTargets = 6

// crossGate builds the server for every goos-goarch pair
// .goreleaser.yaml releases, so a change that breaks one target, such as
// windows/arm64, fails on its commit rather than in the release job.
func crossGate(out io.Writer, _ []string) error {
	g, err := readGoreleaser(".")
	if err != nil {
		return err
	}
	pairs, err := goreleaserMatrix(g)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "cross-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	return crossBuild(out, pairs, func(goos, goarch string) error {
		cmd := exec.Command("go", "build",
			"-trimpath", "-o", filepath.Join(dir, goos+"-"+goarch), "./cmd/pipedrive-mcp")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
		if msg, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(msg)))
		}
		return nil
	})
}

// crossBuild runs build for each "goos-goarch" pair and reports every
// pair that fails, not only the first.
func crossBuild(out io.Writer, pairs []string, build func(goos, goarch string) error) error {
	if len(pairs) < crossMinTargets {
		return fmt.Errorf("cross: %d targets, fewer than the %d the release ships; this read the wrong shape", len(pairs), crossMinTargets)
	}
	var failed []string
	for _, pair := range pairs {
		goos, goarch, ok := strings.Cut(pair, "-")
		if !ok {
			return fmt.Errorf("cross: %q is not a goos-goarch pair", pair)
		}
		if err := build(goos, goarch); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", pair, err))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("cross: %d of %d targets fail to build:\n  %s", len(failed), len(pairs), strings.Join(failed, "\n  "))
	}
	_, err := fmt.Fprintf(out, "cross ok: %d targets build: %s\n", len(pairs), strings.Join(pairs, ", "))
	return err
}

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// crossJob is one release target and how .goreleaser.yaml builds it.
// ldflags are left out: their templates need a version that exists only
// at release time, and an -X stamp cannot break a compile.
type crossJob struct {
	pair  string
	main  string
	env   []string
	flags []string
	tags  []string
}

// crossGate builds the server for every goos-goarch pair
// .goreleaser.yaml releases, the way each build there builds it, so a
// change that breaks one target, such as windows/arm64, fails on its
// commit rather than in the release job.
func crossGate(out io.Writer, _ []string) error {
	g, err := readGoreleaser(".")
	if err != nil {
		return err
	}
	jobs, err := crossJobs(g)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "cross-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	// The toolchain running this gate rather than whichever go is first
	// on PATH: the go command sets GOROOT for the program it runs when it
	// switched toolchains, and leaves it unset when PATH's go is the one.
	goBin := "go"
	if root := os.Getenv("GOROOT"); root != "" {
		goBin = filepath.Join(root, "bin", "go")
	}
	return crossBuild(out, jobs, len(releaseMatrix), func(j crossJob) error {
		goos, goarch, _ := strings.Cut(j.pair, "-")
		args := append([]string{"build"}, j.flags...)
		if len(j.tags) > 0 {
			args = append(args, "-tags", strings.Join(j.tags, ","))
		}
		args = append(args, "-o", filepath.Join(dir, j.pair), j.main)
		cmd := exec.Command(goBin, args...)
		// GOFLAGS and GOEXPERIMENT are cleared, so a developer's own
		// settings cannot change what is compiled.
		cmd.Env = append(append(os.Environ(), "GOFLAGS=", "GOEXPERIMENT="), j.env...)
		cmd.Env = append(cmd.Env, "GOOS="+goos, "GOARCH="+goarch)
		if msg, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%w\n%s", err, strings.TrimSpace(string(msg)))
		}
		return nil
	})
}

// crossJobs reads each build's targets and settings.
func crossJobs(g goreleaserFile) ([]crossJob, error) {
	var jobs []crossJob
	var problems []string
	for _, build := range g.builds() {
		pairs, refused := goreleaserBuildPairs(build)
		problems = append(problems, refused...)
		var j crossJob
		for _, v := range build {
			switch v.keys {
			case "builds.main":
				j.main = v.value
			case "builds.env":
				j.env = append(j.env, v.value)
			case "builds.flags":
				j.flags = append(j.flags, v.value)
			case "builds.tags":
				j.tags = append(j.tags, v.value)
			}
		}
		if j.main == "" {
			problems = append(problems, fmt.Sprintf("%s:%d: a build without main, which this gate does not guess", parityGoreleaser, build[0].line))
		}
		for _, p := range pairs {
			j.pair = p
			jobs = append(jobs, j)
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	return jobs, nil
}

// crossBuild runs build for each job and reports every one that fails,
// not only the first. floor is how many targets the release ships, so a
// gate that read the wrong shape refuses rather than passing on nothing.
func crossBuild(out io.Writer, jobs []crossJob, floor int, build func(crossJob) error) error {
	if len(jobs) < floor {
		return fmt.Errorf("cross: %d targets, fewer than the %d the release ships; this read the wrong shape", len(jobs), floor)
	}
	var failed, built []string
	for _, j := range jobs {
		if _, _, ok := strings.Cut(j.pair, "-"); !ok {
			return fmt.Errorf("cross: %q is not a goos-goarch pair", j.pair)
		}
		if err := build(j); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", j.pair, err))
			continue
		}
		built = append(built, j.pair)
	}
	if len(failed) > 0 {
		return fmt.Errorf("cross: %d of %d targets fail to build:\n  %s", len(failed), len(jobs), strings.Join(failed, "\n  "))
	}
	_, err := fmt.Fprintf(out, "cross ok: %d targets build: %s\n", len(built), strings.Join(built, ", "))
	return err
}

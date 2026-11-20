package main

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

var sixTargets = func() []crossJob {
	jobs := make([]crossJob, 0, 6)
	for _, p := range []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64", "windows-arm64"} {
		jobs = append(jobs, crossJob{pair: p, main: "./cmd/x"})
	}
	return jobs
}()

func TestCrossBuild_PassesWhenEveryTargetBuilds(t *testing.T) {
	var built []string
	err := crossBuild(io.Discard, sixTargets, 6, func(j crossJob) error {
		built = append(built, j.pair)
		return nil
	})
	if err != nil || len(built) != 6 {
		t.Errorf("crossBuild = %v after %d builds; want nil after 6", err, len(built))
	}
}

func TestCrossBuild_NamesTheTargetThatFails(t *testing.T) {
	err := crossBuild(io.Discard, sixTargets, 6, func(j crossJob) error {
		if j.pair == "windows-arm64" {
			return errors.New("undefined: syscall.Thing")
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "windows-arm64: undefined: syscall.Thing") || !strings.Contains(err.Error(), "1 of 6") {
		t.Errorf("crossBuild = %v; want the failing target named with its error", err)
	}
}

func TestCrossBuild_RefusesFewerTargetsThanTheRelease(t *testing.T) {
	err := crossBuild(io.Discard, sixTargets[:5], 6, func(crossJob) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "fewer than the 6") {
		t.Errorf("crossBuild over 5 targets = %v; want the floor to refuse", err)
	}
}

func TestCrossJobs_BuildsEachBuildTheWayItIsConfigured(t *testing.T) {
	jobs, err := crossJobs(parseGoreleaser(`builds:
  - id: a
    main: ./cmd/a
    env:
      - CGO_ENABLED=0
    flags:
      - -trimpath
    tags:
      - netgo
    goos:
      - linux
    goarch:
      - arm64
  - id: b
    main: ./cmd/b
    goos:
      - windows
    goarch:
      - amd64
`))
	if err != nil {
		t.Fatalf("crossJobs: %v", err)
	}
	want := []crossJob{
		{pair: "linux-arm64", main: "./cmd/a", env: []string{"CGO_ENABLED=0"}, flags: []string{"-trimpath"}, tags: []string{"netgo"}},
		{pair: "windows-amd64", main: "./cmd/b"},
	}
	if !reflect.DeepEqual(jobs, want) {
		t.Errorf("crossJobs = %+v\nwant       %+v", jobs, want)
	}
}

func TestCrossJobs_RefusesABuildWithoutMain(t *testing.T) {
	_, err := crossJobs(parseGoreleaser("builds:\n  - id: a\n    goos:\n      - linux\n    goarch:\n      - amd64\n"))
	if err == nil || !strings.Contains(err.Error(), "without main") {
		t.Errorf("crossJobs = %v; want a refusal naming the missing main", err)
	}
}

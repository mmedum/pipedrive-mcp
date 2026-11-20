package main

import (
	"errors"
	"io"
	"strings"
	"testing"
)

var sixTargets = []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64", "windows-arm64"}

func TestCrossBuild_PassesWhenEveryTargetBuilds(t *testing.T) {
	var built []string
	err := crossBuild(io.Discard, sixTargets, func(goos, goarch string) error {
		built = append(built, goos+"-"+goarch)
		return nil
	})
	if err != nil || len(built) != 6 {
		t.Errorf("crossBuild = %v after %d builds; want nil after 6", err, len(built))
	}
}

func TestCrossBuild_NamesTheTargetThatFails(t *testing.T) {
	err := crossBuild(io.Discard, sixTargets, func(goos, goarch string) error {
		if goos == "windows" && goarch == "arm64" {
			return errors.New("undefined: syscall.Thing")
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "windows-arm64: undefined: syscall.Thing") || !strings.Contains(err.Error(), "1 of 6") {
		t.Errorf("crossBuild = %v; want the failing target named with its error", err)
	}
}

func TestCrossBuild_RefusesFewerTargetsThanTheRelease(t *testing.T) {
	err := crossBuild(io.Discard, sixTargets[:5], func(string, string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "fewer than the 6") {
		t.Errorf("crossBuild over 5 targets = %v; want the floor to refuse", err)
	}
}

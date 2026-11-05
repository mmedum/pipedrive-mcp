package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"
)

// floorPackages are the packages the coverage floor holds. A package
// added under internal/ is outside the floor until it is named here.
var floorPackages = []string{"internal/app", "internal/credentials", "internal/pipedrive", "internal/tools"}

// coverageFloor is the statement coverage, in percent, each of
// floorPackages must reach.
const coverageFloor = 80.0

// coverageGate holds each of floorPackages to coverageFloor, reading the
// profile `go test -coverprofile` wrote.
func coverageGate(w io.Writer, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: gates coverage PROFILE")
	}
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	module, err := modulePath(root)
	if err != nil {
		return err
	}
	covered, err := readProfile(args[0])
	if err != nil {
		return err
	}
	below := floorProblems(covered, module, floorPackages, coverageFloor, w)
	if len(below) > 0 {
		return fmt.Errorf("coverage below %.0f%% in: %s", coverageFloor, strings.Join(below, ", "))
	}
	_, err = fmt.Fprintf(w, "coverage ok: %d packages at or above %.0f%%\n", len(floorPackages), coverageFloor)
	return err
}

// floorProblems is each of pkgs whose coverage in s is under floor,
// writing every package's figure to w. A package the profile has no
// blocks for is 0%, not skipped: the run that wrote it did not test it.
func floorProblems(s statements, module string, pkgs []string, floor float64, w io.Writer) []string {
	var below []string
	for _, pkg := range pkgs {
		pct := s.percent(module + "/" + pkg)
		_, _ = fmt.Fprintf(w, "%-22s %6.1f%%\n", pkg, pct)
		if pct < floor {
			below = append(below, fmt.Sprintf("%s (%.1f%%)", pkg, pct))
		}
	}
	return below
}

// statements counts covered and total statements per block, keyed by the
// block's position, so a block reported by several test binaries counts
// once.
type statements struct {
	total map[string]int
	hit   map[string]bool
}

// percent is the statement coverage of the blocks in pkg's own
// directory. A subpackage, or a sibling whose name pkg starts, is another
// package with a figure of its own.
func (s statements) percent(pkg string) float64 {
	var total, cov int
	for block, n := range s.total {
		file, _, _ := strings.Cut(block, ":")
		if path.Dir(file) != pkg {
			continue
		}
		total += n
		if s.hit[block] {
			cov += n
		}
	}
	if total == 0 {
		return 0
	}
	return 100 * float64(cov) / float64(total)
}

// readProfile reads a coverage profile: a mode line, then one
// "path/to/file.go:1.2,3.4 <statements> <count>" line per block.
func readProfile(name string) (statements, error) {
	f, err := os.Open(name) //nolint:gosec // a profile the caller names
	if err != nil {
		return statements{}, fmt.Errorf("coverage profile: %w", err)
	}
	defer func() { _ = f.Close() }()

	s := statements{total: map[string]int{}, hit: map[string]bool{}}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	sc.Scan() // the mode line
	for sc.Scan() {
		block, rest, ok := strings.Cut(sc.Text(), " ")
		if !ok {
			continue
		}
		numStr, countStr, ok := strings.Cut(rest, " ")
		if !ok {
			continue
		}
		num, err1 := strconv.Atoi(numStr)
		count, err2 := strconv.Atoi(countStr)
		if err1 != nil || err2 != nil {
			continue
		}
		if _, seen := s.total[block]; !seen {
			s.total[block] = num
		}
		if count > 0 {
			s.hit[block] = true
		}
	}
	if err := sc.Err(); err != nil {
		return statements{}, err
	}
	if len(s.total) == 0 {
		return statements{}, fmt.Errorf("%s has no coverage blocks; the run that produced it covered nothing", name)
	}
	return s, nil
}

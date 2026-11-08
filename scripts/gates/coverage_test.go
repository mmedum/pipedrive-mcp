package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
)

const coverModule = "example.test/mod"

// writeProfile writes a coverage profile of the given block lines.
func writeProfile(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	put(t, dir, "cov.out", "mode: atomic\n"+strings.Join(lines, "\n")+"\n")
	return filepath.Join(dir, "cov.out")
}

func TestCoverageProfileArithmetic(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		pkg   string
		want  float64
	}{
		{
			name: "half the statements",
			lines: []string{
				coverModule + "/internal/a/x.go:1.1,2.2 3 1",
				coverModule + "/internal/a/x.go:3.1,4.2 3 0",
			},
			pkg:  coverModule + "/internal/a",
			want: 50,
		},
		{
			// Counting a block twice would weigh one package's tests
			// against another's incidental coverage.
			name: "a block reported by two binaries counts once",
			lines: []string{
				coverModule + "/internal/a/x.go:1.1,2.2 4 0",
				coverModule + "/internal/a/x.go:1.1,2.2 4 7",
				coverModule + "/internal/a/x.go:3.1,4.2 4 0",
			},
			pkg:  coverModule + "/internal/a",
			want: 50,
		},
		{
			name:  "a package with no blocks is zero, not a hundred",
			lines: []string{coverModule + "/internal/a/x.go:1.1,2.2 3 1"},
			pkg:   coverModule + "/internal/absent",
			want:  0,
		},
		{
			// internal/servertest starts with internal/server's name.
			name: "a sibling the package's name starts is not the package",
			lines: []string{
				coverModule + "/internal/server/x.go:1.1,2.2 2 1",
				coverModule + "/internal/servertest/y.go:1.1,2.2 2 0",
			},
			pkg:  coverModule + "/internal/server",
			want: 100,
		},
		{
			// internal/server/testutil sits under internal/server's
			// directory.
			name: "a subpackage is not the package",
			lines: []string{
				coverModule + "/internal/server/x.go:1.1,2.2 2 1",
				coverModule + "/internal/server/testutil/y.go:1.1,2.2 2 0",
			},
			pkg:  coverModule + "/internal/server",
			want: 100,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := readProfile(writeProfile(t, c.lines...))
			if err != nil {
				t.Fatal(err)
			}
			if got := s.percent(c.pkg); got != c.want {
				t.Errorf("percent(%q) = %.1f, want %.1f", c.pkg, got, c.want)
			}
		})
	}
}

// A profile with no blocks is a run that tested nothing, and passing it
// would pass every package.
func TestCoverageRefusesAnEmptyProfile(t *testing.T) {
	path := writeProfile(t)
	_, err := readProfile(path)
	if err == nil || !strings.Contains(err.Error(), "has no coverage blocks") {
		t.Errorf("readProfile on an empty profile: err = %v, want one saying it has no coverage blocks", err)
	}
}

func TestFloorProblemsNamesEachPackageBelow(t *testing.T) {
	s, err := readProfile(writeProfile(t,
		coverModule+"/internal/a/x.go:1.1,2.2 4 1",
		coverModule+"/internal/a/x.go:3.1,4.2 1 0",
		coverModule+"/internal/b/x.go:1.1,2.2 3 1",
		coverModule+"/internal/b/x.go:3.1,4.2 1 0",
	))
	if err != nil {
		t.Fatal(err)
	}
	got := coverageFloorProblems(s, coverModule, []string{"internal/a", "internal/b", "internal/c"}, 80, io.Discard)
	want := []string{"internal/b (75.0%)", "internal/c (0.0%)"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("coverageFloorProblems = %q, want %q", got, want)
	}
}

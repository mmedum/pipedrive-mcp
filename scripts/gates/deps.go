package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// staleAfter is how far behind a direct dependency may fall before it
// has to be either upgraded or pinned with a reason. Six months gives
// dependabot time to surface an upgrade without forcing emergency bumps.
const staleAfter = 6 * 30 * 24 * time.Hour

// module is the shape `go list -m -json` returns, narrowed to what this
// reads. Decoding it beats the shell version's jq, which was a second
// tool the check could not run without.
type module struct {
	Path     string
	Version  string
	Indirect bool
	Time     *time.Time
	Update   *struct {
		Version string
		Time    *time.Time
	}
}

// depsGate fails when a direct dependency has an update more than six
// months old, unless go.mod carries `// pinned: <reason>` for it.
//
// It reaches the network, through `go list -m -u`. That is why the
// sibling servers keep their equivalent out of `make check` — a gate
// that fails when a proxy is slow is one people learn to re-run until it
// passes — but this repository has always run it in CI and moving it is
// a separate decision from porting it.
func depsGate(w io.Writer, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: gates deps")
	}
	gomod, err := os.ReadFile("go.mod")
	if errors.Is(err, os.ErrNotExist) {
		_, _ = fmt.Fprintln(w, "no go.mod; nothing to check")
		return nil
	}
	if err != nil {
		return err
	}

	out, err := exec.Command("go", "list", "-m", "-u", "-json", "all").Output()
	if err != nil {
		return fmt.Errorf("go list -m -u: %w", err)
	}

	var stale []string
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var m module
		if err := dec.Decode(&m); err != nil {
			return fmt.Errorf("decode go list output: %w", err)
		}
		if m.Indirect || m.Update == nil {
			continue
		}
		if pinnedWithReason(string(gomod), m.Path) {
			_, _ = fmt.Fprintf(w, "skip  %s — pinned\n", m.Path)
			continue
		}
		if m.Update.Time == nil {
			_, _ = fmt.Fprintf(w, "warn  %s: no release date for %s\n", m.Path, m.Update.Version)
			continue
		}
		age := time.Since(*m.Update.Time)
		if age > staleAfter {
			stale = append(stale, fmt.Sprintf("%s: %s → %s available, released %s (%.0f days ago)\n"+
				"      add `// pinned: <reason>` after the require line in go.mod, or upgrade",
				m.Path, m.Version, m.Update.Version, m.Update.Time.Format(time.DateOnly), age.Hours()/24))
			continue
		}
		_, _ = fmt.Fprintf(w, "ok    %s: %s (%s available, released %s)\n",
			m.Path, m.Version, m.Update.Version, m.Update.Time.Format(time.DateOnly))
	}
	if len(stale) > 0 {
		return fmt.Errorf("%d dependency update(s) older than six months:\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}
	return nil
}

// pinnedWithReason reports whether go.mod holds the module on a require
// line carrying `// pinned:`. The reason is the point: a bare pin is a
// decision nobody recorded.
func pinnedWithReason(gomod, path string) bool {
	re := regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(path) + `\s.*//\s*pinned:`)
	return re.MatchString(gomod)
}

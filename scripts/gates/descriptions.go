package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// descriptionsGate holds the tool descriptions to the house-style rules
// that can be checked mechanically, each stated where it is checked.
//
// It reads the dumped schemas rather than the Go source, because the
// dump is what a client is served. A test walking internal/tools'
// registry would pass on an empty registry while asserting nothing; an
// empty dump fails here.
func descriptionsGate(w io.Writer, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: gates descriptions SCHEMA_DUMP|-")
	}
	// "-" reads the dump from stdin, which is how make invokes this:
	// piping the binary straight in means no file on disk, so there is
	// no fixed path in /tmp for another local user to pre-plant as a
	// symlink, and nothing to clean up afterwards.
	var raw []byte
	var err error
	if args[0] == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(args[0])
	}
	if err != nil {
		return err
	}
	var doc struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema struct {
				Properties map[string]struct {
					Description string `json:"description"`
				} `json:"properties"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parsing %s: %w", args[0], err)
	}
	if len(doc.Tools) == 0 {
		return fmt.Errorf("%s carries no tools; this gate would pass while checking nothing", args[0])
	}

	var problems []string
	for _, t := range doc.Tools {
		// At most one IMPORTANT: per description. Two things claiming
		// to be the most important is none, and it is an easy thing to
		// do while adding a paragraph to prose that already had one.
		if n := strings.Count(t.Description, "IMPORTANT:"); n > 1 {
			problems = append(problems, fmt.Sprintf(
				"%s uses IMPORTANT: %d times; the house style allows one, for the trap that returns a wrong answer rather than an error",
				t.Name, n))
		}
		if strings.TrimSpace(t.Description) == "" {
			problems = append(problems, t.Name+" has no description")
		}
		// Every overwrite input says that a refusal is NOT a retry
		// signal. The refusal names overwrite as the argument that would
		// permit the write, and a model re-sends with it set. The
		// sentence raises the cost of routing around the guard and does
		// not prevent it: the refuse-the-overwrite eval fails with it in
		// place.
		if p, ok := t.InputSchema.Properties["overwrite"]; ok {
			if !strings.Contains(p.Description, "NOT a retry signal") {
				problems = append(problems, t.Name+
					" has an overwrite input that does not say a refusal is NOT a retry signal")
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("tool descriptions break the house style:\n  %s", strings.Join(problems, "\n  "))
	}

	_, _ = fmt.Fprintf(w, "tool descriptions ok: %d tools, each with at most one IMPORTANT: and every overwrite input guarded\n", len(doc.Tools))
	return nil
}

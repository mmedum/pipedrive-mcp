package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mmedum/pipedrive-mcp/internal/credentials"
	"github.com/mmedum/pipedrive-mcp/internal/userconfig"
	"github.com/mmedum/pipedrive-mcp/internal/version"
)

// statusSchemaVersion is the version of the JSON object `status --json`
// prints. A caller may branch on it; it changes only when a field is
// removed or its meaning changes, never when one is added.
const statusSchemaVersion = 1

// statusReport is everything `status` knows, collected once and rendered
// either as the lines a person reads or as the object a script parses.
//
// One collector, two renderers, because the alternative drifts. The text
// answers "can this start" with a label, and a label is free to be
// reworded in any release; the object is the part promised not to move.
//
// Unlike the sibling servers, this one contacts Pipedrive: the probe is
// part of what `status` has always reported, and --no-probe turns it
// off. The exit code is preserved in both renderers, so a caller may use
// either that or the object.
type statusReport struct {
	SchemaVersion int     `json:"schema_version"`
	Binary        string  `json:"binary"`
	Version       string  `json:"version"`
	Domain        *string `json:"domain"`
	DomainSource  *string `json:"domain_source"`
	// Reason carries the first thing that stopped the check, and is null
	// when nothing did. It is the field to read after a non-zero exit.
	Reason      *string           `json:"reason"`
	Credentials statusCredentials `json:"credentials"`
	Probe       statusProbe       `json:"probe"`
}

type statusCredentials struct {
	// Resolved is the field worth branching on: false means every tool
	// will refuse until `login` runs or the environment supplies a
	// token. Always present, so an unauthorised object is
	// distinguishable from a truncated or unparseable one.
	Resolved bool `json:"resolved"`
	// Source is "keyring" or "env", and null when nothing resolved.
	Source *string `json:"source"`
}

// statusProbe is the live check. Ran is false when --no-probe skipped
// it, which is neither a pass nor a failure and must not read as either.
type statusProbe struct {
	Ran    bool    `json:"ran"`
	OK     bool    `json:"ok"`
	Reason *string `json:"reason"`
}

// newStatusReport collects the state and returns the exit code the
// command should use. Every stopping point is a field rather than an
// early return with half an object behind it.
func newStatusReport(noProbe bool) (report statusReport, exitCode int) {
	report = statusReport{
		SchemaVersion: statusSchemaVersion,
		Binary:        "pipedrive-mcp",
		Version:       version.Version,
	}
	ucPath, _ := userconfig.DefaultPath()

	domain, src, err := resolveDomainAtStartup()
	if err != nil {
		report.Reason = orNil(err.Error())
		return report, 1
	}
	report.Domain = orNil(domain)
	report.DomainSource = orNil(src.Label(ucPath))

	token, tokenSrc, err := credentials.Resolve(credentials.Default(), domain)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			report.Reason = orNil(fmt.Sprintf("no token: run `pipedrive-mcp login`, or set %s", credentials.EnvVar))
		} else {
			report.Reason = orNil(fmt.Sprintf("token unavailable: %v", err))
		}
		return report, 1
	}
	report.Credentials.Resolved = true
	report.Credentials.Source = orNil(string(tokenSrc))

	if noProbe {
		return report, 0
	}
	report.Probe.Ran = true
	client := newPipedriveClient(domain, token, 30*time.Second, nil)
	if err := client.ProbeAuth(context.Background()); err != nil {
		report.Probe.Reason = orNil(err.Error())
		return report, 1
	}
	report.Probe.OK = true
	return report, 0
}

// writeText writes the same lines, in the same order, that `status` has
// always printed.
func (r statusReport) writeText(w io.Writer) {
	printf := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format, args...) }

	if r.Domain == nil {
		printf("domain:    (not set)\nhint:      %s\n", deref(r.Reason))
		return
	}
	printf("domain:    %s (%s)\n", *r.Domain, deref(r.DomainSource))

	if !r.Credentials.Resolved {
		reason := deref(r.Reason)
		if len(reason) > 9 && reason[:9] == "no token:" {
			printf("token:     (not set)\nhint:      run `pipedrive-mcp login`, or set %s\n", credentials.EnvVar)
		} else {
			printf("token:     unavailable (%s)\n", reason)
		}
		return
	}
	switch credentials.Source(deref(r.Credentials.Source)) {
	case credentials.SourceKeyring:
		printf("token:     keyring (service=%s, account=%s)\n", credentials.ServiceName, *r.Domain)
	case credentials.SourceEnv:
		printf("token:     env (%s)\n", credentials.EnvVar)
	default:
		printf("token:     %s\n", deref(r.Credentials.Source))
	}

	switch {
	case !r.Probe.Ran:
		printf("probe:     skipped (--no-probe)\n")
	case r.Probe.OK:
		printf("probe:     ok (https://%s.pipedrive.com/api/v2)\n", *r.Domain)
	default:
		printf("probe:     fail (%s)\n", deref(r.Probe.Reason))
	}
}

// writeJSON writes the object, indented and newline-terminated, so the
// whole of stdout is one JSON value.
func (r statusReport) writeJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// Paths are not HTML, and an escaped ampersand is a path a caller
	// cannot compare against its own.
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

// orNil turns an unset string into the JSON null that says so: an empty
// string is a value, and a caller cannot tell a value it does not
// recognise from one that is not there.
func orNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

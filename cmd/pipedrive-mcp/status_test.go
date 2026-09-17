package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// Every stopping point has to be a field rather than an early return
// with half an object behind it: a caller cannot tell a truncated object
// from one it failed to parse.
func TestEveryRefusalIsAWholeObject(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    statusReport
	}{
		{"no domain", statusReport{
			SchemaVersion: statusSchemaVersion,
			Reason:        orNil("no domain configured: set PIPEDRIVE_COMPANY_DOMAIN"),
		}},
		{"domain but no token", statusReport{
			SchemaVersion: statusSchemaVersion,
			Domain:        orNil("acme"),
			DomainSource:  orNil("env"),
			Reason:        orNil("no token: run `pipedrive-mcp login`"),
		}},
		{"token but the probe failed", statusReport{
			SchemaVersion: statusSchemaVersion,
			Domain:        orNil("acme"),
			Credentials:   statusCredentials{Resolved: true, Source: orNil("keyring")},
			Probe:         statusProbe{Ran: true, Reason: orNil("401 unauthorised")},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tc.r.writeJSON(&buf); err != nil {
				t.Fatal(err)
			}
			var back map[string]any
			if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
				t.Fatalf("not JSON: %v\n%s", err, buf.String())
			}
			for _, k := range []string{"schema_version", "domain", "domain_source", "reason", "credentials", "probe"} {
				if _, ok := back[k]; !ok {
					t.Errorf("%s is absent; a short object reads the same as a broken one", k)
				}
			}
			creds, _ := back["credentials"].(map[string]any)
			if _, ok := creds["resolved"]; !ok {
				t.Error("credentials.resolved is absent, which is the field a caller branches on")
			}
			probe, _ := back["probe"].(map[string]any)
			for _, k := range []string{"ran", "ok", "reason"} {
				if _, ok := probe[k]; !ok {
					t.Errorf("probe.%s is absent", k)
				}
			}
		})
	}
}

// A skipped probe is neither a pass nor a failure, and must not read as
// either: ran=false with ok=false is the only honest encoding.
func TestASkippedProbeIsNotAFailure(t *testing.T) {
	r := statusReport{
		Domain:       orNil("acme"),
		DomainSource: orNil("env"),
		Credentials:  statusCredentials{Resolved: true, Source: orNil("env")},
		Probe:        statusProbe{Ran: false},
	}
	var buf bytes.Buffer
	r.writeText(&buf)
	if !strings.Contains(buf.String(), "probe:     skipped") {
		t.Errorf("the text stopped saying skipped:\n%s", buf.String())
	}
	var out bytes.Buffer
	if err := r.writeJSON(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"ran": false`) || !strings.Contains(out.String(), `"ok": false`) {
		t.Errorf("a skipped probe is not encoded as not-run:\n%s", out.String())
	}
	if strings.Contains(out.String(), `"reason"`) && !strings.Contains(out.String(), `"reason": null`) {
		t.Errorf("a skipped probe carries a failure reason:\n%s", out.String())
	}
}

// The whole of stdout has to be one JSON value.
func TestTheObjectIsExactlyOneValue(t *testing.T) {
	var buf bytes.Buffer
	if err := (statusReport{SchemaVersion: statusSchemaVersion}).writeJSON(&buf); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(&buf)
	var first any
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if dec.More() {
		t.Error("stdout carries more than one JSON value")
	}
}

// The text is the half people already read; adding an object under it
// must not move a line.
func TestTextKeepsItsLines(t *testing.T) {
	r := statusReport{
		Domain: orNil("acme"), DomainSource: orNil("env"),
		Credentials: statusCredentials{Resolved: true, Source: orNil("env")},
		Probe:       statusProbe{Ran: true, OK: true},
	}
	var buf bytes.Buffer
	r.writeText(&buf)
	for _, want := range []string{"domain:    acme (env)", "token:     env", "probe:     ok"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("the text lost %q:\n%s", want, buf.String())
		}
	}
}

// TestTextNamesWhatActuallyFailed covers the three ways `status` stops
// after resolving a domain. Calling all of them a token problem sends
// the reader after the wrong thing — and the config case only started
// happening when status began loading the configuration the server
// loads.
func TestTextNamesWhatActuallyFailed(t *testing.T) {
	cases := []struct {
		name, reason, want, notWant string
	}{
		{
			name:    "invalid config",
			reason:  `config: LOG_LEVEL "bogus" is not one of debug|info|warn|error`,
			want:    `config:    LOG_LEVEL "bogus"`,
			notWant: "token:",
		},
		{
			name:   "no token stored",
			reason: "no token: run `pipedrive-mcp login`, or set PIPEDRIVE_API_TOKEN",
			want:   "token:     (not set)",
		},
		{
			name:   "keyring broken",
			reason: "credentials: keyring unavailable and PIPEDRIVE_API_TOKEN is not set",
			want:   "token:     unavailable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := statusReport{
				Domain: orNil("acme"), DomainSource: orNil("env"),
				Reason: orNil(tc.reason),
			}
			var buf bytes.Buffer
			r.writeText(&buf)
			if !strings.Contains(buf.String(), tc.want) {
				t.Errorf("want %q in:\n%s", tc.want, buf.String())
			}
			if tc.notWant != "" && strings.Contains(buf.String(), tc.notWant) {
				t.Errorf("should not blame %q:\n%s", tc.notWant, buf.String())
			}
		})
	}
}

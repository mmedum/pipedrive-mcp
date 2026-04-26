package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoad_Required(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "missing domain",
			env:  map[string]string{},
			want: "domain is required",
		},
		{
			name: "invalid domain",
			env:  map[string]string{"PIPEDRIVE_COMPANY_DOMAIN": "Acme.Corp"},
			want: "is not a valid Pipedrive subdomain",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, tc.env)
			_, err := Load()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestLoad_Defaults(t *testing.T) {
	setEnv(t, map[string]string{
		"PIPEDRIVE_COMPANY_DOMAIN": "acme",
	})
	c, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.LogLevel != LogInfo {
		t.Errorf("LogLevel default = %q, want info", c.LogLevel)
	}
	if c.LogFormat != LogText {
		t.Errorf("LogFormat default = %q, want text", c.LogFormat)
	}
	if c.HTTPTimeout != 30*time.Second {
		t.Errorf("HTTPTimeout default = %s, want 30s", c.HTTPTimeout)
	}
	if c.EnableDestructive {
		t.Error("EnableDestructive default should be false")
	}
	if c.DryRun {
		t.Error("DryRun default should be false")
	}
}

func TestLoad_AllOverrides(t *testing.T) {
	setEnv(t, map[string]string{
		"PIPEDRIVE_COMPANY_DOMAIN":     "Acme",
		"LOG_LEVEL":                    "debug",
		"LOG_FORMAT":                   "json",
		"PIPEDRIVE_ENABLE_DESTRUCTIVE": "true",
		"PIPEDRIVE_DRY_RUN":            "1",
		"PIPEDRIVE_HTTP_TIMEOUT":       "5s",
	})
	c, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.CompanyDomain != "acme" {
		t.Errorf("CompanyDomain = %q, want acme (lowercased)", c.CompanyDomain)
	}
	if c.LogLevel != LogDebug {
		t.Errorf("LogLevel = %q, want debug", c.LogLevel)
	}
	if c.LogFormat != LogJSON {
		t.Errorf("LogFormat = %q, want json", c.LogFormat)
	}
	if !c.EnableDestructive {
		t.Error("EnableDestructive should be true")
	}
	if !c.DryRun {
		t.Error("DryRun should be true")
	}
	if c.HTTPTimeout != 5*time.Second {
		t.Errorf("HTTPTimeout = %s, want 5s", c.HTTPTimeout)
	}
}

func TestLoad_Invalid(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "bad log level",
			env: map[string]string{
				"PIPEDRIVE_COMPANY_DOMAIN": "acme",
				"LOG_LEVEL":                "loud",
			},
			want: "LOG_LEVEL",
		},
		{
			name: "bad log format",
			env: map[string]string{
				"PIPEDRIVE_COMPANY_DOMAIN": "acme",
				"LOG_FORMAT":               "yaml",
			},
			want: "LOG_FORMAT",
		},
		{
			name: "bad bool",
			env: map[string]string{
				"PIPEDRIVE_COMPANY_DOMAIN":     "acme",
				"PIPEDRIVE_ENABLE_DESTRUCTIVE": "maybe",
			},
			want: "PIPEDRIVE_ENABLE_DESTRUCTIVE",
		},
		{
			name: "bad duration",
			env: map[string]string{
				"PIPEDRIVE_COMPANY_DOMAIN": "acme",
				"PIPEDRIVE_HTTP_TIMEOUT":   "soonish",
			},
			want: "PIPEDRIVE_HTTP_TIMEOUT",
		},
		{
			name: "zero duration",
			env: map[string]string{
				"PIPEDRIVE_COMPANY_DOMAIN": "acme",
				"PIPEDRIVE_HTTP_TIMEOUT":   "0s",
			},
			want: "must be positive",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, tc.env)
			_, err := Load()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestLogLevel_Slog(t *testing.T) {
	cases := []struct {
		in   LogLevel
		want string
	}{
		{LogDebug, "DEBUG"},
		{LogInfo, "INFO"},
		{LogWarn, "WARN"},
		{LogError, "ERROR"},
		{LogLevel("unknown"), "INFO"},
	}
	for _, tc := range cases {
		if got := tc.in.Slog().String(); got != tc.want {
			t.Errorf("LogLevel(%q).Slog() = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// setEnv replaces the process environment for the duration of the test.
// All previously-set PIPEDRIVE_* / LOG_* vars are cleared so tests cannot
// leak into one another.
func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, k := range []string{
		"PIPEDRIVE_COMPANY_DOMAIN",
		"LOG_LEVEL",
		"LOG_FORMAT",
		"PIPEDRIVE_ENABLE_DESTRUCTIVE",
		"PIPEDRIVE_DRY_RUN",
		"PIPEDRIVE_HTTP_TIMEOUT",
	} {
		t.Setenv(k, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

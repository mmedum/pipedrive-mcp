package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmedum/pipedrive-mcp/internal/config"
)

func TestNewLogger_TextDefault(t *testing.T) {
	logger := newLogger(config.Config{
		LogLevel:  config.LogInfo,
		LogFormat: config.LogText,
	})
	if logger == nil {
		t.Fatal("newLogger returned nil")
	}
}

func TestNewLogger_JSON(t *testing.T) {
	logger := newLogger(config.Config{
		LogLevel:  config.LogDebug,
		LogFormat: config.LogJSON,
	})
	if logger == nil {
		t.Fatal("newLogger returned nil")
	}
}

func TestNewLogger_AllLevels(t *testing.T) {
	for _, level := range []config.LogLevel{config.LogDebug, config.LogInfo, config.LogWarn, config.LogError} {
		logger := newLogger(config.Config{LogLevel: level, LogFormat: config.LogText})
		if logger == nil {
			t.Errorf("newLogger(%q) returned nil", level)
		}
	}
}

func TestPromptToken_NonTTY(t *testing.T) {
	// os.Pipe is not a TTY; promptToken must fall back to line-read mode.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()

	go func() {
		defer w.Close()
		_, _ = w.WriteString("piped-token\n")
	}()

	var prompt bytes.Buffer
	got, err := promptToken(r, &prompt, "token: ")
	if err != nil {
		t.Fatalf("promptToken: %v", err)
	}
	if got != "piped-token" {
		t.Errorf("token = %q, want piped-token", got)
	}
	if !bytes.Contains(prompt.Bytes(), []byte("token: ")) {
		t.Errorf("prompt not written to writer: %q", prompt.String())
	}
}

func TestPromptToken_StripsCR(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	go func() {
		defer w.Close()
		_, _ = w.WriteString("crlf-token\r\n")
	}()
	got, err := promptToken(r, io.Discard, "")
	if err != nil {
		t.Fatalf("promptToken: %v", err)
	}
	if got != "crlf-token" {
		t.Errorf("token = %q, want crlf-token", got)
	}
}

func TestResolveDomainArg(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		envDomain string
		wantCode  int
		wantValue string
	}{
		{"flag wins", []string{"--domain", "Acme"}, "ignored", 0, "acme"},
		{"env fallback", nil, "Acme", 0, "acme"},
		{"flag empty falls back to env", []string{"--domain", ""}, "Acme", 0, "acme"},
		{"missing both", nil, "", 2, ""},
		{"invalid domain", []string{"--domain", "Acme.Corp"}, "", 2, ""},
		{"flag whitespace falls back to env", []string{"--domain", "   "}, "acme", 0, "acme"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PIPEDRIVE_COMPANY_DOMAIN", tc.envDomain)
			got, code := resolveDomainArg("test", tc.args, nil)
			if code != tc.wantCode {
				t.Errorf("code = %d, want %d", code, tc.wantCode)
			}
			if got != tc.wantValue {
				t.Errorf("domain = %q, want %q", got, tc.wantValue)
			}
		})
	}
}

func TestResolveDomain(t *testing.T) {
	t.Run("env wins over userconfig", func(t *testing.T) {
		ucPath := writeUserConfig(t, `{"default_domain":"fromfile"}`)
		got, src, err := resolveDomain("fromenv", ucPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "fromenv" || src != DomainFromEnv {
			t.Errorf("got (%q, %q), want (fromenv, env)", got, src)
		}
	})

	t.Run("falls through to userconfig when env is empty", func(t *testing.T) {
		ucPath := writeUserConfig(t, `{"default_domain":"fromfile"}`)
		got, src, err := resolveDomain("", ucPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "fromfile" || src != DomainFromUserConfig {
			t.Errorf("got (%q, %q), want (fromfile, userconfig)", got, src)
		}
	})

	t.Run("env whitespace is treated as empty", func(t *testing.T) {
		ucPath := writeUserConfig(t, `{"default_domain":"fromfile"}`)
		got, src, err := resolveDomain("   ", ucPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "fromfile" || src != DomainFromUserConfig {
			t.Errorf("whitespace env should fall through; got (%q, %q)", got, src)
		}
	})

	t.Run("missing both surfaces actionable error", func(t *testing.T) {
		// userconfig file does not exist; env is empty
		ucPath := nonexistentUserConfigPath(t)
		_, _, err := resolveDomain("", ucPath)
		if err == nil {
			t.Fatal("expected error when neither env nor userconfig has a domain")
		}
		if !strings.Contains(err.Error(), "pipedrive-mcp login") {
			t.Errorf("error should hint at `pipedrive-mcp login`; got: %v", err)
		}
	})

	t.Run("invalid env domain is rejected before userconfig is consulted", func(t *testing.T) {
		ucPath := writeUserConfig(t, `{"default_domain":"fromfile"}`)
		_, _, err := resolveDomain("Acme.Corp", ucPath)
		if err == nil {
			t.Fatal("expected validation error for malformed env domain")
		}
	})

	t.Run("invalid userconfig domain is rejected", func(t *testing.T) {
		ucPath := writeUserConfig(t, `{"default_domain":"Bad.Subdomain"}`)
		_, _, err := resolveDomain("", ucPath)
		if err == nil {
			t.Fatal("expected validation error for malformed userconfig domain")
		}
	})

	t.Run("empty userconfig path with empty env errors cleanly", func(t *testing.T) {
		_, _, err := resolveDomain("", "")
		if err == nil {
			t.Fatal("expected error when both inputs are empty")
		}
	})
}

func writeUserConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("seed userconfig: %v", err)
	}
	return p
}

func nonexistentUserConfigPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "does-not-exist.json")
}

func TestNewPipedriveClient(t *testing.T) {
	c := newPipedriveClient("acme", "tok", 0, nil)
	if c == nil {
		t.Fatal("newPipedriveClient returned nil")
	}
}

func TestIsCleanShutdown(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, true},
		{"context canceled", context.Canceled, true},
		{"io.EOF", io.EOF, true},
		{"io.ErrUnexpectedEOF", io.ErrUnexpectedEOF, true},
		{"wrapped EOF", fmt.Errorf("transport: %w", io.EOF), true},
		{"server is closing string", errors.New("server is closing: stdin"), true},
		{"EOF substring", errors.New("read failed: EOF received"), true},
		{"genuine error", errors.New("connection refused"), false},
		{"validation error", errors.New("bad input"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCleanShutdown(tc.err); got != tc.want {
				t.Errorf("isCleanShutdown(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

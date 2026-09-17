package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDomain(t *testing.T) {
	t.Run("env wins over userconfig", func(t *testing.T) {
		ucPath := writeUserConfig(t, `{"default_domain":"fromfile"}`)
		got, src, err := ResolveDomain("fromenv", ucPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "fromenv" || src != DomainFromEnv {
			t.Errorf("got (%q, %q), want (fromenv, env)", got, src)
		}
	})

	t.Run("falls through to userconfig when env is empty", func(t *testing.T) {
		ucPath := writeUserConfig(t, `{"default_domain":"fromfile"}`)
		got, src, err := ResolveDomain("", ucPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "fromfile" || src != DomainFromUserConfig {
			t.Errorf("got (%q, %q), want (fromfile, userconfig)", got, src)
		}
	})

	t.Run("env whitespace is treated as empty", func(t *testing.T) {
		ucPath := writeUserConfig(t, `{"default_domain":"fromfile"}`)
		got, src, err := ResolveDomain("   ", ucPath)
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
		_, _, err := ResolveDomain("", ucPath)
		if err == nil {
			t.Fatal("expected error when neither env nor userconfig has a domain")
		}
		if !strings.Contains(err.Error(), "pipedrive-mcp login") {
			t.Errorf("error should hint at `pipedrive-mcp login`; got: %v", err)
		}
	})

	t.Run("invalid env domain is rejected before userconfig is consulted", func(t *testing.T) {
		ucPath := writeUserConfig(t, `{"default_domain":"fromfile"}`)
		_, _, err := ResolveDomain("Acme.Corp", ucPath)
		if err == nil {
			t.Fatal("expected validation error for malformed env domain")
		}
	})

	t.Run("invalid userconfig domain is rejected", func(t *testing.T) {
		ucPath := writeUserConfig(t, `{"default_domain":"Bad.Subdomain"}`)
		_, _, err := ResolveDomain("", ucPath)
		if err == nil {
			t.Fatal("expected validation error for malformed userconfig domain")
		}
	})

	t.Run("empty userconfig path with empty env errors cleanly", func(t *testing.T) {
		_, _, err := ResolveDomain("", "")
		if err == nil {
			t.Fatal("expected error when both inputs are empty")
		}
	})
}

func TestDomainSource_Label(t *testing.T) {
	if got := DomainFromEnv.Label("/tmp/config.json"); got != "env" {
		t.Errorf("env label = %q; the path belongs to the userconfig source only", got)
	}
	if got := DomainFromUserConfig.Label("/tmp/config.json"); got != "userconfig (/tmp/config.json)" {
		t.Errorf("userconfig label = %q; want the path named", got)
	}
	if got := DomainFromUserConfig.Label(""); got != "userconfig" {
		t.Errorf("label with no path = %q; want a bare source", got)
	}
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

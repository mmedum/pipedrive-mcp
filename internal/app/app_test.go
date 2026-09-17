package app

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/mmedum/pipedrive-mcp/internal/config"
	"github.com/mmedum/pipedrive-mcp/internal/credentials"
)

// fakeKeyring stands in for the OS keyring. Only Get is exercised;
// the assembly never writes. The zero value reports the same
// keyring.ErrNotFound the real backend does, so the not-found case
// covers the branch that runs in production rather than the
// empty-string one beside it.
type fakeKeyring struct{ token string }

func (f fakeKeyring) Get(_, _ string) (string, error) {
	if f.token == "" {
		return "", keyring.ErrNotFound
	}
	return f.token, nil
}
func (fakeKeyring) Set(_, _, _ string) error { return nil }
func (fakeKeyring) Delete(_, _ string) error { return nil }

// workspace points the assembly at a domain and clears the token env
// var, so the keyring fake is what answers unless a test says
// otherwise.
func workspace(t *testing.T, domain string) {
	t.Helper()
	t.Setenv(config.DomainEnv, domain)
	t.Setenv(credentials.EnvVar, "")
}

func mustResolve(t *testing.T, backend credentials.Backend) Settings {
	t.Helper()
	s, err := resolveWith(backend)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return s
}

func TestResolveWith(t *testing.T) {
	t.Run("assembles domain, config and token", func(t *testing.T) {
		workspace(t, "acme")
		t.Setenv("PIPEDRIVE_HTTP_TIMEOUT", "7s")

		s := mustResolve(t, fakeKeyring{token: "from-keyring"})
		if s.Domain() != "acme" {
			t.Errorf("domain = %q; want acme", s.Domain())
		}
		if s.DomainSource != DomainFromEnv {
			t.Errorf("domain source = %q; want env", s.DomainSource)
		}
		if s.TokenSource != credentials.SourceKeyring {
			t.Errorf("token came from %q; want the keyring", s.TokenSource)
		}
		if s.Config.HTTPTimeout != 7*time.Second {
			t.Errorf("timeout = %s; PIPEDRIVE_HTTP_TIMEOUT did not reach Settings", s.Config.HTTPTimeout)
		}
	})

	t.Run("env token wins over the keyring", func(t *testing.T) {
		workspace(t, "acme")
		t.Setenv(credentials.EnvVar, "from-env")

		s := mustResolve(t, fakeKeyring{token: "from-keyring"})
		if s.TokenSource != credentials.SourceEnv {
			t.Errorf("token came from %q; env should win", s.TokenSource)
		}
	})

	t.Run("a missing token stays recognisable as ErrNotFound", func(t *testing.T) {
		workspace(t, "acme")

		// Callers branch on this: main exits with an actionable
		// message, the integration suite skips.
		if _, err := resolveWith(fakeKeyring{}); !errors.Is(err, credentials.ErrNotFound) {
			t.Fatalf("err = %v; want credentials.ErrNotFound", err)
		}
	})

	t.Run("a malformed domain stops before the keyring", func(t *testing.T) {
		workspace(t, "Acme.Corp")
		if _, err := resolveWith(fakeKeyring{token: "tok"}); err == nil {
			t.Fatal("expected a validation error for a malformed domain")
		}
	})

	t.Run("a bad config value stops before the keyring", func(t *testing.T) {
		workspace(t, "acme")
		t.Setenv("LOG_LEVEL", "loud")
		if _, err := resolveWith(fakeKeyring{token: "tok"}); err == nil {
			t.Fatal("expected a validation error for a malformed LOG_LEVEL")
		}
	})
}

// TestSettings_LogValue_RedactsTheToken pins the reason the token is
// unexported and LogValue exists: Settings is a struct main logs
// neighbouring fields of, and one slog.Any away from a keyring token in
// stderr.
func TestSettings_LogValue_RedactsTheToken(t *testing.T) {
	workspace(t, "acme")
	s := mustResolve(t, fakeKeyring{token: "super-secret-token"})

	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("resolved", slog.Any("settings", s))

	if strings.Contains(buf.String(), "super-secret-token") {
		t.Fatal("the token reached the log line")
	}
	for _, want := range []string{"workspace=acme", "domain_source=env", "token_source=keyring"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("log line is missing %q: %s", want, buf.String())
		}
	}
}

// TestRuntime_NewServer_CarriesTheDryRunFloor pins the reason this
// package exists. PIPEDRIVE_DRY_RUN reaches the write tools only
// through the config a Runtime carries, so a caller that assembled the
// arguments by hand — as the integration suite once did — could drop
// the floor without anything failing.
func TestRuntime_NewServer_CarriesTheDryRunFloor(t *testing.T) {
	workspace(t, "acme")
	t.Setenv("PIPEDRIVE_DRY_RUN", "true")

	rt := mustResolve(t, fakeKeyring{token: "tok"}).Connect(nil)
	if !rt.Settings.Config.DryRun {
		t.Fatal("the floor did not reach Settings; the rest of this test proves nothing")
	}
	if rt.Client == nil {
		t.Fatal("Connect returned no client")
	}
	if srv := rt.NewServer(t.Context(), "test"); srv == nil {
		t.Fatal("NewServer returned nil")
	}
}

func TestNewProbeClient(t *testing.T) {
	if c := NewProbeClient("acme", "tok", config.DefaultHTTPTimeout); c == nil {
		t.Fatal("NewProbeClient returned nil")
	}
}

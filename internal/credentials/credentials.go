// Package credentials resolves the Pipedrive API token from secure
// storage. The OS keyring (libsecret on Linux, Keychain on macOS,
// Credential Manager on Windows) is the persistent home; the env var
// fallback exists for CI, automation, and any environment where the
// keyring is unavailable (headless SSH, WSL without DBus, containers).
//
// Resolution order (first non-empty wins):
//  1. PIPEDRIVE_API_TOKEN env var. Mirrors gh's GH_TOKEN, aws's
//     AWS_ACCESS_KEY_ID, etc. — env explicitly overrides stored
//     credentials so an operator can use a different token without
//     running `pipedrive-mcp login` again.
//  2. OS keyring entry under serviceName, account = company domain.
//
// If env is unset and the keyring lookup fails for any reason other
// than "entry not found" (no DBus, no Secret Service, etc.), the
// underlying error is returned so the user sees what to fix.
package credentials

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
)

// ServiceName is the keyring service identifier. Same value across all
// platforms; the keyring backend handles platform-specific storage.
const ServiceName = "pipedrive-mcp"

// EnvVar is the env-var fallback. Documented for CI use.
const EnvVar = "PIPEDRIVE_API_TOKEN"

// Source identifies which path a resolved token came from. Useful for
// log fields and tests.
type Source string

// Source values returned by Resolve.
const (
	SourceKeyring Source = "keyring"
	SourceEnv     Source = "env"
)

// ErrNotFound is returned when no token is configured anywhere.
var ErrNotFound = errors.New("credentials: no token found in keyring or PIPEDRIVE_API_TOKEN")

// Backend is the minimal keyring contract this package depends on.
// Tests substitute an in-memory implementation; production wires it to
// github.com/zalando/go-keyring.
type Backend interface {
	Get(service, account string) (string, error)
	Set(service, account, token string) error
	Delete(service, account string) error
}

type realBackend struct{}

func (realBackend) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}
func (realBackend) Set(service, account, token string) error {
	return keyring.Set(service, account, token)
}
func (realBackend) Delete(service, account string) error {
	return keyring.Delete(service, account)
}

// IsNotFound reports whether err signals a missing keyring entry (as
// opposed to the keyring being unavailable entirely).
func IsNotFound(err error) bool {
	return errors.Is(err, keyring.ErrNotFound)
}

// Default returns the production keyring-backed implementation.
func Default() Backend { return realBackend{} }

// Resolve looks up the token for the given company domain. Env wins
// over keyring (matches gh/aws precedence); a missing keyring entry is
// treated as "fall through", but a broken keyring is surfaced so the
// user knows to set the env var or fix DBus.
func Resolve(b Backend, domain string) (string, Source, error) {
	if envToken := strings.TrimSpace(os.Getenv(EnvVar)); envToken != "" {
		return envToken, SourceEnv, nil
	}

	keyToken, err := b.Get(ServiceName, domain)
	if IsNotFound(err) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", fmt.Errorf("credentials: keyring unavailable and %s is not set: %w", EnvVar, err)
	}
	if keyToken == "" {
		return "", "", ErrNotFound
	}
	return keyToken, SourceKeyring, nil
}

// Store writes the token into the keyring under the given domain.
// Surrounding whitespace is trimmed before write so the keyring is the
// single source of truth for the canonical form.
func Store(b Backend, domain, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("credentials: refusing to store empty token")
	}
	if strings.TrimSpace(domain) == "" {
		return errors.New("credentials: domain is required")
	}
	if err := b.Set(ServiceName, domain, token); err != nil {
		return fmt.Errorf("credentials: keyring write: %w", err)
	}
	return nil
}

// Delete removes the keyring entry for the given domain. No-op if the
// entry did not exist.
func Delete(b Backend, domain string) error {
	if err := b.Delete(ServiceName, domain); err != nil && !IsNotFound(err) {
		return fmt.Errorf("credentials: keyring delete: %w", err)
	}
	return nil
}

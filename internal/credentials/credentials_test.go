package credentials

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"
)

// fakeBackend is an in-memory Backend for tests. Production code uses
// realBackend; the package's exported API operates on the Backend
// interface so we can swap.
type fakeBackend struct {
	mu    sync.Mutex
	store map[string]string
}

func newFake() *fakeBackend { return &fakeBackend{store: map[string]string{}} }

func (f *fakeBackend) key(service, account string) string {
	return service + "\x00" + account
}

func (f *fakeBackend) Get(service, account string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.store[f.key(service, account)]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (f *fakeBackend) Set(service, account, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.store[f.key(service, account)] = token
	return nil
}

func (f *fakeBackend) Delete(service, account string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.store[f.key(service, account)]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.store, f.key(service, account))
	return nil
}

func TestResolve_EnvWinsOverKeyring(t *testing.T) {
	t.Setenv(EnvVar, "from-env")
	b := newFake()
	if err := b.Set(ServiceName, "acme", "from-keyring"); err != nil {
		t.Fatalf("set: %v", err)
	}

	tok, src, err := Resolve(b, "acme")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if tok != "from-env" {
		t.Errorf("token = %q, want from-env (env should take precedence)", tok)
	}
	if src != SourceEnv {
		t.Errorf("source = %q, want env", src)
	}
}

func TestResolve_KeyringFallback(t *testing.T) {
	t.Setenv(EnvVar, "")
	b := newFake()
	if err := b.Set(ServiceName, "acme", "from-keyring"); err != nil {
		t.Fatalf("set: %v", err)
	}

	tok, src, err := Resolve(b, "acme")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if tok != "from-keyring" {
		t.Errorf("token = %q, want from-keyring", tok)
	}
	if src != SourceKeyring {
		t.Errorf("source = %q, want keyring", src)
	}
}

func TestResolve_NotFound(t *testing.T) {
	t.Setenv(EnvVar, "")
	b := newFake()
	_, _, err := Resolve(b, "acme")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestResolve_TrimsEnvWhitespace(t *testing.T) {
	t.Setenv(EnvVar, "  whitespace-token  ")
	b := newFake()
	tok, _, err := Resolve(b, "acme")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if tok != "whitespace-token" {
		t.Errorf("token = %q, want trimmed", tok)
	}
}

// brokenBackend simulates a keyring that's installed but unavailable
// (no DBus, headless session, container, etc.). Get returns a
// non-NotFound error.
type brokenBackend struct{}

func (brokenBackend) Get(_, _ string) (string, error) {
	return "", errors.New("dbus: connection refused")
}
func (brokenBackend) Set(_, _, _ string) error { return errors.New("dbus: connection refused") }
func (brokenBackend) Delete(_, _ string) error { return errors.New("dbus: connection refused") }

func TestResolve_EnvWorksWhenKeyringBroken(t *testing.T) {
	// The headless-fallback bug we fixed: previously, any non-NotFound
	// keyring error caused startup to fail even with the env var set.
	t.Setenv(EnvVar, "from-env")
	tok, src, err := Resolve(brokenBackend{}, "acme")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if tok != "from-env" || src != SourceEnv {
		t.Errorf("got %q/%q, want from-env/env", tok, src)
	}
}

func TestResolve_BrokenKeyringNoEnv(t *testing.T) {
	// When the keyring is broken AND env is unset, we surface the
	// underlying error so the user sees what to fix.
	t.Setenv(EnvVar, "")
	_, _, err := Resolve(brokenBackend{}, "acme")
	if err == nil {
		t.Fatal("expected error from broken keyring with no env fallback")
	}
	if errors.Is(err, ErrNotFound) {
		t.Errorf("err = ErrNotFound, want a real keyring error: %v", err)
	}
	if !strings.Contains(err.Error(), "keyring unavailable") {
		t.Errorf("err = %q, want it to mention keyring unavailable", err.Error())
	}
}

func TestStore_RejectsEmpty(t *testing.T) {
	b := newFake()
	if err := Store(b, "acme", ""); err == nil {
		t.Error("expected error storing empty token")
	}
	if err := Store(b, "acme", "   "); err == nil {
		t.Error("expected error storing whitespace token")
	}
	if err := Store(b, "", "tok"); err == nil {
		t.Error("expected error storing without domain")
	}
}

func TestStoreThenResolve(t *testing.T) {
	t.Setenv(EnvVar, "")
	b := newFake()
	if err := Store(b, "acme", "tok-123"); err != nil {
		t.Fatalf("Store: %v", err)
	}
	tok, src, err := Resolve(b, "acme")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if tok != "tok-123" || src != SourceKeyring {
		t.Errorf("got token=%q src=%q", tok, src)
	}
}

func TestDelete(t *testing.T) {
	t.Setenv(EnvVar, "")
	b := newFake()
	_ = Store(b, "acme", "tok")
	if err := Delete(b, "acme"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := Resolve(b, "acme"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after Delete, Resolve = %v, want ErrNotFound", err)
	}
}

func TestDelete_MissingIsNoError(t *testing.T) {
	b := newFake()
	if err := Delete(b, "never-stored"); err != nil {
		t.Errorf("Delete on missing key returned %v, want nil", err)
	}
}

func TestIsNotFound(t *testing.T) {
	if !IsNotFound(keyring.ErrNotFound) {
		t.Error("IsNotFound should match keyring.ErrNotFound")
	}
	if IsNotFound(errors.New("other")) {
		t.Error("IsNotFound should not match other errors")
	}
}

// TestRealBackend exercises the production keyring adapter against
// go-keyring's in-process mock. Confirms Get/Set/Delete pass through
// correctly and that ErrNotFound is propagated from a real keyring
// backend (not just our fake).
func TestRealBackend(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(func() { _ = keyring.Delete(ServiceName, "real-test") })

	b := Default()
	if _, err := b.Get(ServiceName, "real-test"); !IsNotFound(err) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := b.Set(ServiceName, "real-test", "tok-real"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := b.Get(ServiceName, "real-test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "tok-real" {
		t.Errorf("Get returned %q, want tok-real", got)
	}
	if err := b.Delete(ServiceName, "real-test"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := b.Get(ServiceName, "real-test"); !IsNotFound(err) {
		t.Errorf("after Delete, Get = %v, want ErrNotFound", err)
	}
}

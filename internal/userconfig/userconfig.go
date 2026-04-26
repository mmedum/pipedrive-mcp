// Package userconfig persists per-user, non-secret state in a small JSON
// file at os.UserConfigDir()/pipedrive-mcp/config.json. The keyring stores
// the API token; this file stores the domain pointer so subsequent server
// runs don't need PIPEDRIVE_COMPANY_DOMAIN re-supplied on every invocation.
//
// File contents are intentionally minimal. Anything secret stays in the
// keyring; anything operational stays in env vars. This file is for the
// "which workspace did I last log in to" pointer, the same role
// `~/.config/gh/hosts.yml` plays for `gh`.
package userconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// File is the JSON shape persisted on disk. omitempty so a cleared file
// is `{}`, not `{"default_domain":""}` — readable when inspected by hand.
type File struct {
	DefaultDomain string `json:"default_domain,omitempty"`
}

// DefaultPath returns os.UserConfigDir()/pipedrive-mcp/config.json.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("userconfig: resolve config dir: %w", err)
	}
	return filepath.Join(dir, "pipedrive-mcp", "config.json"), nil
}

// Load reads the file at path. A missing file is the steady state on a
// fresh install — Load returns a zero File and a nil error in that case
// so callers don't need to special-case first-run.
func Load(path string) (File, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return File{}, nil
	}
	if err != nil {
		return File{}, fmt.Errorf("userconfig: read %s: %w", path, err)
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return File{}, fmt.Errorf("userconfig: parse %s: %w", path, err)
	}
	return f, nil
}

// Save writes f to path atomically (write-then-rename in the same dir)
// with 0600 perms on the file and 0700 on the parent dir. A kill -9
// mid-write would otherwise leave a half-truncated file.
func Save(path string, f File) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("userconfig: mkdir %s: %w", dir, err)
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("userconfig: marshal: %w", err)
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(dir, ".config.json.*")
	if err != nil {
		return fmt.Errorf("userconfig: temp file: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("userconfig: chmod %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("userconfig: write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("userconfig: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("userconfig: rename %s -> %s: %w", tmpName, path, err)
	}
	committed = true
	return nil
}

// SetDefaultDomain reads the file at path, updates DefaultDomain, and
// writes it back. Convenience for the `login` subcommand.
func SetDefaultDomain(path, domain string) error {
	f, err := Load(path)
	if err != nil {
		return err
	}
	f.DefaultDomain = domain
	return Save(path, f)
}

// ClearDefaultDomainIfMatches clears DefaultDomain only when it equals
// domain. Used by `logout` so we don't accidentally clear a different
// workspace's pointer when the user logs out of one of several stored
// tokens.
func ClearDefaultDomainIfMatches(path, domain string) error {
	f, err := Load(path)
	if err != nil {
		return err
	}
	if f.DefaultDomain != domain {
		return nil
	}
	f.DefaultDomain = ""
	return Save(path, f)
}

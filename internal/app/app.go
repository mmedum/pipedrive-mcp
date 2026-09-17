// Package app is the startup assembly every entry point shares:
// resolve the workspace domain, load the configuration for it, resolve
// the API token, build the Pipedrive client, wire the MCP server.
// domain.go holds the first of those; this file holds the rest.
//
// It exists because that sequence used to live unexported inside
// package main, so nothing else could perform it and every other caller
// re-derived it. The integration suite re-derived it and dropped the
// PIPEDRIVE_DRY_RUN floor on the way — which docs/security.md promises
// an operator holds everywhere — and that is the kind of divergence a
// shared constructor prevents structurally rather than by comment.
//
// Nothing here logs, exits or touches the network. The caller decides
// what a missing token means: main exits, a test skips.
package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/config"
	"github.com/mmedum/pipedrive-mcp/internal/credentials"
	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server"
)

// Settings is the resolved startup state: which workspace, which token,
// and the configuration around both.
//
// The token is unexported and LogValue redacts it, so neither
// slog.Any("settings", s) nor %+v can put a keyring token in a log
// line. internal/pipedrive sets the same precedent: add a redacting
// accessor, not a field.
type Settings struct {
	Config       config.Config
	DomainSource DomainSource
	TokenSource  credentials.Source

	token string
}

// Domain is the validated workspace subdomain.
func (s Settings) Domain() string { return s.Config.CompanyDomain }

// LogValue renders Settings for slog without the token.
func (s Settings) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("workspace", s.Domain()),
		slog.String("domain_source", string(s.DomainSource)),
		slog.String("token_source", string(s.TokenSource)),
		slog.Bool("dry_run", s.Config.DryRun),
	)
}

// Resolve performs the whole assembly: domain, then configuration for
// that domain, then token. Errors are returned unwrapped enough that
// errors.Is(err, credentials.ErrNotFound) still answers, so a caller
// can tell "no token stored" from "the keyring is broken".
func Resolve() (Settings, error) {
	return resolveWith(credentials.Default())
}

// resolveWith is Resolve against a supplied credential backend, so the
// package's own tests can drive the assembly without the real OS
// keyring. It is the same seam credentials.Resolve takes a Backend for.
func resolveWith(backend credentials.Backend) (Settings, error) {
	domain, domainSource, err := ResolveDomainAtStartup()
	if err != nil {
		return Settings{}, err
	}
	cfg, err := config.LoadFor(domain)
	if err != nil {
		return Settings{}, err
	}
	token, tokenSource, err := credentials.Resolve(backend, cfg.CompanyDomain)
	if err != nil {
		return Settings{}, err
	}
	return Settings{
		Config:       cfg,
		DomainSource: domainSource,
		TokenSource:  tokenSource,
		token:        token,
	}, nil
}

// Runtime is a resolved workspace with its client attached. The pair
// travels together so the two cannot describe different workspaces:
// the client carries the base URL and token, and the settings carry the
// domain every tool output is labelled with.
type Runtime struct {
	Settings Settings
	Client   *pipedrive.Client
}

// Connect builds the Pipedrive client these settings describe,
// honouring PIPEDRIVE_HTTP_TIMEOUT, and pairs it with the settings it
// came from. It opens no connection — the first request is the
// caller's.
func (s Settings) Connect(logger *slog.Logger) Runtime {
	return Runtime{
		Settings: s,
		Client:   newClient(s.Config.CompanyDomain, s.token, s.Config.HTTPTimeout, logger),
	}
}

// NewServer wires the MCP server for this runtime. It is the only place
// a serving process builds one, so the workspace the tools label their
// output with is always the workspace the client talks to, and the
// PIPEDRIVE_DRY_RUN floor always reaches the write tools.
func (r Runtime) NewServer(ctx context.Context, version string) *mcp.Server {
	return server.New(ctx, version, r.Client, r.Settings.Config)
}

// NewProbeClient builds a client for the one-shot paths that hold a
// domain and a token but no Runtime: login validating a token before
// storing it, and `status` probing one. Pass config.DefaultHTTPTimeout
// when there is no Config to take a timeout from.
func NewProbeClient(domain, token string, timeout time.Duration) *pipedrive.Client {
	return newClient(domain, token, timeout, nil)
}

// newClient is the single construction site for pipedrive.Client.
func newClient(domain, token string, timeout time.Duration, logger *slog.Logger) *pipedrive.Client {
	return pipedrive.New(pipedrive.Options{
		BaseURL: pipedrive.BaseURL(domain),
		Token:   token,
		Timeout: timeout,
		Logger:  logger,
	})
}

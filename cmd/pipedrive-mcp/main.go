// Command pipedrive-mcp is the production entrypoint. It speaks the
// Model Context Protocol over stdio and exposes Pipedrive CRM
// operations to LLM-driven clients.
//
// Subcommands:
//
//	pipedrive-mcp login    store an API token in the OS keyring
//	pipedrive-mcp logout   remove the OS keyring entry
//	pipedrive-mcp status   show the active workspace + token source
//	pipedrive-mcp          run the MCP server (default)
//	pipedrive-mcp --version | --dump-schemas
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/term"

	"github.com/mmedum/pipedrive-mcp/internal/config"
	"github.com/mmedum/pipedrive-mcp/internal/credentials"
	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server"
	"github.com/mmedum/pipedrive-mcp/internal/tools"
	"github.com/mmedum/pipedrive-mcp/internal/userconfig"
	"github.com/mmedum/pipedrive-mcp/internal/version"
)

const serverName = "pipedrive-mcp"

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "login":
			os.Exit(cmdLogin(os.Args[2:]))
		case "logout":
			os.Exit(cmdLogout(os.Args[2:]))
		case "status":
			os.Exit(cmdStatus(os.Args[2:]))
		}
	}
	runServer()
}

func runServer() {
	var (
		showVersion bool
		dumpSchemas bool
		skipProbe   bool
	)
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.BoolVar(&dumpSchemas, "dump-schemas", false, "print registered tool schemas as JSON and exit")
	flag.BoolVar(&skipProbe, "skip-probe", false, "skip the Pipedrive auth probe (CI smoke tests only; do not use in production)")
	flag.Parse()

	if showVersion {
		// --version exits before stdio serving begins; stdout is safe
		// here (the "stdout reserved for MCP frames" rule applies to
		// the server path, not one-shot CLI commands).
		fmt.Println(version.Info())
		return
	}

	if dumpSchemas {
		_ = server.New(context.Background(), serverName, version.Version, nil, "")
		if err := tools.DumpJSON(os.Stdout, version.Version); err != nil {
			fail("dump schemas: %v", err)
		}
		return
	}

	domain, domainSource, err := resolveDomainAtStartup()
	if err != nil {
		fail("%v", err)
	}

	cfg, err := config.LoadFor(domain)
	if err != nil {
		fail("%v", err)
	}

	logger := newLogger(cfg)
	slog.SetDefault(logger)

	token, source, err := credentials.Resolve(credentials.Default(), cfg.CompanyDomain)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			fail("no API token found. Run `pipedrive-mcp login` to store one in the OS keyring, or set %s in the environment.", credentials.EnvVar)
		}
		fail("%v", err)
	}
	logger.Info("credentials resolved",
		slog.String("source", string(source)),
		slog.String("workspace", cfg.CompanyDomain),
		slog.String("domain_source", string(domainSource)),
	)

	client := newPipedriveClient(cfg.CompanyDomain, token, cfg.HTTPTimeout, logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if !skipProbe {
		if err := client.ProbeAuth(ctx); err != nil {
			failProbe(err)
		}
		logger.InfoContext(ctx, "auth probe ok",
			slog.String("workspace", cfg.CompanyDomain),
		)
	}

	srv := server.New(ctx, serverName, version.Version, client, cfg.CompanyDomain)
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && !isCleanShutdown(err) {
		fail("server: %v", err)
	}
}

// cmdLogin resolves the workspace domain (flag → env → interactive
// prompt), reads a token from the controlling terminal (no echo),
// validates the token via the auth probe, and stores both in their
// respective stores: token in the OS keyring, domain pointer in the
// user-config file.
//
// The interactive domain prompt mirrors `aws configure` and `gh auth
// login`: scriptable inputs (flag/env) win, but the bare-hands path
// is fully interactive instead of failing with "provide --domain".
func cmdLogin(args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(os.Stderr, "usage: pipedrive-mcp login [--domain <subdomain>]\n\n"+
			"Stores a Pipedrive API token in the OS keyring and records the\n"+
			"workspace domain in user config so subsequent server runs\n"+
			"resolve them automatically. The domain may be supplied via\n"+
			"--domain, PIPEDRIVE_COMPANY_DOMAIN, or interactively. The token\n"+
			"is read without echo and validated before storage.\n")
	}
	domainFlag := fs.String("domain", "", "Pipedrive workspace subdomain (overrides PIPEDRIVE_COMPANY_DOMAIN)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	raw := strings.TrimSpace(*domainFlag)
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("PIPEDRIVE_COMPANY_DOMAIN"))
	}
	if raw == "" {
		prompted, err := promptDomain(os.Stdin, os.Stderr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "login: %v\n", err)
			return 1
		}
		raw = prompted
	}
	domain, err := config.ValidateDomain(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "login: %v\n", err)
		return 2
	}

	token, err := promptToken(os.Stdin, os.Stderr, fmt.Sprintf("Pipedrive API token for %q: ", domain))
	if err != nil {
		fmt.Fprintf(os.Stderr, "login: %v\n", err)
		return 1
	}
	if strings.TrimSpace(token) == "" {
		fmt.Fprintln(os.Stderr, "login: empty token; nothing stored")
		return 1
	}

	if err := validateToken(domain, token); err != nil {
		fmt.Fprintf(os.Stderr, "login: token validation failed: %v\n", err)
		return 1
	}

	if err := credentials.Store(credentials.Default(), domain, token); err != nil {
		fmt.Fprintf(os.Stderr, "login: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "login: token stored in OS keyring (service=%s, account=%s)\n",
		credentials.ServiceName, domain)

	// Best-effort: write domain to userconfig so subsequent runs don't
	// need PIPEDRIVE_COMPANY_DOMAIN re-supplied. The keyring write
	// above is the source of truth; userconfig is a convenience pointer.
	// Failures here become warnings, not errors — a working keyring
	// with a missing config dir is still a usable install via env.
	if ucPath, err := userconfig.DefaultPath(); err == nil {
		if err := userconfig.SetDefaultDomain(ucPath, domain); err != nil {
			fmt.Fprintf(os.Stderr, "login: warning: %v (set PIPEDRIVE_COMPANY_DOMAIN to use this token)\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "login: default domain recorded in %s\n", ucPath)
		}
	}
	return 0
}

// cmdLogout removes the keyring entry for the workspace the user
// asked to log out of. Resolution order: --domain flag,
// PIPEDRIVE_COMPANY_DOMAIN env, recorded default in userconfig. When
// the user has only one stored workspace (the common case), plain
// `pipedrive-mcp logout` Just Works because the userconfig pointer
// from `login` is still in place. To remove a non-default workspace
// out of several stored, pass `--domain`.
func cmdLogout(args []string) int {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(os.Stderr, "usage: pipedrive-mcp logout [--domain <subdomain>]\n\n"+
			"Removes the OS keyring entry for the workspace and clears the\n"+
			"userconfig pointer if it referenced that workspace. With no\n"+
			"flag/env, defaults to the workspace recorded by the most\n"+
			"recent successful `pipedrive-mcp login`.\n")
	}
	domainFlag := fs.String("domain", "", "Pipedrive workspace subdomain (overrides PIPEDRIVE_COMPANY_DOMAIN and userconfig)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	var domain string
	if raw := strings.TrimSpace(*domainFlag); raw != "" {
		d, err := config.ValidateDomain(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "logout: %v\n", err)
			return 2
		}
		domain = d
	} else {
		ucPath, _ := userconfig.DefaultPath()
		d, _, err := resolveDomain(os.Getenv("PIPEDRIVE_COMPANY_DOMAIN"), ucPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "logout: %v\n", err)
			return 2
		}
		domain = d
	}

	if err := credentials.Delete(credentials.Default(), domain); err != nil {
		fmt.Fprintf(os.Stderr, "logout: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "logout: keyring entry removed (service=%s, account=%s)\n",
		credentials.ServiceName, domain)

	// Clear the userconfig pointer iff it referenced the domain we
	// just logged out of. Other workspaces' pointers (if multiple
	// tokens are stored) are left untouched.
	if ucPath, err := userconfig.DefaultPath(); err == nil {
		if err := userconfig.ClearDefaultDomainIfMatches(ucPath, domain); err != nil {
			fmt.Fprintf(os.Stderr, "logout: warning: %v\n", err)
		}
	}
	return 0
}

// cmdStatus prints the active domain, the token source, and the result
// of an auth probe. Output goes to stdout: this is a one-shot CLI
// command, not the stdio MCP server, so the stdout-reserved-for-frames
// rule does not apply.
func cmdStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(os.Stderr, "usage: pipedrive-mcp status [--no-probe]\n\n"+
			"Reports the active workspace domain, where it came from\n"+
			"(env or user config), whether a token is available, and\n"+
			"whether an auth probe against Pipedrive succeeds.\n")
	}
	noProbe := fs.Bool("no-probe", false, "skip the network call to Pipedrive (offline mode)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	return runStatus(os.Stdout, *noProbe)
}

func runStatus(out io.Writer, noProbe bool) int {
	printf := func(format string, args ...any) {
		_, _ = fmt.Fprintf(out, format, args...)
	}

	ucPath, _ := userconfig.DefaultPath()
	domain, src, err := resolveDomainAtStartup()
	if err != nil {
		printf("domain:    (not set)\nhint:      %v\n", err)
		return 1
	}
	printf("domain:    %s (%s)\n", domain, src.Label(ucPath))

	token, tokenSrc, err := credentials.Resolve(credentials.Default(), domain)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			printf("token:     (not set)\nhint:      run `pipedrive-mcp login`, or set %s\n", credentials.EnvVar)
			return 1
		}
		printf("token:     unavailable (%v)\n", err)
		return 1
	}
	switch tokenSrc {
	case credentials.SourceKeyring:
		printf("token:     keyring (service=%s, account=%s)\n", credentials.ServiceName, domain)
	case credentials.SourceEnv:
		printf("token:     env (%s)\n", credentials.EnvVar)
	default:
		printf("token:     %s\n", tokenSrc)
	}

	if noProbe {
		printf("probe:     skipped (--no-probe)\n")
		return 0
	}
	client := newPipedriveClient(domain, token, 30*time.Second, nil)
	if err := client.ProbeAuth(context.Background()); err != nil {
		printf("probe:     fail (%v)\n", err)
		return 1
	}
	printf("probe:     ok (https://%s.pipedrive.com/api/v2)\n", domain)
	return 0
}

// DomainSource names where a resolved domain came from. Surfaced in
// startup logs and `pipedrive-mcp status` so the operator can see
// which input mechanism is in effect.
type DomainSource string

const (
	DomainFromEnv        DomainSource = "env"
	DomainFromUserConfig DomainSource = "userconfig"
)

// Label returns a human-readable description of the source, including
// the userconfig file path when the source is the userconfig pointer.
func (s DomainSource) Label(ucPath string) string {
	if s == DomainFromUserConfig && ucPath != "" {
		return fmt.Sprintf("userconfig (%s)", ucPath)
	}
	return string(s)
}

// errNoDomain is the user-facing message when neither env nor userconfig
// supplies a workspace domain. Hoisted to a const so resolveDomain's
// two terminal branches stay verbatim-equal.
const errNoDomain = "no domain configured: set PIPEDRIVE_COMPANY_DOMAIN, or run `pipedrive-mcp login` to record one"

// resolveDomain returns the active domain for the current process, in
// this order: PIPEDRIVE_COMPANY_DOMAIN env > userconfig.DefaultDomain.
// An empty/missing value in both sources returns a clear error pointing
// the operator at `pipedrive-mcp login`. Inputs are passed explicitly
// so tests can drive the resolution without env or filesystem
// manipulation.
func resolveDomain(envValue, ucPath string) (string, DomainSource, error) {
	if raw := strings.TrimSpace(envValue); raw != "" {
		domain, err := config.ValidateDomain(raw)
		if err != nil {
			return "", "", fmt.Errorf("config: PIPEDRIVE_COMPANY_DOMAIN: %w", err)
		}
		return domain, DomainFromEnv, nil
	}
	if ucPath == "" {
		return "", "", errors.New(errNoDomain)
	}
	uc, err := userconfig.Load(ucPath)
	if err != nil {
		return "", "", err
	}
	if raw := strings.TrimSpace(uc.DefaultDomain); raw != "" {
		domain, err := config.ValidateDomain(raw)
		if err != nil {
			return "", "", fmt.Errorf("userconfig %s: default_domain: %w", ucPath, err)
		}
		return domain, DomainFromUserConfig, nil
	}
	return "", "", errors.New(errNoDomain)
}

func resolveDomainAtStartup() (string, DomainSource, error) {
	ucPath, _ := userconfig.DefaultPath() // empty path falls through cleanly
	return resolveDomain(os.Getenv("PIPEDRIVE_COMPANY_DOMAIN"), ucPath)
}

// promptDomain reads a Pipedrive workspace subdomain from in (with
// echo — the domain is not a secret), trims whitespace, and returns
// it. The first non-empty line wins. Empty input returns an error so
// the caller can decline to proceed; validation against the regex
// happens at the call site via config.ValidateDomain.
func promptDomain(in *os.File, prompt io.Writer) (string, error) {
	_, _ = fmt.Fprint(prompt, "Pipedrive workspace subdomain (e.g. acme for acme.pipedrive.com): ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(line) == "" {
		return "", errors.New("empty subdomain")
	}
	return line, nil
}

// promptToken reads a token without echoing characters when stdin is a
// terminal. Falls back to a buffered line read otherwise (so the
// command remains scriptable, e.g. `echo $TOKEN | pipedrive-mcp login`).
func promptToken(in *os.File, prompt io.Writer, message string) (string, error) {
	_, _ = fmt.Fprint(prompt, message)
	if term.IsTerminal(int(in.Fd())) {
		b, err := term.ReadPassword(int(in.Fd()))
		_, _ = fmt.Fprintln(prompt) // newline after no-echo input
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// validateToken runs the auth probe against the given domain with the
// supplied token before we commit it to storage. A bad token surfaces
// as a 401 here, not on next launch.
func validateToken(domain, token string) error {
	client := newPipedriveClient(domain, token, 30*time.Second, nil)
	return client.ProbeAuth(context.Background())
}

// newPipedriveClient is the single construction site for pipedrive.Client.
// Both runServer (long-lived) and validateToken (one-shot during login)
// route through here.
func newPipedriveClient(domain, token string, timeout time.Duration, logger *slog.Logger) *pipedrive.Client {
	return pipedrive.New(pipedrive.Options{
		BaseURL: pipedrive.BaseURL(domain),
		Token:   token,
		Timeout: timeout,
		Logger:  logger,
	})
}

// failProbe renders an actionable startup-probe message and exits.
func failProbe(err error) {
	var apiErr *pipedrive.APIError
	switch {
	case errors.As(err, &apiErr) && errors.Is(err, pipedrive.ErrUnauthorized):
		fail("auth probe failed: 401 — token rejected. Run `pipedrive-mcp login` again.")
	case errors.As(err, &apiErr):
		fail("auth probe failed: HTTP %d — %s", apiErr.Status, apiErr.Message)
	default:
		fail("auth probe failed: %v", err)
	}
}

func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel.Slog()}
	var handler slog.Handler
	if cfg.LogFormat == config.LogJSON {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.New(handler)
}

// isCleanShutdown returns true for the error classes the stdio server
// normally returns when its parent closes stdin or the process is
// signaled. Treating these as fatal would make every well-behaved MCP
// client exit look like a crash.
//
// The SDK currently surfaces stdin closure as a non-typed error whose
// string contains "server is closing" / "EOF". Match defensively in case
// the wrapping changes — sentinel checks first, string fallback last.
func isCleanShutdown(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "server is closing") || strings.Contains(msg, "EOF")
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

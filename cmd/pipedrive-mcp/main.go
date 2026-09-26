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

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/term"

	"github.com/mmedum/pipedrive-mcp/internal/app"
	"github.com/mmedum/pipedrive-mcp/internal/config"
	"github.com/mmedum/pipedrive-mcp/internal/credentials"
	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server"
	"github.com/mmedum/pipedrive-mcp/internal/tools"
	"github.com/mmedum/pipedrive-mcp/internal/userconfig"
	"github.com/mmedum/pipedrive-mcp/internal/version"
)

// everything below takes them as io.Writer, so nothing else reaches stdout.
//
//nolint:forbidigo // the one place the process's streams are named;
func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run is main with its arguments and its streams passed in, so the
// dispatch can be exercised by a test rather than only by a person at a
// terminal. main itself calls os.Exit, which no test survives.
//
// The command paths below still exit from inside, because they parse
// package-level flags and `fail` ends the process. That is a deeper
// change than this one and needs a real API token to exercise; what
// matters here is that the guard is reachable from a test.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "login":
			return cmdLogin(args[1:])
		case "logout":
			return cmdLogout(args[1:])
		case "status":
			return cmdStatus(args[1:], stdout)
		}

		// Anything the switch did not recognize, and that is not a flag,
		// was meant to be a subcommand. Falling through starts the server
		// instead, which looks like a hang: it blocks on stdin and says
		// nothing. The caller is then handed exit 0 whether it meant to
		// serve or mistyped `status`, so nothing downstream can tell the
		// two apart. A leading dash is all that separates them.
		if !strings.HasPrefix(args[0], "-") {
			_, _ = fmt.Fprintf(stderr, "pipedrive-mcp: unknown command %q\n\nCommands: login, logout, status\nRun with no command to serve MCP over stdio.\n", args[0])
			return 1
		}
	}
	runServer(stdout)
	return 0
}

func runServer(stdout io.Writer) {
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
		_, _ = fmt.Fprintln(stdout, version.Info())
		return
	}

	if dumpSchemas {
		// Every tool registers unconditionally now, so the dump always
		// carries the full surface the schema-diff CI gate compares.
		_ = server.NewForSchemaDump()
		if err := tools.DumpJSON(stdout, version.Version); err != nil {
			fail("dump schemas: %v", err)
		}
		return
	}

	settings, err := app.Resolve()
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			fail("no API token found. Run `pipedrive-mcp login` to store one in the OS keyring, or set %s in the environment.", credentials.EnvVar)
		}
		fail("%v", err)
	}

	logger := newLogger(settings.Config)
	slog.SetDefault(logger)
	logger.Info("credentials resolved", slog.Any("settings", settings))

	rt := settings.Connect(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if !skipProbe {
		if err := rt.Client.ProbeAuth(ctx); err != nil {
			failProbe(err)
		}
		logger.InfoContext(ctx, "auth probe ok",
			slog.String("workspace", settings.Domain()),
		)
	}

	srv := rt.NewServer(ctx, version.Version)
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
		raw = strings.TrimSpace(os.Getenv(config.DomainEnv))
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
		d, _, err := app.ResolveDomain(os.Getenv(config.DomainEnv), ucPath)
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
func cmdStatus(args []string, stdout io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprintf(os.Stderr, "usage: pipedrive-mcp status [--no-probe] [--json]\n\n"+
			"Reports the active workspace domain, where it came from\n"+
			"(env or user config), whether a token is available, and\n"+
			"whether an auth probe against Pipedrive succeeds.\n")
	}
	noProbe := fs.Bool("no-probe", false, "skip the network call to Pipedrive (offline mode)")
	asJSON := fs.Bool("json", false, "print the same state as one JSON object")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	return runStatus(stdout, *noProbe, *asJSON)
}

func runStatus(out io.Writer, noProbe, asJSON bool) int {
	r, code := newStatusReport(noProbe)
	if asJSON {
		if err := r.writeJSON(out); err != nil {
			fail("status: %v", err)
		}
		return code
	}
	r.writeText(out)
	return code
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
	client := app.NewProbeClient(domain, token, config.DefaultHTTPTimeout)
	return client.ProbeAuth(context.Background())
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

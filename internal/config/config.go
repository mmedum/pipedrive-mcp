// Package config loads and validates the server's environment-variable
// configuration. There is no config file and no flag-driven config — the
// only flags on the binary are operational (--version, --dump-schemas,
// --skip-probe). Everything else is env.
//
// Validation runs once at process start. A missing or malformed required
// variable produces a clear error and the caller (cmd/pipedrive-mcp/main.go)
// exits non-zero before announcing the MCP server, so an LLM client never
// sees a half-initialized server.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// EnvPrefix starts the name of every variable the server reads but the
// two logging ones, LOG_LEVEL and LOG_FORMAT.
const EnvPrefix = "PIPEDRIVE_"

// LogLevel is a typed enum constrained at config-load time.
type LogLevel string

// Allowed LogLevel values. Anything else fails Load.
const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

// Slog returns the slog.Level corresponding to this log level.
func (l LogLevel) Slog() slog.Level {
	switch l {
	case LogDebug:
		return slog.LevelDebug
	case LogWarn:
		return slog.LevelWarn
	case LogError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// LogFormat is a typed enum constrained at config-load time.
type LogFormat string

// Allowed LogFormat values. Anything else fails Load.
const (
	LogText LogFormat = "text"
	LogJSON LogFormat = "json"
)

// Config is the validated runtime configuration. The API token is
// resolved separately by the credentials package — it is not part of
// Config so the env-var lookup logic doesn't fight with the keyring.
type Config struct {
	CompanyDomain string
	LogLevel      LogLevel
	LogFormat     LogFormat
	DryRun        bool
	HTTPTimeout   time.Duration
}

// DomainEnv names the workspace. Resolution of which value wins — env
// over the userconfig pointer — belongs to internal/app; this is only
// the variable's name, kept beside ValidateDomain so the two spellings
// cannot drift.
const DomainEnv = EnvPrefix + "COMPANY_DOMAIN"

// EnvAPIToken is the token's variable, which internal/credentials reads.
// Named here so the bundle gate can read the registry without importing
// a keyring.
const EnvAPIToken = EnvPrefix + "API_TOKEN"

// The variables Load reads. Each name is spelled here once, and every
// other mention of one, a message included, uses its constant.
const (
	EnvLogLevel    = "LOG_LEVEL"
	EnvLogFormat   = "LOG_FORMAT"
	EnvDryRun      = EnvPrefix + "DRY_RUN"
	EnvHTTPTimeout = EnvPrefix + "HTTP_TIMEOUT"
)

// envOnly is every variable read outside Load: the domain by
// internal/app, before Load runs, and the token by internal/credentials.
var envOnly = []Var{{Name: DomainEnv}, {Name: EnvAPIToken, Secret: true}}

// Var is one variable the server reads.
type Var struct {
	// Name is the environment variable.
	Name string
	// Switch marks a setting that is on or off.
	Switch bool
	// Secret marks a credential, which a form that asks for it must
	// store as a sensitive value.
	Secret bool
}

// Lookup is the value env gives the variable name, without the spaces
// around it. Spaces alone are "", unset as an empty variable is, so a
// form field cleared to a space means the default rather than a value
// the server refuses. Load reads every setting through it, and so does
// each reader of a variable outside Load.
func Lookup(env func(string) string, name string) string {
	return strings.TrimSpace(env(name))
}

// settings is the raw value of every variable Load reads, each read
// through Lookup and recorded for Vars as it is read, so the registry
// cannot fall behind a setting added here.
type settings struct {
	logLevel, logFormat, dryRun, httpTimeout string
	vars                                     []Var
}

func readSettings(env func(string) string) settings {
	var s settings
	read := func(v Var) string {
		s.vars = append(s.vars, v)
		return Lookup(env, v.Name)
	}
	s.logLevel = read(Var{Name: EnvLogLevel})
	s.logFormat = read(Var{Name: EnvLogFormat})
	s.dryRun = read(Var{Name: EnvDryRun, Switch: true})
	s.httpTimeout = read(Var{Name: EnvHTTPTimeout})
	return s
}

// Vars is every variable the server reads, sorted by name: Load's, and
// the ones read outside it. Load's are recorded rather than listed, so
// they cannot fall behind. The bundle gate derives what it holds from
// these.
func Vars() []Var {
	vars := readSettings(func(string) string { return "" }).vars
	vars = append(vars, envOnly...)
	slices.SortFunc(vars, func(a, b Var) int { return strings.Compare(a.Name, b.Name) })
	return vars
}

// EnvVars is the names of Vars, sorted. A name listed twice is a
// variable read under two rules, which a test refuses, so it is not
// compacted.
func EnvVars() []string {
	vars := Vars()
	names := make([]string, 0, len(vars))
	for _, v := range vars {
		names = append(names, v.Name)
	}
	return names
}

// DefaultHTTPTimeout is the per-request timeout when
// PIPEDRIVE_HTTP_TIMEOUT says nothing. Named so the one-shot probe
// paths, which load no Config, can use the same number.
const DefaultHTTPTimeout = 30 * time.Second

var (
	domainPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$|^[a-z0-9]$`)
	logLevels     = map[LogLevel]bool{LogDebug: true, LogInfo: true, LogWarn: true, LogError: true}
	logFormats    = map[LogFormat]bool{LogText: true, LogJSON: true}
)

// ValidateDomain lowers A to Z in a Pipedrive workspace subdomain and
// validates it. Shared between Load and the login/logout subcommands so
// all entry points apply the same regex. The spaces around a domain are
// its reader's to drop: Lookup's for the variable, and the caller's for
// a flag, the prompt and the userconfig pointer.
func ValidateDomain(s string) (string, error) {
	d := asciiLower(s)
	if d == "" {
		return "", errors.New("domain is required")
	}
	if !domainPattern.MatchString(d) {
		return "", fmt.Errorf("%q is not a valid Pipedrive subdomain", d)
	}
	return d, nil
}

// asciiLower lowers A to Z alone, so a character Unicode folds into a
// to z, such as the Kelvin sign, is refused rather than read as another
// workspace's letter.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

// LoadFor reads configuration for a pre-resolved company domain. The
// domain is validated through ValidateDomain; an empty or malformed
// value returns an error. All other config comes from the environment.
//
// There is deliberately no loader that reads the domain itself.
// Resolving which domain applies — env over the userconfig pointer —
// belongs to internal/app, and a second resolver here would be one a
// caller could reach for to bypass the pointer without noticing.
func LoadFor(rawDomain string) (Config, error) {
	return Load(rawDomain, os.Getenv)
}

// Load is LoadFor against the environment env gives, so the bundle gate
// can start the server's own loader from the environment a bundle's
// defaults build. It takes the domain resolved, for LoadFor's reason.
func Load(rawDomain string, env func(string) string) (Config, error) {
	c := Config{
		LogLevel:    LogInfo,
		LogFormat:   LogText,
		HTTPTimeout: DefaultHTTPTimeout,
	}

	domain, err := ValidateDomain(rawDomain)
	if err != nil {
		return Config{}, fmt.Errorf("config: company domain: %w", err)
	}
	c.CompanyDomain = domain

	s := readSettings(env)
	if v := strings.ToLower(s.logLevel); v != "" {
		level := LogLevel(v)
		if !logLevels[level] {
			return Config{}, fmt.Errorf("config: %s %q is not one of debug|info|warn|error", EnvLogLevel, v)
		}
		c.LogLevel = level
	}

	if v := strings.ToLower(s.logFormat); v != "" {
		format := LogFormat(v)
		if !logFormats[format] {
			return Config{}, fmt.Errorf("config: %s %q is not one of text|json", EnvLogFormat, v)
		}
		c.LogFormat = format
	}

	if v := s.dryRun; v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: %s %q: use true or false", EnvDryRun, v)
		}
		c.DryRun = b
	}

	if v := s.httpTimeout; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: %s %q: %w", EnvHTTPTimeout, v, err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("config: %s must be positive, got %s", EnvHTTPTimeout, d)
		}
		c.HTTPTimeout = d
	}

	return c, nil
}

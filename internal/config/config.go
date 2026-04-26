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
	"strconv"
	"strings"
	"time"
)

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
	CompanyDomain     string
	LogLevel          LogLevel
	LogFormat         LogFormat
	EnableDestructive bool
	DryRun            bool
	HTTPTimeout       time.Duration
}

var (
	domainPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$|^[a-z0-9]$`)
	logLevels     = map[LogLevel]bool{LogDebug: true, LogInfo: true, LogWarn: true, LogError: true}
	logFormats    = map[LogFormat]bool{LogText: true, LogJSON: true}
)

// ValidateDomain normalizes (trim, lowercase) and validates a Pipedrive
// workspace subdomain. Shared between Load and the login/logout
// subcommands so all entry points apply the same regex.
func ValidateDomain(s string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(s))
	if d == "" {
		return "", errors.New("domain is required")
	}
	if !domainPattern.MatchString(d) {
		return "", fmt.Errorf("%q is not a valid Pipedrive subdomain", d)
	}
	return d, nil
}

// Load reads configuration from the process environment and validates it.
func Load() (Config, error) {
	c := Config{
		LogLevel:    LogInfo,
		LogFormat:   LogText,
		HTTPTimeout: 30 * time.Second,
	}

	domain, err := ValidateDomain(os.Getenv("PIPEDRIVE_COMPANY_DOMAIN"))
	if err != nil {
		return Config{}, fmt.Errorf("config: PIPEDRIVE_COMPANY_DOMAIN: %w", err)
	}
	c.CompanyDomain = domain

	if v := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))); v != "" {
		level := LogLevel(v)
		if !logLevels[level] {
			return Config{}, fmt.Errorf("config: LOG_LEVEL %q is not one of debug|info|warn|error", v)
		}
		c.LogLevel = level
	}

	if v := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_FORMAT"))); v != "" {
		format := LogFormat(v)
		if !logFormats[format] {
			return Config{}, fmt.Errorf("config: LOG_FORMAT %q is not one of text|json", v)
		}
		c.LogFormat = format
	}

	if v := os.Getenv("PIPEDRIVE_ENABLE_DESTRUCTIVE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: PIPEDRIVE_ENABLE_DESTRUCTIVE %q: use true or false", v)
		}
		c.EnableDestructive = b
	}

	if v := os.Getenv("PIPEDRIVE_DRY_RUN"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: PIPEDRIVE_DRY_RUN %q: use true or false", v)
		}
		c.DryRun = b
	}

	if v := strings.TrimSpace(os.Getenv("PIPEDRIVE_HTTP_TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: PIPEDRIVE_HTTP_TIMEOUT %q: %w", v, err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("config: PIPEDRIVE_HTTP_TIMEOUT must be positive, got %s", d)
		}
		c.HTTPTimeout = d
	}

	return c, nil
}

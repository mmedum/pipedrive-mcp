package app

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mmedum/pipedrive-mcp/internal/config"
	"github.com/mmedum/pipedrive-mcp/internal/userconfig"
)

// DomainSource names where a resolved domain came from. Surfaced in
// startup logs and `pipedrive-mcp status` so the operator can see
// which input mechanism is in effect.
type DomainSource string

// The two sources a domain can come from, in precedence order.
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
// supplies a workspace domain. Hoisted to a const so ResolveDomain's
// two terminal branches stay verbatim-equal.
const errNoDomain = "no domain configured: set " + config.DomainEnv + ", or run `pipedrive-mcp login` to record one"

// ResolveDomain returns the active domain for the current process, in
// this order: PIPEDRIVE_COMPANY_DOMAIN env > userconfig.DefaultDomain.
// An empty/missing value in both sources returns a clear error pointing
// the operator at `pipedrive-mcp login`. Inputs are passed explicitly
// so tests can drive the resolution without env or filesystem
// manipulation.
func ResolveDomain(envValue, ucPath string) (string, DomainSource, error) {
	if raw := strings.TrimSpace(envValue); raw != "" {
		domain, err := config.ValidateDomain(raw)
		if err != nil {
			return "", "", fmt.Errorf("config: %s: %w", config.DomainEnv, err)
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

// ResolveDomainAtStartup is ResolveDomain against the real environment
// and the real userconfig path.
func ResolveDomainAtStartup() (string, DomainSource, error) {
	ucPath, _ := userconfig.DefaultPath() // empty path falls through cleanly
	return ResolveDomain(os.Getenv(config.DomainEnv), ucPath)
}

# pipedrive-mcp

[![CI](https://github.com/mmedum/pipedrive-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/mmedum/pipedrive-mcp/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

A production-grade [MCP](https://modelcontextprotocol.io) server that
exposes [Pipedrive CRM](https://pipedrive.com) over stdio to LLM-driven
clients such as Claude Desktop and Claude Code. Single static Go binary,
distroless Docker image, signed releases, semver-disciplined surface.

> **Status: pre-implementation, Phase 0.** The server boots and the auth
> probe runs, but no tools are registered yet. The first tool ships in
> `v0.1.0`. See `CHANGELOG.md` and the
> [phase plan](#phase-plan) for what lands when.

## Highlights

- **Pipedrive API v2 first.** The legacy v1 sunsets 2026-07-31; this
  server commits to v2.
- **No destructive tools by default.** Deletes are gated behind
  `PIPEDRIVE_ENABLE_DESTRUCTIVE`. A server-wide `PIPEDRIVE_DRY_RUN` makes
  every write a rehearsal that returns "would have done X" without firing
  the request.
- **Stdio only for v1.** No remote transport, no OAuth — simple and
  auditable.
- **Tiny static binary** (`CGO_ENABLED=0`, `-trimpath`, `-s -w`) and a
  ~10 MB distroless Docker image.
- **Reproducible builds** (verified in CI). Releases are signed via
  [cosign](https://github.com/sigstore/cosign) keyless OIDC and ship with
  a CycloneDX SBOM.

## Install

### From a release binary

Download the latest release binary for your platform from the
[Releases page](https://github.com/mmedum/pipedrive-mcp/releases). Verify
the signature (recommended):

```sh
cosign verify-blob \
  --certificate pipedrive-mcp-vX.Y.Z-linux-amd64.cert \
  --signature   pipedrive-mcp-vX.Y.Z-linux-amd64.sig \
  --certificate-identity-regexp 'https://github.com/mmedum/pipedrive-mcp/.*' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  pipedrive-mcp-vX.Y.Z-linux-amd64

chmod +x pipedrive-mcp-vX.Y.Z-linux-amd64
sudo mv pipedrive-mcp-vX.Y.Z-linux-amd64 /usr/local/bin/pipedrive-mcp
```

### From the Docker image

```sh
docker pull ghcr.io/mmedum/pipedrive-mcp:latest
```

### From source

Requires Go 1.26.2.

```sh
go install github.com/mmedum/pipedrive-mcp/cmd/pipedrive-mcp@latest
```

## Configure

The server stores your API token in the **OS keyring** (libsecret on
Linux, Keychain on macOS, Credential Manager on Windows) rather than
asking you to paste it into a JSON config. Full reference and
validation rules: [`docs/configuration.md`](docs/configuration.md).

```sh
pipedrive-mcp login
# Pipedrive workspace subdomain (e.g. acme for acme.pipedrive.com): your-subdomain
# Pipedrive API token for "your-subdomain": ········
# login: token stored in OS keyring (service=pipedrive-mcp, account=your-subdomain)
# login: default domain recorded in ~/.config/pipedrive-mcp/config.json
```

Pass `--domain your-subdomain` (or set `PIPEDRIVE_COMPANY_DOMAIN`) to
skip the domain prompt — useful in scripts.

After that, your MCP client config needs nothing about the workspace
domain or the token — both are resolved from the keyring + a small
non-secret pointer file (`os.UserConfigDir()/pipedrive-mcp/config.json`).
Run `pipedrive-mcp status` to verify, or `pipedrive-mcp logout` to
clear them.

To override the recorded domain in one shell (e.g. point at a
different workspace temporarily), set `PIPEDRIVE_COMPANY_DOMAIN` —
env wins over the userconfig pointer. For CI, automation, or one-off
use of a different token, set `PIPEDRIVE_API_TOKEN`. Env takes
precedence over the keyring, matching the `gh` and `aws` CLIs.

| Env var | Required | Default | Purpose |
| --- | --- | --- | --- |
| `PIPEDRIVE_COMPANY_DOMAIN` | only without `login` | — | Subdomain (e.g. `acme` for `acme.pipedrive.com`). After `pipedrive-mcp login`, recorded in user config and no longer required in env. |
| `PIPEDRIVE_API_TOKEN` | no | — | CI/automation fallback. Prefer `pipedrive-mcp login` for interactive use. |
| `LOG_LEVEL` | no | `info` | `debug` / `info` / `warn` / `error`. |
| `LOG_FORMAT` | no | `text` | `text` / `json`. |
| `PIPEDRIVE_ENABLE_DESTRUCTIVE` | no | `false` | When `true`, registers the destructive tools. |
| `PIPEDRIVE_DRY_RUN` | no | `false` | When `true`, every write becomes a rehearsal. |
| `PIPEDRIVE_HTTP_TIMEOUT` | no | `30s` | Per-request outbound HTTP timeout. |

## Quick start

### Claude Desktop, binary

After `pipedrive-mcp login` has stored the token in your keyring AND
recorded the default domain in user-config, the Claude Desktop config
needs nothing — no secret, no domain:

```json
{
  "mcpServers": {
    "pipedrive": {
      "command": "/usr/local/bin/pipedrive-mcp"
    }
  }
}
```

To pin a specific workspace (e.g., when you have several stored), add
`PIPEDRIVE_COMPANY_DOMAIN` under `env` to override the recorded
default for this MCP server only.

### Claude Desktop, Docker

Docker containers can't reach the host OS keyring, so the Docker path
needs `PIPEDRIVE_API_TOKEN` in the env block. Use Docker's
`--env-file` pattern so the token isn't visible in `docker inspect`:

```json
{
  "mcpServers": {
    "pipedrive": {
      "command": "docker",
      "args": [
        "run", "-i", "--rm",
        "--env-file", "/secure/path/to/pipedrive.env",
        "ghcr.io/mmedum/pipedrive-mcp:latest"
      ]
    }
  }
}
```

with `/secure/path/to/pipedrive.env` (mode 600):

```env
PIPEDRIVE_API_TOKEN=...
PIPEDRIVE_COMPANY_DOMAIN=acme
```

The `-i` flag is required so Docker keeps stdin open for MCP framing.

After saving the config, restart Claude Desktop and confirm the
`pipedrive` server appears as connected.

## Tool catalog

The catalog is auto-generated from the binary's `--dump-schemas` output
once tools start landing in Phase 1. Until then this section lists the
planned surface; see [`docs/architecture.md`](docs/architecture.md) and
[`CHANGELOG.md`](CHANGELOG.md) for what is actually shipped.

Planned categories:

- **Reads** — search/get for deals, persons, organizations, activities,
  pipelines, stages, products, deal-products, notes, plus a
  cross-resource `search_anything`.
- **Writes** — create/update for deals, persons, organizations,
  activities; add notes; attach/update deal line items.
- **Workflows** — move deal to stage, mark won/lost, complete activity,
  log activity composite.
- **Destructive (opt-in)** — detach product from deal.

## Troubleshooting

- **`401 Unauthorized` at startup, immediate exit.** The API token is
  invalid, revoked, or for the wrong workspace. Fix the token and restart;
  the server does not poll for token changes mid-process.
- **`custom_field "Region" not found`.** The cache is stale. Either run
  the `refresh_field_cache` tool (Phase 1+) or restart the server.
- **LLM does something surprising.** The MCP transport does not carry the
  user's prompt, so server logs cannot tell you *why* the LLM called a
  tool. Correlate the request ID in stderr with your MCP client's prompt
  log to reconstruct intent.

Run book and operational guidance: [`docs/operations.md`](docs/operations.md).

## Security

- Read [`SECURITY.md`](SECURITY.md) before reporting a vulnerability.
- Read [`docs/security.md`](docs/security.md) for token handling, the
  `--env-file` Docker pattern, and the threat model.

## Phase plan

Versioning is strict semver. The MCP tool surface is the public contract.

| Tag | Phase | What ships |
| --- | --- | --- |
| `v0.0.1` | Phase 0 | Repo scaffolding, CI, server boot, startup probe |
| `v0.1.0` | Phase 1 | All read tools |
| `v0.2.0` | Phase 2 | All write tools |
| `v0.3.0` | Phase 3 | Workflow tools |
| `v0.9.0` → `v1.0.0-rc.N` | Phase 4 | Polish, evals, security review |
| `v1.0.0` | Phase 5 | Stable surface, supported-version table |

Each phase boundary requires explicit maintainer approval before the next
phase starts.

## Supported versions

See [`SECURITY.md`](SECURITY.md#supported-versions).

## Contributing

- [`CONTRIBUTING.md`](CONTRIBUTING.md) — PR process, gate checklist,
  Phase 0 spike checklist, sandbox account ownership.
- [`docs/development.md`](docs/development.md) — local-run guide:
  prerequisites, `make check`, sandbox-based end-to-end verification,
  Claude Desktop wiring.

## License

[Apache License 2.0](LICENSE).

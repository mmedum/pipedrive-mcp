# pipedrive-mcp

[![CI](https://github.com/mmedum/pipedrive-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/mmedum/pipedrive-mcp/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

A production-grade [MCP](https://modelcontextprotocol.io) server that
exposes [Pipedrive CRM](https://pipedrive.com) over stdio to LLM-driven
clients such as Claude Desktop and Claude Code. Single static Go binary,
distroless Docker image, signed releases, semver-disciplined surface.

> **Status: Phase 1 shipped — `v0.1.0` released 2026-04-27.** Fifteen
> tools are registered by default: eleven v2 reads (`list_pipelines`,
> `list_stages`, `get_deal`, `list_deals`, `get_person`,
> `list_persons`, `get_organization`, `list_organizations`,
> `get_activity`, `list_activities`, `search` — the natural-language
> gateway), two v1 reads on the notes carve-out (`get_note`,
> `list_notes`), one v1 write (`create_note`, honours
> `PIPEDRIVE_DRY_RUN` for rehearsal mode), and one operator tool
> (`refresh_field_cache`). When the server is started with
> `PIPEDRIVE_ENABLE_DESTRUCTIVE=true` an additional destructive tool
> (`delete_note`) is registered. See `CHANGELOG.md` for what's
> landed; Phase 2 (write tools) is the next milestone.

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
# Replace X.Y.Z with the release version. Note: no `v` prefix in the
# archive filename — goreleaser strips it. The `.sig` and `.cert`
# sign the .tar.gz archive itself, not the binary inside.
cosign verify-blob \
  --certificate pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz.cert \
  --signature   pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz.sig \
  --certificate-identity-regexp 'https://github.com/mmedum/pipedrive-mcp/.*' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz

tar -xzf pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz
sudo mv pipedrive-mcp /usr/local/bin/pipedrive-mcp
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

Run `pipedrive-mcp --dump-schemas | jq '[.tools[].name]'` for the
authoritative list of tools the binary registers. As of v0.1.0 the
shipped tools are:

| Tool | Surface |
| --- | --- |
| `search` | Free-text search across deals / persons / organizations / products / files / leads. The natural-language gateway: resolve a name to an id, then drill in. |
| `list_pipelines` | Every pipeline the API token's user can see. |
| `list_stages` | Stages, optionally filtered to one pipeline. |
| `get_deal` | One deal by id, custom fields resolved by name. |
| `list_deals` | Deals filtered by status / pipeline / stage / owner / person / org / update window, sorted, cursor-paginated. |
| `create_deal` | Create a new deal. `title` required; everything else has Pipedrive defaults. Honours `PIPEDRIVE_DRY_RUN` for rehearsal mode. Custom fields not writable yet — edit in Pipedrive UI for now. |
| `get_person` | One person by id, with emails / phones / org link / custom fields. |
| `list_persons` | Persons filtered by owner / linked organization / update window, cursor-paginated. |
| `get_organization` | One organization by id, with structured address and custom fields. |
| `list_organizations` | Organizations filtered by owner / update window, cursor-paginated. |
| `get_activity` | One activity (call / email / meeting / task) by id, with location, participants, and conference details. |
| `list_activities` | Activities filtered by status / owner / deal / person / org / lead / update window, cursor-paginated. |
| `get_note` | One note by id (Pipedrive v1 carve-out — v2 has no /notes endpoint). |
| `list_notes` | Notes filtered by anchor (deal / person / org / lead), author, date range, or update window. |
| `create_note` | Attach a new note to a deal / person / org / lead / project. Honours `PIPEDRIVE_DRY_RUN` for rehearsal mode. |
| `delete_note` | Remove a note by id. Destructive — registered only when `PIPEDRIVE_ENABLE_DESTRUCTIVE=true`. Honours `PIPEDRIVE_DRY_RUN`. |
| `refresh_field_cache` | Re-fetch deal / person / org custom-field metadata. Operator escape hatch when fields change in the Pipedrive UI without a server restart. |

The categories below outline the planned post-v0.1.0 surface; see
[`CHANGELOG.md`](CHANGELOG.md) for what has actually shipped.

- **Writes** — create/update for deals, persons, organizations,
  activities; attach/update deal line items.
- **Workflows** — move deal to stage, mark won/lost, complete activity,
  log activity composite.
- **Destructive (opt-in)** — detach product from deal.

## Troubleshooting

- **`401 Unauthorized` at startup, immediate exit.** The API token is
  invalid, revoked, or for the wrong workspace. Fix the token and restart;
  the server does not poll for token changes mid-process.
- **`custom_field "Region" not found`.** The cache is stale. Call the
  `refresh_field_cache` tool to re-fetch deal / person / organization
  field metadata in parallel without restarting the server.
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
| (no tag) | Phase 0 | Repo scaffolding, CI, server boot, startup probe — folded into `v0.1.0` rather than cut as `v0.0.1` |
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

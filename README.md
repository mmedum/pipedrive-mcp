# pipedrive-mcp

[![CI](https://github.com/mmedum/pipedrive-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/mmedum/pipedrive-mcp/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

Pipedrive CRM as MCP tools. Read deals, people, organisations and activities, and write notes.

A single static Go binary that speaks [MCP](https://modelcontextprotocol.io)
over stdio to Claude Code, Claude Desktop or any other MCP client, against
your own Pipedrive API token. Signed releases and a semver-disciplined
tool surface.

> **Status: Phase 1 shipped.** Fifteen
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
## Why pipedrive-mcp

Pipedrive's own API is two APIs: a v2 that is current and a v1 that
sunsets on 2026-07-31. This server commits to v2, and carves out only the
endpoints that exist nowhere else — notes — on v1, so nothing here stops
working on that date by surprise.

It runs as a single static binary over stdio, against your own API token.
No remote transport and no OAuth, which makes what it can reach auditable
in one sitting.


- **No destructive tools by default.** Deletes are gated behind
  `PIPEDRIVE_ENABLE_DESTRUCTIVE`. A server-wide `PIPEDRIVE_DRY_RUN` makes
  every write a rehearsal that returns "would have done X" without firing
  the request.
- **Tiny static binary** (`CGO_ENABLED=0`, `-trimpath`, `-s -w`).
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

### From source

Requires Go 1.26.2.

```sh
go install github.com/mmedum/pipedrive-mcp/cmd/pipedrive-mcp@latest
```

## Set up Pipedrive

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

## Connect a client

### Claude Desktop

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

## Tools

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
| `create_person` | Create a new person (contact). `name` required; optional first/last name, emails, phones, org_id, owner_id. Honours `PIPEDRIVE_DRY_RUN`. |
| `get_organization` | One organization by id, with structured address and custom fields. |
| `list_organizations` | Organizations filtered by owner / update window, cursor-paginated. |
| `create_organization` | Create a new organization (account / company). `name` required; optional address (single-line, server-parsed) and owner_id. Honours `PIPEDRIVE_DRY_RUN`. |
| `get_activity` | One activity (call / email / meeting / task) by id, with location, participants, and conference details. |
| `list_activities` | Activities filtered by status / owner / deal / person / org / lead / update window, cursor-paginated. |
| `create_activity` | Create a new activity (call / email / meeting / task / ...). `subject` required; `type` defaults to `task` upstream. Honours `PIPEDRIVE_DRY_RUN`. |
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

## Safety

- **No destructive tools by default.** Deletes are registered only with
  `PIPEDRIVE_ENABLE_DESTRUCTIVE=true`. A tool that is not registered
  cannot be called, whatever a model asks for.
- **`PIPEDRIVE_DRY_RUN` makes every write a rehearsal**, returning what
  would have been sent without firing the request.
- **Stdout carries only MCP JSON-RPC frames.** That is the MCP stdio
  transport's own rule, and `forbidigo` enforces it: the process's
  streams are named in `main` and nowhere else. A stray print corrupts
  the stream and the client silently stops working.
- **A mistyped subcommand exits non-zero** rather than starting the
  server and reporting success.

## How it works

```
MCP client ──stdio──► pipedrive-mcp
                       ├── tools       one handler per tool; shapes the reply
                       ├── server      SDK wiring and the registered surface
                       ├── pipedrive   REST client for the v2 and v1 APIs
                       ├── config      every setting, from environment and flags
                       ├── credentials the API token: keyring, file, environment
                       └── userconfig  the non-secret profile
```

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

### Reading the setup from a script

`pipedrive-mcp status --json` prints the same state as one JSON object on
stdout. `credentials.resolved` is the field to branch on, `probe.ran`
distinguishes a skipped check from a failed one, and the command still
exits non-zero on every refusal, so a caller may read either. A label in
the human output is free to be reworded in any release; the object is
not.

## Getting help

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

If that does not explain it,
[open an issue](https://github.com/mmedum/pipedrive-mcp/issues). Never
paste an API token, a company domain or record contents into one;
describe the shape instead. Security problems go through
[`SECURITY.md`](SECURITY.md), privately.

## Versioning

See [`SECURITY.md`](SECURITY.md#supported-versions).

## Development

```bash
make build     # the binary
make test      # race detector, coverage
make check     # everything CI runs
```

`make check` is the definition of done: gofmt, `go vet`, golangci-lint,
race tests with coverage, `govulncheck`, a licence allow-list, the stdio
smoke test and the staleness check.

## Documentation

- [`docs/architecture.md`](docs/architecture.md) — the design and the
  decisions behind it.
- [`docs/configuration.md`](docs/configuration.md) — every setting.
- [`docs/development.md`](docs/development.md) — building and testing.
- [`docs/operations.md`](docs/operations.md) — running it in anger.
- [`docs/release.md`](docs/release.md) — how a release is cut.
- [`docs/security.md`](docs/security.md) — the threat model.

## Contributing

- [`CONTRIBUTING.md`](CONTRIBUTING.md) — PR process, gate checklist,
  Phase 0 spike checklist, sandbox account ownership.
- [`docs/development.md`](docs/development.md) — local-run guide:
  prerequisites, `make check`, sandbox-based end-to-end verification,
  Claude Desktop wiring.

## Security

- Read [`SECURITY.md`](SECURITY.md) before reporting a vulnerability.
- Read [`docs/security.md`](docs/security.md) for token handling and
  the threat model.

## Code of conduct

[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md) — Contributor Covenant.

## License

[Apache License 2.0](LICENSE).

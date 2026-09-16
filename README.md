# pipedrive-mcp

[![CI](https://github.com/mmedum/pipedrive-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/mmedum/pipedrive-mcp/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

Pipedrive CRM as MCP tools. Read and write deals, people, organisations, activities and notes, against your own API token.

A single static Go binary that speaks [MCP](https://modelcontextprotocol.io)
over stdio to Claude Code, Claude Desktop or any other MCP client, against
your own Pipedrive API token. Signed releases and a semver-disciplined
tool surface.

> **Status: aligned to the Google Workspace MCP conventions.** Twenty
> tools, all registered unconditionally and guarded at the call instead:
> fourteen reads (`search`, `whoami`, five `get_`, seven `list_`), five
> `manage_` tools carrying every mutation behind an `action`, and one
> operator tool (`refresh_field_cache`). Five resource templates mirror
> the `get_` tools. Everything is Pipedrive v2 except notes and
> `whoami`, which v2 does not expose at all. See `CHANGELOG.md` for what
> landed.

## Why pipedrive-mcp

Pipedrive's own API is two APIs: a v2 that is current and a v1 that
sunsets on 2026-07-31. This server commits to v2 and carves out only what
exists nowhere else — notes, and the `whoami` lookup — so nothing here
stops working on that date by surprise. Both carve-outs are documented at
their call sites and in `CHANGELOG.md`, because a v1 dependency that
nobody wrote down is one nobody migrates.

It runs as a single static binary over stdio, against your own API token.
No remote transport and no OAuth, which makes what it can reach auditable
in one sitting.

Its tool surface follows the shipped Google Workspace MCP servers rather
than a house style of its own. Those are the servers a model has most
likely already seen, so matching their conventions — discrete reads,
`manage_` tools for mutations, guarded writes that explain their
refusals, a server-level brief on where to start — is what makes this one
legible without being explained.


- **Writes are guarded at the call, not at startup.** Every reshaped
  write takes `dry_run` and reports what it would change without sending
  anything; `overwrite` is required before one may replace content that
  is already there, and `expect_version` refuses a write whose record
  moved since you read it. A refusal names what it protects and the
  argument that permits it.
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
# archive filename — goreleaser strips it.
sha256sum -c SHA256SUMS --ignore-missing

# SHA256SUMS is signed with a keyless Sigstore certificate tied to the
# release workflow's identity. The bundle carries the signature and the
# certificate together, and the checksum file covers every archive and
# every SBOM.
cosign verify-blob SHA256SUMS \
  --bundle SHA256SUMS.bundle \
  --certificate-identity-regexp 'https://github.com/mmedum/pipedrive-mcp/.*' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'

# And the archive itself carries build provenance.
gh attestation verify pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz --owner mmedum

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
| `PIPEDRIVE_DRY_RUN` | no | `false` | Server-wide dry-run floor: every write becomes a rehearsal. A per-call `dry_run` can only turn one on, never off. |
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

Twenty tools, shaped the way the Google Workspace MCP servers shape
theirs: **reads stay discrete, mutations of one record collapse behind a
single `manage_` tool with an `action`.** `pipedrive-mcp --dump-schemas
| jq '[.tools[].name]'` is the authoritative list; this table is the map.

**Start with `search`.** It is the natural-language gateway — it turns a
name into the numeric id every other tool needs, across deals, persons,
organizations, products, files and leads in one call. The `list_` tools
are the precision filters for when you already hold the ids. Reaching
for `list_deals` to find "the Acme deal" is the common mistake; it
filters, it does not match names.

| Read | What it answers |
| --- | --- |
| `search` | Free text across deals, persons, organizations, products, files and leads. Resolve a name to an id, then drill in. |
| `whoami` | Which account the token acts as and which workspace it points at. Its `user_id` is the `owner_id` a record gets when you create one without naming an owner, so it is what turns "my open deals" into a filter. Also reports the timezone an activity's `due_time` is written in. |
| `get_deal` / `get_person` / `get_organization` / `get_activity` / `get_note` | One record by id. Custom fields come back under the names the workspace gives them, not the 40-character hashes Pipedrive stores them under. |
| `list_deals` | Deals by status, pipeline, stage, owner, person, organization or update window. Cursor-paginated. |
| `list_persons` / `list_organizations` | By owner, linked record or update window. Cursor-paginated. |
| `list_activities` | By status, owner, deal, person, organization, lead or update window. Notes are stripped unless you ask for them. |
| `list_notes` | By the record they hang off, author, date range or update window. |
| `list_pipelines` / `list_stages` | The pipeline and stage a deal is filed under. No paging: a workspace rarely has twenty pipelines. |
| `refresh_field_cache` | Re-read custom-field names after someone adds or renames one in the Pipedrive UI. The operator escape hatch, so a rename does not need a restart. |

| Write | Actions |
| --- | --- |
| `manage_deal` | `create`, `update`, `move_stage`, `mark_won`, `mark_lost`, `reopen` |
| `manage_person` | `create`, `update` |
| `manage_organization` | `create`, `update` |
| `manage_activity` | `create`, `update`, `complete`, `reopen` |
| `manage_note` | `create`, `update`, `delete` |

### Guarded writes

Pipedrive has no undo, so a refusal is the only guard there is. Every
`manage_` tool reads its target before it writes and refuses what the
write would destroy that you cannot see. Each refusal names two things:
the fields it is protecting, and the argument that would permit the
write — a refusal you cannot act on is a bug.

- **`dry_run`** reports what the write would find and change and sends
  nothing.
- **`overwrite`** is required before an update may replace a field that
  already holds a value. Filling an empty field destroys nothing and
  needs no permission.
- **`expect_version`** carries the `update_time` from the read that
  informed the write, and refuses if the record moved since. Best
  effort: Pipedrive has no compare-and-set, so it catches a concurrent
  edit, not a determined race.

The named transitions — `mark_won`, `mark_lost`, `move_stage`, `reopen`,
`complete` — take no `overwrite`, because the field they change is the
field you named. Nothing is a one-way door on purpose: `reopen` undoes
both `mark_lost` and `complete`. The exception is `manage_note`'s
`delete`, which is soft — Pipedrive v1 clears `active_flag`, `get_note`
still returns the note, and **nothing here sets the flag back**.

A guarded write that stops comes back tagged `[refused]`, its own error
class so a model can tell it from a validation failure and from an
upstream error. Retrying is useless for one and correct for another.

### Resources

Five resource templates mirror the `get_` tools for clients that attach
records rather than calling tools:

```
pipedrive://deals/{id}          pipedrive://organizations/{id}
pipedrive://persons/{id}        pipedrive://activities/{id}
pipedrive://notes/{id}
```

They share the same code the tools do, so the two cannot drift into
describing a record differently. What a URI cannot carry is a tool's
options — `include_attendees`, `include_notes` — so those still need the
tool.

### What is not here

Products, leads, files, projects and goals have no tools yet. Custom
fields are readable everywhere and **not writable** — edit them in the
Pipedrive UI. Two things the API itself cannot do, which no retry will
fix: activity type cannot be filtered server-side (ask for the rows and
filter on their `type`), and activities are not indexed by `search`
(reach them through `list_activities`).

## Safety

- **Every write guards itself at the call.** `dry_run` reports what a
  write would find and change and sends nothing. `overwrite` is required
  before an update may replace content that is already there, and
  `expect_version` refuses a write whose record changed since you read
  it. Registration is no longer the gate: a tool that does not exist
  cannot explain why it will not act, so the refusal does it instead.
- **`PIPEDRIVE_DRY_RUN` is a floor, not a default.** Every write tool
  honours it. A per-call `dry_run` can turn a rehearsal on for one
  write; nothing on the wire can turn one off while the flag is set, so
  "set it and the server cannot write" stays true whatever a model asks
  for.
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

### What shipped

| Tag | Phase | What it was |
| --- | --- | --- |
| `v0.1.0` | Phase 1 | The read surface, the notes v1 carve-out, `create_note` / `delete_note`, `refresh_field_cache`. Phase 0's scaffolding folded in rather than cut as `v0.0.1`. |
| `v0.2.0` – `v0.3.2` | — | Release and supply-chain engineering, no tool-surface change: the gate scripts rewritten as one Go command, every action pinned by commit SHA, cosign signing, CycloneDX SBOMs, build provenance, reproducible builds, `--version`. |
| `v0.4.0` | Phases 2 and 3 | The write and workflow surface in one release — five `manage_*` tools with guarded writes, plus `whoami` and MCP resource templates — and the whole surface aligned to the Google Workspace MCP conventions. |

The middle tags went to release engineering rather than to phases, so the
phase numbers and the version numbers stopped tracking each other. The
table above says what actually happened rather than what was planned.

### What is left

| Tag | Phase | What it needs |
| --- | --- | --- |
| `v0.5.0` | Phase 3.5 | Custom-field **writes** (they are readable everywhere and writable nowhere), and an integration suite — no `//go:build integration` files ship yet, so the sandbox gate is still a manual rundown. |
| `v0.9.0` → `v1.0.0-rc.N` | Phase 4 | An eval suite (a release gate from Phase 4 onwards, and it does not exist yet), polish, and validation against a second workspace. |
| `v1.0.0` | Phase 5 | A stable surface and a supported-version table. |

**`v1.0.0` is gated on more than a checklist.** It means breaking changes
require a MAJOR bump, and two parts of this surface — the notes tools and
`whoami` — sit on Pipedrive **v1, which sunsets 2026-07-31**. If that
date passes without a v2 `/notes`, those tools break or change shape, and
a 1.0.0 cut before then would be a promise the API will not let us keep.
The v1 sunset needs a resolution first.

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

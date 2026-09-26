# pipedrive-mcp

[![CI](https://github.com/mmedum/pipedrive-mcp/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/mmedum/pipedrive-mcp/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/mmedum/pipedrive-mcp?sort=semver)](https://github.com/mmedum/pipedrive-mcp/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/mmedum/pipedrive-mcp.svg)](https://pkg.go.dev/github.com/mmedum/pipedrive-mcp)
[![License: Apache 2.0](https://img.shields.io/github/license/mmedum/pipedrive-mcp)](./LICENSE)

Pipedrive CRM as MCP tools. Find deals, people and companies, write to them, and move deals through a pipeline.

A single Go binary that speaks [Model Context Protocol](https://modelcontextprotocol.io)
over stdio — protocol revisions `2026-07-28` (the current one) back to
`2024-11-05`, negotiated per client. It runs as a subprocess of your client, on your own machine,
against one Pipedrive workspace: search by name and get back the ids
everything else needs, read deals, people, organizations, activities and
notes with their custom fields under the names your workspace gives them
and dropdown values under their labels, create and edit any of them, and
move a deal through its pipeline — won, lost, reopened, or into another
stage.

## Why pipedrive-mcp

Pipedrive's own API is two APIs: a v2 that is current and a v1 whose
sunset date, 2026-07-31, **has passed**. This server commits to v2 and
carves out only what exists nowhere else — notes, and the `whoami`
lookup. Those four tools run on an API that is out of support; they
still answer, and there is no v2 to move them to, because v2 exposes no
`/notes` and no `/users`. Both carve-outs are documented at their call
sites and in [CHANGELOG.md](CHANGELOG.md), because a v1 dependency
nobody wrote down is one nobody migrates — and a 410 from either now
reaches you as `[gone]`, naming the sunset, rather than as a validation
error blaming your request.

Its tool surface follows the shipped Google Workspace MCP servers rather
than a house style of its own: reads stay discrete, every mutation goes
through one `manage_` tool with an `action`, and writes explain their
refusals. Those are the servers a model has most likely already seen, so
matching them is what makes this one legible without being explained.

**Writing is guarded because Pipedrive has no undo.** Every write reads
its target first and refuses to replace a field that already holds a
value unless you say so, naming each field it is protecting. A refusal
you cannot act on is a bug.

Every tool below is driven against a real Pipedrive workspace as well as
against the fakes the tests use — reads, resources, the guard refusals,
the rehearsal paths, and a reversible write on each resource that has
one. That rundown is a Go suite rather than a habit now: it lives in
`internal/integration/` behind `-tags=integration`, and `make
integration` runs it. Two things still bound it. The write probes need
a second opt-in on top of the tag, `PIPEDRIVE_INTEGRATION_WRITES=1`, so
a run without that proves the read half only — writing to a live CRM is
a decision, not a default. And it has been exercised against one
workspace, so a custom-field or permission setup unlike that one is
untested ground.

## Install

```
go install github.com/mmedum/pipedrive-mcp/cmd/pipedrive-mcp@latest
```

That puts the binary in Go's bin directory, which is often not on your
`PATH`. If the next command says `command not found`, either use the full
path or add the directory once:

```
"$(go env GOPATH)/bin/pipedrive-mcp" --version    # check it landed
export PATH="$(go env GOPATH)/bin:$PATH"          # or add it to your shell profile
```

Or take a signed archive from the
[latest release](https://github.com/mmedum/pipedrive-mcp/releases/latest)
— Linux, macOS and Windows, on amd64 and arm64 — and put the binary on
your `PATH`. Nothing about a release has to be taken on trust:

```bash
# --ignore-missing, because SHA256SUMS covers every archive and every
# SBOM, and you will have downloaded one of them.
sha256sum -c SHA256SUMS --ignore-missing

# One signature over the checksum file, keyless, tied to the release
# workflow's own identity.
cosign verify-blob SHA256SUMS \
  --bundle SHA256SUMS.bundle \
  --certificate-identity-regexp 'https://github.com/mmedum/pipedrive-mcp/.*' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'

# And the archive itself carries build provenance.
gh attestation verify pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz --owner mmedum
```

## Set up Pipedrive

The server stores your API token in the **OS keyring** (libsecret on
Linux, Keychain on macOS, Credential Manager on Windows) rather than
asking you to paste it into a JSON config. Full reference and
validation rules: [docs/configuration.md](docs/configuration.md).

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

## Configuration

Every setting is a `PIPEDRIVE_*` environment variable. The full list, with
defaults and validation rules, is in
[docs/configuration.md](docs/configuration.md). The ones that change what
the server will do at all:

| Env var | Required | Default | Purpose |
| --- | --- | --- | --- |
| `PIPEDRIVE_COMPANY_DOMAIN` | only without `login` | — | Subdomain (e.g. `acme` for `acme.pipedrive.com`). After `pipedrive-mcp login`, recorded in user config and no longer required in env. |
| `PIPEDRIVE_API_TOKEN` | no | — | CI/automation fallback. Prefer `pipedrive-mcp login` for interactive use. |
| `LOG_LEVEL` | no | `info` | `debug` / `info` / `warn` / `error`. |
| `LOG_FORMAT` | no | `text` | `text` / `json`. |
| `PIPEDRIVE_DRY_RUN` | no | `false` | Server-wide dry-run floor: every write becomes a rehearsal. A per-call `dry_run` can only turn one on, never off. |
| `PIPEDRIVE_HTTP_TIMEOUT` | no | `30s` | Per-request outbound HTTP timeout. |

## Tools

Twenty tools. Reads stay discrete and every mutation goes through one
`manage_` tool with an `action`, which is how the Google Workspace MCP
servers shape theirs.

**Start with `search`.** It is the natural-language gateway: it turns a
name into the numeric id every other tool needs. Reaching for `list_deals`
to find "the Acme deal" is the common mistake — it filters, it does not
match names.

| Tool | What it does |
|---|---|
| `search` | Free text across deals, people, organizations, products, files and leads — the way to turn a name into an id |
| `whoami` | Which account the token acts as and which workspace it points at, plus the timezone an activity's due time is written in |
| `get_deal` | One deal: value, currency, status, stage, the people and company on it, and custom fields under their workspace names, dropdowns as labels |
| `list_deals` | Deals by status, pipeline, stage, owner, person, organization or update window, cursor-paginated. Archived deals live in their own collection — pass `archived` |
| `manage_deal` | Create or edit a deal, move it between stages, close it, archive it, or delete it — `create`, `update`, `move_stage`, `mark_won`, `mark_lost`, `reopen`, `archive`, `unarchive`, `delete`. Custom fields by name, dropdowns by label |
| `get_person` | One contact: names, every email and phone with its label, the company they belong to, and custom fields |
| `list_persons` | People by owner, linked organization or update window, cursor-paginated |
| `manage_person` | Create, edit or delete a contact — `create`, `update`, `delete`. Name it with `name` or with `first_name`/`last_name`, never both. Custom fields by name, dropdowns by label |
| `get_organization` | One company: the address Pipedrive parsed, how many people hang off it, and custom fields |
| `list_organizations` | Companies by owner or update window, cursor-paginated |
| `manage_organization` | Create, edit or delete a company — `create`, `update`, `delete`. Custom fields by name, dropdowns by label |
| `get_activity` | One call, email, meeting or task, with its location, participants and conference details |
| `list_activities` | Activities by status, owner, deal, person, organization, lead or update window; notes stripped unless asked for |
| `manage_activity` | Create or edit an activity, tick it off, or delete it — `create`, `update`, `complete`, `reopen`, `delete` |
| `get_note` | One note: its HTML, who wrote it, and which record it hangs off |
| `list_notes` | Notes by the record they hang off, author, date range or update window |
| `manage_note` | Create, edit or remove a note — `create`, `update`, `delete` |
| `list_pipelines` | Every pipeline the token can see; no paging, a workspace rarely has twenty |
| `list_stages` | The stages of a pipeline, with the deal probability Pipedrive gives each |
| `refresh_field_cache` | Re-read custom-field names and dropdown labels after somebody adds, renames or extends one in the Pipedrive UI |

### Guarded writes

Pipedrive has no undo, so a refusal is the only guard there is. Every
`manage_` tool reads its target before it writes and refuses what the
write would destroy that you cannot see. Each refusal names two things:
the fields it is protecting, and the argument that would permit the
write — a refusal you cannot act on is a bug.

- **`dry_run`** reports what the write would find and change and sends
  nothing.
- **`overwrite`** names the fields an update may replace — e.g.
  `["title", "value"]`, exactly as the refusal lists them. Naming fewer
  than it listed is still refused, over the ones you left out, so
  agreeing to replace a title is not agreeing to replace a value.
  Filling an empty field destroys nothing and needs no permission.
- **`expect_version`** carries the `update_time` from the read that
  informed the write, and refuses if the record moved since. Best
  effort: Pipedrive has no compare-and-set, so it catches a concurrent
  edit, not a determined race.

The named transitions — `mark_won`, `mark_lost`, `move_stage`, `reopen`,
`archive`, `unarchive` and `complete` — take no `overwrite`, because the
field they change is the field you named. Nothing is a one-way door on
purpose: `reopen` undoes `mark_won` and `mark_lost` on a deal, and
`complete` on an activity. What it does **not** do is clear a deal's
`lost_reason` — Pipedrive accepts that field only on a deal that is
lost, so the text of the last loss stays on the record until the deal
is lost again with a new one. The exception is `manage_note`'s
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

A stable surface is only half a promise without the other half, so this
is the list. Nothing below is an oversight to be reported; each is a
decision or an API limit.

**Resources with no tools.** Products, leads, files, projects and goals
are a milestone of their own. `search` will *find* products, files and
leads — its `item_type` covers them — so you can turn a name into an id
and then do nothing else with it, which is worth knowing before you try.

**Resources not modeled at all**, and not planned for 1.x: webhooks,
mail and mail threads, subscriptions and recurring revenue, saved
filters, currencies, deal-to-lead conversion, followers, deal
participants as a managed relation, and creating or altering custom
**field definitions** (their values are readable and writable; the
definitions are not).

**Merging records is not here.** Pipedrive can merge two people or two
organizations; this server cannot, and the reason is the guard contract
rather than the API — a merge destroys one record's field values in
favor of another's, across a record the caller has not read, and there
is no refusal that could name what it was about to lose.

**No field can be cleared** once it holds a value — Pipedrive v2 rejects
a null and stores an empty string as a value, so a field can be changed
but not emptied.

**Two the API itself cannot do**, which no retry will fix: activity type
cannot be filtered server-side (ask for the rows and filter on their
`type`), and activities are not indexed by `search` (reach them through
`list_activities`).

**Pipedrive's search index keeps deleted records**, and returns them
carrying nothing that says so — a deleted organization stays indexed
indefinitely, a deleted person for a while after the delete. `search`
therefore checks its hits against `/organizations` and `/persons` and
drops the ones that are gone: one extra call per type a page holds, and
none on a page that holds neither. Deals are *not* checked, because
`/deals` excludes archived deals — which are alive — so the check would
drop live records; Pipedrive drops deleted deals from its own index.
Products, files and leads are returned unchecked, because there is no
`list_` tool here to check them against. Because the drop happens after
Pipedrive has paged, a page can come back short, or empty with
`next_cursor` still set: an empty page is not the end.

**Four tools sit on Pipedrive API v1**, whose sunset has passed —
`get_note`, `list_notes`, `manage_note` and `whoami`, because v2 exposes
no `/notes` and no `/users`. They are explicitly outside the 1.0
compatibility promise; see [CHANGELOG.md](CHANGELOG.md).

## Safety

- **Every write guards itself at the call.** `dry_run` reports what a
  write would find and change and sends nothing. `overwrite` is required
  before an update may replace content that is already there, and
  `expect_version` refuses a write whose record changed since you read
  it. Registration is no longer the gate: a tool that does not exist
  cannot explain why it will not act, so the refusal does it instead.
- **`PIPEDRIVE_DRY_RUN` is a floor, not a default.** Every write tool
  honors it. A per-call `dry_run` can turn a rehearsal on for one
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

## Getting help

Run `pipedrive-mcp status`. It shows the workspace it resolved, where the
domain came from, whether the token was found and in which store, and
whether the startup auth probe reached Pipedrive — which is most of what
goes wrong on a first run. `status --json` prints the same thing as one
JSON object for a script that has to decide whether this server is
authorized before starting it; `credentials.resolved` is the field to
branch on, and `probe.ran` distinguishes a skipped check from a failed
one. The shape is in
[docs/configuration.md](docs/configuration.md#reading-the-setup-from-a-script).

Past that, the ones worth knowing:

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

Run book and operational guidance: [docs/operations.md](docs/operations.md).

If that does not explain it,
[open an issue](https://github.com/mmedum/pipedrive-mcp/issues). Never
paste an API token, a company domain or record contents into one;
describe the shape instead. Security problems go through
[SECURITY.md](SECURITY.md), privately.

## Versioning

Tool names, their arguments and the shape of their output are stable
within a major version. That promise is live as of 1.0.0: a break now
needs a major bump, where before 1.0.0 a minor release could still move
the tool surface. A change that
needs you to do something — a renamed tool, an argument that moved, a
different command in your client config — is marked **BREAKING** in
[CHANGELOG.md](CHANGELOG.md), which is what the release notes are made
from. Which versions get fixes is in
[SECURITY.md](SECURITY.md#supported-versions).

## Development

```bash
make build     # the binary
make test      # race detector, per-package coverage floor
make check     # everything CI runs
make smoke     # drive the built binary over stdio and read the reply
```

`make check` is the definition of done: gofmt, `go vet`, golangci-lint,
race tests with a per-package coverage floor, `govulncheck`, a license
allow-list, a leak gate refusing anything that looks like a real account's
data in the working tree, a pinned-version gate holding every action to a
commit SHA and every tool it installs to an exact version, a staleness
gate over the dependency pins, a manifest gate holding the installable
bundle to its schema, a link gate over the changelog's version headings,
and a stdio smoke test. CI adds a schema diff against the base branch
that fails an unacknowledged change to the tool surface, and a changelog
gate that fails source changes with no entry under `[Unreleased]`.

Conventions are in [CONTRIBUTING.md](CONTRIBUTING.md); building, testing
and releasing are in [docs/development.md](docs/development.md) and
[docs/release.md](docs/release.md).

## Documentation

- [docs/architecture.md](docs/architecture.md) — the design, the evidence
  behind it, and the phase plan.
- [docs/configuration.md](docs/configuration.md) — every setting.
- [docs/development.md](docs/development.md) — building and testing.
- [docs/operations.md](docs/operations.md) — running it in anger.
- [docs/release.md](docs/release.md) — how a release is cut.
- [docs/security.md](docs/security.md) — the threat model.

## Contributing

- [CONTRIBUTING.md](CONTRIBUTING.md) — PR process, gate checklist,
  Phase 0 spike checklist, sandbox account ownership.
- [docs/development.md](docs/development.md) — local-run guide:
  prerequisites, `make check`, sandbox-based end-to-end verification,
  Claude Desktop wiring.

## Security

- Read [SECURITY.md](SECURITY.md) before reporting a vulnerability.
- Read [docs/security.md](docs/security.md) for token handling and
  the threat model.

## Code of conduct

[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) — Contributor Covenant 3.0.

## License

[Apache License 2.0](LICENSE).

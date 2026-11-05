# pipedrive-mcp

[![CI](https://github.com/mmedum/pipedrive-mcp/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/mmedum/pipedrive-mcp/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/mmedum/pipedrive-mcp?sort=semver)](https://github.com/mmedum/pipedrive-mcp/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/mmedum/pipedrive-mcp.svg)](https://pkg.go.dev/github.com/mmedum/pipedrive-mcp)
[![License: Apache 2.0](https://img.shields.io/github/license/mmedum/pipedrive-mcp)](./LICENSE)

Pipedrive CRM as MCP tools. Find deals, people and companies, write to them, and move deals through a pipeline.

A single Go binary that speaks [Model Context Protocol](https://modelcontextprotocol.io)
over stdio, from the current protocol revision back to `2024-11-05`. It
runs as a subprocess of your client, on your own machine, against one
Pipedrive workspace, with your own API token. There is no server to host
and no shared deployment.

It is for a person who works in Pipedrive and wants an assistant to do
that work with them: turn a name into a record, read deals, people,
organizations, activities and notes with their custom fields under the
names the workspace gives them, edit any of them, and move a deal
through its pipeline: won, lost, reopened, archived or into another
stage.

> **Unofficial.** Not affiliated with or endorsed by Pipedrive.

## Goals and non-goals

Goals:

- **Nothing is overwritten that you did not mean to replace.** A write
  to an existing record reads it first, and refuses to replace a field
  that holds a value unless the call names that field. Pipedrive has no
  undo.
- **A refusal says how to proceed.** It names what it protects and the
  argument that would permit the write.
- **Results speak the workspace's language.** Custom fields come back
  under their names and dropdowns as their labels, and are written the
  same way.
- **Your workspace stays yours.** Every call runs with your token, only
  to your workspace's Pipedrive address, with no telemetry. Logs never
  carry a payload or the token.

Non-goals:

- **More than one workspace per server.** It resolves one at startup.
- **Clearing a field.** Pipedrive v2 rejects a null and stores an empty
  string as a value, so a field can be changed but not emptied.
- **Merging records.** A merge destroys one record's values across a
  record nobody read, and no refusal could name what it would lose.
- **Products, leads, files, projects and goals.** `search` finds some of
  them, and no tool reads or writes them.
- **Fetching every page for you.** Each list call returns one page and
  the cursor for the next.
- **Asking you before a call.** Your client approves each call; the
  server asks nothing.

## What makes this one different

**It stays on Pipedrive v2, except where v2 has nothing.** Notes and the
`whoami` lookup run on API v1, because v2 exposes no `/notes` and no
`/users`. Pipedrive has deprecated a list of v1 endpoints, and notes and
`/users/me` are not on it. If v1 answers one of them with a 410, the
`[gone]` error says the endpoint has no v2 equivalent, rather than
blaming your request. Any other answer is classified by its status.

**Its tool surface follows the shipped Google Workspace MCP servers.**
Reads stay discrete, every mutation goes through one `manage_` tool with
an `action`, and writes explain their refusals. Those are the servers a
model has most likely already seen, so this one needs less explaining.

**It is checked against a real workspace.** Beside the unit tests, a
suite in `internal/integration` drives the server against a live
Pipedrive workspace: reads, resources, guard refusals, rehearsals, and a
reversible write on each resource that has one. Writing to a live CRM is a separate
opt-in, and the suite has run against one workspace, so a custom-field
or permission setup unlike it is untested.

## Tools

**Start with `search`.** It turns a name into the numeric id every other
tool needs. `list_deals` filters; it does not match names.

| Tool | What it does |
|---|---|
| `search` | Free text across deals, people, organizations, products, files and leads — the way to turn a name into an id |
| `whoami` | Which account the token acts as, the workspace it points at, and the account's timezone |
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
| `list_pipelines` | The pipelines the token can see, in one call with no paging |
| `list_stages` | The stages of a pipeline, with the deal probability Pipedrive gives each |
| `refresh_field_cache` | Re-read custom-field names and dropdown labels after somebody adds, renames or extends one in the Pipedrive UI |

### Guarded writes

Every `manage_` tool reads its target before it writes, and refuses what
the write would destroy that you cannot see. Each refusal names the
fields it is protecting and the argument that would permit the write.

- **`dry_run`** reports what the write would find and change, and sends
  nothing.
- **`overwrite`** names the fields an update may replace, such as
  `["title", "value"]`, exactly as the refusal lists them. Naming fewer
  than it listed is still refused over the rest. Filling an empty field
  destroys nothing and needs no permission.
- **`expect_version`** carries the `update_time` from the read that
  informed the write, and refuses if the record moved since. Pipedrive
  has no compare-and-set, so it catches a concurrent edit, not a
  determined race.

The named transitions — `mark_won`, `mark_lost`, `move_stage`, `reopen`,
`archive`, `unarchive` and `complete` — take no `overwrite`, because the
field they change is the field you named. `reopen` undoes `mark_won` and
`mark_lost` on a deal, and `complete` on an activity. It does not clear
a deal's `lost_reason`: Pipedrive accepts that field only on a lost
deal, so the last loss's text stays until the deal is lost again.

A delete is soft. Pipedrive removes a deleted deal, person,
organization or activity for good after 30 days, and nothing here puts
one back. A deleted note keeps its text, and nothing here restores it
either.

A guarded write that stops returns `[refused]`, a class of its own, so a
model can tell it from a validation failure and from an upstream error.

### Resources

Resource templates mirror the `get_` tools for clients that attach
records rather than calling tools:

```
pipedrive://deals/{id}          pipedrive://organizations/{id}
pipedrive://persons/{id}        pipedrive://activities/{id}
pipedrive://notes/{id}
```

They share the tools' code, so the two cannot describe a record
differently. A URI carries no options, so `include_attendees` and
`include_notes` still need the tool.

### What is not here

Each of these is a decision or an API limit, not an oversight.

- **Resources not modeled at all**: webhooks, mail and mail threads,
  subscriptions and recurring revenue, saved filters, currencies,
  deal-to-lead conversion, followers, deal participants as a managed
  relation, and custom field definitions. Custom field values are
  readable and writable; the definitions are not.
- **Two things the API cannot do**, which no retry fixes. Activity type
  cannot be filtered server-side, so ask for the rows and filter on
  their `type`. Activities are not indexed by `search`, so reach them
  through `list_activities`.
- **Pipedrive's search index keeps deleted records**, with nothing
  saying so. `search` checks organization and person hits against
  Pipedrive and drops the deleted ones, at one extra call per type a
  page holds. Deals are not checked, because Pipedrive drops deleted
  deals itself, and a check against `/deals` would drop archived ones.
  Products, files and leads are returned unchecked. Because the drop
  happens after Pipedrive has paged, a page can come back short, or
  empty with `next_cursor` still set: an empty page is not the end.
- **The v1 tools sit outside the compatibility promise.** `get_note`,
  `list_notes`, `manage_note` and `whoami` leave in a minor release if
  v1 stops answering them; see [CHANGELOG.md](CHANGELOG.md).

## Install

```bash
go install github.com/mmedum/pipedrive-mcp/cmd/pipedrive-mcp@latest
```

That puts the binary in Go's bin directory, which is often not on your
`PATH`. If the next command says `command not found`, use the full path
or add the directory once:

```bash
"$(go env GOPATH)/bin/pipedrive-mcp" --version    # check it landed
export PATH="$(go env GOPATH)/bin:$PATH"          # or add it to your shell profile
```

Or take a signed archive from the
[latest release](https://github.com/mmedum/pipedrive-mcp/releases/latest),
for Linux, macOS or Windows on amd64 or arm64, and put the binary on
your `PATH`. Verify it first:

```bash
# One keyless signature over the checksum file, tied to the release
# workflow on a tag. Until it passes, the checksums are only what the
# release says.
cosign verify-blob checksums.txt \
  --bundle checksums.txt.bundle \
  --certificate-identity-regexp '^https://github\.com/mmedum/pipedrive-mcp/\.github/workflows/release\.yml@refs/tags/' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'

# checksums.txt covers every archive and SBOM, and you downloaded one.
# On macOS, use shasum -a 256 -c.
sha256sum -c checksums.txt --ignore-missing

# The archive carries build provenance.
gh attestation verify pipedrive-mcp_X.Y.Z_linux_amd64.tar.gz --owner mmedum
```

## Set up Pipedrive

The server keeps your API token in the OS keyring: libsecret on Linux,
Keychain on macOS, Credential Manager on Windows. Store it once:

```sh
pipedrive-mcp login
# Pipedrive workspace subdomain (e.g. acme for acme.pipedrive.com): your-subdomain
# Pipedrive API token for "your-subdomain": ········
# login: token stored in OS keyring (service=pipedrive-mcp, account=your-subdomain)
# login: default domain recorded in ~/.config/pipedrive-mcp/config.json
```

Pass `--domain your-subdomain`, or set `PIPEDRIVE_COMPANY_DOMAIN`, to
skip the domain prompt. `login` checks the token with Pipedrive before
it stores anything, and records the domain in a non-secret file so your
client config needs neither. Run `pipedrive-mcp status` to check the
setup, and `pipedrive-mcp logout` to remove it.

`PIPEDRIVE_API_TOKEN` and `PIPEDRIVE_COMPANY_DOMAIN` in the environment
win over the keyring and the recorded domain, as the `gh` and `aws`
CLIs order theirs.

## Connect a client

After `pipedrive-mcp login`, a client needs only the command.

Claude Code:

```bash
claude mcp add pipedrive /absolute/path/to/pipedrive-mcp
```

Claude Desktop, in `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "pipedrive": {
      "command": "/usr/local/bin/pipedrive-mcp"
    }
  }
}
```

To pin one workspace when you have several stored, add
`PIPEDRIVE_COMPANY_DOMAIN` under `env`.

Each release also carries `pipedrive-mcp_<version>.mcpb`, a Claude
Desktop bundle. Opening it installs the server and asks for the company
domain, an API token and whether to run as a dry run. It is listed in
`checksums.txt`, so the release signature covers it.

## Configuration

Every setting is an environment variable;
[docs/configuration.md](docs/configuration.md) lists them with their
defaults and validation. `PIPEDRIVE_DRY_RUN=true` turns every write into
a rehearsal.

## Safety

- **Every write guards itself at the call.** A tool that cannot act
  says why in its refusal, so the destructive actions are registered and
  guarded rather than left out.
- **`PIPEDRIVE_DRY_RUN` is a floor, not a default.** Every write tool
  honors it. A per-call `dry_run` can turn a rehearsal on, and nothing a
  model sends can turn one off, so a server started with it cannot
  write.
- **Stdout carries only MCP JSON-RPC frames.** A stray print would
  corrupt the stream, so the process's streams are named in `main` and
  nowhere else.
- **A mistyped subcommand exits non-zero** rather than starting the
  server.

## Getting help

Run `pipedrive-mcp status`. It shows the workspace it resolved, where
the domain and the token came from, and whether Pipedrive accepts the
token, which covers most first-run problems.
[docs/troubleshooting.md](docs/troubleshooting.md) lists each error the
server returns or exits with, what causes it and what to do, then
rotating and revoking the token.

The server keeps no record of tool calls, and the MCP transport does not
carry your prompt. To see why a model called a tool, read your client's
transcript; to see what changed, read Pipedrive's own history.

If that does not explain it,
[open an issue](https://github.com/mmedum/pipedrive-mcp/issues). Never
paste an API token, a company domain or record contents into one;
describe the shape instead.

## Versioning

Tool names, their arguments and the shape of their output are stable
within a major version. A change that needs you to do something, such
as a renamed tool or a different command in your client config, is
marked **Breaking** in [CHANGELOG.md](CHANGELOG.md), which the release
notes are made from. Only the latest release receives fixes
([SECURITY.md](SECURITY.md#supported-versions)).

## Documentation

- [docs/configuration.md](docs/configuration.md): every setting, the
  commands, and how the token and the domain resolve.
- [docs/troubleshooting.md](docs/troubleshooting.md): symptoms and their
  fixes, then rotating and revoking the token.
- [docs/security.md](docs/security.md): the threat model.
- [docs/architecture.md](docs/architecture.md): how it works, and the
  design decisions a contributor should not undo.
- [docs/release.md](docs/release.md): how a release is cut and checked.
- [CONTRIBUTING.md](CONTRIBUTING.md): building, the gates, the live run
  and the pull request flow.
- [CHANGELOG.md](CHANGELOG.md): what each release changed.

## Contributing

Questions and bugs go in
[issues](https://github.com/mmedum/pipedrive-mcp/issues); pull requests
are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) covers building, the
gates and the pull request flow, and `make check` is what has to pass.

## Security

[SECURITY.md](SECURITY.md) says how to report a vulnerability. Please do
not open a public issue for one.

## Code of conduct

[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md), the Contributor Covenant 3.0.

## License

[Apache License 2.0](LICENSE).

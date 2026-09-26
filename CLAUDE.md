# CLAUDE.md — pipedrive-mcp project instructions

These are project-specific instructions for Claude Code working in this
repository. They override default behavior. The user's global `~/.claude/CLAUDE.md`
still applies; this file adds project-level rules.

## Project mission

`pipedrive-mcp` is a production-grade MCP (Model Context Protocol) server
that exposes Pipedrive CRM operations over stdio to LLM-driven clients
(Claude Desktop, Claude Code, etc.). The architecture overview is in
`docs/architecture.md`. The phased delivery plan lives in the repo — the
tables under "Phase plan" in `docs/architecture.md` for what each tag
shipped and what is left, and "Phases and approvals" in
`CONTRIBUTING.md` for the approval rule. It used to live in a plan file
outside the repo; that file is gone, which is why it now lives somewhere
a contributor can actually read.

## Hard rules

1. **Pipedrive v2-only — except notes and `whoami`.** Phase 0 takes zero v1
   dependencies. The notes carve-out (`/api/v1/notes/*`) landed in
   Phase 1.6 because v2 does not expose `/notes` (Pipedrive's
   developer team officially recommends v1 for notes). The carve-out
   is implemented via `Client.doV1` / `Client.postV1` / `Client.putV1` /
   `Client.deleteV1` in `internal/pipedrive/client.go`; v2 remains the
   default for every other resource.

   The **second carve-out is `whoami`** (`GET /api/v1/users/me`),
   added with an explicit user go-ahead: v2 exposes no users resource
   at all, which is also why the startup auth probe uses `/dealFields`.
   See `internal/pipedrive/users.go`.

   **Any further v1 dependency** needs a CHANGELOG entry under
   `### Changed` explaining why no v2 endpoint exists, an inline code
   comment at the call site, and an explicit user go-ahead.
2. **Stdout is reserved for MCP JSON-RPC frames**. This is the protocol,
   not a house preference. MCP's stdio transport says the server "MUST
   NOT write anything to its `stdout` that is not a valid MCP message",
   and "MAY write UTF-8 strings to its standard error (`stderr`) for
   logging purposes" —
   <https://modelcontextprotocol.io/specification/2025-06-18/basic/transports>.
   All logs go to stderr through `slog`. A stray print corrupts the
   JSON-RPC stream and the client silently stops working.

   `forbidigo` enforces it: `fmt.Print*` and `os.Stdout` are forbidden
   outside `main`, which names the process's streams once and passes them
   down as `io.Writer`. Check the message text when verifying it — a
   settings block that fails to load leaves forbidigo on its defaults,
   firing, looking like it works.
3. **Destructive tools register by default and guard at call time.**
   The Google servers ship `trash_file`, `delete_message` and
   `delete_space` registered, and guard per call instead of per
   process. `PIPEDRIVE_ENABLE_DESTRUCTIVE` is retired. `dry_run` is a
   per-call input on every write. A guard is only added where the
   caller cannot already see what they are about to lose — see
   "Guarded writes" below and `docs/security.md`.
4. **Custom fields nest under `custom_fields`** in v2 request and response
   bodies (root-level on v1). Every typed struct in `internal/pipedrive/`
   reflects this.
5. **403 disambiguation** uses the `error` string and request endpoint, not
   `error_info` (which is free-text on v2). The signal-substring list
   in `internal/pipedrive/errors.go` is the source of truth.
6. **The startup auth probe is `GET /api/v2/dealFields?limit=1`**, not
   a users call: `/api/v2/users` does not exist, and `/dealFields` is
   confirmed v2-supported and present in every workspace. The probe
   response is discarded; the LLM never sees it. This is separate from
   the `whoami` tool, which answers the LLM's "who am I acting as" over
   the v1 carve-out in rule 1 — the probe runs once at startup and
   tells nobody anything.
7. **No auto-commit, no auto-push.** The user manages git.

## Where things go

- `cmd/pipedrive-mcp/` — main entrypoint. Subcommand dispatch (login/logout/server), flag parsing, thin wiring.
- `internal/app/` — the startup assembly every entry point shares:
  domain → `config.LoadFor` → token → client → server. `Settings.NewServer`
  is the only place a **serving** process builds `tools.RegisterOptions`,
  so the `PIPEDRIVE_DRY_RUN` floor cannot be dropped by a caller
  assembling the arguments by hand — which is exactly how it was dropped
  once. (`--dump-schemas` builds a clientless server to walk the registry;
  no handler runs there, so it has no floor to drop.) Nothing here logs,
  exits or hits the network; the caller decides what a missing token
  means.
- `internal/config/` — env-var loading and validation. Does NOT handle the API token — that lives in `internal/credentials/`.
- `internal/credentials/` — OS keyring-based token storage (`pipedrive-mcp login` writes here). Falls back to `PIPEDRIVE_API_TOKEN` env for CI.
- `internal/userconfig/` — non-secret JSON pointer file at `os.UserConfigDir()/pipedrive-mcp/config.json`. Records the active workspace domain so subsequent runs don't need `PIPEDRIVE_COMPANY_DOMAIN` re-supplied. The env > userconfig > error resolution itself lives in `internal/app`.
- `internal/version/` — build-time version string.
- `internal/pipedrive/` — HTTP client. One file per resource type, plus
  shared `client.go`, `errors.go`, `types.go`. No MCP imports here.
- `internal/server/` — MCP SDK wiring. Calls each tool package's
  `Register(...)` function.
- `internal/integration/` — the live suite, every file behind
  `//go:build integration`. It drives the server `internal/app` builds,
  over an in-memory transport against a real workspace, so it tests the
  wiring the binary ships rather than a re-registration of it. Put a test here only when
  a fake cannot answer the question — wire format, field resolution,
  paging, the guards over real records. Writes need
  `PIPEDRIVE_INTEGRATION_WRITES=1` and must restore from `t.Cleanup`
  and verify the restore with a fresh read; see the contract at the top
  of `write_test.go`.
- `internal/tools/` — MCP tool registrations, one file per resource
  type, plus the machinery they share. Each `Register(...)` adds tools to
  both `mcp.AddTool` and the parallel registry in `registry.go` (so
  `--dump-schemas` can walk them). Also here: `guard.go` (the
  guarded-write mechanism — field tables, projections, the overwrite
  refusal), `resources.go` (the MCP *resource* templates mirroring the
  `get_` tools), `errors.go`, `pagination.go`, `sort.go`.
- `docs/` — user-facing documentation (configuration, operations, security, architecture overview, development, release runbook).
- `audit/security-reviews/` — committed `/security-review` outputs from each release boundary.
- `audit/release-smoke/` — committed Claude Desktop release smoke transcripts.
- `scripts/` — CI helper scripts.

When adding a new tool: add it to `internal/tools/<resource>.go` with a
`Register` function, ensure the parallel registry pattern is followed, add
unit tests for input validation and output shape, update the README's
tool catalog table, add a CHANGELOG entry under `[Unreleased]`, and
update relevant `docs/`.

## Definition of done (every PR)

`make check` runs every per-PR gate in order. Run it, not the individual
commands — the list below has drifted three times, once naming three
`scripts/*.sh` files that no longer exist.

```
make check   # verify-tool-versions fmt vet lint test vuln licenses
             # staleness leaks pins mcpb changelog-links checklist
             # descriptions smoke
```

That list is no longer maintained by hand: `make checklist` holds it
against the Makefile's own `check:` target, so a new gate fails the
build until this block names it. It had drifted three times before that
existed — most recently in the commit that added `descriptions` to the
Makefile and not to this file, one day after the previous drift was
fixed by hand and a note was written saying a gate was the real fix.

Individually, when you need to isolate one: `make fmt` (gofmt, must be
empty), `make vet`, `make lint` (golangci-lint), `make test` (`-race`,
with coverage ≥ 80% on `internal/pipedrive`, `internal/tools`,
`internal/credentials` and `internal/app` — the same threshold the CI
gate enforces),
`make vuln` (govulncheck), `make licenses` (allow-list), `make staleness`,
`make leaks`, `make pins`, `make mcpb` (the bundle manifest against its
schema), `make changelog-links` (every version heading linked),
`make checklist` (this list against the Makefile), `make descriptions`
(the tool descriptions against the house-style rules that can be checked
mechanically) and `make smoke` (drives the built binary over stdio).
The CHANGELOG *entry* gate is not in `make check` because it needs a
base ref:
`go run ./scripts/gates changelog <base> HEAD`, and it only fires when a
watched source path changed.

**Run the gates against the pinned toolchain**, not whatever `go` is on
PATH: `export GOTOOLCHAIN=go1.26.6` (the `toolchain` line in `go.mod`,
and `GO_VERSION` in `.github/workflows/ci.yml` — keep all three in step).
A newer local Go silently passes things CI will fail.

One known exception: `make licenses` fails under `GOTOOLCHAIN=go1.26.6`
on a machine whose local Go is newer, because Go downloads the pinned
toolchain into the module cache and `go-licenses` cannot resolve stdlib
packages from there (`does not have module info`). CI installs a real SDK
via `actions/setup-go` and is unaffected. Run that one gate with the
local toolchain; if it fails there, the failure is real.

Plus the manual skills:

- Run `/simplify` on changed files; resolve issues or document why not.
- Run `/security-review` on changed files; resolve findings or document
  acceptance with rationale in the PR description.
- Update `README.md` and the relevant `docs/*.md` if user-facing behavior
  changed.
- Capture design rationale in the relevant `docs/` file or as a code
  comment if the change makes a non-trivial decision. No separate ADR
  tree — see `CONTRIBUTING.md`.
- Update `CHANGELOG.md` `[Unreleased]`.

If any of the above fails or is skipped, the task is not done. Surface the
failure to the user; do not silently move on.

## Release boundaries

**The phases are done.** Phase 5 shipped as `v1.0.0` on 2026-09-24, and
the go-ahead for it is recorded in `audit/release-smoke/v1.0.0.md`, per
the rule that used to live here. There is no phase x+1 to be authorized
for.

What survives the phases is the part that was never really about them:
**do not tag without the user saying so, each time.** A tag publishes a
GitHub Release, cosign signatures and an MCP registry entry that
**cannot be withdrawn**, and from 1.0.0 the tool surface carries a
stability promise, so a mistaken tag is not a thing that can be quietly
re-cut. Do not self-advance from a green branch to a tag.

Per-release gates are in addition to the per-PR gates above:

- `go test -race -count=3 ./...` (three shuffled runs).
- `make integration-writes` against the sandbox — the suite in
  `internal/integration/`, behind `//go:build integration`. Reads,
  resource reads, guard refusals and dry runs run on the tag alone;
  the reversible write probes also need
  `PIPEDRIVE_INTEGRATION_WRITES=1`. It does not replace the manual
  Claude Desktop smoke below, which exercises a client this suite
  cannot.
- Eval suite three runs (Phase 4+).
- `/simplify` and `/security-review` over the cumulative diff since the
  prior tag; outputs committed under `audit/security-reviews/v<tag>.md`.
- Reproducible-build verification (two clean builds, byte-identical
  SHA256).
- Documentation freshness review across `README.md`, `docs/`, and this
  file.
- Manual Claude Desktop smoke; transcript committed under
  `audit/release-smoke/v<tag>.md`.

## Ask before doing

These actions need explicit user confirmation each time, even in
auto/yolo modes:

- Modifying `.github/workflows/release.yml` or any code path that publishes
  artifacts.
- Rotating, regenerating, or invalidating any sandbox or production API
  token.
- Changing the schema-diff CI gate (semver discipline depends on it).
- Force-pushing, deleting branches, or anything that rewrites published
  history.
- Deleting files outside obvious scratch locations.
- Disabling any quality gate (`--no-verify`, lint-skip annotations,
  `t.Skip` on a previously passing test).

## MCP error mapping (codified before Phase 1)

Per the MCP spec (<https://modelcontextprotocol.io/specification/2025-06-18/server/tools>):
two distinct error channels.

- **Protocol errors** (JSON-RPC `-32xxx`) for: unknown tool, invalid
  arguments that fail schema validation, server panics. The SDK emits
  these automatically; we rarely raise them by hand.
- **Tool execution errors** (`isError: true` on `CallToolResult`) for:
  upstream API failures (Pipedrive 401/403/404/429/5xx), business-logic
  rejections, our own validation (e.g., notes-size cap). Pattern
  matches GitHub's `github-mcp-server` (`pkg/github/issues.go` returns
  `(*CallToolResult, nil)` with `isError: true`, never raises a JSON-RPC
  error for upstream HTTP).

Tool handlers translate `internal/pipedrive` errors to `isError: true`
results. Reserve JSON-RPC errors for "the LLM called us wrong" cases
the SDK already handles.

## Tool description house style

The standard is the shipped Google Workspace MCP servers (`google-drive`,
`google-sheets`, `google-docs`, `google-chat`). Read one of their tool
descriptions before writing a new one. The voice is plain declarative
prose — not bullets, not the schema restated in English.

**Open with what the tool does, in one sentence.** Spend the rest of the
description on what will burn the caller.

> Find files across My Drive, files shared with you, and every shared
> drive, by name, content, kind, folder, owner, star or date.

**Explain the why inline, never just the rule.** A constraint the caller
understands is one they can reason around; a bare prohibition gets
retried verbatim.

> the sheet title exactly as get_spreadsheet reports it […] nothing is
> defaulted, because Google names the first sheet in the account's
> language

**`IMPORTANT:` in caps, at most once per description**, reserved for the
trap that silently returns a wrong answer instead of an error.

> IMPORTANT: Drive does not do substring search.

**Caps for the two or three words that carry the sentence.**

> Everything about one file, folder or shortcut: what kind of thing it
> is, WHERE IT LIVES, its link […] WHO CAN SEE IT

**State the cost** where one tool is much more expensive than its
neighbor, so the caller reaches for the cheap one.

> a search costs twenty times what a read does

**Name the negative space.** What the API cannot do is as load-bearing as
what it can.

> Drive cannot search a folder recursively, so this does not reach
> subfolders

**Close by pointing at the sibling tool** that covers the case this one
does not.

> Use get_spreadsheet first for sheet names and sizes, and
> find_in_spreadsheet when you do not yet know which cells you want.

**Say what the result sets up**, so the caller knows what becomes
possible next.

> The result lists the sheets afterwards, so the next call can name one
> exactly.

### Input field descriptions

Lowercase, no trailing period. One clause of purpose, one of trap.
Defaults and maxima inline. Enums verbatim.

> default 25, maximum 200

> add, rename, duplicate, copy_to, hide, unhide, reorder, resize, freeze
> or tab_color

> Off by default: a currency symbol inside a number you may want to
> compute with is a trap

### Carried over from the previous house style

These survive the switch, because the Google servers do them too:

- **Imperative verb first.** "Search deals by …", not "This tool
  searches …".
- **Name the resource explicitly**: `deal_id`, `pipeline_name` — not
  `deal`, `pipeline`. Ambiguous names cost retrieval precision.
- **Enumerate small enums verbatim** in the description
  ("Status: open | won | lost | deleted"), so the LLM pre-validates
  instead of guessing.
- **Anti-confabulation guards** on free-text optional fields: "if the
  user did not give a reason, leave it blank — do NOT invent one."
- **Resolve UUIDs to semantic names in OUTPUT** — custom fields surfaced
  by name, never by 40-char hash.

### Dropped

- ~~State at most one key constraint.~~ Google states every constraint
  that changes what the caller should do, and explains each one. The cap
  was costing accuracy without buying brevity.
- ~~Registration is the enforcement mechanism.~~ Annotations remain
  advisory metadata, but destructiveness is now enforced at call time by
  the guarded-write contract below, not by whether a tool registered.

## Guarded writes

Every write tool follows the Google contract: **read the target first,
then refuse anything the write would destroy that the caller cannot
see.** Pipedrive has no undo, so the refusal is the only guard there is.

Each refusal names two things: what it is protecting, and the argument
that would allow the write. A refusal the caller cannot act on is a bug.

The guards, and only these — an extra confirmation flag on an operation
the caller can already see the consequences of is friction, not safety:

- **`dry_run`** on every write tool, no exceptions. Reports what the
  write would find and change, sends nothing. It is a per-call input, so
  the caller decides per write — but `PIPEDRIVE_DRY_RUN` remains a
  **floor**, not a default: a call can turn a rehearsal on and cannot
  turn one off. `docs/security.md` promises an operator that the env
  flag makes speculative LLM work safe, and a tool that honored only
  the per-call input would make that promise false.
- **`overwrite`** where the write would clobber a populated field the
  caller has not read. It is a **list of field names**, not a flag:
  `["title", "value"]` permits exactly those two, and a write that also
  lands on a third is still refused over the third. The refusal names
  **every** protected field and prints the array to pass back — derive
  that set from the same diff that produces the `changed` report rather
  than hand-checking one field, or the guard drifts from the report.
  Not needed to fill a field that is empty: filling destroys nothing.
- **`expect_version`** carries the `update_time` from the read that
  informed the write. The write is refused if the record changed since.
  Best effort — Pipedrive has no atomic compare-and-set — so it catches
  the concurrent-edit case, not a determined race.
- **`force`** ONLY where the write destroys collateral the caller cannot
  see: content belonging to someone else, or rows the tool did not
  return. `google-chat`'s `delete_message` is the model — `force` exists
  there solely because deleting a message takes other people's threaded
  replies with it. Do not add `force` to an operation whose entire blast
  radius is already on screen.

**A reversible delete needs no permitting argument.** `trash_file` takes
`dry_run` and nothing else, and spends its description saying restore
brings it back. Pipedrive v1 deletes are soft — `active_flag=false`,
still readable through `get_note` — so they are the same case. Say so in
the description instead of adding a flag.

Return the stored values back after a write and name every one the
upstream changed, the way `write_values` names each value Google
coerced. Diff against the record read before the write, not against the
request: Pipedrive normalizes some of what it stores.

**One list of field names per resource.** The diff, the overwrite guard
and the update-request builder all walk the same names. Write them once
(`noteUpdatableFields` is the pattern) so adding a field is one edit
rather than three that can disagree.

**Optional scalars on an update take pointers**, so nil can mean "leave
this alone" and a value can mean "set it". A bare `int64` or `string`
with `omitempty` collapses those two, and an update that could not tell
them apart would overwrite every field the caller did not mention.
Input shape is a schema-diff and therefore a semver event — decide it
once, up front.

**Clearing a field is not supported, and no tool may claim otherwise.**
Pipedrive v2 rejects a null and stores an empty string as a value, so
neither empties a field. v1 clears correctly, which is how we know it is
a v2 gap and not a law of nature — but reaching for it would be a third
carve-out on an API whose 2026-07-31 sunset has PASSED. Every optional
input reads "omit to leave it as it is". `docs/architecture.md`,
"Clearing a field",
has the request-by-request evidence; read it before adding a third state
to a request struct.

## LLM-facing summary types

Every LLM-facing output type (`dealSummary`, `personSummary`,
`organizationSummary`, `activitySummary`, ...) is a **parallel
shadow** of its upstream `pipedrive.X` type — defined in
`internal/tools/<resource>.go` with all `jsonschema:` tags scoped
to that file. Test-side decode mirrors (`dealRow`, `activityRow`, ...)
follow the same pattern.

**Do NOT** embed `pipedrive.X` in a summary type. **Do NOT** put
`jsonschema:` tags on `internal/pipedrive/` types.

**Why:** The duplication is the price of an explicit allow-list
contract — the next upstream field addition cannot silently leak
into the LLM-facing schema, and per-tool presentation decisions
(e.g. `include_notes` stripping `note` / `public_description` from
`activitySummary` rows) live cleanly in the tools package without
the architectural exception that an embed would require.
Precedents: `github/github-mcp-server` (`Minimal*` types with
`convertToMinimal*` helpers), Anthropic's "Writing tools for
agents" (concise/detailed `response_format` pattern), and Nigel
Tao's documented `bug.Gray` / `image.Gray` Go embed-regression
(<https://nigeltao.github.io/blog/2024/go-embedding-back-compat.html>),
which proves the upstream-leak risk is real, not theoretical.

**Nested types** (`pipedrive.ContactPoint`, `pipedrive.Address`,
`pipedrive.ActivityLocation`, ...) may be referenced directly from
a summary struct without a shadow when the upstream shape matches
the LLM-facing shape — see `personSummary.Emails` for precedent.
Wrap only when narrowing fields (e.g. `addressRow` in
`organizations.go`).

## Style notes

- Prefer the standard library. Add a third-party Go module only when the
  alternative is materially worse, and document the choice in
  `CHANGELOG.md` and a code comment at the import site.
- Prefer many small, well-named functions over long ones.
- Comments only when the *why* is non-obvious. The code says *what*.
- Match the existing file's style. Don't reformat unrelated code in a PR.
- For tool description prose, see "Tool description house style" above.

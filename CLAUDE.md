# CLAUDE.md — pipedrive-mcp project instructions

These are project-specific instructions for Claude Code working in this
repository. They override default behavior. The user's global `~/.claude/CLAUDE.md`
still applies; this file adds project-level rules.

## Project mission

`pipedrive-mcp` is a production-grade MCP (Model Context Protocol) server
that exposes Pipedrive CRM operations over stdio to LLM-driven clients
(Claude Desktop, Claude Code, etc.). The architecture overview is in
`docs/architecture.md`; the phased delivery plan and exit criteria live
in the user's plan file under
`~/.claude/plans/pipedrive-go-mcp-validated-river.md`.

## Hard rules

1. **Pipedrive v2-only — except notes.** Phase 0 takes zero v1
   dependencies. The notes carve-out (`/api/v1/notes/*`) landed in
   Phase 1.6 because v2 does not expose `/notes` (Pipedrive's
   developer team officially recommends v1 for notes). The carve-out
   is implemented via `Client.doV1` / `Client.postV1` in
   `internal/pipedrive/client.go`; v2-GET remains the default for
   every other resource. **Any new v1 dependency beyond notes**
   needs a CHANGELOG entry under `### Changed` explaining why no v2
   endpoint exists, an inline code comment at the call site, and an
   explicit user go-ahead.
2. **Stdout is reserved for MCP JSON-RPC frames**. All logs go to stderr.
   Never `fmt.Println(...)` from anywhere reachable at runtime; use
   `slog` writing to `os.Stderr`.
3. **No destructive tools registered by default.** The
   `PIPEDRIVE_ENABLE_DESTRUCTIVE` env flag is the only way to register them.
   See `docs/security.md`.
4. **Custom fields nest under `custom_fields`** in v2 request and response
   bodies (root-level on v1). Every typed struct in `internal/pipedrive/`
   reflects this.
5. **403 disambiguation** uses the `error` string and request endpoint, not
   `error_info` (which is free-text on v2). The signal-substring list
   in `internal/pipedrive/errors.go` is the source of truth.
6. **`whoami` is intentionally absent.** The startup auth probe is
   `GET /api/v2/dealFields?limit=1` (`/api/v2/users` does NOT exist on
   v2 either — `/dealFields` is confirmed v2-supported and present in
   every workspace). The probe response is discarded; the LLM never
   sees it.
7. **No auto-commit, no auto-push.** The user manages git.

## Where things go

- `cmd/pipedrive-mcp/` — main entrypoint. Subcommand dispatch (login/logout/server), flag parsing, thin wiring.
- `internal/config/` — env-var loading and validation. Does NOT handle the API token — that lives in `internal/credentials/`.
- `internal/credentials/` — OS keyring-based token storage (`pipedrive-mcp login` writes here). Falls back to `PIPEDRIVE_API_TOKEN` env for CI.
- `internal/userconfig/` — non-secret JSON pointer file at `os.UserConfigDir()/pipedrive-mcp/config.json`. Records the active workspace domain so subsequent runs don't need `PIPEDRIVE_COMPANY_DOMAIN` re-supplied. Resolution: env > userconfig > error.
- `internal/version/` — build-time version string.
- `internal/pipedrive/` — HTTP client. One file per resource type, plus
  shared `client.go`, `errors.go`, `types.go`. No MCP imports here.
- `internal/server/` — MCP SDK wiring. Calls each tool package's
  `Register(...)` function.
- `internal/tools/` — MCP tool registrations. One file per resource type.
  Each `Register(...)` adds tools to both `mcp.AddTool` and the parallel
  registry in `registry.go` (so `--dump-schemas` can walk them).
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

Before declaring a task complete, run **all** of:

1. `go build ./...`
2. `go vet ./...`
3. `gofmt -l .` (must be empty)
4. `golangci-lint run`
5. `go test -race -coverprofile=cov.out ./...` (and verify ≥ 80% on
   `internal/pipedrive`, `internal/tools`, and `internal/credentials`
   — the same threshold the CI gate enforces).
6. `govulncheck ./...`
7. `go-licenses check ./...` against the allow-list.
8. `bash scripts/staleness-check.sh`
9. `bash scripts/changelog-check.sh <base> HEAD` (if source paths changed)
10. `docker build -t pipedrive-mcp:dev .` and trivy scan
11. `bash scripts/stdio-smoke.sh binary ./pipedrive-mcp`
12. `bash scripts/stdio-smoke.sh docker pipedrive-mcp:dev`

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

## Phase boundaries

Each phase ends with a tagged release (`v0.0.1`, `v0.1.0`, ..., `v1.0.0`).
**Tagging does not authorize starting the next phase.** The user reviews
the release and explicitly says "go" before phase x+1 begins. Do not
self-advance. Record the user's go-ahead in the next phase's release notes
when the time comes.

Per-phase release gates are in addition to the per-PR gates above:

- `go test -race -count=3 ./...` (three shuffled runs).
- `go test -tags=integration -race ./...` against the sandbox once
  the integration suite exists (no `//go:build integration` files
  ship today; live verification is done by manually driving the
  binary through Claude Code against the sandbox until that suite
  lands).
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

Tool descriptions are the dominant quality lever for LLM tool selection.
House style, derived from Anthropic's *Writing tools for agents*:

- **Imperative verb first**: "Search deals by ...", "Mark a deal won.",
  "Move a deal to a different stage." Not "This tool searches ..."
- **Name the resource explicitly**: `user_id`, `deal_id`, `pipeline_name`,
  not `user`, `deal`, `pipeline`. Anthropic specifically calls out that
  ambiguous names cost retrieval precision.
- **State at most one key constraint** in the description (e.g.,
  "Default limit is 25, max 100."). Move the rest to the input field's
  own `description`.
- **Enumerate enums verbatim** in the description when the field is
  small ("Status: open | won | lost | deleted"). The LLM uses these
  to pre-validate; making it guess hurts accuracy.
- **Anti-confabulation guards** for free-text fields the LLM might
  invent: spell out "if the user did not provide a reason, leave
  blank — do NOT invent one." Apply this to any free-text optional
  input (e.g. a future `mark_deal_lost`'s `lost_reason`).
- **Resolve UUIDs to semantic names in OUTPUT** where possible (e.g.,
  custom fields surfaced by name not by 40-char hash). Anthropic:
  *"resolving arbitrary alphanumeric UUIDs to more semantically
  meaningful and interpretable language ... significantly improves
  Claude's precision in retrieval tasks."*
- **Annotations are advisory metadata, not enforcement.** Set
  `mcp.ToolAnnotations.ReadOnlyHint` / `DestructiveHint` /
  `IdempotentHint` per tool — they're correct metadata that may
  inform future client UX. Do NOT rely on them to gate registration:
  destructive tool registration is gated by `PIPEDRIVE_ENABLE_DESTRUCTIVE`
  at server build time, which is enforcement.

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

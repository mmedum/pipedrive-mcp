# Changelog

All notable changes to this project are documented here. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project adheres to [Semantic Versioning 2.0](https://semver.org/spec/v2.0.0.html).

The public versioning contract is the MCP tool surface — tool names, input
schemas, output schemas, and documented behavior. Internal package layout,
error message wording, and log line formats are not part of the contract.

Pre-1.0 minor releases may break the tool surface. From 1.0.0 onwards,
breaking changes require a MAJOR bump.

## [Unreleased]

### Changed
- Cache-warm goroutine in `server.New` now respects the parent
  context, so SIGTERM mid-warm cancels in-flight `/dealFields` /
  `/personFields` / `/organizationFields` requests cleanly instead
  of letting them run orphaned to completion. No user-visible
  behavior change in the happy path.

### Added
- `get_activity` and `list_activities` tools — fetch and filter
  Pipedrive activities (calls / emails / meetings / tasks). Filters
  cover status (`open` / `done` / `all`), owner, deal, person,
  organization, lead, and update window. Default sort is
  `update_time desc` (most-recently-touched first) for the natural
  "what's been happening with X lately" query; pass
  `sort_by=due_date status=open` for an upcoming-calendar view.
  `include_attendees=true` opts into the calendar attendees array.
  `include_notes=true` opts into `note` + `public_description`
  (off by default — these are often multi-KB HTML and would bloat
  LLM context on a sweep). `get_activity` always returns notes.
  Activity-type filtering is intentionally client-side: Pipedrive v2
  dropped the `type` query param and the documented behaviour is to
  filter the returned rows on `type` post-hoc. The LLM-facing
  `activitySummary` is a parallel shadow of `pipedrive.Activity`
  (matching the `dealSummary` / `personSummary` / `organizationSummary`
  pattern); jsonschema annotations are scoped to `internal/tools`.

### Changed
- `internal/tools` — `list_deals`, `list_activities`, and `search`
  now share a single page-size policy (`defaultListLimit=25`,
  `maxListLimit=100`, `clampLimit()` helper in
  `internal/tools/pagination.go`). Per-tool constants and the
  identical clamp blocks were removed. No user-visible behavior
  change — the caps were already aligned; the consolidation prevents
  silent drift.
- `pipedrive-mcp status` subcommand. Reports the active workspace
  domain (and where it was resolved from), the token source (keyring
  or env var), and the result of an auth probe against Pipedrive.
  Supports `--no-probe` for offline status checks.
- `pipedrive-mcp login` now prompts for the workspace subdomain
  interactively when neither `--domain` nor `PIPEDRIVE_COMPANY_DOMAIN`
  is supplied. Mirrors the `aws configure` / `gh auth login` pattern:
  scriptable inputs win, but the bare-hands path is fully interactive.
  Logout still requires an explicit `--domain` or env value (it's
  managing existing entries, not gathering input).
- After `pipedrive-mcp login` succeeds, the chosen workspace domain is
  now recorded in `os.UserConfigDir()/pipedrive-mcp/config.json`
  (`~/.config/pipedrive-mcp/config.json` on Linux). Subsequent
  invocations of `pipedrive-mcp` (server) and `pipedrive-mcp status`
  no longer require `PIPEDRIVE_COMPANY_DOMAIN` to be set in env when a
  default is recorded. Resolution order: env > userconfig > error.
- `pipedrive-mcp logout` no longer requires `--domain` or
  `PIPEDRIVE_COMPANY_DOMAIN`. Plain `pipedrive-mcp logout` resolves
  the workspace from the recorded default in user config (the same
  pointer `login` writes), so the common single-workspace case Just
  Works. Pass `--domain` to remove a non-default workspace when
  several are stored. Logout also clears the recorded default domain
  when it matches the workspace being logged out of; other
  workspaces' tokens and pointers are left untouched.
- `internal/userconfig/` package wrapping the JSON config file, with
  atomic write, 0600 file perms, and 0700 dir perms.
- `config.LoadFor(domain)` so callers (the server entrypoint) can
  resolve the domain from any source and validate it through the
  same code path as `config.Load()`.

### Added (Phase 1)
- **`get_person`** tool — fetch a single Pipedrive person by
  `person_id`. Returns id, full/first/last name, all emails (with
  primary flag and label), all phones, owner_id, linked org_id,
  add/update timestamps, and any custom fields resolved by name.
  Unknown person_id returns `[not_found]`. Pair with `search` to
  resolve a name to an id first.
- **`get_organization`** tool — fetch a single Pipedrive organization
  by `org_id`. Returns id, name, formatted address, owner_id,
  people_count, add/update timestamps, and any custom fields
  resolved by name. Unknown org_id returns `[not_found]`.
- `internal/pipedrive/Client.personFields` and `.organizationFields`
  — per-resource lazy field caches mirroring the deals pattern.
  Warmed in `server.New` alongside `dealFields` so the first
  `get_person`/`get_organization` call doesn't pay the metadata
  round-trip on the critical path.
- **`search`** tool — free-text search across deals, persons,
  organizations, products, files, and leads via Pipedrive's
  `/api/v2/itemSearch`. Designed as the gateway tool for natural-
  language CRM queries: when a user asks about "deals for Acme", the
  LLM calls `search(term="Acme", types=["organization"])` first to
  resolve the org_id, then drills into `list_deals(org_id=...)` /
  `get_deal(...)`. Returns id, type, name, relevance score, and
  type-specific details (org country/city, person email/phone, deal
  value/currency/status). Cursor-paginated; default limit 25, max
  100. Output includes a `truncated` boolean — set when more results
  exist beyond the page — so the LLM never silently undercounts.
  Tool description disambiguates use vs `list_deals`: search for
  name/term lookups, `list_deals` for structured filters
  (stage/owner/value/dates) since itemSearch only covers a subset of
  custom-field types.
- **`get_deal`** tool — fetch a single Pipedrive deal by `deal_id`.
  Returns id, title, value, currency, status (open | won | lost |
  deleted), stage_id, pipeline_id, owner_id, person_id, org_id,
  expected_close_date, won/lost timestamps, lost_reason, and any
  custom fields resolved by name. Unknown deal_id returns
  `[not_found]`.
- **`list_deals`** tool — search deals with optional filters
  (`status`, `pipeline_id`, `stage_id`, `owner_id`, `person_id`,
  `org_id`). Cursor-paginated; default limit 25, max 100. Returns
  the same shape as `get_deal` plus a `next_cursor` for additional
  pages.
- `internal/pipedrive/FieldCache` — generic, lazy-loaded, sync.Once-
  guarded cache for Pipedrive field metadata. Reload() clears the
  cache so the future `refresh_field_cache` tool (Phase 1.9) can
  trigger a refetch. Persons / organizations / products will reuse
  this same type as their PRs land.
- `*pipedrive.Client.DealFields` — per-Client deal-field cache wired
  to `ListDealFields`. First `get_deal` or `list_deals` call
  triggers the fetch; subsequent calls hit the cache.
- Custom-field hash-key resolution: deal output's `custom_fields` is
  keyed by human-readable name (e.g. `"Account Manager"`) where the
  field metadata is in cache. Unknown keys (newly-created fields the
  cache hasn't seen yet) pass through under their original 40-char
  hash so no data is silently lost.
- Cursor-based pagination plumbing (`additional_data.next_cursor`)
  surfaced through the deals tools.
- HTTP body read limit raised from 1 MiB to 8 MiB to accommodate
  list responses with heavy custom fields. Tool inputs cap page size
  at 100 so the limit is generous in practice.


- **`list_pipelines`** tool — returns every Pipedrive pipeline the API
  token's user can see (id, name, order, active flag, link to the
  Pipedrive UI). No filtering or pagination; workspaces typically have
  under 20 pipelines total.
- **`list_stages`** tool — returns Pipedrive stages, optionally filtered
  to one pipeline via the `pipeline_id` input. Each stage carries id,
  name, order_nr, active flag, owning pipeline_id, and Pipedrive's
  default deal_probability (0-100). When `pipeline_id` is set, the tool
  validates the pipeline exists and is visible to the API token's user;
  unknown IDs return a `[not_found]` error rather than an empty array
  (Pipedrive's `/api/v2/stages` returns `[]` for both real-but-empty and
  nonexistent pipelines, so the validation is needed for the LLM to tell
  them apart).
- `internal/server/testutil` package — Connect helper that wires an
  in-memory client to a server with a tool registered, via
  `mcp.NewInMemoryTransports`. The canonical pattern for tool-handler
  tests in this codebase.

### Added
- `pipedrive-mcp login` and `pipedrive-mcp logout` subcommands. Tokens
  are stored in the OS keyring (libsecret on Linux, Keychain on macOS,
  Credential Manager on Windows) so they no longer need to live in
  `claude_desktop_config.json` / `~/.claude.json`. `login` reads
  without echo, validates against Pipedrive before storing.
- `internal/credentials/` package wrapping
  `github.com/zalando/go-keyring` with a Backend interface for tests.
- `internal/version` reads VCS metadata from `runtime/debug.ReadBuildInfo()`.
  `pipedrive-mcp --version` now prints `vX.Y.Z (commit, built time, goX.Y.Z)`
  with a `-dirty` marker when the working tree has uncommitted changes.
- Initial repository scaffolding (Go module, Dockerfile, lint config).
- CI pipeline (build, vet, lint, `go mod verify`, race+`-shuffle=on`+coverage,
  govulncheck, license check, staleness, schema diff, Docker build + trivy,
  stdio smoke for binary and Docker, CHANGELOG enforcement, gitleaks).
- Release pipeline driven by goreleaser (pinned to v2.13.1) for
  cross-compiled archives, checksums, cosign-signed blobs (keyless
  OIDC), CycloneDX SBOM via syft, and GitHub Release publishing.
  Release notes come from the matching `## [vX.Y.Z]` section of
  `CHANGELOG.md` rather than goreleaser's git-log changelog. SLSA L2
  build-provenance attestations via `actions/attest-build-provenance@v4`.
  Reproducible-build sanity check (linux/amd64) gates publication.
- Docker publish split into `.github/workflows/docker-publish.yml`:
  multi-arch (linux/amd64, linux/arm64) push to GHCR with cosign image
  signing by digest. Pattern matches `github/github-mcp-server`.
- Project documentation tree under `docs/` (architecture, configuration,
  development, operations, release, security).
- Project meta files (`SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`,
  `CLAUDE.md`).
- CI helper scripts (`scripts/changelog-check.sh`, `scripts/stdio-smoke.sh`,
  `scripts/staleness-check.sh`).
- Dependabot config groups minor/patch bumps per ecosystem to keep
  weekly noise to one PR per ecosystem rather than per-dep.
- `CLAUDE.md` codifies the MCP error mapping (upstream API failures →
  `isError: true` on `CallToolResult`, not JSON-RPC errors) and the
  tool-description house style (Anthropic's *Writing tools for agents*
  guidance).

### Changed
- `PIPEDRIVE_API_TOKEN` is now an opt-in CI/automation override rather
  than a required env var. Token resolution: env first, keyring
  second (matches `gh`/`aws`/`gcloud` precedence). When env is unset
  and the keyring is unavailable for a reason other than
  "entry-not-found" (no DBus, no Secret Service, headless container),
  the underlying error is surfaced so the user can fix the keyring or
  set the env var explicitly.

### Security
- HTTP client refuses to follow 3xx redirects. Pipedrive v2 doesn't
  redirect during normal operation, and Go's default redirect-follower
  forwards custom headers — including the `x-api-token` we attach —
  to redirect targets verbatim. This was hardening, not a known
  exploit, but the fix is one line.
- `APIError.RawBody` field removed. The raw response body — which can
  contain deal/contact PII echoed back by Pipedrive — was unused.
  External callers can still branch on the error class via `errors.Is`
  and read `Status`/`Message`/`Endpoint`.

[Unreleased]: https://github.com/mmedum/pipedrive-mcp/compare/HEAD...HEAD

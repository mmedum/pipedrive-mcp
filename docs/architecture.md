# Architecture

This document describes how `pipedrive-mcp` is put together. It is the
operator/contributor view; the LLM-facing view is in
[`../README.md`](../README.md) and the per-tool descriptions in the binary
itself.

## Transport and process model

The server speaks the [Model Context Protocol](https://modelcontextprotocol.io)
over stdio. There is no HTTP, no socket, no IPC of any other kind.

The process is a single Go binary with no persistent state. Memory
footprint is bounded (target < 50 MB resident). The only background
goroutines are the MCP SDK's transport reader and a one-shot
field-cache warm-up at startup; we do not spawn workers, queues, or
schedulers.

```
+----------------------+        stdio (JSON-RPC frames)        +-------------+
|   MCP client (LLM)   | <----------------------------------->  |  pipedrive- |
| Claude Desktop, etc. |                                        |     mcp     |
+----------------------+                                        +-------------+
                                                                       |
                                                                       v
                                                              +------------------+
                                                              | api/v2/<resource>|
                                                              +------------------+
```

## Module layout

```
pipedrive-mcp/
├── cmd/pipedrive-mcp/         # entrypoint: subcommand dispatch (login/logout/status), flag parsing, wiring
├── internal/
│   ├── config/                # env loading + validation; LoadFor accepts a domain from any source
│   ├── credentials/           # OS keyring storage + PIPEDRIVE_API_TOKEN env fallback
│   ├── userconfig/            # ~/.config/pipedrive-mcp/config.json (default-domain pointer; non-secret)
│   ├── version/               # build-time version string (VCS metadata via runtime/debug)
│   ├── pipedrive/             # HTTP client (no MCP imports)
│   │   ├── client.go          # shared HTTP, x-api-token header, redirect refusal, retry/backoff
│   │   ├── errors.go          # status -> typed error mapping + 403 disambiguation
│   │   ├── types.go           # response types + generic itemEnvelope[T] / listEnvelope[T]
│   │   ├── path.go            # buildPath + setLimitCursor query helpers
│   │   ├── urls.go            # WebURL helper for LLM-facing UI links
│   │   ├── fieldcache.go      # generic, lazy-loaded, sync.Once-guarded hash↔name cache
│   │   ├── probe.go           # auth probe (GET /dealFields?limit=1)
│   │   ├── search.go          # /itemSearch wrapper + ItemType constants
│   │   ├── deals.go           # /deals + ListDealsOptions
│   │   ├── dealfields.go      # /dealFields + cache wiring (Resolve/Warm)
│   │   ├── persons.go         # /persons + /personFields + cache wiring
│   │   ├── organizations.go   # /organizations + /organizationFields + cache wiring
│   │   ├── pipelines.go       # /pipelines
│   │   ├── stages.go          # /stages
│   │   └── activities.go      # /activities
│   ├── server/                # MCP SDK wiring + cache warm-up fan-out
│   │   ├── server.go
│   │   └── testutil/          # in-memory MCP transport for tool-handler tests
│   └── tools/                 # tool registrations (one file per resource type)
│       ├── registry.go        # parallel registry + DumpJSON for --dump-schemas
│       ├── addtool.go         # generic AddTool[In, Out] schema-inferring wrapper
│       ├── errors.go          # errorResult + validateEnum + validatePositiveID
│       ├── pipelines.go       # list_pipelines + list_stages
│       ├── deals.go           # get_deal + list_deals
│       ├── persons.go         # get_person
│       ├── organizations.go   # get_organization
│       ├── activities.go      # get_activity + list_activities
│       └── search.go          # search (unified itemSearch wrapper)
├── docs/                      # operator docs (this tree)
└── scripts/                   # CI helper scripts
```

The `internal/pipedrive` ↔ `internal/tools` split is deliberate: the HTTP
client is testable and reusable independently of the MCP transport, and
tool packages contain only the schema definitions, validation, and a thin
adapter that calls the client. Per-type rendering decisions (e.g. deal
`title` vs everyone else's `name`) live in tools, not in the HTTP layer.

## Request lifecycle

1. `cmd/pipedrive-mcp/main.go` resolves the workspace domain (env >
   userconfig > error), loads config, builds the Pipedrive client,
   runs the auth probe (`GET /api/v2/dealFields?limit=1`), constructs
   the MCP server, and starts the stdio transport.
2. `internal/server/server.go` registers every tool package and kicks
   off a single goroutine that warms the deal/person/org field caches
   in parallel, off the critical path.
3. An incoming MCP `tools/call` is decoded by the SDK into the tool's
   typed input struct (defined in the matching `internal/tools/*.go`).
4. The tool's handler validates input (`validatePositiveID`,
   `validateEnum`), calls the matching `internal/pipedrive` function,
   and resolves any custom-field hashes to names via the per-Client
   cache.
5. The Pipedrive client (`internal/pipedrive/client.go`) adds
   `x-api-token`, fires the HTTP request, refuses redirects, decodes
   the response body into a typed struct using `itemEnvelope[T]` /
   `listEnvelope[T]`, and maps any non-2xx response through `classify`
   in `errors.go`.
6. The tool wraps the result into its typed output struct (e.g.
   `dealSummary`), including a `pipedrive.WebURL` pointing at the
   Pipedrive web UI for the affected entity, and returns to the SDK.
7. The SDK marshals the output against its declared schema and writes
   the response frame.

## Error mapping

`internal/pipedrive/errors.go` maps HTTP status to a small set of typed
sentinel errors. `internal/tools/errors.go` translates those into
`mcp.CallToolResult{IsError: true}` with a leading `[<class>]` tag the
LLM can branch on without parsing free text.

| Status | Sentinel | LLM-facing class | Behavior |
| --- | --- | --- | --- |
| 200/201/204 | — | — | success path |
| 400 | `ErrValidation` | `[validation]` | upstream message surfaced |
| 401 | `ErrUnauthorized` | `[auth]` | startup: exit non-zero. Mid-process: return per-call. |
| 403 (permission) | `ErrForbiddenPermission` | `[permission]` | upstream message surfaced |
| 403 (business rule) | `ErrForbiddenBusinessRule` | `[business_rule]` | upstream message surfaced |
| 404 | `ErrNotFound` | `[not_found]` | upstream message surfaced |
| 429 | `ErrRateLimited` | `[rate_limited]` | retry honoring `Retry-After`, jittered |
| 5xx | `ErrServerError` | `[server_error]` | retry with jittered exponential backoff |

Retry policy: `maxAttempts = 3` (so up to 3 attempts total: at most 2
backoff sleeps between them). 429 honors `Retry-After`; otherwise base
delay 1s with ±25% jitter, doubled per attempt up to a 30s cap.

403 disambiguation does **not** rely on `error_info` — that field is
free text on v2 and not a structured discriminator. Disambiguation is by
the upstream `error` string. The signal-substring list lives in
`internal/pipedrive/errors.go` as `businessRule403Signals`.

The LLM-facing error text is intentionally terse:
`[not_found] Deal not found` — not `[not_found] not found: HTTP 404
Deal not found [/api/v2/deals/99999]`. The `[class]` tag conveys
class; HTTP status and endpoint go to slog at debug level for
operator triage.

## Custom-field cache

Pipedrive surfaces custom fields by 40-char hash key. Each per-resource
client (`Client.dealFields`, `Client.personFields`,
`Client.organizationFields`) holds a `*FieldCache` (the same type) that
fetches `/dealFields` / `/personFields` / `/organizationFields` once
and resolves hash→name. Caches are private; tools call the typed
adapter methods (`ResolveDealCustomFields`, etc.).

Refresh strategy as implemented today:

- **Lazy load**, guarded by `sync.Once`. First call that needs the
  cache triggers the fetch; concurrent callers block on the same
  fetch.
- **Eager warm-up at server startup** in a background goroutine
  fanning out across all three resource types — so the first
  user-facing `get_X` doesn't pay the metadata round-trip on the
  critical path.
- **Soft failure on cache fetch error** — `Resolve` returns the input
  map untouched, so the LLM sees raw hash keys but no data is lost.
- **`Reload()` method** on `FieldCache` to clear the cache; reserved
  for the future operator-facing `refresh_field_cache` tool.

In v2, custom fields nest under a `custom_fields` object on both
request and response bodies. The cache is responsible for translating
that nested map's keys (hashes) into human-readable names on output.

## Dry-run mechanism

Two layers, planned for the first write tools:

1. Per-call: every write tool accepts an optional `dry_run: bool`
   (default `false`).
2. Server-wide: `PIPEDRIVE_DRY_RUN=true` forces every write to dry-run
   regardless of per-call input. The env var always wins.

When dry-run is active, the tool short-circuits before the HTTP call
and returns a structured "would have done X" response. Validation
still runs (size limits, custom-field name resolution,
stage-belongs-to-pipeline checks) so the rehearsal catches the same
input errors a real call would.

No write tools have shipped yet; this section describes the contract
the first one will implement.

## Parallel tool registry

The MCP Go SDK does not expose a public `Server.Tools()` /
`ListTools()` to walk registered tools after `mcp.AddTool`. The
`--dump-schemas` flag needs that capability for the schema-diff CI
gate.

The workaround lives in `internal/tools/`:

- `addtool.go` exposes a generic `AddTool[In, Out](s, t, h)`. It
  infers the input/output JSON schemas using `google/jsonschema-go`
  (the same library the SDK uses internally), populates `t.InputSchema`
  / `t.OutputSchema` on the `*mcp.Tool` BEFORE calling `mcp.AddTool`,
  then registers the tool in both the SDK and a process-wide
  `Default` registry.
- `registry.go` holds the registry: a `Registry` struct with a slice
  of `*mcp.Tool` pointers plus `DumpJSON`, which emits a deterministic
  JSON document (tools sorted alphabetically; SDK version pinned in
  the header) for the schema-diff gate.

Why the schema-inference dance: `mcp.AddTool` does `tt := *t`
internally, then mutates the copy with inferred schemas. Without
populating the schemas first, our parallel registry stores the
original (bare) `*Tool` and `--dump-schemas` would emit empty
schemas. By inferring + assigning before `mcp.AddTool`, both copies
hold the same schema.

## Concurrency

The MCP server is request/response. The Pipedrive client uses
`net/http`'s default `http.Client` (with the configured timeout) and is
safe for concurrent use across goroutines. We do not pool or rate-limit
client-side.

The single non-handler goroutine is the cache-warm fan-out in
`server.New`, which exits within ~30 seconds of startup (or on
parent-context cancellation, whichever first).

## Logging

All logs go to stderr (stdout is reserved for MCP frames). `log/slog`,
JSON or text via `LOG_FORMAT`, level via `LOG_LEVEL`.

Today's emitted log lines (informational, no per-request lifecycle
yet):

- `credentials resolved` (info, at startup): records the token
  source (`keyring` or `env`), the workspace, and the domain source
  (`env` or `userconfig`).
- `auth probe ok` (info, at startup): records the workspace.
- `pipedrive response` (debug, in `client.go`): URL, HTTP status,
  duration. Suppressed at `info` level; turn on with `LOG_LEVEL=debug`.
- `pipedrive request failed` (warn): URL, attempt number, duration,
  error string — only on transport-level failures.

A per-request log line with correlation ID, tool name, and outcome
is on the roadmap but not implemented; tool handlers don't currently
emit log records of their own.

The MCP transport does not carry the LLM's prompt, so the server cannot
log the *intent* behind a tool call. See [`security.md`](security.md) for
the audit-trail caveat.

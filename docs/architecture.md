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
goroutines are those owned by the MCP SDK's transport reader; we do not
spawn workers, queues, or schedulers.

```
+----------------------+        stdio (JSON-RPC frames)        +-------------+
|   MCP client (LLM)   | <----------------------------------->  |  pipedrive- |
| Claude Desktop, etc. |                                        |     mcp     |
+----------------------+                                        +-------------+
                                                                       |
                                                                       v
                                                              +------------------+
                                                              | api/v2/<resource>|
                                                              | api/v1/notes (carveout)
                                                              +------------------+
```

## Module layout

```
pipedrive-mcp/
├── cmd/pipedrive-mcp/         # entrypoint: flag parsing, wiring
├── internal/
│   ├── config/                # env loading + validation
│   ├── version/               # build-time version string
│   ├── pipedrive/             # HTTP client (no MCP imports)
│   │   ├── client.go          # shared HTTP, auth, retry
│   │   ├── errors.go          # status -> typed error mapping
│   │   ├── types.go           # response types (custom_fields nested)
│   │   ├── users.go           # auth probe
│   │   └── (one file per resource type — added per phase)
│   ├── server/                # MCP SDK wiring
│   │   └── server.go
│   └── tools/                 # tool registrations + parallel registry
│       ├── registry.go        # parallel registry + DumpJSON
│       └── (one file per resource type — added per phase)
├── docs/                      # operator docs (this tree)
└── scripts/                   # CI helper scripts
```

The `internal/pipedrive` ↔ `internal/tools` split is deliberate: the HTTP
client is testable and reusable independently of the MCP transport, and
tool packages contain only the schema definitions, validation, and a thin
adapter that calls the client.

## Request lifecycle

1. `cmd/pipedrive-mcp/main.go` loads config, builds the Pipedrive client,
   runs the auth probe (`GET /api/v2/dealFields?limit=1`), constructs the MCP
   server, calls each tool package's `Register(...)`, and starts the
   stdio transport.
2. An incoming MCP `tools/call` is decoded by the SDK into the tool's
   typed input struct.
3. The tool's handler validates input, resolves any custom-field names
   against the cache, and calls the matching `internal/pipedrive`
   function.
4. The Pipedrive client adds `x-api-token`, fires the HTTP request, maps
   any non-2xx response through `errors.Map`, and decodes the response
   body into a typed struct (custom fields nested under `custom_fields`).
5. The tool wraps the result into its typed output struct, including a
   URL pointing at the Pipedrive web UI for the affected entity, and
   returns to the SDK.
6. The SDK marshals the output struct against its declared schema and
   writes the response frame.

## Error mapping

`internal/pipedrive/errors.go` maps HTTP status to a small set of typed
errors:

| Status | Error type | Behavior |
| --- | --- | --- |
| 200/201/204 | none | success path |
| 400 | `ErrValidation` | surface field-level message verbatim |
| 401 | `ErrUnauthorized` | startup: exit non-zero. Mid-process: return per-call. |
| 403 (permission) | `ErrForbiddenPermission` | "you do not have permission to access this {resource}" |
| 403 (business rule) | `ErrForbiddenBusinessRule` | surface Pipedrive's exact message |
| 404 | `ErrNotFound` | "not found" |
| 429 | `ErrRateLimited` | retry honoring `Retry-After`, jittered, max 3 attempts |
| 5xx | `ErrServerError` | retry with jittered exponential backoff, max 3 attempts |

403 disambiguation does **not** rely on `error_info` — that field is free
text on v2 and not a structured discriminator. Disambiguation is by the
`error` string and the request endpoint. The set of business-rule
signal substrings lives in `internal/pipedrive/errors.go` as
`businessRule403Signals`; the Phase 0 sandbox spike will tighten it.

## Custom field cache

Pipedrive surfaces custom fields by 40-char hash key. The client builds a
`hash ↔ name` cache on startup by fetching `/api/v2/dealFields`,
`/api/v2/personFields`, `/api/v2/organizationFields`. Tools accept either
the hash or the human name on input; outputs always present the human
name.

Refresh strategy:

- Background refresh every hour.
- Lazy refresh on miss (single retry).
- Manual refresh via the `refresh_field_cache` tool (Phase 1+).
- Hard fail on the *initial* startup fetch — degraded mode is not
  allowed.

In v2, custom fields nest under a `custom_fields` object on both request
and response bodies (root-level on v1). Every typed struct in
`internal/pipedrive/` reflects this. The future notes carve-out (Phase 2,
when it lands) will need to handle the v1 root-level layout for that
single resource.

## Dry-run mechanism

Two layers:

1. Per-call: every write tool accepts an optional `dry_run: bool`
   (default `false`).
2. Server-wide: `PIPEDRIVE_DRY_RUN=true` forces every write to dry-run
   regardless of per-call input. The env var always wins.

When dry-run is active, the tool short-circuits before the HTTP call and
returns:

```json
{
  "dry_run": true,
  "would_perform": "create_deal",
  "method": "POST",
  "endpoint": "/api/v2/deals",
  "body": { ... }
}
```

Validation still runs in dry-run mode (size limits, custom-field name
resolution, stage-belongs-to-pipeline checks) so the rehearsal catches
the same input errors a real call would.

## Parallel tool registry

The MCP Go SDK does not expose a public `Server.Tools()` or `ListTools()`
to walk registered tools after `mcp.AddTool`. The `--dump-schemas` flag
needs that capability for the schema-diff CI gate.

The workaround: every tool package's `Register(...)` function appends to a
package-local `[]ToolMeta` slice in `internal/tools/registry.go` *and*
calls `mcp.AddTool`. The flag iterates the slice and emits the schema
JSON deterministically (alphabetic sort of tools and properties; SDK
version recorded in the header). The rationale lives as a comment at the
top of `internal/tools/registry.go`.

## Concurrency

The MCP server is request/response. The Pipedrive client uses
`net/http`'s default `http.Client` (with the configured timeout) and is
safe for concurrent use across goroutines. We do not pool or rate-limit
client-side; spec §6 explains why.

## Logging

All logs go to stderr (stdout is reserved for MCP frames). `log/slog`,
JSON or text via `LOG_FORMAT`, level via `LOG_LEVEL`.

Per-request log lines include:

- correlation ID (UUID4)
- tool name
- upstream HTTP status
- duration (milliseconds)
- error class on failure

The MCP transport does not carry the LLM's prompt, so the server cannot
log the *intent* behind a tool call. See [`security.md`](security.md) for
the audit-trail caveat.

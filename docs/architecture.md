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
│   ├── app/                   # startup assembly: domain -> config -> token -> client -> server
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
│   │   ├── activities.go      # /activities
│   │   └── notes.go           # /api/v1/notes (carve-out — see CLAUDE.md hard rule #1)
│   ├── integration/           # live end-to-end suite (//go:build integration)
│   ├── server/                # MCP SDK wiring + cache warm-up fan-out
│   │   ├── server.go
│   │   └── testutil/          # in-memory MCP transport for tool-handler tests
│   └── tools/                 # tool registrations (one file per resource type)
│       ├── registry.go        # parallel registry + DumpJSON for --dump-schemas
│       ├── addtool.go         # generic AddTool[In, Out] schema-inferring wrapper
│       ├── errors.go          # errorResult + validateEnum + validatePositiveID
│       ├── pagination.go      # shared clampLimit + default/max page-size constants
│       ├── pipelines.go       # list_pipelines + list_stages
│       ├── deals.go           # get_deal + list_deals
│       ├── persons.go         # get_person + list_persons
│       ├── organizations.go   # get_organization + list_organizations
│       ├── activities.go      # get_activity + list_activities
│       ├── notes.go           # get_note + list_notes + manage_note
│       ├── cache.go           # refresh_field_cache (operator escape hatch)
│       └── search.go          # search (unified itemSearch wrapper)
├── docs/                      # operator docs (this tree)
└── scripts/                   # CI helper scripts
```

`internal/app` exists because that assembly used to live unexported
inside `package main`, so nothing else could perform it and every other
caller re-derived it. The integration suite re-derived it and dropped
the `PIPEDRIVE_DRY_RUN` floor on the way, which `docs/security.md`
promises an operator holds everywhere.

Two shapes keep that from recurring rather than merely discouraging it.
`server.New` takes the whole `config.Config` instead of a `domain
string` and a `tools.RegisterOptions`, so there is no zero-options
literal for the next entry point to copy and no way for the workspace a
tool labels its output with to disagree with the floor it honours;
`--dump-schemas` calls `server.NewForSchemaDump`, whose name says no
handler runs. And `Settings.Connect` returns a `Runtime` that carries
the client beside the settings it came from, so `Runtime.NewServer`
cannot be handed a client for a different workspace.

Nothing in the package logs, exits or touches the network: `main` exits
on a missing token and the integration suite skips, and both decide that
for themselves. The token is unexported on `Settings` and `LogValue`
redacts it, so neither `%+v` nor `slog.Any` can put it in a log line.

The `internal/pipedrive` ↔ `internal/tools` split is deliberate: the HTTP
client is testable and reusable independently of the MCP transport, and
tool packages contain only the schema definitions, validation, and a thin
adapter that calls the client. Per-type rendering decisions (e.g. deal
`title` vs everyone else's `name`) live in tools, not in the HTTP layer.

## Request lifecycle

1. `internal/app` resolves the workspace domain (env > userconfig >
   error), loads config for it, resolves the token, and pairs a
   Pipedrive client with the settings it was built from.
   `cmd/pipedrive-mcp/main.go` then runs the auth probe
   (`GET /api/v2/dealFields?limit=1`), asks that pair for the MCP
   server, and starts the stdio transport.
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

Pipedrive surfaces custom fields by 40-char hash key, and a dropdown
field's value by option id. Each per-resource client
(`Client.dealFields`, `Client.personFields`,
`Client.organizationFields`) holds a `*FieldCache` (the same type) that
fetches `/dealFields` / `/personFields` / `/organizationFields` once
and resolves both: hash→name on the key, option id→label on the value.
Caches are private; tools call the typed adapter methods
(`ResolveDealCustomFields`, etc.).

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
- **`Reload()` method** on `FieldCache` to clear the cache and let the
  next access re-fetch. Wired up to the `refresh_field_cache` operator
  tool in `internal/tools/cache.go`, which fans out across the three
  resource caches in parallel.

In v2, custom fields nest under a `custom_fields` object on both
request and response bodies. The cache is responsible for translating
that nested map into the words the workspace uses — keys (hashes) into
human-readable names, and option ids into labels — on output.

### Option labels

An `enum` stores one option id and a `set` stores an array of them, so
which one a stored value is gets decided by the value's own shape rather
than by the field's declared `field_type`. That keeps this independent
of Pipedrive's type taxonomy: a field type we have never seen resolves
correctly as long as its value is either an id or a list of them.

`FieldOption.ID` is `any` rather than `int64`. Custom fields carry
numeric option ids, but built-in fields like `status` carry string ones
(`"open"`, `"won"`), and a write has to keep those apart — a numeric
option sent as `"107"` is a different request. What `any` does **not**
do is preserve the literal wire form: every JSON number decodes to a
float64 first, so the type distinction survives and the spelling does
not.

Comparison therefore happens on a rendered form (`OptionKey`) rather
than on the decoded value. Keying a `map[any]string` on the decoded id
would be cheaper and would be less tolerant: the stored value and the
option that names it reach us from two different calls, and the one
thing the field metadata proves is that Pipedrive does not spell an id
one way. Rendering both sides costs an allocation per resolved value and
buys a comparison that cannot fail on a type mismatch.

An option id with no matching option passes through as itself, matching
what an unrecognised field key does. Both are the same bet: a workspace
can add a field or an option at any moment, and a caller seeing a raw id
is recoverable where a caller seeing nothing is not. `refresh_field_cache`
re-reads the option tables along with the names.

### Writing a custom field

`FieldCache.Encode` is `Resolve` run backwards: the names the workspace
shows become hash keys, and a dropdown's label becomes its option id.
That is what makes a custom field writable, and it is the only place the
translation happens — the tools layer hands it what the caller typed and
gets back a body.

It refuses rather than guesses, and each refusal names what to do
instead: an unknown field points at `refresh_field_cache`, a bad choice
lists the choices, a name two custom fields share names their keys so
the caller can pass one, a field Pipedrive marks `is_writable: false` is
named as read-only, and a null is refused because v2 cannot empty a
field. A refusal names **every** field that was wrong, for the reason
the overwrite guard names every field it protects. And **one bad field
refuses the whole map**: encoding the rest would report a success over a
record that never received the field the caller cared about, and there
is no undo to fall back on.

**A built-in is refused whether it is named by label or by key.** The
field metadata carries Pipedrive's own fields alongside the workspace's,
so `byKey` holds `title` and `status` as well as the 40-char hashes.
Checking only the name refused `"Title"` and admitted `"title"` — and
the guard then measured the wrong thing, because a built-in's value does
not live in the record's `custom_fields` map, so it read as empty and no
`overwrite` refusal could fire over it. Pipedrive rejects such a body
outright (`Validation failed: custom_fields: Unknown key 'title'`), so
the damage was bounded upstream rather than here, which is not where
this server's guard is meant to hold.

Two asymmetries with the read direction are deliberate:

- **A failed cache load is fatal here.** `Resolve` falls through and
  costs the caller a hash key in place of a name; `Encode` would be
  inventing a field.
- **An id is accepted where a label is expected.** `Resolve` hands back a
  bare id whenever it cannot name an option, so refusing to take one
  again would make that output unusable.

The guard reaches custom fields through specs derived per call
(`withCustomFields`) rather than a static table, because which custom
fields exist is the workspace's business. A static list would be a
second place to add a field — the thing "one list of field names per
resource" exists to prevent.

Reaching a custom field needs two things done: the field table extended
so the diff and the overwrite guard see it, and the value overlaid onto
the predicted record so the diff has something to see. Both hang off one
pair of adjacent fields on `guardedWrite` — `Custom` and `CustomOf` — so
a resource does both or neither. They were two edits in two functions
once, and that is a silent failure: the dry run reports nothing, the
write goes out, and the field changes unreported. A multi-select is compared as a set, since
two orderings of the same choices are the same value and Pipedrive does
not promise to echo one back in the order it was sent.

## Guarded writes

Pipedrive has no undo, so a refusal is the only guard there is. Every
reshaped write reads its target before writing and refuses what the
write would destroy that the caller cannot see. The contract follows
`google-sheets`' `write_values`, and each refusal names two things: what
it is protecting, and the argument that permits the write.

- `dry_run` — a per-call input on every write. Reports what the write
  would find and change, sends nothing. Validation still runs, so a
  rehearsal catches the same input errors a real call would.
- `overwrite` — required before an update may replace a populated field.
  Filling an empty field destroys nothing and needs no flag.
- `expect_version` — carries the `update_time` from the read that
  informed the write, and refuses if the record moved since. Best effort:
  Pipedrive has no compare-and-set, so this catches a concurrent edit,
  not a determined race.

### The upstream may change more than you asked for

A dry run predicts what **this server would send**. Pipedrive may derive
more from it, and the rehearsal cannot know that without reimplementing
its rules.

Established live on 2026-09-17: changing a person's `first_name` comes
back reporting `["name", "first_name"]`, because Pipedrive recomputes
`name` from the parts. The dry run for the same call predicts
`["first_name"]` alone.

That direction is the safe one and it is worth being clear about why.
The rehearsal **under**-reports rather than over-promising, so a caller
is never told less will change than does — they are told less *will*
change and then shown the full set afterwards, since the post-write
report diffs against the record Pipedrive echoed rather than against the
prediction. The guard is unaffected: it refuses over the fields the
request touches, and a request that would change nothing is skipped
before any derivation could occur, which protects a manually-set `name`
rather than clobbering it.

A refusal comes back as a tool-execution error (`isError: true`) tagged
`[refused]`, which is its own class precisely so a model can tell it
apart from a validation failure and from an upstream error. Retrying is
useless for one and correct for another.

After a write the tool returns the record the upstream echoed back and a
`changed` list naming every field that actually moved — diffed against
the pre-write read, not against the request, because Pipedrive
normalises some of what it stores.

### Clearing a field

**Not through this server, and the tool descriptions say so.** The
reason is a v2 gap rather than a Pipedrive-wide impossibility, which is
worth keeping straight — established live on 2026-09-16 against a real
deal:

| Call | Body | Result |
| --- | --- | --- |
| `PATCH /api/v2/deals/{id}` | `{"expected_close_date": null}` | **rejected** — `The value is not a valid 'string'` |
| `PATCH /api/v2/deals/{id}` | `{"expected_close_date": ""}` | accepted, `success: true`, stores `0000-00-00` (MySQL's zero date) |
| `PUT /api/v1/deals/{id}` | `{"expected_close_date": null}` | **accepted, and actually clears it** — the field reads back absent |

So v2 has no spelling that empties a date field: a null is refused and
an empty string is stored as a value. A deal that never had a date omits
the field entirely, so the zero date is distinguishable from absent on a
read, and a tool that called `""` a clear would be lying to the model.

v1 does it correctly. This server does not use that, and should not
without a deliberate decision: it would be a third v1 carve-out on an
API that **sunsets 2026-07-31**, built for a capability nobody has asked
for. Every `manage_*` input says "omit to leave it as it is" instead,
and the `Update*Request` types stay `*T` with `omitempty` — nil omits,
non-nil sends, no third state, because there is no third behaviour worth
reaching for.

If v2 later grows a working clear, change the `Update*Request` types and
the input descriptions together, never one without the other.

`PIPEDRIVE_DRY_RUN` sits under all of it as a server-wide floor: every
write honours it, and a per-call `dry_run` can only turn a rehearsal on.

`manage_note` implements the full contract today — per-call `dry_run`,
`overwrite` and `expect_version`. The `create_*` tools predate it: they
honour the floor but take no per-call `dry_run` of their own, and they
have nothing to guard anyway, since a create clobbers nothing. They gain
the per-call input when they move to `manage_*`.

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

## Phase plan

Versioning is strict semver. The MCP tool surface is the public contract.

### What shipped

| Tag | Phase | What it was |
| --- | --- | --- |
| `v0.1.0` | Phase 1 | The read surface, the notes v1 carve-out, `create_note` / `delete_note`, `refresh_field_cache`. Phase 0's scaffolding folded in rather than cut as `v0.0.1`. |
| `v0.2.0` – `v0.3.2` | — | Release and supply-chain engineering, no tool-surface change: the gate scripts rewritten as one Go command, every action pinned by commit SHA, cosign signing, CycloneDX SBOMs, build provenance, reproducible builds, `--version`. |
| `v0.4.0` | Phases 2, 3 and 3.5 | The write and workflow surface in one release — five `manage_*` tools with guarded writes, plus `whoami` and MCP resource templates — and the whole surface aligned to the Google Workspace MCP conventions. It also absorbed work that was planned for 0.5.0 and landed before the tag: the live integration suite, `internal/app`, a Claude Desktop bundle with an MCP registry entry, and the supply-chain fixes the release review turned up. |

The middle tags went to release engineering rather than to phases, so the
phase numbers and the version numbers stopped tracking each other. The
table above says what actually happened rather than what was planned.

### What is left

| Tag | Phase | What it needs |
| --- | --- | --- |
| `v0.5.0` | Phase 4 prep | Custom-field **writes** — they are readable everywhere and writable nowhere — and the items below that a second workspace or a decision unblocks. Phase 3.5's own work shipped inside `v0.4.0`. |
| `v0.9.0` → `v1.0.0-rc.N` | Phase 4 | An eval suite (a release gate from Phase 4 onwards, and it does not exist yet), polish, and validation against a second workspace. |
| `v1.0.0` | Phase 5 | A stable surface and a supported-version table. |

### v0.5.0 in detail

0.5.0 closes the gaps 0.4.0 knowingly shipped with. Ordered by what would
hurt most to leave undone.

**1. Custom-field writes.** They are readable everywhere and writable
nowhere, which is the first wall a user hits — a CRM whose custom fields
are read-only is a CRM you still have to open a browser for. `FieldCache`
has `Resolve` (hash → workspace name) and no inverse, so a write cannot
accept `"Renewal owner"` and turn it into the 40-char key Pipedrive
stores. Touches `internal/pipedrive/fieldcache.go`, every `manage_*`
input, and — per the invariant in `fieldtable_test.go` — the field tables,
since a field a write can set must be tabled or it is written unguarded
and unreported.

**2. An integration suite.** *Shipped in `v0.4.0`*, not here —
`internal/integration/`, behind `//go:build integration`, run with
`make integration` and `make integration-writes`. It was the
highest-value item on this list and it overtook the rest, which is why
it is recorded as done rather than pending: **every serious defect found
during the 0.4.0 work was found by driving the live API, and not one of
them was visible to the unit tests**, which assert against fakes. Three
guard bugs, a wire-format error, and Pipedrive's derived-`name`
behaviour all came from real calls. `docs/development.md` has the safety
contract the write probes keep.

It is left in this list rather than deleted because the reasoning is the
argument for the suite existing at all, and a reader asking "why is
there a live suite" should find it here.

**3. A second workspace.** Everything so far ran against one. Custom-field
configurations, pipeline shapes and permission levels vary, and the 403
spike below needs a second account regardless.

**4. The two remaining Phase 0 spikes**, both in `CONTRIBUTING.md`:
403 disambiguation (needs a permission-denied and a business-rule 403 from
a real account, to tighten `businessRule403Signals`), and the hand-rolled
HTTP versus OpenAPI-generator decision, which needs recording either way.

**5. A Claude Desktop smoke.** Still open. 0.4.0 was driven through stdio and Claude
Code. The runbook accepts either, so this is a gap in coverage rather than
in process.

**Deferred review findings.** Each was raised by a `/simplify` pass during
0.4.0 and judged not worth blocking that release. All were taken during
0.5.0; none had been a defect.

- ~~`dealRequestFor` returns `*mcp.CallToolResult`~~ — it returns `error`
  now, like every other validator in the package, and the caller wraps.
- ~~The action taxonomy is written four times per resource~~ — `manage_deal`
  and `manage_activity` each have one `map[string]xAction` table saying
  whether an action creates and whether it authorises its own overwrite.
  The guard posture used to read `in.Action != "update"`, which states the
  rule by exclusion: a new action was a transition unless it happened to
  be called update, whatever it did. Now an action says what it is where
  it is declared to exist. `manage_person`, `manage_organization` and
  `manage_note` keep a plain set — they carry no per-action facts, and a
  struct with no fields in it would be worse.
- ~~95 hand-rolled `Connect` + `CallTool` + `DecodeStructured` blocks~~ —
  `testutil.CallTool` and `testutil.CallToolInto` are the helper, in the
  package the finding named, and 57 of the 91 call sites now use them.
  The remainder are not the same shape: they drive several calls over one
  harness, or connect a server and then assert on something other than a
  tool call. Converting those would mean bending the helper to fit tests
  it was not the right answer for.
- ~~`projectString` is an identity function~~ — gone. A plain string field
  returns the field.
- ~~`populatedFields` builds a `map[string]bool`~~ — a linear scan over the
  handful of names one write touches.

**6. The bundle manifest is validated against its schema.** *Done.* The
gate checks the *declaration* — that `$schema` agrees with the declared
`manifest_version`, that the ref is a complete release tag, and that the
version meets a floor — and it checks referential integrity against the
files the packer stages. It now also parses the document against the
schema it cites, which is what catches a wrong type, a missing required
field, or a misspelled top-level key: the schema closes its root with
`additionalProperties: false`, so that last class was invisible to every
other check we had.

The schema is vendored with its SHA256 recorded beside the pinned URL,
which holds both halves at once. `google-chat-mcp` has the mirror gap —
it validates against a vendored copy and never checks that the URL
agrees with the declaration — and the recorded hash is what stops this
repository acquiring the same gap from the other side. The stamped
manifest is checked too, after the version is substituted rather than
before.
**Explicitly not in 0.5.0.** Products, leads, files, projects and goals
are new resources and belong to their own milestone. Field clearing is
blocked on Pipedrive v2, not on us. The v1 sunset migration is its own
piece of work and gates 1.0 rather than 0.5.

**`v1.0.0` is gated on more than a checklist.** It means breaking changes
require a MAJOR bump, and two parts of this surface — the notes tools and
`whoami` — sit on Pipedrive **v1, which sunsets 2026-07-31**. If that
date passes without a v2 `/notes`, those tools break or change shape, and
a 1.0.0 cut before then would be a promise the API will not let us keep.
The v1 sunset needs a resolution first.

Each phase boundary requires explicit maintainer approval before the next
phase starts.

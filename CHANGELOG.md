# Changelog

All notable changes to this project are documented here. The format is
based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project adheres to [Semantic Versioning 2.0](https://semver.org/spec/v2.0.0.html).

The public versioning contract is the MCP tool surface — tool names, input
schemas, output schemas, and documented behavior. Internal package layout,
error message wording, and log line formats are not part of the contract.

Pre-1.0 minor releases may break the tool surface. From 1.0.0 onwards,
breaking changes require a MAJOR bump.

**One carve-out, and it is deliberate.** `get_note`, `list_notes`,
`manage_note` and `whoami` run on Pipedrive API v1, whose 2026-07-31
sunset has passed, because v2 exposes no `/notes` and no `/users` — so
there is no equivalent to move them to. Those four sit **outside** the
compatibility promise: if v1 stops answering they are removed in a
MINOR release. The alternative is letting a third party decide when
this project cuts a MAJOR. Every other tool is covered by the promise
in full.

## [Unreleased]

### Fixed

- **`tools.SDKVersion` said `v1.6.0` while `go.mod` pinned `v1.8.0`.**
  That constant is written into the schema dump header, where its whole
  job is to let a reviewer classify a diff as "the SDK moved" rather
  than "the surface changed" — so a stale value says the opposite of
  what it is for. It drifted the same way once before (`v1.5.0` against
  a `v1.6.0` pin) and both times the fix was a comment saying "update in
  lockstep". `TestSDKVersionMatchesGoMod` is the check.
  `runtime/debug.ReadBuildInfo` would remove the constant outright but
  does not list the dependency in a test binary, so there would be
  nothing to test it with.

- **The docs described a project that does not exist**, found by
  auditing what a 1.0 needs:
  - `CONTRIBUTING.md`, `docs/development.md` and `docs/release.md` all
    described a **trivy image-CVE gate**. There is no Dockerfile and no
    such job; it is a leftover from the container phase. A contributor
    reading `CONTRIBUTING.md` believed an image-CVE gate protected them.
    (`security/known-cves.yaml`, its ignore list, is now unreferenced —
    left in place rather than deleted.)
  - `docs/development.md` pinned Go at `1.26.2` against a `1.26.6`
    toolchain, told contributors to install golangci-lint, govulncheck
    and go-licenses at `@latest` while the Makefile pins all three and
    `make verify-tool-versions` fails on a mismatch, named a
    `sdkVersion` constant that is exported as `SDKVersion`, listed two
    coverage-gated packages where CI gates four, and quoted a
    no-domain error message the binary never emits — unreachable to
    check, because the domain also resolves from the userconfig file.
  - `docs/architecture.md` described `create_*` tools that "gain the
    per-call input when they move to `manage_*`". They moved two
    releases ago and no `create_*` tool remains.
  - `README.md` carried the resource-template block **twice**,
    near-verbatim.

### Changed

- **`README.md` now writes down the whole negative space.** "A stable
  surface" is half a promise without the other half, so the list is
  explicit: webhooks, mail, subscriptions, saved filters, currencies,
  deal-to-lead conversion, followers, deal participants and custom-field
  *definitions* are not modelled and are not planned for 1.x; merging
  records is refused by the guard contract rather than missing from the
  API; `search` finds products, files and leads that no tool can then
  act on; and four tools sit on the v1 carve-out outside the 1.0
  promise.

### Added

- **An eval suite** — `scripts/evals`, `make evals` — which was a
  declared release gate from Phase 4 onwards and did not exist.

  It drives a model through this server's tools alone and scores every
  task twice: the **end state**, read back through this server because a
  model's account of what it did is the least reliable thing in the run,
  and the **trace**, because a task can be completed by a model that
  guessed an id and was lucky. Twelve tasks, each aimed at a trap the
  tool descriptions already warn about — search as the gateway rather
  than a list sweep, the overwrite refusal not being routed around,
  `dry_run` actually rehearsing, archiving not being closing, and no
  invented `lost_reason`.

  The fixture is invented, built through the server's own tools, and
  deleted afterwards; those deletes are soft, so Pipedrive purges the
  remainder after 30 days.

  **The harness counts the workspace either side of the run.** An eval
  drives a model, not a script: a task can be answered by creating
  something nobody asked for, and against a real workspace that is a row
  in somebody's CRM. The census cannot prevent that and cannot remove
  what it did not create — it fails the run instead of letting it pass
  unnoticed.

  The task table, the stream parser and the census carry **no build
  tag**, so `go test ./scripts/evals` reaches them without credentials.
  That is where the unsubstituted-placeholder guard belongs — a sibling
  project's first full eval run passed two tasks while sending the agent
  a literal `{folder}` — and where the parser belongs too: one that
  mis-attributes a tool result scores a refusal as a success, and
  nothing about that is visible in a passing run. Writing those tests
  found two defects in this change: a task whose check returned true
  unconditionally, and a parser test whose own fixture was shaped so the
  line scanner skipped it.


- **`delete` on `manage_deal`, `manage_person`, `manage_organization`
  and `manage_activity`**, on an explicit maintainer decision taken
  2026-09-19 — which is what `docs/security.md` had been asking for
  since it listed `delete_deal` as off the roadmap.

  They take `dry_run` and `expect_version` and **no permitting flag
  beyond them**. Pipedrive's v2 delete is soft and time-boxed — its
  documentation says "Marks a <resource> as deleted. After 30 days, the
  <resource> will be permanently deleted" — which is the reversible case
  `trash_file` handles the same way, and the read each action performs
  puts the record on screen before it goes. Nothing here restores one;
  Pipedrive's own UI can, inside the window.

  **What becomes of the records hanging off a deleted one is not
  documented by Pipedrive and has not been verified here**, because the
  only available workspace is a real one and the probe would have meant
  creating and deleting records in it. So the descriptions tell the
  caller to read the children first rather than the code guarding
  against a behaviour nobody has established. If it is ever established,
  the guard to add is `force` on the three parents; an activity is a
  leaf — notes anchor to deals, persons, organizations, leads and
  projects, never to an activity — and would still not need one.

  A deal is the one resource whose deleted state is readable, through
  `status: deleted`, so deleting one twice reports instead of firing
  again. The other three have no deleted marker and cannot grow one:
  the v2 response fixture `spec_test.go` checks against does not declare
  `is_deleted` for them, so the tag would fail that gate.

- **A `descriptions` gate**, in `make check`. It holds the tool
  descriptions against the house-style rules that can be checked
  mechanically — today, at most one `IMPORTANT:` per description. It
  reads the built binary's schema dump rather than the Go source,
  because the first version of this was a unit test walking the package
  registry, which is empty unless something registered into it: it
  passed while asserting on nothing.

### Fixed

- **`manage_deal` offered six actions and dispatched on eight.** The
  `action` field's description read "create, update, move_stage,
  mark_won, mark_lost or reopen" while `dealActions` also held `archive`
  and `unarchive` — in the same schema whose tool description names "the
  six transitions ... archive and unarchive". A model reads the field's
  description as the allowed-value list, so it never tried either.

  This is the third place the same drift has surfaced: v0.5.0 fixed it
  in the MCP instructions, the previous release fixed it in `README.md`,
  and this is the tool schema itself — the one a model actually reads.
  `TestActionFieldDescribesEveryAction` now holds every `manage_` tool's
  action map against its own field description.

### Added

- **A `[gone]` error class, and a canary on the v1 carve-out.**
  Pipedrive's v1 sunset date, 2026-07-31, has passed. Four tools run on
  v1 — `get_note`, `list_notes`, `manage_note` and `whoami` — because v2
  exposes no `/notes` and no `/users`, so there is nothing to migrate
  to. The risk cannot be engineered away; it can only be classified and
  watched.

  A 410 previously fell through `status >= 400` to `ErrValidation` and
  reached the model as `[validation]` — "your input was wrong" — sending
  it to fix the one thing that was fine, and to retry forever. A retired
  endpoint is neither a caller error nor retryable. On a v1 request the
  message also names the sunset and says there is no v2 equivalent,
  because `[gone]` alone reads as a deleted record.

  `TestV1CarveOutStillAnswers` in the live suite asserts the
  **aggregate**: a retirement fails every v1 tool at once, whatever
  status it arrives as, and keying on 410 alone would have been a guess
  about another company's deprecation hygiene — a route can equally be
  removed (404), gated (403), or answered by an edge with an HTML page.
  Green against a live workspace on 2026-09-18, so v1 is out of support
  rather than switched off. It runs at each release boundary, not
  continuously; `docs/architecture.md` says so rather than implying a
  watchdog that does not exist.

- **Two gates over lists that had already drifted.**
  `TestEverySentinelHasItsOwnClass` holds every sentinel against
  `errorClass`, which ends in a default returning `"error"`: a sentinel
  added without a case did not fail anything before, it just arrived at
  the model wearing the same label as every other unmapped failure.
  `TestOperationsDocListsEveryErrorClass` holds `docs/operations.md`'s
  `[class]` enumeration against the same list — it was missing
  `[refused]` since guarded writes shipped, and would have been missing
  `[gone]` on arrival. Both walk `allSentinels`, the list the package
  already keeps, rather than adding a third copy of the names.

### Changed

- **A non-JSON error body is reported as one.** `classifyResponse`
  discarded the unmarshal error, so an HTML error page from an edge —
  the likeliest shape of a retirement — produced a message-less error
  indistinguishable from an empty one. It now reads
  `non-JSON response (N bytes)`. The length is reported and the body is
  not, so the no-PII rule on `APIError` still holds.

- **`classify` takes the API version instead of guessing it.** It was
  recovering v1-ness by prefix-matching the request path, re-deriving a
  fact the package holds as a typed `apiVersion` three frames up — and
  `pathOf` falls back to the whole URL on a parse failure, so the guess
  returned false and dropped the explanation on exactly the defensive
  branch it was written for. The version is now threaded from `exec`.

- **The docs stopped saying the sunset is in the future.** Every
  occurrence outside this changelog was written in the future tense —
  `README.md`, `docs/architecture.md`, `CONTRIBUTING.md`, `CLAUDE.md`
  and a comment in `internal/pipedrive/deals.go` — including the
  paragraph gating 1.0 on it, which said "if that date passes" for seven
  weeks after it did. The evidence behind the "no v2 equivalent" claim
  now carries the date it was measured.

- **The versioning contract names one carve-out.** `get_note`,
  `list_notes`, `manage_note` and `whoami` sit outside the 1.0
  compatibility promise: if Pipedrive v1 stops answering they are
  removed in a MINOR, rather than a third party's retirement schedule
  forcing this project into a MAJOR. Decided 2026-09-19; the contract
  at the top of this file and `docs/architecture.md` both say so.

- **The server speaks the current MCP protocol revision, `2026-07-28`.**
  `go-sdk` v1.6.0 → v1.8.0; v1.7.0 is the release that added it. The
  revision replaces the `initialize` handshake with per-request `_meta`
  carrying the protocol version and the client's capabilities, and makes
  `server/discover` a mandatory RPC. Until now this server answered only
  the handshake-based revisions, which the spec's own compatibility
  matrix puts on the legacy side of an era boundary: a modern-only
  client **fails** against a legacy-only server.

  The SDK negotiates, so the bump is the whole migration — no handler
  changed. Verified against the built binary: `server/discover` now
  answers with `supportedVersions` `2026-07-28, 2025-11-25, 2025-06-18,
  2025-03-26, 2024-11-05`, a `tools/list` sent with no handshake at all
  is answered at the new revision, and the `2025-06-18` handshake still
  works. Nothing is dropped; five revisions are served.

  `make smoke` now asserts this rather than trusting it. It sends
  `server/discover` as a fourth frame and fails unless the current
  revision is in the list — watched failing against the v0.5.0 binary,
  which refuses the method. The assertion is on the returned list
  because the SDK answers `server/discover` with method-not-found unless
  the request carries the new `_meta`, so a frame sent the old way gets
  a refusal that looks nothing like a missing feature.

- **Six tools report annotation hints they previously left unset.**
  `manage_deal`, `manage_person`, `manage_organization`,
  `manage_activity` and `manage_note` now state
  `idempotentHint: false` and `readOnlyHint: false` alongside the
  `destructiveHint: true` they already carried, and
  `refresh_field_cache` states `readOnlyHint: false`. This comes from
  the SDK, not from a change here. It is additive and not a permissive
  flip — `false` is what the spec already assumes for an absent hint —
  but it is a tool-surface diff, so it is named here rather than left
  for the schema gate to surprise somebody with. Tool names, input
  schemas and the tool count (20) are unchanged.

### Fixed

- **`SECURITY.md` said the shipped release was unsupported.** The
  supported-versions table was written forward — `1.x (current)`,
  `0.9.x`, `< 0.9 → no` — and stood that way through five releases, so
  the published security policy told every user of every shipped
  release that it received no patches. It now describes what is
  shipped, and updating it is a step in the release runbook rather than
  something to remember.

- **`README.md` named five of the seven self-authorising transitions**,
  omitting `archive` and `unarchive` and so telling a reader those two
  need an `overwrite` they do not take. This is the defect v0.5.0 added
  `TestInstructionsNameEverySelfAuthorisingAction` to catch, one
  document over: that gate reads the MCP instructions string and
  nothing read the README. The check is now one helper asserting the
  same sentence against the same `tools.SelfAuthorisingActions()` in
  both documents.

## [0.5.0] - 2026-09-18

### Added

- **Two gates against documentation drifting from code**, both watched
  failing before being believed. `TestInstructionsNameEverySelfAuthorisingAction`
  holds the MCP instructions' transition sentence against the actions
  that actually grant their own overwrite — the first version of that
  test searched the whole instructions string and passed while the bug
  was present, because "archive" also appears in the paragraph warning
  that archiving is not closing. And `gates changelog-links`, now in
  `make check`, asserts every `## [x.y.z]` heading has its link
  definition and that `[Unreleased]` compares from the newest release;
  v0.4.0 shipped with neither and it took a release to notice.

- **The upstream types are held against Pipedrive's own OpenAPI
  description.** `internal/pipedrive/spec_test.go` walks the `json` tags
  in this package against a fixture derived from the published v2
  document and fails on a tag Pipedrive does not return, or a Go type
  that cannot hold what the spec declares. All three defects above were
  found by writing it.

  It is a test, not a generator — nothing generated is vendored or
  compiled in. The spec has zero `$ref`, so a generator emits a separate
  anonymous struct per endpoint rather than one `Deal`, and models-only
  emits no response types at all. `docs/architecture.md`, "Hand-rolled
  HTTP", has the measurements; `CONTRIBUTING.md` records the spike this
  closes.

  Two exemptions are recorded with reasons, both verified live:
  `is_writable`, which the `*Fields` endpoints return and the write guard
  depends on while the spec has never declared it, and `people_count`,
  which is an `include_fields` value rather than part of the default
  response.

- **Custom fields are writable.** `manage_deal`, `manage_person` and
  `manage_organization` take a `custom_fields` object keyed by the names
  the matching `get_` tool reports, with a dropdown given its label:
  `{"Renewal owner": "Acme Partners", "Segment": "Enterprise"}`. It is
  the shape a read returns, so the model writes back what it just read.
  Until now a CRM's own fields were readable everywhere and writable
  nowhere, which is the first wall a user hits.

  `FieldCache.Encode` is `Resolve` run backwards — names to hash keys,
  labels to option ids — and it refuses rather than guesses. An unknown
  name, a label that is not one of the choices, a name two custom fields
  share, a built-in named by either its label or its key, a field
  Pipedrive reports as read-only, and an attempt to null a field out are
  each refused with what the caller should do instead: the choices are
  listed, the colliding field keys are named, and a stale cache is
  answered with `refresh_field_cache`. A refusal names **every** field
  that was wrong, not the first one a map range happened to yield, and
  **one bad field refuses the whole map** — encoding the rest would
  report success over a record that never got the field the caller cared
  about. Naming one field twice in a single write — once by name and
  once by its key — is refused too, because ranging a map would otherwise
  pick the winning value at random.

  A workspace whose field definitions cannot be read is reported with the
  upstream error's own class rather than as a validation failure. A 401
  on `/dealFields` is not the caller's field name being wrong, and
  telling the model it was would send it off to fix the one thing that
  was fine.

  The guard covers them exactly as it covers a typed field. The specs
  are derived per call from the fields a write names, because which
  custom fields exist is the workspace's business and a static table
  would be a second place to add one. Extending the table and carrying
  the fields into the predicted record are one thing, not two: they are
  a pair of adjacent fields on `guardedWrite`, which does both or
  neither. Done as two edits in two functions, a resource that made one
  of them dry-ran as "nothing changes" and then changed the field
  without reporting it. So a custom field appears in
  `changed` under its workspace name, the `overwrite` refusal names it,
  and filling an empty one still needs no permission. A multi-select is
  compared as a set: the same choices in a different order are not a
  change, and Pipedrive does not promise to echo one back in the order
  it was sent.

  Two deliberate limits. A deal transition (`mark_won`, `move_stage`,
  ...) ignores `custom_fields` and does not even validate it, the same
  way it already drops a title passed alongside it — a refusal over a
  field the transition was never going to write is one the caller cannot
  act on. And clearing is still unsupported: v2 cannot empty a field, so
  every custom field reads "omit to leave it as it is" like every other
  optional input.

- **The MCP registry entry is validated against the schema it cites**,
  before it is printed and therefore before anything publishes it. The
  entry named a `$schema` that nothing parsed it against — the same gap
  as the bundle manifest's below, and the one that matters more: a
  registry entry cannot be withdrawn once published, so failing in
  somebody else's tool is not a recoverable outcome. The Go struct
  guarantees the shape we thought of; the schema is what catches a field
  the registry requires and the type never had. Vendored and hash-pinned
  the same way, through the same `validateAgainstVendored`.

- **The bundle manifest is validated against the schema it cites.** The
  gate checked the *declaration* — that `$schema` agrees with
  `manifest_version`, that the ref is a release tag, that the version
  meets a floor — and it checked referential integrity against the files
  the packer stages. Nothing parsed the document against the schema, so
  a wrong type, a missing required field or a misspelled top-level key
  passed here and failed in somebody else's tool. The schema closes its
  root with `additionalProperties: false`, which makes that last class
  invisible to every check we had.

  The schema is vendored (`packaging/mcpb/mcpb-manifest-v0.3.schema.json`)
  rather than fetched: a gate that needs the network fails on somebody
  else's bad day, and `make check` runs offline. Its SHA256 is recorded
  beside the pinned URL, because a vendored copy is only worth its
  provenance — the sibling `google-chat-mcp` validates against a vendored
  copy and never checks that the cited URL agrees with it, which is the
  mirror of the gap this closes. Both halves are now held: the URL by
  `schemaFor`, the bytes by hash.

  The **stamped** manifest is validated too, after the version is
  substituted rather than before, since what ships carries a version that
  came from a tag rather than from the file somebody reviewed.
  `google/jsonschema-go` was already a dependency.

### Changed

- **The `/simplify` findings 0.4.0 deferred are taken.** `dealRequestFor`
  returns an `error` like every other validator in the package rather
  than a `*mcp.CallToolResult`. `manage_deal` and `manage_activity` each
  have one action table saying whether an action creates and whether it
  authorises its own overwrite — the guard used to read
  `in.Action != "update"`, which states the rule by exclusion, so a new
  action was a transition unless it happened to be named update.
  `testutil.CallTool` and `CallToolInto` replaced 57 of the 91
  hand-rolled connect-call-decode blocks in the tests. `projectString`,
  an identity function wrapped at 19 table entries, is gone, and
  `populatedFields` scans the handful of names a write touches instead
  of building a set to search them.

- **A dropdown custom field now reads as its label, not its option id.**
  `get_deal`, `get_person`, `get_organization`, the three `list_` tools
  and the resource templates all resolved a custom field's *key* to the
  workspace's name for it and then handed the *value* over untouched —
  so an enum came back as `107` and a set as `[68, 69]`. A number is not
  something the caller can repeat back to the user, cite in an answer or
  reason about, and it was the half of "resolve to semantic names in
  output" that had never been done. The field metadata already carried
  the option table; nothing read it.

  It lands in `FieldCache.Resolve`, the pass that already renames the
  key, so one fetch, one reload and one soft-failure policy cover both
  halves and `refresh_field_cache` picks up a newly added option for
  free. An option id the cache has not heard of passes through as
  itself, the same soft failure an unrecognised field key takes: a
  workspace can add an option at any moment, and a caller seeing a
  number is better than a caller seeing nothing. `docs/architecture.md`,
  "Option labels", carries the design and the two decisions behind it.

  No input or output changes shape — `custom_fields` was already an open
  object — but the tool and field *descriptions* do, and descriptions are
  part of the dumped schema, so the schema-diff gate sees this change and
  the commit carries a `SCHEMA-CHANGE:` footer.

### Fixed

- **The docs promised an audit trail that has never existed, in three
  places.** `docs/security.md` said dry-run invocations are logged at
  `info` for an operator to audit, and its "LLM audit trail" section
  documented an incident-response procedure built on correlating a
  `request_id` in the server's stderr — a field that appears nowhere in
  the source. `docs/configuration.md` carried the same dry-run promise,
  and `docs/operations.md` asked bug reporters to supply that
  `request_id`. `internal/tools` holds no logger at all, so a successful
  write produces no log line and a rehearsal leaves no trace. An
  operator following that run book after a surprising write would have
  found nothing, and an absent log reads as a quiet period rather than a
  missing feature. All three now say so and point at the two trails that
  do exist: Pipedrive's own change record, and the MCP client's
  transcript.

- **The server's own MCP instructions told every client that custom
  fields were "not yet writable".** That is the first thing a connecting
  LLM reads, and this release makes it false on three resources. The
  tool descriptions and the README were updated when the capability
  landed; the server-level instructions were not, so a model would have
  believed a capability it had was unavailable. They now also name
  `archive` and `unarchive` among the self-authorising transitions, and
  say that a dropdown reads back as its label. Found by the v0.5.0
  release security review.

- **`docs/security.md` promised an audit trail that has never
  existed.** It said dry-run invocations are logged at `info` with
  `dry_run=true` so an operator could audit what the LLM tried;
  `internal/tools` emits no log records at all. An operator relying on
  that sentence would have found nothing. It now says so, and points at
  the roadmap entry for the log line that would make it true.

- **`list_deals` was quietly short.** Pipedrive moved archived deals to
  their own collection on 2025-07-15 and stopped returning them from
  `/deals`; nothing here knew. An archived deal was absent from every
  list, `get_deal` could not say a deal was archived, and `manage_deal`
  met a bare 403 with no way to explain it. Deals now carry
  `is_archived`, `list_deals` takes `archived` to read the other
  collection — no filter reaches it, so that is the only way in — and
  `manage_deal` gains `archive` and `unarchive`. An edit to an archived
  deal is refused by the read the write already does, naming the action
  that fixes it rather than passing the 403 along.

- **Every pipeline and stage reported itself inactive.** `Pipeline` read
  `active` and `Stage` read `active_flag`; v2 returns neither — both
  carry `is_deleted` — so both decoded to `false` for every record, and
  `list_pipelines` told the model each pipeline was inactive under a
  schema description saying exactly that. Both spellings are v1
  vocabulary that came across in the port, and the comment above them
  claimed they had been confirmed against the live v2 endpoints. The
  LLM-facing `active` field keeps its name and now means something:
  `is_deleted` inverted.

- **`people_count` was promised and never sent.** v2 returns it only when
  a read asks through `include_fields`, which this client never did, so
  the field was absent from every organization while three tool
  descriptions listed it. Every organization read now requests it.

- **One fractional deal probability failed a whole page.** `Probability`
  was `*int` where Pipedrive declares `number`. Go's decoder refuses
  `12.5` into an `int` and the client returns that error, so a single
  such deal made `list_deals` fail rather than returning the page. It is
  `*float64` now, on the record, the request and the LLM-facing summary.

## [0.4.0] - 2026-09-17

### Added

- An integration suite in `internal/integration/`, every file behind
  `//go:build integration`. It connects an in-memory MCP client to the
  server `server.New` builds — the wiring the binary ships, cache warm-up
  and server instructions included — and points it at a live workspace.
  What it covers is only what a fake cannot answer: the JSON a real
  `list_` and a real `get_` each return, custom fields resolving against
  the workspace's own field definitions rather than fixture names, cursor
  paging, the guards reading records somebody really filled in, and the
  error class a real 404 maps to. Every serious defect found while
  building 0.4.0 was found this way, and none of them was visible to the
  tests that mock the API.

  Reads, resource reads, guard refusals and dry runs run on the build tag
  alone, because none of them writes. The reversible write probes need
  `PIPEDRIVE_INTEGRATION_WRITES=1` as well: the workspace on the other end
  is a real CRM and Pipedrive has no undo. Each captures the original
  value, restores it from `t.Cleanup` so a failure mid-test still puts it
  back, and verifies the restore with a fresh read rather than the write's
  echo. `make integration` and `make integration-writes` run the two
  halves; without credentials every test skips with the reason, so the tag
  is safe to carry in a job that holds no secret.

  The suite goes through `internal/app` (below), so
  `PIPEDRIVE_HTTP_TIMEOUT` and the `PIPEDRIVE_DRY_RUN` floor mean here what
  they mean in the binary — under the floor the write probes skip rather
  than rehearse against assertions that expect a record to have moved.
  `docs/configuration.md` now lists `PIPEDRIVE_INTEGRATION_WRITES`, which
  the server never reads, so the one `PIPEDRIVE_`-prefixed variable that
  decides whether a live CRM is written to is in the env-var reference
  rather than only in the source.

- `internal/app`, the startup assembly every entry point shares: resolve
  the workspace domain, load the configuration for it, resolve the API
  token, build the client, wire the server. It used to live unexported
  inside `package main`, so nothing but `main` could perform it and every
  other caller re-derived it — and the first one to try dropped the
  `PIPEDRIVE_DRY_RUN` floor, which `docs/security.md` promises an operator
  holds everywhere.

  Two shapes make that recurrence unavailable rather than merely
  discouraged. `server.New` now takes the whole `config.Config` instead of
  a `domain string` and a `tools.RegisterOptions`, so there is no
  zero-options literal for a future entry point to copy, and the workspace
  a tool labels its output with cannot disagree with the floor it honours;
  `--dump-schemas` calls `server.NewForSchemaDump`, whose name says no
  handler runs. And `Settings.Connect` returns a `Runtime` carrying the
  client beside the settings it was built from, so `Runtime.NewServer`
  cannot be handed a client for another workspace. The API token is
  unexported on `Settings` and `LogValue` redacts it, so neither `%+v` nor
  `slog.Any` can put a keyring token in a log line.

  No behaviour change to the binary: same resolution order, same messages,
  same exit codes. `config.Load()` is gone — it was a second, env-only
  domain resolver with no callers, and leaving it there invited exactly
  the bypass this change exists to remove. `config.DomainEnv` and
  `config.DefaultHTTPTimeout` replace the literals that had been spelled
  out in three files.

- Tests for `scripts/gates/mcpb.go`, `mcpbpack.go` and `mcpregistry.go`,
  which arrived with none. This was the only one of the seven repositories
  running all three of those gates and the only one testing any of them,
  in the repository whose definition of done requires tests for new code.
  The packer tests pack a real bundle from a fake `dist/` and read the zip
  back: the version is stamped through a JSON decode and encode, every
  staged row is present, the generated launcher arrives executable, a
  `dist/` missing a binary is refused, and the archive is reproducible —
  which takes two assertions, not one. Comparing two packs catches
  ordering but cannot catch the clock: zip stores DOS timestamps at
  two-second granularity, so two packs a millisecond apart are identical
  whatever `zipTime` says, and that test passes with `time.Now()`
  substituted. Pinning every entry's timestamp to the expected instant —
  written out in the test rather than read from `zipTime`, because a
  test asserting a value equals itself is the shape of the bug — is what
  fails. `scripts/gates` coverage goes 45.9% → 58.7%.

- `docs/release.md` covers the bundle and the registry. It is the only
  release runbook among the seven and it described neither, while
  `release.yml` had gained a job calling `publish-mcp.yml`. It now names
  the bundle and the registry entry in the post-release checks, and adds
  the three steps that only ever run on a real tag — cosign, the
  provenance attestation and the registry publish — with the recovery for
  each, which differ: cosign failing means no release at all, attestation
  failing means a published release that is unattested, and the registry
  failing means a fine release with no entry, recoverable by dispatching
  `publish-mcp.yml` against the tag that already shipped.

- A **Claude Desktop bundle** (`.mcpb`) on every release, and the MCP
  registry entry that points at it. This server shipped archives and
  nothing else, so installing it meant hand-editing a config file and it
  could not appear in the registry at all — the registry's `mcpb`
  package type needs a bundle.

  The bundle carries a macOS universal binary, a Windows one, both Linux
  architectures and a launcher that picks between them from `uname -m`
  and **execs** it — not a call, because the server talks MCP over that
  process's stdio and a shell left in the middle would own the pipes. On
  an unknown architecture it writes to stderr, never stdout.

  The API token is a `sensitive` user_config field, so Claude Desktop
  stores it as a secret and prompts for it on install. Unlike the OAuth
  servers in this family, this bundle CAN be fully configured from the
  install dialog — so its manifest is held to naming where the token
  comes from rather than to saying it cannot log you in.

- `make mcpb`, which holds the manifest against the bundle the packer
  stages: every path a file going in, every `${user_config.x}` declared,
  every claimed platform spawning the file staged FOR it, and the Linux
  launcher choosing between the packer's own names. A schema catches none
  of those — each produces a bundle that installs and then does nothing.

- `gates registry-publish`, building the entry from the release's own
  `SHA256SUMS`, so the hash describes the bytes that were published. Its
  own workflow with `id-token: write` and `contents: read` and nothing
  else, and `mcp-publisher` verified with cosign before it is unpacked.
  A prerelease tag skips it: an entry cannot be taken back.

- A **Claude Desktop bundle** (`.mcpb`) on every release, and the MCP
  registry entry that points at it. This server shipped archives and
  nothing else, so installing it meant hand-editing a config file and it
  could not appear in the registry at all — the registry's `mcpb`
  package type needs a bundle.

  The bundle carries a macOS universal binary, a Windows one, both Linux
  architectures and a launcher that picks between them from `uname -m`
  and **execs** it — not a call, because the server talks MCP over that
  process's stdio and a shell left in the middle would own the pipes. On
  an unknown architecture it writes to stderr, never stdout.

  The API token is a `sensitive` user_config field, so Claude Desktop
  stores it as a secret and prompts for it on install. Unlike the OAuth
  servers in this family, this bundle CAN be fully configured from the
  install dialog — so its manifest is held to naming where the token
  comes from rather than to saying it cannot log you in.

- `make mcpb`, which holds the manifest against the bundle the packer
  stages: every path a file going in, every `${user_config.x}` declared,
  every claimed platform spawning the file staged FOR it, and the Linux
  launcher choosing between the packer's own names. A schema catches none
  of those — each produces a bundle that installs and then does nothing.

- `gates registry-publish`, building the entry from the release's own
  `SHA256SUMS`, so the hash describes the bytes that were published. Its
  own workflow with `id-token: write` and `contents: read` and nothing
  else, and `mcp-publisher` verified with cosign before it is unpacked.
  A prerelease tag skips it: an entry cannot be taken back.

- `manage_note` can edit a note. There was no update path anywhere in the
  client before this — `postV1`, `postV2` and `deleteV1` existed and no
  PUT did — so `Client.putV1` and `Client.UpdateNote` add one. This stays
  inside the existing notes carve-out rather than opening a new one: v2
  still exposes no `/notes` endpoint to move to, which is the standing
  exception in hard rule 1, not a new dependency on v1. Pipedrive treats
  the v1 notes PUT as a partial update, so the request body carries only
  the fields the caller asked to change.

- Writes report a `changed` list naming every field the write actually
  altered, diffed against the record read immediately before it rather
  than against the request. Pipedrive normalises some of what it stores,
  and the caller should see what landed rather than what was asked for.
  On a dry run the same field names what *would* change, predicted by
  overlaying the request onto the stored record — one diff function
  serving both paths, so the rehearsal cannot disagree with the real
  write.

- The `overwrite` guard covers every populated field a write would
  replace, not just `content`. It is derived from the same diff that
  produces `changed`, so the guard and the report cannot drift apart,
  and the refusal names each field it is protecting. Moving a note off
  the deal it is filed under is now refused the same way replacing its
  text is — the schema warned that an update "moves the note" while
  nothing actually stopped it.

- `[refused]` joins the error classes a tool result can carry. A guarded
  write that stops is neither a validation failure nor an upstream error,
  and a model that cannot tell the three apart will retry the one thing
  that can never work. Every refusal names what it is protecting and the
  argument that permits the write, because a refusal the caller cannot
  act on is a bug.

- **BREAKING.** Optional scalars on an update are pointers, so nil means
  "leave this alone" and a value means "set it". A bare `int64` collapses
  those two, and an update that could not tell them apart would overwrite
  every field the caller did not mention.

  **Clearing a field is documented as unsupported**, which is a change
  from what an earlier draft of this work claimed. A live probe found
  that `PATCH /api/v2/deals/{id}` rejects a null outright (`The value is
  not a valid 'string'`) and stores an empty string as the zero date
  `0000-00-00` rather than removing the value — so v2 has no spelling
  that empties a field. `PUT /api/v1/deals/{id}` with a null does clear
  it, which is how we know this is a v2 gap rather than a Pipedrive-wide
  one; the server deliberately does not reach for that, since it would
  be a third v1 carve-out on an API that sunsets 2026-07-31, for a
  capability nobody has asked for.

  Every optional input now reads "omit to leave it as it is" rather than
  promising an unlink the API will not perform, and
  `docs/architecture.md` keeps the request-by-request evidence under
  "Clearing a field".

- Every mutation now runs the guarded-write contract, not just notes.
  `manage_deal`, `manage_person`, `manage_organization` and
  `manage_activity` read their target before writing and refuse to
  replace any field already holding a value unless `overwrite` says so.
  The client gained `PATCH /api/v2/<resource>/{id}` (`Client.patchV2`)
  and an `Update*` method per resource; there was no update path
  anywhere in it before — `postV1`, `postV2` and `deleteV1` existed and
  no PATCH or PUT did.

  The named transitions take no `overwrite`. `mark_won` names both the
  change and the field it lands on, so the caller already sees the whole
  blast radius, and a flag there would be friction rather than safety.
  They also ignore the descriptive fields entirely: a caller who passes
  a title to `mark_won` does not silently get it written.

- The read-guard-write sequence is one mechanism rather than five.
  `guardedWrite` in `internal/tools/guard.go` owns the ordering — read,
  check the version, predict, refuse over what would be clobbered, honour
  the rehearsal, write, re-diff against the echo — and each resource
  supplies only what differs: its field table, its record label, and how
  to fetch, predict and write.

  This was the top finding of two independent review passes, and it had
  already cost something: with the sequence written out per resource, the
  overwrite check drifted, gated behind the action in deals and
  activities and unconditional in persons and organizations, with nothing
  making the five agree. Ordering that matters this much belongs in one
  place.

- The field machinery is likewise one mechanism rather than five. Each resource
  supplies a table of its LLM-facing field names and how to read each
  stored value; that single table drives the `changed` report, the
  `overwrite` refusal and the dry-run prediction, so the three cannot
  disagree about what a write would do. Adding a field is one edit.

  **Read tools declare `idempotentHint` as well as `readOnlyHint`.** A
  read is idempotent by definition; only `whoami` said so before.

  **Collections are guarded on their whole contents, not on whether they
  are occupied.** Pipedrive replaces a person's emails and phones, and an
  activity's participants, wholesale rather than merging into them, so
  the destructive case is not "the primary is being replaced" but "the
  primary survives and the other four are deleted" — which is exactly
  what an update sending back only the entry it edited does. Those
  fields project to every value in stored order, so a truncation is
  visible to the diff, refused without `overwrite`, and named in
  `changed`. A test asserts that every field an `Update*Request` can send
  has a table entry, because a writable-but-untabled field is one that
  gets written without being guarded or reported.

- `whoami` is memoised for the life of the process, behind the same
  `sync.Once` shape the field caches use. Its description tells the model
  the answer does not change during a session, which is an invitation to
  call it every turn; without the memo each of those was a full v1 round
  trip for a value a fixed token cannot change.

- `whoami` reports which account the token acts as and which workspace
  it points at. Its `user_id` is the `owner_id` a record gets when you
  create one without naming an owner, which is what turns "my open
  deals" into a filter instead of a guess, and it reports the timezone
  an activity's `due_time` is written in — getting that wrong schedules
  a meeting on the wrong day.

  **This is the second v1 carve-out**, added with an explicit user
  go-ahead per hard rule 1. Pipedrive v2 exposes no users resource at
  all — `/api/v2/users` does not exist, which is also why the startup
  auth probe reads `/dealFields` — so `GET /api/v1/users/me` is the only
  endpoint that answers this. A `whoami` tool either lives on v1 or does
  not exist. The call site says so, and `docs/architecture.md` records
  what has to change when v1 sunsets on 2026-07-31.

- A server-level `instructions` block, which every shipped Google
  Workspace MCP server carries and this one had none of. It is where the
  things no single tool description owns belong: start from `search`,
  what each call costs, that a short page is not the last page, that
  activity type cannot be filtered server-side, and what is simply not
  here.

- Five MCP resource templates mirroring the `get_` tools, for clients
  that attach records rather than calling tools:
  `pipedrive://deals/{id}` and the same for persons, organizations,
  activities and notes. They call the same `summarize*` functions the
  tools do, so a resource read and a tool call cannot drift into
  describing one record two ways.

### Changed

- The bundle manifest is brought to the shape four of the seven sibling
  MCP servers already used, and the gate now enforces it. `manifest_version`
  goes 0.2 → 0.3, `support` is added, and `$schema` is pinned to the
  versioned `mcpb-manifest-v0.3.schema.json` rather than the `dist/` path
  that serves whatever is current.

  The pinning is the part that matters. The manifest declared conformance
  to 0.2 while validating against a URL that by then served 0.3 — a
  document disagreeing with its own schema, in a repository whose pins
  gate exists because "latest" drifts. `checkManifestShape` now fails when
  `$schema` is absent or disagrees with the declared `manifest_version`,
  and when `support` is missing. Nothing read either field before, which
  is why three of the seven drifted onto 0.2 and nobody found out.

  Two further pins on the same thought. The schema ref is the tag
  `v2.1.2` rather than `main`: the version in the PATH pins the format,
  the ref pins the BYTES, and `main` can change under a path that still
  reads as pinned — the two are byte-identical today, which is the
  argument for the tag rather than against it, because nothing would
  show that changing. And `manifest_version` has a floor, because a
  check that only asks whether the document agrees with its own schema
  is satisfied by a stale-but-self-consistent 0.2, which is precisely
  what three repositories were shipping. 0.3 rather than 0.4 on purpose:
  upstream serves 0.2, 0.3 and 0.4, and 0.4 differs from 0.3 only by a
  `uv` value in the `server.type` enum, which a `binary` server cannot
  use.

- The Linux launcher is generated from `bundleFiles` at pack time instead
  of being committed at `packaging/mcpb/launch-linux.sh`. A committed
  launcher is a second list of the packer's binary names, and a second
  list can disagree with the first — so the gate had to read the script
  back and compare the two. Generating it makes the disagreement
  unrepresentable rather than detected. The script the packer writes is
  byte-equivalent to the one deleted.

- **BREAKING.** The tool surface follows the shipped Google Workspace MCP
  servers — `google-drive`, `google-sheets`, `google-docs`, `google-chat` —
  instead of a house style of its own. Those four are the MCP servers a
  model has most likely already seen, so matching their conventions is
  what makes this one legible without being explained. `CLAUDE.md` records
  the standard, and what was dropped to reach it.

  Reads stay discrete, because that is what Google actually does:
  `get_file`, `list_folder` and `search_files` are three tools, not one
  with a mode switch. Mutations of a single noun collapse behind one
  `manage_` tool taking an `action`, which is `manage_sheet`,
  `manage_labels` and `manage_revision`.

  So the five `create_*` tools and `delete_note` are gone, replaced by
  five `manage_` tools:

  | Tool | Actions |
  | --- | --- |
  | `manage_deal` | `create`, `update`, `move_stage`, `mark_won`, `mark_lost`, `reopen` |
  | `manage_person` | `create`, `update` |
  | `manage_organization` | `create`, `update` |
  | `manage_activity` | `create`, `update`, `complete`, `reopen` |
  | `manage_note` | `create`, `update`, `delete` |

  Nothing is a one-way door that did not have to be. `reopen` undoes
  both `mark_lost` and `complete`, which is why `done` and the deal
  status are pointers on the request: a bare `bool` cannot tell "reopen
  this" from "say nothing about done", and the tool that could close a
  record but not open it would be the worst of both.

- **BREAKING.** `PIPEDRIVE_ENABLE_DESTRUCTIVE` is retired, and the
  registration-time gate it drove with it. Registration was never the
  right enforcement point: a tool that does not exist cannot explain
  itself, so an operator who left the flag off handed the model a missing
  capability to guess at rather than a refusal telling it why. The Google
  servers register `trash_file` and `delete_message` unconditionally and
  guard the call instead.

  What replaces it is per-call, and narrower. `dry_run` is an input on
  every reshaped write and reports what the write would find and change
  without sending anything — the server-wide `PIPEDRIVE_DRY_RUN` it
  supersedes decided for the whole process, which is the wrong grain,
  since the caller is who knows whether a given write is a rehearsal.
  `overwrite` is required before an update may replace content that is
  already there. `expect_version` carries the `update_time` from the read
  that informed the write, and refuses if the record moved since.

  A guard is only added where the caller cannot already see what they
  would lose. A v1 note delete just clears `active_flag`, so it takes
  `dry_run` and nothing more — the same call `trash_file` makes for a
  reversible trash. Nothing in this server sets that flag back, which the
  tool description says outright rather than implying with a flag.

  An operator upgrading with `PIPEDRIVE_ENABLE_DESTRUCTIVE` still set in
  their MCP client config is not warned: the variable is simply no longer
  read. It was only ever a registration gate, and nothing it used to
  withhold is withheld now, so a stale `=false` does not make the server
  less safe than the flag promised — but it does not do anything either,
  and can be deleted.

  `PIPEDRIVE_DRY_RUN` is **not** retired, and is now documented as what
  it has to be: a floor. Every write tool ORs it with the per-call input,
  so a call can turn a rehearsal on and nothing on the wire can turn one
  off. `docs/security.md` answers the "malicious tool selection" threat
  with "use `PIPEDRIVE_DRY_RUN` for speculative LLM work", and a flag the
  one delete-capable tool could override would have made that promise
  false.

### Fixed

- `app.Settings` implements `String()`, so `%v` and `%+v` cannot print
  the API token. It was unexported with a redacting `LogValue`, and the
  comment claimed that covered `fmt` too — it did not, because `fmt`
  reads unexported fields by reflection. No call site formatted a
  Settings, which is why it would have gone unnoticed until one did. The
  test now asserts the fmt verbs alongside the slog path, and fails
  without the method.

- `pipedrive-mcp status` now loads the configuration the server loads. It
  had resolved the domain and the token and stopped, so a typo in
  `LOG_LEVEL`, `LOG_FORMAT`, `PIPEDRIVE_DRY_RUN` or `PIPEDRIVE_HTTP_TIMEOUT`
  reported green from the one command whose job is answering "can this
  start" — and the server then refused to start on it. Its auth probe also
  honours `PIPEDRIVE_HTTP_TIMEOUT` now instead of a hard-coded 30s.

- The CHANGELOG gate watches `internal/app/`, and the 80% coverage gate
  covers it. The startup assembly moved out of `cmd/`, which the gate
  watched, into a package it did not — so the next change to it, including
  one that dropped the dry-run floor again, would have shipped with no
  entry and nothing firing.

- The CHANGELOG and schema-diff CI gates measure against the base branch
  as it is now, rather than against `github.event.pull_request.base.sha`.
  That value is a snapshot taken when the event fired and does not follow
  the base branch afterwards, so in a stack of pull requests — the normal
  case here, not an edge one — merging the PR underneath leaves the one
  above comparing a diff that contains its own dependency. The changelog
  gate reads that as "no new lines under [Unreleased]" and the schema gate
  reads it as a breaking tool-surface change, both on branches where
  neither is true.

  Reopening a PR does not refresh it; only a push to the head branch does,
  and a push is the one thing that cannot be done here without displacing
  the `BREAKING CHANGE:` footer the schema gate greps for on the head
  commit. Both gates now take the merge-base of the current base branch
  and the head, via `.github/merge-base.sh`, which is what they both meant
  by "what this PR adds" all along.

- The dry run under-reports when Pipedrive derives a field. Changing a
  person's `first_name` reports `["name", "first_name"]`, because the
  upstream recomputes `name` from the parts, while the rehearsal for the
  same call predicts `["first_name"]` alone. That direction is the safe
  one — the rehearsal never promises less will change than does — and the
  guard is unaffected, but nobody had written it down. Established by a
  live write probe; `docs/architecture.md` has the reasoning under "The
  upstream may change more than you asked for", and `manage_person`'s
  description says it where it bites.

- `README.md` did not follow the shape the sibling servers use, which is
  what a reader comparing them side by side notices first. It now carries
  the same section skeleton as `google-drive-mcp`, `google-sheets-mcp`,
  `google-docs-mcp` and `google-chat-mcp` — identical heading for
  heading — plus the four badges they carry rather than two, an `Install`
  that leads with `go install` and the `PATH` gotcha that follows it, a
  `Configuration` section instead of an env table buried under setup, and
  a `Tools` table with one row per tool saying what it does rather than
  grouped rows saying what it answers.

  Two sections were pointers rather than content and are now written out:
  `Versioning` said "see SECURITY.md" where the siblings state the
  stability contract, and `Getting help` opened with troubleshooting
  without mentioning that `pipedrive-mcp status` answers most of it —
  `status --json` included, which existed and went unadvertised.

  The phase plan moved to `docs/architecture.md` and the `status --json`
  shape to `docs/configuration.md`, which is where the siblings keep
  both; the README links to them.

- `CODE_OF_CONDUCT.md` was Contributor Covenant 2.x while all five
  sibling servers are on 3.0. Adopted 3.0, which replaces the
  "Our Standards / Enforcement" shape with "Encouraged Behaviors /
  Restricted Behaviors / Reporting an Issue / Addressing and Repairing
  Harm". The reporting route is unchanged — GitHub's private security
  advisory flow, and no email address anywhere in the tree, which a gate
  enforces.

- The phase plan in `README.md` claimed `v0.2.0` shipped the write tools
  and `v0.3.0` the workflow tools. Neither did: every tag from `v0.2.0`
  to `v0.3.2` went to release and supply-chain engineering, and the write
  and workflow surface both land here in `v0.4.0`. The table now says
  what shipped rather than what was planned, and separates that from what
  `v1.0.0` still needs — including a resolution to the Pipedrive v1
  sunset, which the notes tools and `whoami` both sit on.

- Two references to files that no longer exist. `CLAUDE.md` pointed the
  phased delivery plan at a plan file outside the repo that has since
  been deleted; the plan now lives in `README.md` and `CONTRIBUTING.md`,
  where a contributor can read it. `docs/development.md` still named
  `scripts/stdio-smoke.sh` and `scripts/staleness-check.sh`, which became
  one Go command back in `v0.2.0`.

- `tools.SDKVersion` said `v1.5.0` while `go.mod` pinned `v1.6.0`. That
  constant is stamped into the schema dump so a surface diff caused by an
  SDK upgrade can be classified as PATCH rather than as a breaking change;
  left stale, it would have misattributed the next one.

### Security

- The Claude Desktop bundle is a subject of the build-provenance
  attestation in its own right. It was covered only transitively, by its
  row in `SHA256SUMS` — so a verifier needed the checksum file and its
  signature to say anything about the `.mcpb`, and
  `gh attestation verify` run against the bundle itself answered "no
  attestation found". That is the one artifact most people install
  without opening a terminal, and the one the MCP registry points at.

- The MCP registry publish verifies the checksum file's signature before
  reading it. `publish-mcp.yml` took `SHA256SUMS` from the published
  release and fed it straight to `gates registry-publish`, which lifts
  the bundle's row out of it and writes that digest into the registry
  entry as `fileSha256` — the number a registry-driven client verifies
  the download against. Nothing checked it. `SHA256SUMS.bundle`, the
  cosign signature goreleaser makes over exactly that artifact, was
  never downloaded, and the workflow's `cosign verify-blob` ran only
  against the `mcp-publisher` tarball, while the file's own header
  asserted the opposite.

  Exploiting it needs `contents: write` — replace the `.mcpb` asset,
  edit `SHA256SUMS` to match. The signature and the attestation both
  break, which is the detection the release pipeline exists to provide,
  but neither was consulted here, so a re-publish would have written the
  attacker's digest into a registry whose entries cannot be withdrawn.
  The job now verifies `SHA256SUMS` against its bundle, identity pinned
  to this repository's `release.yml` on a tag ref, before anything reads
  it.

## [0.3.2] - 2026-09-15

### Fixed
- `--version` says which release the binary came from, and says it the
  same way however it was installed. Two bugs, one line:
  `go install …@latest` applies no ldflags, so an installed binary called
  itself `dev (unknown, built unknown, …)` and could not name the release
  at all; and goreleaser stamps its `{{.Version}}` with the leading `v`
  stripped, so an archive said `0.3.1` where the module version says
  `v0.3.1`. The same release therefore reported two different strings
  depending on how somebody installed it, and anything parsing
  `--version` got a different answer per install method.

  The linker stays the source of truth for a release build; the module
  version out of `debug.BuildInfo` is the fallback, which is the honest
  answer when nothing stamped anything. Both spellings normalise to the
  one the tag, the module version and the release all use. The four
  sibling servers fixed this after an outside reader compared them side
  by side; this one had not.

- syft is pinned to v1.51.1 in the release workflow, which is what the
  four sibling servers pin. The action was pinned by SHA and the tool it
  installs was not — the same hole that failed v0.3.0 through cosign, one
  step below it in the same job. syft writes the SBOM attached to every
  archive, so an unpinned one changes what a release ships, or fails it,
  without anything in this repository moving.

### Added
- The `pins` gate classifies every action, and an unknown one fails it.
  The gate could only ever check the versions that were *written*; an
  action that installs a tool and names no version at all is an absence,
  which is how both the cosign and the syft holes sat there unreported.
  Every action is now in one of two tables — the installers, with the
  input that pins the tool each one installs, or the actions that install
  nothing, each with the reason — and an action in neither fails the
  gate, because being unclassified is the state that let the first two
  through.

  Watched failing on all three shapes before being trusted: `cosign-release`
  removed, `syft-version` removed, and an unclassified installer added.

## [0.3.1] - 2026-09-15

### Fixed
- The release workflow publishes again. v0.3.0 is a tag that published
  nothing: goreleaser failed at "signing artifacts" with `create bundle
  file: open : no such file or directory`, and the tag is left where it
  is because the Go module proxy caches a tag within minutes and is
  immutable — re-pointing it would leave the proxy and the repository
  naming different commits.

  Two causes, both fixed. The cosign installer was pinned by SHA but the
  cosign it installs was not, so the release took whatever was newest;
  that cosign defaults to `--new-bundle-format`, which ignores
  `--output-signature` and `--output-certificate` and then fails because
  no `--bundle` path was given. **A SHA pins the wrapper, not the tool**
  — the same lesson the `pins` gate learned for gitleaks one release
  ago, in this same repository. `cosign-release` is pinned to v3.1.3 now,
  as the four sibling servers already pin it.

  And the signing block is the siblings' one: a single bundle over
  `SHA256SUMS` rather than a `.sig` and a `.cert` beside every artifact.
  Every archive and every SBOM is covered by its hash in that file, so
  signing each separately bought a longer asset list rather than more
  assurance. Verification instructions in `README.md`, `SECURITY.md` and
  `docs/release.md` follow it, and the runbook loses a step that told the
  reader to `cosign verify` a Docker image removed in 0.3.0.

## [0.3.0] - 2026-09-15

### Added
- `status --json` prints the same state as one JSON object on stdout, so
  a script can read whether this server can start instead of parsing
  output written for a person. `credentials.resolved` is the field to
  branch on; `schema_version` changes only when a field is removed or its
  meaning changes. The design is an outside contributor's, from the
  Google Drive server where it landed first.

  This command differs from the siblings' in two ways that the object had
  to keep. It **exits non-zero** on every refusal, and it still does — a
  caller may read the code or the object. And it **contacts Pipedrive**:
  the probe is part of what `status` reports, so `probe.ran` is separate
  from `probe.ok`, because a probe skipped with `--no-probe` is neither a
  pass nor a failure and must not read as either.

  Every stopping point is a field rather than an early return with half
  an object behind it: no domain, no token, and a failed probe each
  produce a whole object with a reason, since a caller cannot tell a
  truncated object from one it failed to parse.

  One collector, two renderers. The text output is byte-identical to what
  the released binary prints, asserted by diffing them, and the no-token
  refusal was driven end to end — whole object, reason naming the fix,
  exit 1.
- A `pins` gate, in `make check` and CI. Every action reference must be a
  full commit SHA, every tool version must be exactly one version, and
  every workflow must set `defaults: run: shell: bash`. Twenty-nine
  action references, eighteen tool versions, three workflows. Watched
  failing both ways before being trusted: one action put back on a tag,
  and the two gitleaks versions drifted apart.

  It found three things on the way in. Neither workflow set the bash
  default, so a `run` line would be parsed as PowerShell on a Windows
  runner. The gitleaks *action* was pinned by SHA while the gitleaks it
  installs was not — a SHA pins the wrapper, not the tool — so
  `GITLEAKS_VERSION` is named in `ci.yml` now. And there was no
  `.pre-commit-config.yaml`, so gitleaks ran only after a push; there is
  one now, held to the same version by the gate.

  The gate came from a sibling and gained something in the port: it
  resolves `version: ${{ env.X }}` against the workflow's own `env`
  block. This repository pins tool versions in one place and references
  them, which the siblings do not do and which is the better pattern —
  two copies cannot disagree. Worth sending back to them.
- A CodeQL workflow, which this repository did not have: push, pull
  request, and weekly so a rule added after a merge still finds old code.

### Changed
- `gitleaks/gitleaks-action` to v3.0.0, which the four sibling servers
  were already on. It was held back while the concern was that a major
  bump might move the version out of `GITLEAKS_VERSION` and break the
  equality the `pins` gate holds between CI and pre-commit. It does not:
  the release says "No changes to inputs, outputs, or behavior", the
  action's source still reads `GITLEAKS_VERSION`, and the only change is
  Node 20 to Node 24 — which GitHub is removing from Actions this month,
  so v2 was the riskier place to sit.
- `golang.org/x/term` v0.46.0 and `golang.org/x/sys` v0.48.0. The MCP SDK
  is **deliberately left at v1.6.0**: taking `@latest` pulled v1.8.0, and
  the schema-diff gate refused it because the emitted tool schemas
  changed. That is the gate working — an SDK minor that moves the tool
  surface is the repository's stated contract moving, and it belongs in a
  change of its own with the diff read rather than riding in behind a
  security fix.

### Removed
- **The Docker image, and everything that carried it**: `Dockerfile`,
  `.dockerignore`, `docker-publish.yml`, the CI build-scan-smoke job, the
  docker mode of the smoke gate, the dependabot docker ecosystem, and the
  sections across the README and five documents that described it.

  It did not earn its keep, and the reason is specific to this server
  rather than a dislike of containers. An MCP stdio server is launched as
  a subprocess by the client, on the user's own machine, so the container
  buys no isolation the client does not already have — and it costs the
  thing this server's auth story is built on. `docs/security.md` said so
  already: "Containers can't reach the host OS keyring, so the Docker
  path needs the env var." The containerised route therefore pushed the
  API token into the environment, which is precisely what `login` and the
  keyring exist to avoid, and the README's Docker section never mentioned
  it.

  Against that it cost a 48-line CI job, a 61-line publish workflow,
  Trivy scanning, base-image digest bookkeeping at every release, weekly
  dependabot churn, and a stdio smoke that needed a fifteen-second hold
  for container cold start where a binary needs three. The sibling MCP
  servers ship no image and are simpler for it.

  Anyone using `ghcr.io/mmedum/pipedrive-mcp` should move to the signed
  archive or `go install`, and run `pipedrive-mcp login` once so the token
  lands in the keyring rather than the environment.

### Security
- A `leaks` gate, in `make check` and CI, and **it found a real address
  on its first run**, at a live Danish domain, sitting in two test files
  seven times as fixture data. It is an `example.com` address now — `example.com` is reserved by RFC 2606 precisely so a fixture can
  use it.

  The rules are this server's own rather than a sibling's, because the
  identifiers differ: an address at a domain someone could own, a
  customer's `*.pipedrive.com` subdomain, and a 40-hex run on a line that
  also says token. Placeholders, Pipedrive's own hosts and the `%s` in a
  format string are not matches, each for a reason written beside the
  rule. It also refuses a committed build artifact, which this repository
  is one `git add -A` away from at any time.

  Watched failing on three planted identifiers, one of each kind, and
  watched passing again with them removed.
- The maintainer's address is out of `SECURITY.md`, `CODE_OF_CONDUCT.md`
  and `CONTRIBUTING.md`. Reports go through GitHub's private security
  advisory flow, which reaches the same person without putting an address
  in a public repository — the same change the sibling servers made.

## [0.2.0] - 2026-09-15

### Changed
- The verification block moved from `release.header` to `release.footer`,
  so the release page reads notes first and how-to-verify after, as the
  sibling servers do. It was a header while the notes were being
  discarded, when it was the only thing on the page and the order could
  not matter.

### Changed
- The four shell scripts under `scripts/` are one Go command with a
  registry, `gates`, and **there is no shell left in the repository**.
  That is how the five sibling MCP servers do it, for reasons that
  applied here too: a shell script is held to no gofmt, vet, lint or
  test; `make check` needed bash *and* jq, which are a dependency rather
  than a given; and a check that reads JSON with `grep` is how a quote
  ends up inside a string.

  Each ported gate is now covered by tests of its own, which the shell
  had none of, and two of the four came out better for being rewritten
  rather than translated:

  - **`release-notes` stops at a markdown link definition.** The shell
    version published the compare-link footer as part of the oldest
    release's notes, because the footer follows that section with no
    heading in between. It also lifts headings one level, so a section
    no longer starts at h3 directly under the tag's h1.
  - **`smoke` decodes the reply instead of grepping it.** `grep '"tools"'`
    passes on any line that merely contains the word, including an error
    message that happens to mention it. It now parses the frame, matches
    the request id, and reports how many tools came back. It also holds
    stdin open with a pipe rather than a `sleep`, and porting it found
    why that hold exists: without it the server sees EOF and exits before
    flushing a reply, which reads exactly like a server that never
    answered.
  - `deps` decodes `go list -m -u -json` rather than shelling to jq, and
    `changelog` keeps the wide diff window the shell version needed so
    the `[Unreleased]` heading is in view however far the entries sit
    below it.

### Security
- Every GitHub Action is pinned to a commit SHA rather than a version
  tag, with the tag kept beside it as a comment. All twelve were on tags
  — `actions/checkout@v4`, `docker/build-push-action@v7` and the rest —
  and a tag is mutable: whoever can move it can change what runs in a
  workflow holding `contents: write` and an OIDC token. Thirty-two
  references across the three workflows. The sibling MCP servers pin the
  same way and have a gate for it.

  The SHAs were resolved from each repository rather than copied, and an
  annotated tag was dereferenced to the commit it points at rather than
  recorded as the tag object's own hash.

### Changed
- `github.com/modelcontextprotocol/go-sdk` v1.6.0, `golang.org/x/term`
  v0.43.0 and `golang.org/x/sys` v0.44.0 — what dependabot proposed,
  applied on top of current `main` rather than merged from a branch that
  predates the Go toolchain bump.
- `actions/checkout` v4 to v6, `actions/setup-go` v5 to v6 and
  `goreleaser/goreleaser-action` v6 to v7, which is what the three open
  dependabot pull requests asked for; they are closed in favour of this,
  since each would have reintroduced a mutable tag.

### Fixed
- The release page carries the release notes. `extract-release-notes.sh`
  has always pulled the matching `CHANGELOG.md` section and the workflow
  has always passed it with `--release-notes`; `.goreleaser.yaml` threw
  it away. `changelog: disable: true` is evaluated in the changelog
  pipe's `Skip`, which runs before `Run`, so `ctx.ReleaseNotes` was never
  assigned and the file the workflow had just written was never opened.
  The v0.1.0 page is the verification footer with nothing above it. The
  block is deleted rather than set to false.
- A mistyped subcommand exits non-zero instead of starting the server.
  `pipedrive-mcp statsu` used to start the MCP server and exit 0, so a
  typo in a setup script was indistinguishable from a correct invocation
  and surfaced later as a server that was mysteriously not there. A
  leading dash is the only thing separating a flag from a mistyped
  subcommand, so that is the whole rule.

### Added
- `forbidigo` holds the rule that stdout carries only MCP JSON-RPC
  frames. That rule is hard rule 2 here and it restates a MUST NOT in the
  MCP stdio transport, and nothing enforced it. `fmt.Print*` and
  `os.Stdout` are forbidden outside `main`, which names the process's
  streams once and passes them down as `io.Writer`. Both spellings were
  injected into `internal/` and watched to fail, with the configured
  message at the offending line.
- `.github/ISSUE_TEMPLATE`, the community-health file GitHub's checklist
  names that this repository lacked. The bug form asks for `status`
  output and the version, and says plainly what not to paste: no API
  token, no company domain, no record contents.

### Changed
- `main` is one line and the dispatch lives in `run(args, stdout,
  stderr) int`, so the unknown-command guard is held by a test of its
  behaviour rather than of a predicate. Verified by neutering the guard
  so the file still compiles and watching the test go red.

  The command paths still exit from inside. They parse package-level
  flags and `fail` ends the process, so a second call panics with "flag
  redefined" and the first attempt at testing the other direction opened
  a live API connection with the machine's own credentials. Holding that
  direction needs `runServer` to take a `FlagSet` and its own client,
  which is a larger change than this one.
- The README follows the skeleton shared with the sibling MCP servers,
  checked against GitHub's own README guidance, the community profile
  checklist and the standard-readme spec: an opening line under 120
  characters, a `Why pipedrive-mcp` section, `Safety`, `How it works`,
  `Getting help`, `Documentation` and `Code of conduct`, with `Configure`
  becoming `Set up Pipedrive`, `Quick start` becoming `Connect a client`,
  `Tool catalog` becoming `Tools` and `Supported versions` becoming
  `Versioning`. The status line loses its version number: the release
  badge carries that and cannot go stale.
- Hard rule 2 in `CLAUDE.md` quotes the specification it restates, links
  it, and names what enforces it.
- Go is 1.26.6 everywhere it is pinned — `go.mod`, both workflows and the
  Dockerfile. It was 1.26.2, which govulncheck reports as affected by
  GO-2026-4918 and GO-2026-4971: stdlib flaws in `crypto/tls`,
  `crypto/x509`, `net/http`, `net/url`, `net/textproto` and
  `encoding/asn1`, fixed across 1.26.3 to 1.26.6. Every release published
  so far was built with a vulnerable toolchain.

  The Dockerfile pins by digest as well as by tag, so the digest moved
  with it — `sha256:3889b425…`, checked against the registry to be an
  image whose `GOLANG_VERSION` really is 1.26.6. Changing the tag alone
  would have left the build on 1.26.2 while claiming otherwise, which is
  worse than leaving it.
- golangci-lint is pinned to v2.13.2, matching the sibling servers. The
  local pin had drifted to v2.11.4 and `verify-tool-versions` failed on a
  machine set up for the others.

### Added
- `create_activity` tool — fourth v2 write tool. Required input:
  `subject`. Optional: `type` (defaults to `task` upstream when
  omitted), `due_date` / `due_time` / `duration`, `deal_id` /
  `person_id` / `org_id` / `lead_id`, `owner_id`, `note` (private
  HTML), `public_description`, `location` (single-line, server-
  parsed), `participants`, `done`, `busy`. To log an activity that
  already happened, pass `done=true` plus the `note`. To schedule
  one, pass `due_date` (and optionally `due_time` / `duration`).
  Returns the created activity as Pipedrive echoes it (with parsed
  structured `Location`). Honours `PIPEDRIVE_DRY_RUN=true`. v2
  activities don't have custom fields, so no name/hash resolution
  applies on this surface.

### Changed
- `tools.RegisterOptions{DryRun, EnableDestructive}` replaces the
  positional bool args on `RegisterDeals` / `RegisterPersons` /
  `RegisterOrganizations` / `RegisterNotes` and on `server.New`.
  Stops a future caller from swapping the two flags positionally
  (both type-check). Internal refactor — no surface change.
- Schema-diff CI gate now compares against the PR's base branch
  (`pull_request.base.sha`) instead of the most recent release tag.
  The tag-based comparison flagged every PR opened since the last
  release with the cumulative diff of all merged PRs in between,
  even on internal-only PRs that introduced no surface change of
  their own. The new comparison reflects what *this* PR actually
  introduces. Base-build failure now hard-fails the gate (under
  the old tag base, a stale-toolchain skip was defensible; under
  a branch base, a failure means main is broken).

### Added
- `create_organization` tool — third v2 write tool. Required input:
  `name`. Optional: `address` (single-line string; Pipedrive parses
  it server-side into structured country/locality/postal_code on
  the response) and `owner_id`. Pipedrive defaults apply for any
  field omitted. Returns the created organization as Pipedrive
  echoes it (with the parsed structured address). Honours the
  server-wide `PIPEDRIVE_DRY_RUN=true` env by returning a synthetic
  preview (`dry_run=true`, `id=0`); on the dry-run path the address
  is wrapped as `{value: <input>}` since no server-side parsing
  happens. Custom-field *writing* is not yet supported (the same
  deferred slice as `create_deal` / `create_person`).
- `create_person` tool — second v2 write tool. Required input:
  `name`. Optional: `first_name`, `last_name`, `emails`, `phones`
  (each `{value, primary, label}`), `org_id`, `owner_id`. Pipedrive
  defaults apply for any field omitted. Returns the created person
  as Pipedrive echoes it. Honours the server-wide
  `PIPEDRIVE_DRY_RUN=true` env by returning a synthetic preview
  (`dry_run=true`, `id=0`) without issuing the upstream POST.
  Custom-field *writing* is not yet supported (the same deferred
  slice as `create_deal`).
- `create_deal` tool — first v2 write tool in the server.
  Required input: `title`. Optional: `value`, `currency`,
  `pipeline_id`, `stage_id`, `owner_id`, `person_id`, `org_id`,
  `expected_close_date`, `probability`. Pipedrive's defaults
  apply for any field omitted (workspace currency, owner =
  API-token user, status = open, first stage of the chosen /
  default pipeline, etc.). Returns the created deal as Pipedrive
  echoes it. Honours the server-wide `PIPEDRIVE_DRY_RUN=true` env
  by returning a synthetic preview (`dry_run=true`, `id=0`)
  without issuing the upstream POST. Custom-field *writing* is
  not yet supported (output continues to resolve hash keys to
  names as before); edit the deal in the Pipedrive UI for now.
  This is the first PR opening Phase 2 (foundation write tools).
- `internal/pipedrive/Client` gains a `postV2` helper paired with
  the existing `postV1`; same retry policy (only 429 is retried,
  never 5xx, since Pipedrive may have committed before
  responding). The new helper is internal but documented so future
  `create_*` write tools can wire through it.
- `list_deals` now accepts `updated_since`, `updated_until`,
  `sort_by`, and `sort_direction` — bringing it to parity with
  `list_persons`, `list_organizations`, and `list_activities`.
  Default sort is `update_time desc` (most-recently-touched first),
  matching the convention. Pipedrive v2 `/deals` supports all four
  upstream; the gap was a holdover from when `list_deals` was the
  first list_X tool. Additive change — no breakage to existing
  callers; tagged `SCHEMA-CHANGE:` per the gate's new convention.

### Fixed
- `FieldCache.Count()` no longer triggers `sync.Once` on a fresh
  cache entry. The previous defensive `once.Do(func(){})` could
  race with a concurrent first `Load()` and silently seal the
  once, causing the real fetch to be skipped and the cache to
  surface unresolved hash keys to the LLM. `Count` now reads a
  separate `loaded` atomic flag set inside `Load`'s once.Do
  *after* `byKey` is written. Internal-only; no behavior change
  on the `refresh_field_cache` happy path (which always calls
  `Reload` → `Load` → `Count` sequentially).

### Changed
- Schema-diff CI gate now distinguishes additive from breaking
  changes. The previous gate required a `BREAKING CHANGE:` footer
  on *any* schema diff — including purely additive ones (new
  optional input field, new tool, expanded enum). The new gate
  accepts a `SCHEMA-CHANGE:` footer for additive/non-breaking
  changes and reserves `BREAKING CHANGE:` for actual breaks
  (tool removed/renamed, required input removed/renamed, output
  type changed, narrowed enum). Empty diff still passes without
  any footer. PR template updated with the new convention.
- `internal/tools/sort.go` exports a single
  `commonV2TimestampSortFields` set ({id, update_time, add_time});
  `list_persons`, `list_organizations`, and `list_activities`
  reuse it (activities extends with `due_date`). No tool-surface
  change — the per-tool enum is identical. Drops a duplicated
  literal map.
- `dealSummary.Probability` is now a fresh copy of the upstream
  `*int` rather than aliasing it. Currently safe (no upstream
  mutators), copy is for parallel-shadow symmetry.
- `github.com/google/jsonschema-go` `v0.4.2` → `v0.4.3` (patch
  bump; library has no breaking changes).

### Documentation
- `README.md` status banner updated: `v0.1.0` is shipped, not
  pending. Phase plan table footnotes that `v0.0.1` was elided
  (Phase 0 work rolled into `v0.1.0`).
- `CHANGELOG.md` `[Unreleased]` link points at
  `compare/v0.1.0...HEAD` (was `compare/HEAD...HEAD`); added the
  missing `[0.1.0]: …/releases/tag/v0.1.0` anchor.
- `pipedrive.Address` GoDoc clarified: typed-struct address is
  returned by `/organizations/{id}` only — persons inherit none
  at the typed level, address-typed *custom* fields go through
  `custom_fields` raw decoding.
- `pipedrive.ListActivitiesOptions.IncludeAttendees` GoDoc
  corrected (default is off, set true to opt in — earlier text
  was inverted).
- `noteSummary` GoDoc no longer claims the pinned-to-* fields
  translate v1's 0/1 ints; `pipedrive.Note` already decodes them
  as JSON booleans (live verification 2026-04 confirmed external
  docs were wrong).

## [0.1.0] - 2026-04-27

Phase 1 closes here: the read surface (deals, persons, organizations, activities, notes,
pipelines/stages, search), the notes-only v1 carve-out, the first
non-destructive write tool (`create_note`), the destructive write
(`delete_note`, gated by `PIPEDRIVE_ENABLE_DESTRUCTIVE`), and the
operator-facing `refresh_field_cache`. Fifteen tools are registered
by default; a sixteenth (`delete_note`) registers when the
destructive flag is on.

### Changed
- `list_stages(pipeline_id=N)` now fans the existence-check and the
  stages fetch out concurrently, cutting the wall-clock cost from
  `t(pipelines)+t(stages)` to `max(...)`. Same `[not_found]` semantics
  for unknown/invisible pipelines.
- Cache-warm goroutine in `server.New` now respects the parent
  context, so SIGTERM mid-warm cancels in-flight `/dealFields` /
  `/personFields` / `/organizationFields` requests cleanly instead
  of letting them run orphaned to completion. No user-visible
  behavior change in the happy path.
- `internal/pipedrive/Client` now supports per-call API version and
  HTTP method via two new helpers (`doV1`, `postV1`); the existing
  `do(ctx, path, out)` continues to be a v2-GET shortcut for the
  read tools and is unchanged at every existing call site. Retry
  policy split: GET retries 5xx as before; POST retries only 429
  (server explicitly told us to back off without committing) so a
  partially-applied write isn't duplicated. Reason: v1 carve-out
  for notes — Pipedrive's v2 API does not expose `/notes` and
  Pipedrive's developer team officially recommends staying on
  `/api/v1/notes` for now (developer community, May 2025).

### Added
- `refresh_field_cache` tool — operator escape hatch for picking up
  custom-field renames or additions made in the Pipedrive UI without
  restarting the server. Refreshes the deals / persons / organizations
  caches in parallel; returns a per-resource row with the live field
  count after reload, plus an `errors` count if any resource failed.
  Activities don't have a custom-field cache on Pipedrive v2 and are
  intentionally absent from the output. Public `FieldCache.Count()`
  and per-resource `Client.ReloadDealFields` / `ReloadPersonFields` /
  `ReloadOrganizationFields` are the underlying primitives — each
  clears the cache, eagerly re-fetches, and returns the field count
  so a partial failure (e.g. transient 503 on one resource) doesn't
  leave the cache in an undefined state.
- `list_persons` and `list_organizations` tools — cursor-paginated
  list endpoints over Pipedrive v2's `/persons` and `/organizations`.
  Filters: owner, update window (`updated_since`/`updated_until`),
  sort (id / update_time / add_time, asc/desc). `list_persons` also
  supports `org_id`. Both default to `update_time desc` (most-
  recently-touched first) for the natural "what's been worked on
  lately" query. Custom fields resolved by name via the existing
  per-resource caches. `search` remains the natural-language
  gateway for finding records by name; these tools are the
  precision filter when the IDs are already known.
- `get_note`, `list_notes`, `create_note`, `delete_note` tools —
  Pipedrive notes attached to deals / persons / organizations /
  leads / projects. These are the only v1-API tools in this server
  (per CLAUDE.md hard rule #1, the notes carve-out). `list_notes`
  exposes the same opaque-cursor surface as the v2 list tools —
  internally the cursor wraps v1's `start` + `next_start` offset
  block so the LLM doesn't see the v1/v2 difference. `create_note`
  is the first and (in Phase 1) only non-destructive write tool:
  it requires `content` plus at least one anchor
  (deal_id / person_id / org_id / lead_id / project_id), caps
  content at 16 KiB at the MCP boundary, and honours the
  server-wide `PIPEDRIVE_DRY_RUN=true` env by returning a
  synthetic preview (id=0, dry_run=true) without issuing the
  upstream POST. `delete_note` is destructive and registers ONLY
  when `PIPEDRIVE_ENABLE_DESTRUCTIVE=true` is set on the server
  (per CLAUDE.md hard rule #3, server-build-time gating, not
  annotation-based); also honours `PIPEDRIVE_DRY_RUN`.
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

[Unreleased]: https://github.com/mmedum/pipedrive-mcp/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/mmedum/pipedrive-mcp/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/mmedum/pipedrive-mcp/compare/v0.3.2...v0.4.0
[0.3.2]: https://github.com/mmedum/pipedrive-mcp/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/mmedum/pipedrive-mcp/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/mmedum/pipedrive-mcp/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/mmedum/pipedrive-mcp/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/mmedum/pipedrive-mcp/releases/tag/v0.1.0

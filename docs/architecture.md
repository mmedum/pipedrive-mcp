# Architecture

How `pipedrive-mcp` is put together, and the decisions a contributor
should not undo without a better reason than the one that put them here.
Read the decision before changing the tool surface, the write guard, the
error classes or the v1 carve-out. Part 1 is the overview. Part 2, from
[Decisions](#decisions), is one entry per decision, cited elsewhere by
its D-ID. What was verified about Pipedrive lives in the comment at the
code that depends on it, with Pipedrive's reference linked and, for a
load-bearing behavior, the integration test that checks it named. A
model reads the server instructions and the tool descriptions in the
binary; the README is the person's view.

## Context

`pipedrive-mcp` is a Model Context Protocol server for Pipedrive CRM.
One Go binary runs as a subprocess of an MCP client and speaks MCP over
stdio. It holds one workspace's domain and one API token, and calls
`https://{domain}.pipedrive.com` and nothing else. Nothing is hosted,
and the process keeps no state between runs (D-01). The SDK serves the
current MCP revision beside the handshake revisions before it, and the
server implements none of the protocol itself (D-02).

It calls Pipedrive API v2 for everything except notes and the signed-in
user. v2 exposes no `/notes` and no `/users`, so those come from v1,
which Pipedrive has not deprecated for them. The tools on v1 sit outside
the compatibility promise (D-03, D-04).

It is more than a wrapper around the REST API. `search` turns a name
into the id every other tool needs, and drops the records Pipedrive's
index keeps after a delete (D-11, D-12). Custom fields read and write
under the names the workspace gives them, and dropdowns as labels, where
Pipedrive stores hashes and option ids (D-18, D-22). And every write
reads its target first and refuses to replace what the caller has not
named, because Pipedrive has no undo (D-27).

The parties around the server:

- **The person** issues the API token, which carries every permission of
  their Pipedrive user. It lives in the OS keyring, or in
  `PIPEDRIVE_API_TOKEN`, which wins (D-63).
- **The operator** sets the domain, the log level and format, the HTTP
  timeout and the dry-run floor through the environment (D-66, D-56,
  D-33). The operator is usually the person.
- **The client and the model** choose each call and approve it by the
  client's own rules. The MCP transport does not carry the prompt, so
  the server cannot tell why a call was made (D-60). The threats this
  leaves open are in [security.md](security.md).
- **Pipedrive** is the only network peer. The token travels in one
  header and no redirect is followed (D-62, D-55).

It is not a multi-workspace server (D-01), a client for products, leads,
files, projects or goals (D-07), a way to merge records (D-08) or to
clear a field (D-42), or an audit log (D-60).

## Quality goals

In priority order:

1. **Nothing is lost by a call nobody meant.** Every update, transition
   and delete reads first, and an update refuses to replace a field the
   caller has not named. A rehearsal sends nothing, the operator's
   dry-run floor cannot be lifted from the wire, and a delete is soft
   and says so (D-27, D-30, D-31, D-32, D-33, D-47).
2. **A result says what Pipedrive did.** The `changed` report diffs
   Pipedrive's echo against the read before the write. A write is never
   repeated once it may have landed, and search does not return records
   that are gone (D-35, D-54, D-11). The unit tests run against fakes
   written from what Pipedrive is believed to do, and only the live
   suite tests that belief (D-78).
3. **The model can act on every answer.** Every error carries a class
   from one closed set, every refusal names the argument that permits
   the write, and custom fields read and write in the workspace's own
   words (D-51, D-29, D-18, D-22).
4. **Nothing about the workspace leaves where it belongs.** Stdout
   carries frames only, a log line carries no query string, body or
   token, and the repository holds nothing from a real account (D-58,
   D-59, D-61, D-89).
5. **The surface is a contract.** Tool names, inputs and outputs change
   only with a commit footer naming each item, and a break needs a MAJOR
   release. The v1 tools are the disclosed exception (D-75, D-76, D-04).

## Codemap

Imports run one way: `cmd/pipedrive-mcp` to `internal/app` to
`internal/server` to `internal/tools` to `internal/pipedrive`, with
`internal/config`, `internal/credentials`, `internal/userconfig` and
`internal/version` beside the assembly. `internal/pipedrive` imports
the standard library alone, which depguard holds (D-70, D-71). The docs
gate holds this list to the tree.

- `cmd/pipedrive-mcp` is the program. With no subcommand it serves.
  `login`, `logout` and `status` manage the credential, and
  `status --json` prints the same report as one object (D-68).
  `--version`, `--dump-schemas` and `--skip-probe` are the operational
  flags. It names the process's streams once and passes them down as
  writers (D-58), runs the startup probe and exits non-zero when it
  fails (D-06), and refuses an unknown subcommand (D-69).
- `internal/app` is the startup assembly every entry point shares:
  domain, configuration for it, token, client, server. `Settings`
  carries the token unexported and redacts it in `LogValue` and `String`
  (D-61). `Settings.Connect` returns a `Runtime` that pairs the client
  with the settings it was built from, and `Runtime.NewServer` is how a
  serving process gets its server (D-34). Domain resolution is here too
  (D-64). Nothing in it logs, exits or touches the network.
- `internal/config` holds every setting the server reads as
  `config.Vars`, each variable spelled once and read through
  `config.Lookup` (D-66). `Load` and `LoadFor` validate at startup, the
  domain included (D-67, D-65).
- `internal/credentials` resolves the token from `PIPEDRIVE_API_TOKEN`,
  then from the OS keyring under the service `pipedrive-mcp` with the
  domain as the account. It is the only writer of the keyring (D-63).
- `internal/userconfig` is the non-secret pointer file under
  `os.UserConfigDir()` that records the default domain, written 0600 in
  a 0700 directory (D-64).
- `internal/version` holds the build's identity: a release stamps it,
  and a `go install` build reports the module version Go embeds.
  `--version` prints it.
- `internal/pipedrive` is the REST client, one file per resource, with
  no MCP imports (D-70, D-71). `client.go` holds the request path: the
  `x-api-token` header, the redirect refusal, the retry policy and the
  log lines (D-62, D-55, D-54, D-59). `errors.go` maps a status to a
  sentinel, with the 403 split and the v1 note on a 410 (D-51, D-52,
  D-05). `fieldcache.go` is the custom-field cache, in both directions
  (D-18, D-19, D-22). `liveness.go` answers which ids still exist
  (D-13). `notes.go` and `users.go` are the v1 carve-out (D-03), and
  `probe.go` the startup probe (D-06). `types.go` holds the wire types
  and the generic envelopes, and `spec_test.go` holds them to
  Pipedrive's v2 description, whose extract sits in the package's
  `testdata` directory (D-72).
- `internal/tools` is the MCP surface, one file per resource with its
  `Register` function, each tool added through `mcp.AddTool` (D-74).
  `guard.go` is the write guard: the field tables, the projections,
  `guardedWrite` and the overwrite refusal (D-37, D-38, D-39, D-40,
  D-30). `errors.go` turns an error into a `[class]` result and holds
  the refusal sentinel (D-50, D-51). `annotations.go` holds the two hint
  postures (D-48), `options.go` the dry-run floor every handler ORs in
  (D-33), and `resources.go` the templates that mirror the `get_` tools
  (D-16). `search.go` holds the liveness filter (D-11, D-14), and
  `pagination.go` and `sort.go` the shared limits and sort keys (D-15).
  Every output type is a shadow of its upstream type, defined beside its
  tool (D-17).
- `internal/server` is the constructor that registers every tool package
  and warms the field caches off the critical path (D-19). `server.New`
  takes the whole configuration, and `NewForSchemaDump` builds a server
  with no client for the schema dump, which lists the surface through an
  in-memory client (D-34, D-73).
- `internal/server/testutil` is the in-memory MCP transport that the
  tool tests drive handlers through, with `CallTool` and `CallToolInto`.
- `internal/integration` is the live suite. Every test is behind
  `//go:build integration` and drives the server `internal/app` builds
  against a real workspace, and `write_test.go` states the write
  contract at its top (D-78, D-79).

Beside the packages, `scripts/gates` is every check the repository runs
on itself, one Go program whose commands `go run ./scripts/gates` lists
(D-77, D-83). `scripts/evals` holds the model-driven evals behind the
`live` build tag, which `make evals` runs (D-80, D-81). `packaging/mcpb`
holds the Claude Desktop bundle's manifest and its vendored schema, and
`packaging/registry` the MCP registry schema (D-95, D-96, D-97).

## Request flow

### Startup

`main` parses the flags and dispatches a subcommand. With
`--dump-schemas` it prints the surface and exits before it reads a
credential (D-73). Otherwise `app.Resolve` takes the domain from
`PIPEDRIVE_COMPANY_DOMAIN`, else the pointer file, loads and validates
the configuration for it, and resolves the token (D-64, D-66, D-63). A
missing or malformed setting exits non-zero before the server announces
itself (D-67).

`Settings.Connect` builds the client, and `main` logs the settings
without the token. It sends the probe, `GET /api/v2/dealFields?limit=1`,
and exits non-zero if the probe fails (D-06). `Runtime.NewServer` builds
the server, which registers every tool and starts the field-cache
warm-up in the background (D-34, D-19). The stdio transport then serves
until the client closes the pipe.

### A read

A client calls `get_deal`. The SDK decodes the call into the tool's
input type, and the handler checks the id and any enum before a request
goes out. The client sends the request with the token header and refuses
a redirect (D-62, D-55). It retries a 429, a 5xx or a lost connection,
and decodes the envelope or classifies the failure into a sentinel
(D-54, D-51). The handler resolves the deal's custom fields to the
workspace's names and its dropdowns to labels through the cache (D-18).
It returns the shadow type with a link to the deal in Pipedrive's web
app (D-17).

`search` adds one step. After `/itemSearch` answers, it asks
`/organizations` and `/persons` which of the hits still exist and drops
the rest. A failed check fails the search (D-11, D-12, D-14). Because
the drop happens after Pipedrive has paged, a page can come back short
or empty with `next_cursor` set (D-15).

### A write

A client calls `manage_deal` with `action: update`. The handler checks
the action and the input, and ORs the server's floor into `dry_run`
(D-33). Custom fields are encoded by name and label, and a field it
cannot translate refuses the whole write (D-22, D-23). Then the one
spine runs (D-37):

1. Read the deal. An archived deal is refused here until it is
   unarchived (D-45).
2. Check `expect_version` against the stored `update_time` (D-31).
3. Extend the field table with this write's custom fields, predict the
   record and diff it (D-38, D-40).
4. Stop if nothing would change (D-36).
5. Refuse every populated field that `overwrite` does not name (D-30).
6. Stop and report if this is a rehearsal (D-32).
7. Send the PATCH. It is sent again only after a 429 or a failed dial,
   when Pipedrive cannot have applied it (D-54).

The result diffs Pipedrive's echo against the read, so it names what
Pipedrive changed, a derived field included (D-35). A named transition
such as `mark_won` sends only its own field and needs no `overwrite`
(D-43). A delete reads the record, checks the version and honors the
floor, and takes no permitting argument (D-47).

## Invariants

- Stdout carries only MCP JSON-RPC frames (D-58).
- No update reaches Pipedrive without the read, the version check, the
  overwrite refusal and the dry-run gate, in that order (D-37).
- No call can lift `PIPEDRIVE_DRY_RUN` (D-33).
- No field is written that the diff cannot see, and no collection
  renders two different values alike (D-38, D-39).
- A write is never repeated once it may have reached Pipedrive (D-54).
- Every tool error carries a class from the closed set (D-51).
- No log line carries a query string, a body or the token (D-59, D-61).
- No tool clears a field, and no tool claims to (D-42).
- `internal/pipedrive` imports nothing from the MCP SDK (D-70).
- Notes and whoami are the only v1 dependencies (D-03).
- The v1 tools sit outside the compatibility promise, and every other
  tool is inside it (D-04, D-75).
- `make check` and CI run the same gates (D-84).

## Cross-cutting concerns

**Errors.** Every tool error is a tool result of the form
`[class] message`, and the class tells the caller what to do next. The
sentinels are declared in `internal/pipedrive/errors.go`, and
`errorClass` in `internal/tools/errors.go` maps each to its class.
[troubleshooting.md](troubleshooting.md) documents each class for a
reader, and `TestTroubleshootingDocListsEveryErrorClass` holds the doc
to the code. An upstream failure or a refused input is a tool-execution
error, and JSON-RPC errors are the SDK's, for a call it rejects before a
handler runs (D-50, D-51). A 403 splits into `permission` and
`business_rule` by the upstream message (D-52), and a 410 is `gone`
(D-05). The model reads the upstream message alone, and the status and
path go to the debug log (D-53).

**Retries and timeouts.** A read retries a 429, a 5xx or a lost
connection, and a write retries a 429 and a failed dial alone, so a
write that may have landed is never sent twice (D-54). Every attempt is
bounded by `PIPEDRIVE_HTTP_TIMEOUT` (D-56). The client builds its own
`http.Client`, refuses redirects and neither pools nor rate-limits on
its own (D-55, D-57). The numbers are the code's, in
`internal/pipedrive/client.go`.

**Custom fields.** One cache per resource loads the field definitions
lazily, once, and is warmed at startup. A failed load is soft on a read,
which returns hashes, and fatal on a write, which would invent a field.
`refresh_field_cache` reloads all of them (D-19, D-25). An unknown key
or option passes through raw, on the bet that a raw id is recoverable
where nothing is not (D-18).

**Logging.** Logs go to stderr through `slog`, as text or JSON, at the
level `LOG_LEVEL` sets (D-58). The log sites are these. At startup,
`credentials resolved` carries a `settings` group with `workspace`,
`domain_source`, `token_source` and `dry_run`, and `auth probe ok`
carries `workspace`. Each response logs `pipedrive response` at debug
with `path`, `method`, `status` and `duration`. A transport failure logs
`pipedrive request failed` as a warning with `path`, `method`,
`attempt`, `duration` and `error`. The path carries no query string
(D-59). Tool handlers log nothing, so the client's transcript is the
only record of a call (D-60).

**Concurrency.** The server is request and response, and the client is
safe for concurrent use. The only background work is the field-cache
warm-up, one goroutine that starts one more per resource, bounded by its
own timeout and the parent context (D-19).

**Configuration and secrets.** Every setting is an environment variable,
read once at startup through one registry, and no flag configures the
server (D-66). The one file is the non-secret domain pointer `login`
writes (D-64). The token lives in the keyring or the environment, and
never in a log line, an error or a URL (D-63, D-61, D-62). Every setting
is in [configuration.md](configuration.md).

**Testing.** Unit tests drive the handlers through an in-memory MCP
session against fakes, and `make cover` holds a statement-coverage floor
(D-82). A fake agrees with whatever it was written from. So the live
suite drives the real server against the configured workspace, a real
CRM, with its write probes behind a second opt-in, and the evals score
whether a model can use the tools at all (D-78, D-79, D-80, D-81). None
of the live checks runs in CI. How to run them is in
[CONTRIBUTING.md](../CONTRIBUTING.md#the-live-run).

**Gates.** `go run ./scripts/gates` lists every gate, and `make check`
runs the set CI runs, which the `parity` gate holds (D-84). A gate is
watched failing before it is trusted, and refuses to pass on an empty
input (D-83). The docs gate holds every D-ID citation to an entry, every
Held by cell to something that exists, the codemap to the tree and every
integration test a comment names to the suite.

**The surface as a contract.** `--dump-schemas` prints what an in-memory
client is served: the instructions, the tools and the resource templates
(D-73). On a pull request, `schema-ack` compares the dumps of the
merge-base and the head and needs a commit footer naming each changed
item (D-75, D-76).

**Distribution.** A release is built from a tag: six archives and a
Claude Desktop bundle, one cosign signature over `checksums.txt`, an
SBOM per archive and build provenance, from a build the release job
checks is reproducible (D-92, D-93, D-94). Its notes are that version's
CHANGELOG section (D-91). The bundle's settings derive from
`config.Vars` (D-96), and the registry entry is published by its own
workflow (D-97). Releasing is in [release.md](release.md), and fixing a
symptom in [troubleshooting.md](troubleshooting.md).

## Decisions

| ID | Decision | Status | Held by |
| --- | --- | --- | --- |
| D-01 | One Go binary serves one Pipedrive workspace over stdio; nothing is hosted | accepted | smoke, review |
| D-02 | The server serves the current MCP revision beside the handshake revisions, through an SDK kept current | accepted | smoke, TestSDKVersionMatchesGoMod, staleness |
| D-03 | Pipedrive v2 is the default, and notes and whoami are the only v1 carve-outs | accepted | TestClient_WhoAmI_UsesV1, TestClient_ListNotes_FiltersAndV1Pagination, review |
| D-04 | The v1 tools sit outside the compatibility promise and leave in a MINOR release if v1 stops answering | accepted | TestV1CarveOutStillAnswers, integration |
| D-05 | A 410 is [gone], never retried, and on a v1 path says the endpoint has no v2 equivalent | accepted | TestClassify_410ExplainsOnlyOnV1, TestEverySentinelHasItsOwnClass |
| D-06 | The startup probe is GET /api/v2/dealFields?limit=1, its answer discarded and its failure fatal | accepted | TestProbeAuth_SucceedsAgainstTheWorkspace, TestNewProbeClient, integration |
| D-07 | Products, leads, files, projects, goals and the unmodeled resources are outside the 1.x surface | accepted | review |
| D-08 | No tool merges two records | accepted | review |
| D-09 | The surface follows the shipped Google Workspace MCP servers: discrete reads, one manage_ tool per noun taking an action | accepted | schema-ack, review |
| D-10 | Tool descriptions follow one house style, and its mechanical rules are checked on the dump | accepted | descriptions, TestActionFieldDescribesEveryAction, review |
| D-11 | search drops organizations and persons Pipedrive's index keeps after a delete | accepted | TestSearch_DropsDeletedOrganizations, TestSearch_DropsDeletedPersonsAndBillsPerType, TestSearch_NoCheckableTypesCostsNoExtraCall, TestWrite_SearchDropsWhatItDeleted |
| D-12 | search never checks deals for liveness | accepted | TestSearch_NeverChecksDeals, TestWrite_SearchDropsWhatItDeleted |
| D-13 | Which types are checked is policy in internal/tools, over a LiveIDs primitive in internal/pipedrive | accepted | TestClient_LiveIDs_RefusesTypesItCannotAnswerFor, review |
| D-14 | A failed liveness check fails the search | accepted | TestSearch_LivenessCheckFailureFailsTheSearch, TestErrorText_HintReachesTheCallerAndKeepsTheClass |
| D-15 | One call is one page: lists default to 25 rows and cap at 100, and a short or empty page with next_cursor is not the end | accepted | TestClampLimit, TestSearch_EmptyAfterFilteringStaysTruncated, TestListDeals_CursorPages |
| D-16 | Five resource templates mirror the get_ tools through the same code | accepted | TestResources_ReadMirrorsTheGetTool, TestResources_EveryTemplateResolves, TestResource_MirrorsTheGetTool |
| D-17 | LLM-facing output types are parallel shadows of the upstream types, never embeds | accepted | review |
| D-18 | Custom fields read under the workspace's names and dropdowns as labels; an unknown key or option passes through raw | accepted | TestFieldCache_Resolve, TestFieldCache_ResolveOptionLabels, TestFieldCache_ResolveUnknownOption, TestCustomFields_ResolveToWorkspaceWords |
| D-19 | Field metadata is one lazy cache per resource, warmed at startup, soft on failure and reloaded by refresh_field_cache | accepted | TestFieldCache_LazyAndOnce, TestFieldCache_FetchErrorDoesNotPanic, TestNew_WarmsAllFieldCaches, TestNew_WarmGoroutineCancelsWithParentContext, TestRefreshFieldCache_RunsInParallel, TestFieldCache_ReloadDuringInFlightLoad |
| D-20 | A stored option value's shape says whether it is one id or a set, and ids compare on a rendered key | accepted | TestOptionKey, TestFieldCache_ResolveOptionLabels |
| D-21 | Read dropdown labels through include_option_labels | rejected | review |
| D-22 | Custom fields are written by name and dropdowns by label through FieldCache.Encode alone | accepted | TestFieldCache_Encode, TestManageUpdate_CustomFieldReachesTheRequestAndTheReport, TestWrite_CustomFieldRoundTripsByLabel |
| D-23 | Encode refuses rather than guesses, names every bad field, and one bad field refuses the whole map | accepted | TestFieldCache_EncodeRefusals, TestFieldCache_EncodeNamesEveryBadField, TestFieldCache_EncodeRefusesTheWholeMap, TestFieldCache_EncodeRefusesAFieldNamedTwice, TestManageDeal_CustomFieldEncodeRefusalStopsTheWrite |
| D-24 | A built-in field is refused in custom_fields whether named by label or by key | accepted | TestFieldCache_EncodeRefusals |
| D-25 | A failed field-cache load refuses the write | accepted | TestFieldCache_EncodeFailsWhenTheCacheDoes |
| D-26 | Encode accepts an option id where a label is expected | accepted | TestFieldCache_Encode |
| D-27 | Every write reads its target first and refuses what it would destroy that the caller cannot see | accepted | TestGuardedWrite_RefusalNeverReachesPut, TestGuard_RefusesAPopulatedFieldAndNamesIt, TestManageDeal_Update_RefusesPopulatedFieldsAndNamesThem |
| D-28 | force exists only where a write destroys collateral the caller cannot see, and no tool has one | accepted | TestWrite_DeletingAParentLeavesWhatHangsOffIt, review |
| D-29 | A refusal names what it protects and the argument that permits the write | accepted | TestGuard_RefusesAPopulatedFieldAndNamesIt, TestManageNote_Update_RefusesReplacingPopulatedFields |
| D-30 | overwrite lists the fields an update may replace, taken from the diff that makes the changed report | accepted | TestOverwrite_NamingSomeFieldsStillRefusesTheRest, TestOverwrite_NamingEveryFieldPermitsTheWrite, TestGuardedWrite_FillingAnEmptyFieldNeedsNoPermission, descriptions |
| D-31 | expect_version refuses a write whose record moved since the read that informed it | accepted | TestGuard_StaleExpectVersionRefuses, TestGuardedWrite_VersionCheckPrecedesEverything, TestDelete_RefusesWhenTheRecordMoved |
| D-32 | dry_run is a per-call input on every write, and validation still runs under it | accepted | TestGuardedWrite_DryRunNeverReachesPut, TestDelete_DryRunSendsNothing, TestManageNote_Update_DryRun_SuppressesUpstream |
| D-33 | PIPEDRIVE_DRY_RUN is a floor every write honors, and a call can only turn a rehearsal on | accepted | TestRuntime_NewServer_CarriesTheDryRunFloor, TestManageDeal_DryRunFloorAndPerCall, TestManageNote_ServerWideDryRunIsAFloor, TestDelete_HonorsTheDryRunFloor |
| D-34 | internal/app is the one startup assembly, and server.New takes the whole configuration | accepted | TestRuntime_NewServer_CarriesTheDryRunFloor, TestResolveWith |
| D-35 | The dry run predicts what this server sends, and the result diffs Pipedrive's echo against the pre-write read | accepted | TestGuardedWrite_OverwritePermitsAndReportsTheEcho, TestManageDeal_Update_HappyPathAndChangedReport, TestManageDeal_DryRunOverlayPredictsWithoutWriting |
| D-36 | A write that would change nothing is skipped and reported unchanged | accepted | TestGuardedWrite_NoOpNeverReachesPut, TestManageDeal_NoOpWriteReportsNothingChanged |
| D-37 | One guardedWrite spine fixes the order of every update | accepted | TestGuardedWrite_RefusalNeverReachesPut, TestGuardedWrite_DryRunNeverReachesPut, TestGuardedWrite_VersionCheckPrecedesEverything, TestGuardedWrite_UpstreamErrorsStopTheSequence |
| D-38 | One field table per resource drives the diff, the guard and the request builder | accepted | TestEveryWritableFieldHasAFieldSpec, TestEverySettableFieldReachesTheDiff, TestManageDeal_UpdateOverlayCoversEveryField |
| D-39 | A collection's projection renders every sub-field a write can set | accepted | TestCollectionProjectionsRenderEverySubField, TestCollectionProjectionsSeeATruncation, TestCollectionProjectionsCannotCollide, TestManagePerson_TruncatingContactPointsIsGuarded |
| D-40 | Custom fields reach the guard through per-call specs, with Custom and CustomOf as one pair | accepted | TestCustomFieldsReachTheDiffAndTheGuard, TestCustomFieldSetOrderIsNotAChange, TestWithCustomFieldsDoesNotMutateTheBaseTable, TestGuardedWriteRefusesAPopulatedCustomField |
| D-41 | Optional scalars on an update are pointers: nil omits and a value sends | accepted | TestClient_PatchV2_NilFieldsNeverReachTheWire, TestClient_PatchV2_SendsOnlyWhatIsSet, TestClient_UpdateActivity_DoneFalseIsSent |
| D-42 | No tool clears a field | accepted | TestClient_PatchV2_EmptyStringIsSentVerbatimNotAsNull, TestFieldCache_EncodeRefusals, review |
| D-43 | The named transitions take no overwrite and send only the field they name | accepted | TestGuard_TransitionNeedsNoOverwrite, TestManageDeal_TransitionIgnoresCustomFields, TestManageActivity_TransitionIgnoresDescriptiveFields, TestInstructionsNameEverySelfAuthorizingAction, TestREADMENamesEverySelfAuthorizingAction |
| D-44 | Every close has its reopen | accepted | TestManageDeal_Transitions, TestManageActivity_CompleteAndReopen, TestWrite_DealReopensFromWonAndFromLost, TestWrite_ActivityCompletesAndReopens |
| D-45 | An archived deal lives in its own collection and is refused for edits until unarchived | accepted | TestManageDeal_RefusesToEditAnArchivedDeal, TestManageDeal_UnarchiveReachesAnArchivedDeal |
| D-46 | Destructive actions register by default and guard at call time | accepted | TestDelete_SendsTheDeleteAndReportsIt, review |
| D-47 | A soft delete takes dry_run and expect_version and no permitting argument | accepted | TestDelete_DryRunSendsNothing, TestDelete_RefusesWhenTheRecordMoved, TestManageNote_Delete_DryRun_SuppressesUpstream, TestManageNote_Delete_AlreadyInactiveIsNoOp |
| D-48 | Annotations are advisory: reads are read-only and idempotent, and every manage_ tool is destructive | accepted | schema-ack, review |
| D-49 | A dry-run create shows the defaults Pipedrive would fill, read with GETs alone | accepted | TestCreateDeal_DryRunShowsPipedrivesDefaults, TestCreate_DryRunShowsTheOwnerPipedriveGives, TestWrite_CreateDefaultsMatchTheDryRun |
| D-50 | Upstream and validation failures are tool-execution errors; JSON-RPC errors are the SDK's | accepted | TestManageNote_MissingActionIsASchemaError, TestManageWrite_UpstreamErrorsSurface |
| D-51 | Every tool error starts with a [class] tag from one closed set, classified by sentinel | accepted | TestEverySentinelHasItsOwnClass, TestErrorClassReadsAnAPIError, TestTroubleshootingDocListsEveryErrorClass, TestClassify, TestGetDeal_NotFoundMapsToItsClass |
| D-52 | A 403 is split into permission and business_rule by signal substrings in the upstream message | accepted | TestClassify, TestClassify_ErrorInfoFallback |
| D-53 | The model reads the upstream message alone; status and path go to the debug log | accepted | TestErrorText_UnhintedUpstreamIsUnchanged, TestClient_ErrorMessageComesFromTheBody, TestAPIError_ErrorString |
| D-54 | A read retries 429, 5xx and a lost connection; a write retries 429 and a failed dial alone | accepted | TestClient_429RetriesThenSucceeds, TestClient_5xxRetriesThenFails, TestClient_BackoffDoublesAndCaps, TestClient_RetryAfterHonored, TestClient_AWriteWhoseAnswerIsLostIsSentOnce, TestClient_AReadWhoseAnswerIsLostIsRetried, TestShouldRetryNetwork, TestClient_PatchV2_NoRetryOn5xx |
| D-55 | Redirects are refused | accepted | TestClient_RefusesRedirects |
| D-56 | Every outbound attempt is bounded by PIPEDRIVE_HTTP_TIMEOUT | accepted | TestLoad_Defaults, TestLoad_AllOverrides |
| D-57 | The client neither pools nor rate-limits on its own | accepted | review |
| D-58 | Stdout carries only MCP JSON-RPC frames, and logs go to stderr through slog | accepted | forbidigo, smoke, TestTheGeneratedLauncherRefusesToStdout |
| D-59 | A log line carries path, method, status, attempt and duration, never a query string, a body or the token | accepted | TestClient_NoLogOrErrorCarriesTheSearchTerm, TestSettings_LogValue_RedactsTheToken |
| D-60 | No tool handler logs, so the server keeps no record of tool calls | accepted | review |
| D-61 | The token is unexported on Settings, and LogValue and String redact it | accepted | TestSettings_LogValue_RedactsTheToken |
| D-62 | The token travels only in the x-api-token header | accepted | TestClient_HappyPath |
| D-63 | The token lives in the OS keyring, and PIPEDRIVE_API_TOKEN overrides it | accepted | TestResolve_EnvWinsOverKeyring, TestResolve_KeyringFallback, TestResolve_EnvWorksWhenKeyringBroken, TestResolve_BrokenKeyringNoEnv |
| D-64 | The domain resolves from the environment, then a non-secret pointer file, then an error | accepted | TestResolveDomain, TestSave_FilePermsAre0600, TestSave_DirPermsAre0700, TestClearDefaultDomainIfMatches_OnlyMatching |
| D-65 | The domain is validated as a Pipedrive subdomain before it reaches a URL or the keyring | accepted | TestLoad_Invalid, TestPromptDomain_RejectsEmpty |
| D-66 | Every setting is an environment variable registered in config.Vars and read once through config.Lookup | accepted | TestEveryReadGoesThroughLookup, TestEachVariableIsSpelledOnce, TestEveryVariableTheCodeNamesIsInEnvVars, TestASettingOfSpacesIsUnset, TestLoadReadsEachVariableOnce |
| D-67 | A bad setting exits non-zero before the server announces itself | accepted | TestLoad_Invalid, TestLoad_Required |
| D-68 | status reports what resolved, and status --json is one stable object | accepted | TestEveryRefusalIsAWholeObject, TestTheObjectIsExactlyOneValue, TestASkippedProbeIsNotAFailure, TestTextNamesWhatActuallyFailed |
| D-69 | A mistyped subcommand exits non-zero | accepted | TestAnUnknownCommandIsReported |
| D-70 | internal/pipedrive has no MCP imports, and internal/tools holds schemas, validation and presentation | accepted | depguard, review |
| D-71 | The Pipedrive client is hand-written, not generated | accepted | depguard, review |
| D-72 | Pipedrive's v2 description is a test oracle for the response types | accepted | TestUpstreamTypesMatchTheSpec, TestUpstreamTypeCoverage |
| D-73 | --dump-schemas prints what an in-memory client is served | accepted | TestTheDumpIsWhatAClientIsServed, TestTheDumpListsResourcesAndPrompts |
| D-74 | Every tool is added through mcp.AddTool, which infers its schemas | accepted | smoke, TestRegister_ListsItsToolsWithTheirSchemas |
| D-75 | The tool surface is the versioning contract, and a break needs a MAJOR | accepted | schema-ack, TestSchemaAckAsksForBreakingChangeOnlyForAReleasedBreak |
| D-76 | A pull request that changes the surface names each changed item in a commit footer | accepted | schema-ack, TestSchemaAckNeedsEveryItemNamed, TestABreakIsABreakOfARelease, TestARefusalNamesTheFooterThatFixesIt |
| D-77 | Every repository script is Go | accepted | review |
| D-78 | A live suite behind the integration tag drives the server internal/app builds, for what a fake cannot answer | accepted | integration, TestV1CarveOutStillAnswers |
| D-79 | Live write probes need a second opt-in, stay in their own pipeline, and restore and verify every write | accepted | integration-writes |
| D-80 | Evals drive a model through the tools and score the end state and the trace | accepted | evals, TestEveryPromptSubstitutesFully, TestEveryTaskIsAnswerable, TestParseAttributesResultsToTheRightCall |
| D-81 | The eval census fails a run that created records outside its fixture, and reports edits | accepted | TestUnaccountedSeparatesCreatedFromEdited, TestUnaccountedIgnoresTheFixture, TestUnaccountedIsSilentWhenNothingMoved |
| D-82 | Coverage is held at 80% on internal/pipedrive, internal/tools, internal/credentials and internal/app | accepted | cover, TestFloorProblemsNamesEachPackageBelow, TestCoverageRefusesAnEmptyProfile |
| D-83 | A check is watched failing, and a gate refuses to pass on an empty input | accepted | TestParityRefusesToRunOnTooLittle, TestReleaseProblemsRefusesToReadNothing, descriptions |
| D-84 | make check and CI run the same gates | accepted | parity, TestParityCatchesDivergence, TestParityHoldsThePullRequestGates, TestParityRequiresAVetPassPerBuildTag |
| D-85 | Every action and tool is pinned, and make check runs each tool at the version CI pins | accepted | pins, lint, licenses, vuln |
| D-86 | Every action is pinned by commit SHA and every installed tool by exact version | accepted | pins, TestPinsGate, TestEveryActionIsClassified |
| D-87 | A direct dependency more than six months behind fails unless go.mod pins it with a reason | accepted | staleness, TestPinnedWithReasonNeedsTheReason |
| D-88 | Dependencies are held to a license allow-list | accepted | licenses |
| D-89 | Nothing from a real Pipedrive account is committed, in the tree or the history | accepted | leaks, leaks-history, TestFindLeaks, TestHistoryProblemsReadsWhatWasPublished |
| D-90 | A change to watched source needs a CHANGELOG entry under [Unreleased] | accepted | changelog, TestAddedUnderUnreleasedCountsTheCutSectionOnly, TestTouchesWatchedReadsThePaths |
| D-91 | The release notes are the CHANGELOG section, and every version heading has its link | accepted | changelog-links, TestSectionForTakesOnlyItsOwnVersion |
| D-92 | One cosign signature over checksums.txt covers every archive, SBOM and the bundle, beside build provenance | accepted | review |
| D-93 | Release builds are reproducible | accepted | review |
| D-94 | A release is six archives named as the sibling servers name theirs, and the bundle carries the Windows amd64 binary alone | accepted | mcpb, TestTheReleaseMatrixIsTheSix, TestTheArchivesAreNamedNameVersionOSArch, TestReleaseDocProblems, TestTheBundleReachesEveryPair |
| D-95 | The bundle manifest is validated against a vendored schema pinned by SHA256 | accepted | mcpb, TestCommittedManifestSatisfiesItsSchema, TestVendoredSchemaMatchesItsRecordedHash, TestTheSchemaRefIsATagNotABranch |
| D-96 | The bundle's settings come from config.Vars, and its defaults are started through config.Load | accepted | mcpb, TestBundleVarsAreDerivedFromTheSettings, TestDefaultsProblemsReportsEveryUnreadableDefaultAndTheRest, TestSettingProblemsRefuseABundleWithNoSecret |
| D-97 | The registry entry is published by its own workflow, from checksums.txt after cosign verifies it | accepted | TestTheRegistryEntryIsBuiltFromTheRepository, TestTheBundleRowMustBeExactlyOne, TestRegistrySchemaMatchesItsRecordedHash, review |

### D-01 One Go binary serves one Pipedrive workspace over stdio; nothing is hosted

The server is one Go binary that an MCP client starts as a subprocess
and talks to over stdio. It holds one Pipedrive workspace's domain and
one API token, and every call acts as the user behind that token.
Nothing is hosted, and the process keeps no state between runs.

The reason is reach. A Pipedrive API token carries every permission of
the user it belongs to, so a server per person keeps each call inside
that person's own access, with the token in their own keyring (D-63). A
stdio process has no port to expose and no session of its own to
authenticate. It speaks to one peer, `https://{domain}.pipedrive.com`,
and nothing else.

Rejected: a hosted server holding many people's tokens, and an HTTP
transport, which needs authentication of its own and a process that
outlives the client.
Cost: the server trusts whatever connects to its stdio, so it is only as
safe as the client that starts it. A person with two workspaces runs two
processes, each with its own `PIPEDRIVE_COMPANY_DOMAIN`.

### D-02 The server serves the current MCP revision beside the handshake revisions, through an SDK kept current

The server serves the current MCP revision, `2026-07-28`, beside the
handshake revisions before it, and the SDK negotiates which one a client
gets. The Go SDK is kept current rather than pinned to a version that
works.

The server implements none of the protocol itself: the revisions it
answers are the ones its SDK pin answers. `2026-07-28` drops the
`initialize` handshake and makes `server/discover` mandatory, and a
client that speaks only that revision fails against a server that does
not. An available SDK upgrade and a protocol era change look the same
from a version number, so the `smoke` gate asserts the revision list
itself: it sends a legacy handshake and a `server/discover` call and
requires the current revision in the answer. The `staleness` gate fails
a dependency more than six months behind (D-87).

Rejected: pinning the SDK, which leaves the server on the legacy side of
an era boundary with every gate green; asserting the dependency version
rather than the revisions a client is served.
Cost: what the server serves moves with the SDK pin. `sdkVersion` in
`internal/server/dump.go` has to move with `go.mod`, which
`TestSDKVersionMatchesGoMod` holds.

### D-03 Pipedrive v2 is the default, and notes and whoami are the only v1 carve-outs

Every call goes to Pipedrive API v2, except notes and the signed-in
user. Those come from v1, through `Client.doV1`, `postV1`, `putV1` and
`deleteV1` in `internal/pipedrive/client.go`, and back `get_note`,
`list_notes`, `manage_note` and `whoami`.

Pipedrive's v2 description has no `/notes` and no `/users`
([v2 description](https://developers.pipedrive.com/docs/api/v1/openapi-v2.yaml)),
so for those two there is nothing on v2 to call. Pipedrive deprecated a
list of v1 endpoints and then put them out of support; the list names
activities, deals, persons, organizations, products, pipelines, stages
and itemSearch, not notes and not `/v1/users/me`, and says it does not
affect the whole v1 platform
([deprecation of selected v1 endpoints](https://developers.pipedrive.com/changelog/post/deprecation-of-selected-api-v1-endpoints),
[deprecated v1 endpoints out of support](https://developers.pipedrive.com/changelog/post/deprecated-apiv1-endpoints-become-out-of-support)).
Everything else this server reads or writes has a v2 endpoint.

Rejected: a third v1 carve-out for clearing a field, which v1's PUT can
do and v2 cannot (D-42).
Cost: the notes tools and `whoami` rest on an API version Pipedrive
treats as the older one. A further v1 dependency needs a CHANGELOG entry
saying why no v2 endpoint exists, a comment at the call site and the
owner's go-ahead.

### D-04 The v1 tools sit outside the compatibility promise and leave in a MINOR release if v1 stops answering

The v1 tools, `get_note`, `list_notes`, `manage_note` and `whoami`, sit
outside the compatibility promise. If v1 stops answering for them, they
are removed in a MINOR release. The CHANGELOG's preamble states the
exception, and every other tool is covered in full (D-75).

A break of the tool surface needs a MAJOR release. These tools sit on an
API a third party can retire at will, and there is nothing to migrate
them to, because v2 exposes no `/notes` and no `/users` (D-03). Keeping
them inside the promise would hand Pipedrive the power to force a MAJOR.
`TestV1CarveOutStillAnswers` checks that whoami, list_notes and get_note
still answer, and asserts the aggregate: a retirement fails them
together, whatever status it arrives as.

Rejected: a migration, which has no destination; letting a third party
decide when this project cuts a MAJOR.
Cost: the carve-out check runs only when someone runs `make
integration`, so a retirement between releases reaches a user first.
Nothing checks Pipedrive's v2 description for `/notes` and `/users` at
each refresh, or runs the carve-out check on a schedule; whether to do
both is open (#117).

### D-05 A 410 is [gone], never retried, and on a v1 path says the endpoint has no v2 equivalent

A 410 from Pipedrive is classed `[gone]` and never retried. On a v1 path
the message adds that the endpoint is on API v1 and has no v2
equivalent, so changing the request will not help.

410 is the code Pipedrive documents for a resource that is gone
([HTTP status codes](https://pipedrive.readme.io/docs/core-api-concepts-http-status-codes)).
It is neither the caller's mistake nor a passing failure. Classed as
`[validation]`, it would send the model to change the one thing that was
right; retried, it would spend calls on an answer that cannot change.
The v1 note matters because the v1 tools have no fallback (D-03): a
model told only "gone" may read it as a deleted record rather than a
retired endpoint. The note is a package constant with nothing
interpolated, and `TestClassify_410ExplainsOnlyOnV1` holds it to v1
paths.

Rejected: falling through to `[validation]`, which tells the model its
input was wrong.
Cost: a retired route may as well answer 404, 403 or an edge's HTML
error page, and those classify as something other than `[gone]`. Only
the live carve-out check, which asserts on the tools together rather
than on a status, sees that shape (D-04).

### D-06 The startup probe is GET /api/v2/dealFields?limit=1, its answer discarded and its failure fatal

At startup the server sends `GET /api/v2/dealFields?limit=1`, discards
the answer and exits non-zero if it fails. The model never sees the
probe; `--skip-probe` skips it for the stdio smoke test alone.

The probe confirms the domain and the token before the server announces
itself, so a wrong token is a clear exit rather than a server whose
every tool fails. `/dealFields` is a v2 endpoint, every workspace has
deal fields, and one row is a small answer
([getDealFields](https://developers.pipedrive.com/docs/api/v1/DealFields#getDealFields)).
`login` and `status` run the same probe through `NewProbeClient`, so a
token is checked before it is stored. The integration test
`TestProbeAuth_SucceedsAgainstTheWorkspace` checks the probe against a
real workspace.

Rejected: a users call, since `/api/v2/users` does not exist and
`/v1/users/me` would make startup depend on the v1 carve-out (D-03).
Cost: a token that passes the probe can still lack permission for a
resource, which surfaces later as `[permission]`. `--skip-probe` must
stay out of production use, and nothing but its help text keeps it
there.

### D-07 Products, leads, files, projects, goals and the unmodeled resources are outside the 1.x surface

Products, leads, files, projects and goals have no tools in the 1.x
surface. Nor do webhooks, mail and mail threads, subscriptions and
recurring revenue, saved filters, currencies, deal-to-lead conversion,
followers, deal participants as a managed relation, or the creation and
alteration of custom-field definitions. A field's values are readable
and writable (D-18, D-22); its definition is not.

Each of these is a resource of its own, with its own write semantics,
and a tool for it would need the guarded-write contract worked out for
it (D-27). Products, leads, files, projects and goals belong together in
a milestone of their own rather than in pieces inside 1.x. The README
lists them, so a missing tool reads as a decision rather than an
oversight.

Rejected: tools for these resources added inside the 1.x line one at a
time.
Cost: `search` finds products, files and leads, so a model can turn a
name into an id and then do nothing with it. Whether and when products,
leads, files, projects and goals get their milestone is open (#119).

### D-08 No tool merges two records

No tool merges two persons or two organizations, though Pipedrive's v1
API can
([mergePersons](https://developers.pipedrive.com/docs/api/v1/Persons#mergePersons)).

A merge keeps one record, and where the two conflict, its values win.
The record whose values lose is one the caller has not read. The write
guard works by reading the target and naming every populated field a
write would replace (D-27, D-29). A merge destroys values on the record
that disappears, and there is no refusal that could name what it was
about to lose, so the guard has nothing to stand on. The reason is the
guard contract rather than the API.

Rejected: a merge tool, which destroys one record's values in favor of
another's with no refusal able to name the loss.
Cost: duplicates are merged in Pipedrive's own UI. A model that finds
two records for one company can report both, and can move what it is
asked to move with `manage_` writes, which the guard covers.

### D-09 The surface follows the shipped Google Workspace MCP servers: discrete reads, one manage_ tool per noun taking an action

The tool surface follows the shipped Google Workspace MCP servers,
google-drive-mcp, google-sheets-mcp, google-docs-mcp and
google-chat-mcp. Reads stay discrete: `get_X`, `list_X` and `search` are
separate tools. Every mutation of one noun goes through one `manage_`
tool that takes an `action`.

Those servers are the MCP servers a model has most likely already seen.
Matching their conventions makes this one legible without being
explained: a model that has used `manage_sheet` knows what `manage_deal`
with `action: mark_won` is. Google keeps its reads discrete, three tools
rather than one with a mode switch, and collapses a noun's writes behind
one tool with an action. The write contract follows theirs too (D-27).

Rejected: a house style of its own; one read tool with a mode switch; a
separate `create_*` or `delete_*` tool per operation.
Cost: a `manage_` tool's input carries every action's fields, so which
fields an action reads is said in prose. The `action` description has to
name every action, which `TestActionFieldDescribesEveryAction` holds.

### D-10 Tool descriptions follow one house style, and its mechanical rules are checked on the dump

Every tool description follows one house style, taken from the Google
Workspace servers. It opens with what the tool does in one sentence and
spends the rest on what will burn the caller. It explains why inline,
uses `IMPORTANT:` at most once for the trap that returns a wrong answer
rather than an error, states a cost where a neighbor is much cheaper,
names what the API cannot do, points at the sibling tool and says what
the result sets up. An input description is lowercase with no trailing
period, with defaults, maxima and enums inline. Free-text inputs warn
against inventing a value.

A constraint the caller understands is one it can reason around, and a
bare prohibition gets retried verbatim. The `descriptions` gate reads
the schema dump, which is what a client is served, and checks the two
rules a program can: at most one `IMPORTANT:` per description, and every
`overwrite` input saying a refusal is NOT a retry signal (D-30).

Rejected: "state at most one key constraint", which cost accuracy
without buying brevity; bullets, or the schema restated in English.
Cost: the gate holds two rules, and the rest of the style is held by
review alone.

### D-11 search drops organizations and persons Pipedrive's index keeps after a delete

`search` drops the organizations and persons Pipedrive's index still
returns after a delete. After `/itemSearch` answers, it asks
`/organizations` and `/persons` which of the hits still exist and keeps
only those.

Pipedrive's search index keeps a deleted record and returns it with
nothing to say so: no `is_deleted`, no `active_flag`, no `status`, and
`/itemSearch` takes no parameter that excludes it
([searchItem](https://developers.pipedrive.com/docs/api/v1/ItemSearch#searchItem)).
Every `list_` tool drops deleted records, so without the check one
workspace reads clean through the lists and littered through search. A
deleted organization stays indexed indefinitely and a deleted person for
a while; the integration tests
`TestWrite_SearchDropsWhatItDeleted/organization` and
`TestWrite_SearchDropsWhatItDeleted/person` check both against the API.

Rejected: checking organizations alone, which leaves a deleted person in
results for as long as the index keeps it.
Cost: one extra call per checkable type a page holds, and none on a page
that holds neither. Because the drop happens after Pipedrive has paged,
a page can come back short, or empty with `next_cursor` still set
(D-15).

### D-12 search never checks deals for liveness

`search` never checks deals. A deal hit is returned as Pipedrive's index
gives it.

`/deals` leaves out archived deals, which are alive and live in their
own collection
([getArchivedDeals](https://developers.pipedrive.com/docs/api/v1/Deals#getArchivedDeals)).
Reading a deal's absence from `/deals` as deletion would drop live deals
out of search, which is worse than the stale hits the check exists to
remove. Pipedrive drops a deleted deal from its own index promptly, and
the integration test `TestWrite_SearchDropsWhatItDeleted/deal` checks
that, so a change on Pipedrive's side moves only that test.
`livenessPaths` in `internal/pipedrive/liveness.go` holds two
collections, and the deal's absence from it is stated there.

Rejected: reading absence from `/deals` as deletion, which drops
archived deals that are alive.
Cost: products, files and leads come back unchecked, because this server
has no collection to ask for them, and the tool description says a
deleted one can still appear. If Pipedrive stops dropping deleted deals,
search returns them until this decision changes.

### D-13 Which types are checked is policy in internal/tools, over a LiveIDs primitive in internal/pipedrive

Which item types `search` checks for liveness is policy in
`internal/tools`. `internal/pipedrive` offers the primitive: `LiveIDs`,
which answers which of a set of ids one collection still lists, and
`CanCheckLiveness`, which says which types it can answer for.

Which types get checked, what happens when a check fails and what the
model is told are decisions about the tool, so they belong with the tool
(D-14). The client stays a set of one-resource calls: `ItemSearch` asks
`/itemSearch` and nothing else. The tool asks `CanCheckLiveness` rather
than keeping the type list a second time, and `LiveIDs` refuses a type
it cannot answer for. `LiveIDs` chunks at the documented cap of 100 ids
per call, because silence about an id would read as deletion
([getOrganizations](https://developers.pipedrive.com/docs/api/v1/Organizations#getOrganizations)).

Rejected: an `ItemSearch` that fans out to another resource's endpoint,
which hides a round trip from every reader of it.
Cost: the rule lives in two packages, the list in the client and the
policy in the tool, and a reader of one has to know the other exists.

### D-14 A failed liveness check fails the search

When the liveness check fails, the search fails. The error keeps the
class of the failure underneath it and adds what to do: the search
matched, the check of its organizations or persons failed, and narrowing
`types` skips that check.

Returning the page unfiltered would be the defect the check exists to
fix, silently. A caller that cannot tell a deleted record from a live
one is the caller most likely to act on it. The guidance travels through
`withHint`, because the model reads only the upstream message otherwise
(D-53), and a bare rate-limit message would say the search itself was
rate-limited.

Rejected: returning the page unfiltered; a `liveness_checked: false`
field, which hands the model a set it cannot act on.
Cost: a rate limit or an outage on `/organizations` or `/persons` fails
a search whose index call succeeded. The caller's way out is `types`,
which only works when it does not need the type that failed.

### D-15 One call is one page: lists default to 25 rows and cap at 100, and a short or empty page with next_cursor is not the end

One tool call is one page. A `list_` tool returns 25 rows unless asked
for more, at most 100, and a `next_cursor` for the next page; nothing
pages on the caller's behalf. A page shorter than its limit, or empty,
is not the end while `next_cursor` is set. `list_pipelines` and
`list_stages` take no paging.

A model pays for every call and every row in its context. A tool that
followed cursors itself would make one call cost what the workspace's
size decides, with nothing in the request to say so. The default of 25
keeps a careless call small, and the cap of 100 bounds a deliberate one.
`search` filters after Pipedrive pages (D-11), so a short or empty page
with a cursor is ordinary, and the server instructions say so in
capitals.

Rejected: tools that follow `next_cursor` and return everything, which
hides the cost and the size of a sweep.
Cost: a model that stops at a short page reads an incomplete answer, and
only the descriptions stop it. `list_pipelines` and `list_stages` read
one page of Pipedrive's default size, on the bet that a workspace has
far fewer pipelines than that.

### D-16 Five resource templates mirror the get_ tools through the same code

The resource templates, `pipedrive://deals/{id}` and the same for
persons, organizations, activities and notes, return what the matching
`get_` tool returns. They call the same code the tools do.

Some clients attach a record rather than calling a tool. A template
written separately could describe the same record differently, with a
field one side shows and the other drops. Sharing the code means the two
cannot drift, and `TestResources_ReadMirrorsTheGetTool` and the
integration test `TestResource_MirrorsTheGetTool` hold the output equal.

Rejected: templates with their own rendering, which can drift from the
tools.
Cost: a URI carries no options, so `include_attendees` and
`include_notes` still need the tool. A template exists only for a
resource with a `get_` tool.

### D-17 LLM-facing output types are parallel shadows of the upstream types, never embeds

Every output type the model sees, such as `dealSummary` or
`activitySummary`, is a parallel shadow of its `internal/pipedrive`
type, defined beside its tool in `internal/tools` with its `jsonschema:`
tags. No summary type embeds a `pipedrive` type, and no type in
`internal/pipedrive` carries a `jsonschema:` tag. A nested type such as
`pipedrive.ContactPoint` may be used directly where its shape already is
what the model should see.

An embed makes the output schema an echo of Pipedrive's: the next field
Pipedrive adds, or this package decodes, appears in the tool's schema
without anyone deciding it should. A field or method added to an
embedded type can also break the type that embeds it
([embedding and backward compatibility](https://nigeltao.github.io/blog/2024/go-embedding-back-compat.html)).
A shadow is an explicit allow-list, and a per-tool choice, such as
`include_notes` stripping an activity's note, stays in the tool.

Rejected: embedding `pipedrive.X` in a summary type.
Cost: each type is written twice, and only review holds the rule. Each
shadow's descriptions are this server's own claims about a field, and
one is open: `whoami` says an activity's `due_time` is in the user's
time zone, which Pipedrive's v2 reference does not state and its v1
pages give as UTC (#127).

### D-18 Custom fields read under the workspace's names and dropdowns as labels; an unknown key or option passes through raw

Custom fields come back under the names the workspace gives them, and a
dropdown's value as its label. A key or an option id the field cache
does not know passes through raw.

Pipedrive stores a custom field under a 40-character hash key and a
dropdown value as an option id, nested under `custom_fields` on v2
([getDealFields](https://developers.pipedrive.com/docs/api/v1/DealFields#getDealFields)).
A model cannot read either, and cannot quote either back to a person.
The integration test `TestCustomFields_ResolveToWorkspaceWords` checks
that no set custom field on a real workspace reads as a hash or a list
of option ids. The passthrough is a bet: a workspace can add a field or
an option at any moment, and a caller seeing a raw id can recover where
a caller seeing nothing cannot.

Rejected: dropping an unknown field or option, which loses data the
caller cannot know was there.
Cost: a raw hash or option id shows until `refresh_field_cache` runs,
and the model has to recognize one when it sees it.

### D-19 Field metadata is one lazy cache per resource, warmed at startup, soft on failure and reloaded by refresh_field_cache

Field metadata is one cache per resource, deals, persons and
organizations, inside the Pipedrive client. Each loads lazily, once, on
the first call that needs it. The server warms all three in the
background at startup, bounded by its own timeout and the parent
context. A failed load is soft on a read: `Resolve` returns the map
unchanged. `refresh_field_cache` reloads all three in parallel.

Field definitions change rarely, and every read would otherwise pay a
round trip to `/dealFields`, `/personFields` or `/organizationFields`
before its own. The warm-up takes that round trip off the first
user-visible call, and a call arriving mid-warm waits on the same fetch
rather than starting a second. A read whose cache failed still returns
the record, with hashes in place of names (D-18); a write cannot do the
same (D-25).

Rejected: fetching field definitions on every call, which doubles the
calls; a periodic refresh, which spends calls whether or not anything
changed.
Cost: definitions are cached for the life of the process. A field
someone adds in the Pipedrive UI reads as a hash, and cannot be written
by name, until `refresh_field_cache` runs.

### D-20 A stored option value's shape says whether it is one id or a set, and ids compare on a rendered key

Whether a stored dropdown value is one option or a set is decided by the
value's own shape, an id or a list of ids, rather than by the field's
declared `field_type`. Option ids are compared on a rendered key,
`OptionKey`, rather than on the decoded value.

Deciding by shape keeps the resolution independent of Pipedrive's type
taxonomy: a field type this server has never seen resolves correctly as
long as its value is an id or a list of them. `FieldOption.ID` is `any`,
because custom fields carry numeric option ids and built-in fields such
as `status` carry strings like `"won"`, and a write must keep the two
apart. Every JSON number decodes to a float64, so the type survives and
the spelling does not. The stored value and the option that names it
arrive from two different calls, so rendering both sides is the
comparison that cannot fail on a type mismatch.

Rejected: keying a `map[any]string` on the decoded id, which misses
whenever the two calls spell an id differently; deciding by
`field_type`, which breaks on a type not yet seen.
Cost: an allocation per resolved value.

### D-21 Read dropdown labels through include_option_labels

Pipedrive's deal, person and organization reads take
`include_option_labels=true`, which returns a dropdown value as
`{"id": 107, "label": "Direct"}` rather than `107`. This server would
read labels through it. It is declined.

The parameter returns an object, not a label, so the tool layer would
still have to unwrap it, which is the work the field cache already does.
It settles only the value half: the 40-character key still needs
`/dealFields` to become a name, and a write still needs the table from
label to id (D-22). Adopting it would trade one shape for another, add a
dependency on a parameter younger than the endpoints it decorates, and
leave the cache and the unwrapping in place.

Rejected: reading dropdown labels through `include_option_labels`.
Cost: the one thing the parameter would buy is a correct label for an
option the cache has not seen. Here that option reads as its raw id
until `refresh_field_cache` reloads the definitions (D-18, D-19).

### D-22 Custom fields are written by name and dropdowns by label through FieldCache.Encode alone

A custom field is written under the name the workspace shows, through
`custom_fields` on `manage_deal`, `manage_person` and
`manage_organization`, and a dropdown takes its label.
`FieldCache.Encode` is the one place the translation runs: it is
`Resolve` run backwards, turning names into hash keys and labels into
option ids. The tool hands it what the caller typed and gets back a
body.

Reads answer in names (D-18), so a write that took hash keys would make
the caller name a field it has never seen. The changed report and the
overwrite refusal use the same names for the same reason: a refusal
naming a 40-character hash tells the caller about a field they cannot
recognize. One translation site means the read and write directions
cannot disagree. The integration test
`TestWrite_CustomFieldRoundTripsByLabel` writes a dropdown by label and
reads it back.

Rejected: writing custom fields by hash key and option id.
Cost: two open questions. Labels match case-insensitively, so two
options that differ only in case resolve to the same one; whether to
refuse an ambiguous label or match case exactly is undecided (#121). And
the field-definition listings read one page of Pipedrive's default size
without following `next_cursor`, so a workspace with more custom fields
of one kind than that page holds cannot name the rest (#126).

### D-23 Encode refuses rather than guesses, names every bad field, and one bad field refuses the whole map

`FieldCache.Encode` refuses what it cannot translate exactly. An unknown
field points at `refresh_field_cache`. A bad dropdown value lists the
choices. A name two custom fields share names their keys, so the caller
can pass one. A field Pipedrive marks not writable is named read-only, a
null is refused because no field can be cleared (D-42), and a field
named twice in one write is refused as ambiguous. The refusal names
every bad field, and one bad field refuses the whole map.

Encoding the rest would report a success over a record that never
received the field the caller cared about, and Pipedrive has no undo to
fall back on. Naming every bad field is the rule the overwrite refusal
follows (D-29): a caller made to discover their mistakes one round trip
at a time is told less than the server knows.

Rejected: encoding the fields that translate and dropping the rest;
guessing the nearest name or label.
Cost: one typo stops the whole write, field and record alike
(`TestManageDeal_CustomFieldEncodeRefusalStopsTheWrite`), and the caller
sends it again with every named field fixed.

### D-24 A built-in field is refused in custom_fields whether named by label or by key

A built-in Pipedrive field such as `title` or `status` is refused in
`custom_fields`, whether it is named by its label, `"Title"`, or by its
key, `"title"`. The refusal says to use the tool's own input for it.

The field metadata carries Pipedrive's own fields beside the
workspace's, so the cache's key index holds `title` and `status` as well
as the hashes. A built-in's value does not live in the record's
`custom_fields` map. If one passed through `custom_fields`, the guard
would read its stored value as empty, and no overwrite refusal could
fire over it (D-27). Pipedrive rejects such a body with `Unknown key`,
but that bounds the damage upstream, which is not where this server's
guard is meant to hold.

Rejected: checking only the name, which admits a built-in named by its
key and lets the guard read an always-empty value.
Cost: a caller who reaches for `custom_fields` out of habit is refused
and has to resend through the tool's own input, which the refusal names.

### D-25 A failed field-cache load refuses the write

When the field definitions cannot be loaded, a write that carries
`custom_fields` is refused. The error carries the class of the failure
that stopped the load, not `[validation]`.

On a read, a failed load costs the caller a hash in place of a name, and
the record still arrives (D-19). On a write, carrying on would mean
inventing a field: a name with no definition behind it has no key to
send. Nothing the caller wrote is wrong, so classing the refusal as
validation would send a model to fix a field name over what may be a
401; the upstream class is the one worth acting on (D-51).

Rejected: falling through as `Resolve` does, which would invent a field.
Cost: a transient failure of the definitions call stops every write that
names a custom field, the rest of that write included, until a later
call loads the cache.

### D-26 Encode accepts an option id where a label is expected

Where a dropdown takes a label, `FieldCache.Encode` also accepts the
option's id.

`Resolve` hands back a bare id whenever it cannot name an option (D-18).
A caller echoing back what a read gave it would otherwise be refused for
passing the server's own output, and the refusal would leave it nothing
to send instead. A label is still the form the descriptions ask for, and
a refusal for a bad value lists the labels (D-23).

Rejected: refusing an id, which makes `Resolve`'s own output unusable as
input.
Cost: an option has two spellings on the way in. A string is looked up
as a label first, so a label spelled like another option's id resolves
to the label's option.

### D-27 Every write reads its target first and refuses what it would destroy that the caller cannot see

Every update, transition and delete reads its target before it writes,
and refuses what the write would destroy that the caller cannot see. For
an update, that is any field that already holds a value and that the
caller has not named in `overwrite` (D-30). Filling an empty field
destroys nothing and needs no permission.

Pipedrive has no undo, so a refusal is the only guard there is. The
contract follows the Google Workspace servers' `write_values`, which
reads its target and refuses to overwrite what is there without being
told to. The guards are `dry_run` (D-32), `overwrite` and
`expect_version` (D-31), and only these: a guard is added where the
caller cannot already see what they are about to lose. The order they
run in is fixed once (D-37).

Rejected: an extra confirmation flag on an operation whose consequences
the caller can already see, which is friction rather than safety.
Cost: an update costs a read before its write. Anything a refusal names,
a model can echo back, so the guard informs a model and cannot stop one
(D-30).

### D-28 force exists only where a write destroys collateral the caller cannot see, and no tool has one

A `force` argument exists only where a write destroys collateral the
caller cannot see, such as content belonging to someone else or rows the
tool did not return. No tool here has one.

google-chat-mcp's `delete_message` is the model: `force` exists there
because deleting a message takes other people's threaded replies with
it. Here, a delete of an organization, a person or a deal deletes only
that record. The integration test
`TestWrite_DeletingAParentLeavesWhatHangsOffIt` deletes each parent of a
tree the suite creates and finds the deals, persons, activities and
notes under it still there. A soft delete that reaches nothing else is
the reversible case (D-47), and every other write's blast radius is on
screen after the read (D-27).

Rejected: `force` on an operation whose whole blast radius is on screen;
a `force` guard on parent deletes.
Cost: whether a record still points at a deleted parent, a deal at a
deleted organization say, is not checked, and nothing here reports it.

### D-29 A refusal names what it protects and the argument that permits the write

Every refusal names two things: what it is protecting, and the argument
that permits the write. The overwrite refusal names each populated field
and prints the `overwrite` array to pass back. The `expect_version`
refusal says to re-read and retry with the current version, since no
flag should let a stale copy write over a newer one. `refuse` in
`internal/tools/errors.go` takes both parts, and neither is optional.

A refusal the caller cannot act on is a bug. A model told only that a
write was refused retries it verbatim or gives up; a model told which
fields and which argument can take the decision back to the person.
Every refusal comes back tagged `[refused]`, its own class, so a model
can tell it from a validation failure and from an upstream error (D-51).

Rejected: a bare refusal that names neither the loss nor the way past
it, which gets retried verbatim.
Cost: the refusal hands the model the exact argument that gets past it.
That makes it informative rather than preventive (D-30).

### D-30 overwrite lists the fields an update may replace, taken from the diff that makes the changed report

`overwrite` is a list of field names, such as `["title", "value"]`,
exactly as the refusal names them. An update may replace a populated
field only when `overwrite` names it, and naming fewer than the refusal
listed is still refused over the rest. The set it is checked against
comes from the same diff that produces the `changed` report.

A blanket permit lets a caller who meant "change the title" replace
`value` on the same flag, having agreed to nothing in particular. Naming
each field makes the permit per field and records which ones the call
claimed. Deriving the protected set from the diff keeps the guard and
the report from drifting apart. Matching is exact, so a misspelled name
fails closed, and the transitions grant their own permit from a table,
never from caller input (D-43).

Rejected: `overwrite` as a boolean, a blanket; a field description
alone, which does not change what a model does.
Cost: anything a refusal says, a model can echo back. The eval task
`refuse-the-overwrite` fails every run on a model re-sending the write
with the exact list the refusal printed, so the number is a measurement
of that, not a defect to close.

### D-31 expect_version refuses a write whose record moved since the read that informed it

`expect_version` carries the `update_time` from the read that informed
the write. If the record's `update_time` has moved since, the write is
refused and the caller is told to re-read. Empty means the caller did
not ask for the check. The check runs before anything else in the write
and before a delete.

A write decided on an old read can replace a colleague's edit the caller
never saw. `update_time` is the only version a Pipedrive record carries,
and the read and the write are two calls, so the check is the caller's
copy against the stored one
([updateDeal](https://developers.pipedrive.com/docs/api/v1/Deals#updateDeal)).
The integration test `TestGuard_StaleExpectVersionRefuses` checks the
refusal against a real record.

Rejected: no version check, which lets a write decided on a stale read
replace a concurrent edit unseen.
Cost: Pipedrive has no compare-and-set, so the check catches a
concurrent edit and not a determined race: a change landing between the
check and the write goes through. `update_time` has one-second
granularity, so two edits inside one second carry the same version.

### D-32 dry_run is a per-call input on every write, and validation still runs under it

Every write tool takes `dry_run`. A rehearsal reads the target, runs
every guard and reports what the write would find and change, and sends
nothing. Validation runs under it too, so a rehearsal is refused on the
same input a real write would be.

The caller is who knows whether a given write is a rehearsal, so the
switch belongs on the call. A rehearsal that skipped validation would
pass an input the real write then refuses, and the model would learn the
wrong thing from it. The guard runs before the dry-run gate (D-37), so a
rehearsal also shows the refusal a real write would meet.

Rejected: a process-wide switch alone, which is the wrong grain for a
decision made per write.
Cost: a rehearsal predicts what this server sends, so it under-reports
what Pipedrive derives from it (D-35). It also leaves no record
anywhere: no tool handler logs (D-60), and Pipedrive saw nothing.

### D-33 PIPEDRIVE_DRY_RUN is a floor every write honors, and a call can only turn a rehearsal on

`PIPEDRIVE_DRY_RUN=true` makes every write a rehearsal. Each write
handler ORs it with the per-call `dry_run`, so a call can turn a
rehearsal on and nothing on the wire can turn one off. Reads ignore it.

An operator sets the variable to make speculative work against a real
CRM safe, and `docs/security.md` promises that it holds whatever a model
asks. A tool that honored only the per-call input would make that
promise false, and the tool that can delete is exactly the one that
would. The floor reaches the handlers through `tools.RegisterOptions`,
which `server.New` builds from the whole configuration (D-34), so no
entry point can hand a handler a zero value by assembling the arguments
itself.

Rejected: a tool that honors only the per-call input, which makes the
operator's promise false.
Cost: the floor holds only for a server built through `internal/app` and
`server.New`. A variable set to spaces alone is unset, so
`PIPEDRIVE_DRY_RUN` set to a space is off (D-66).

### D-34 internal/app is the one startup assembly, and server.New takes the whole configuration

`internal/app` performs the whole startup assembly: the domain, the
configuration for it, the token and the client. `Settings.Connect`
returns a `Runtime` that pairs the client with the settings it was built
from, and `Runtime.NewServer` is how a serving process gets its server.
`server.New` takes the whole `config.Config`, not a domain and a
`tools.RegisterOptions`. Nothing in the package logs, exits or touches
the network; `main` exits on a missing token and the integration suite
skips.

The reason is the `PIPEDRIVE_DRY_RUN` floor (D-33). An entry point that
rebuilt the sequence by hand could hand the tools a zero options value
and drop the floor for a whole process. With one assembly there is
nothing to copy. A `Runtime` also cannot pair a client for one workspace
with the domain another workspace's output is labeled with.
`--dump-schemas` calls `server.NewForSchemaDump`, whose name says no
handler runs, so it has no floor to drop.

Rejected: each entry point re-deriving the assembly; `server.New` taking
a domain string and `tools.RegisterOptions`, which leaves a zero-options
literal for the next caller to copy.
Cost: the server has two constructors, and `login` and `status` build
their client through `app.NewProbeClient`, since they hold a domain and
a token but no `Runtime`.

### D-35 The dry run predicts what this server sends, and the result diffs Pipedrive's echo against the pre-write read

A dry run predicts what this server would send, and reports the fields
that prediction changes. After a real write, the result carries the
record Pipedrive echoed back and a `changed` list diffed against the
record read before the write, never against the request.

Pipedrive derives and normalizes some of what it stores. A write of a
person's `first_name` comes back with `name` changed as well, because
Pipedrive recomputes `name` from the parts, and the integration test
`TestWrite_PersonFieldRoundTrips` checks it
([update a person](https://developers.pipedrive.com/docs/api/v1/Persons#updatePerson)).
Diffing against the echo reports what Pipedrive did, including what it
derived. The rehearsal errs in the safe direction: it can tell the
caller less will change than does, and the real write then names the
full set. The overwrite guard is unaffected, since it refuses over the
fields the request touches (D-30).

Rejected: reimplementing Pipedrive's derivation rules in the rehearsal,
which would be a copy of another company's logic with nothing to hold
it; diffing against the request, which reports a normalized value as
unchanged or a derived one as untouched.
Cost: the rehearsal under-reports a derived field such as `name`, and
says nothing of it until the real write.

### D-36 A write that would change nothing is skipped and reported unchanged

A write whose predicted record matches the stored one is not sent. The
tool reports the record as it stands with nothing changed, and the
result is the same under a dry run.

Sending it would spend a call to learn what the read already showed. It
would also give Pipedrive the chance to re-derive a field from a write
that meant to change nothing, such as recomputing a person's `name` over
one somebody set by hand (D-35). The check fails closed: when the diff
sees nothing, the write is skipped, never waved through unguarded. An
empty `changed` list stops the spine before the overwrite guard and the
PATCH, so a change the diff cannot see means no write rather than an
unchecked one.

Rejected: sending the unchanged write anyway and reporting Pipedrive's
echo, which costs a call and lets Pipedrive re-derive what the caller
did not touch.
Cost: the no-op is decided on the field projections, so it is only as
sharp as they are. Two values that render alike are treated as the same
value, which is why a collection renders every sub-field a write can set
(D-39); a projection that left one out would report a real change as
already done.

### D-37 One guardedWrite spine fixes the order of every update

Every update and every named transition runs through one generic
`guardedWrite` in `internal/tools/guard.go`, which fixes the order once:
read the record (where a resource's own refusal, such as an archived
deal's, fires first), check `expect_version`, build the field table with
this write's custom fields, predict the record, diff it, refuse what
`overwrite` does not name, stop for a dry run, and only then send the
write. `guardedWrite.run` is the only route to an upstream PATCH or PUT
for an update.

The order is the guard. A resource that checked the version after the
diff, or the dry-run flag before the overwrite refusal, would hold a
different promise from its neighbors while describing itself the same
way. Written once, the sequence cannot drift per resource, and the tests
that hold it hold every resource: a refusal never reaches the write, a
dry run never does, the version check precedes everything, and an
upstream error stops the sequence.

Rejected: a copy of the order per resource, where the postures drift
apart one edit at a time.
Cost: creates and deletes enter outside the spine. A create has no
target to guard. Each delete repeats the read, the `expect_version`
check and the dry-run gate itself (D-47), so those three lines exist
once per delete action.

### D-38 One field table per resource drives the diff, the guard and the request builder

Each resource has one field table, a list of `fieldSpec` entries naming
every field a write can touch with a way to project its stored value to
a string. The `changed` diff, the overwrite guard and the prediction all
walk that one table.

A field the write path sends that the table does not describe is written
without being guarded and without appearing in `changed`, while the tool
description promises to refuse over any populated field and name it.
Three lists, one per job, can disagree in exactly that way. With one
table, adding a field is one edit. The tests hold the table to the
request types: `TestEveryWritableFieldHasAFieldSpec` reads every field
an update or create request can send and fails on one with no entry,
`TestEverySettableFieldReachesTheDiff` sets each in turn and expects it
in `changed`, and `TestManageDeal_UpdateOverlayCoversEveryField` holds
the prediction to the same fields.

Rejected: three lists, one each for the diff, the guard and the request
builder, which can disagree.
Cost: the table and the request types are still two declarations, held
together by tests rather than derived from one. A field added to a
request type fails the build's tests until it is tabled, and a field
left out of both is invisible to all three jobs.

### D-39 A collection's projection renders every sub-field a write can set

A collection field, such as a person's emails and phones or an
activity's participants, projects to a string that renders every
sub-field a write can set, through one helper, `projectCollection`. A
contact point renders its value, its label and its primary flag, and the
free-text parts are quoted.

A projection that leaves a settable sub-field out lets the caller change
it while the diff sees nothing: the write is skipped as a no-op and the
caller is told the record already looks that way (D-36). Projected to
the primary entry's value alone, a write that kept the primary and
dropped the others read as no change, and an update replaces the stored
collection wholesale, which the integration test
`TestWrite_PersonEmailsReplaceWholesale` checks for emails
([update a person](https://developers.pipedrive.com/docs/api/v1/Persons#updatePerson)).
Projected to values alone, re-flagging the primary and relabeling were
invisible. Quoting stops a label that contains the separator from making
two different collections render alike.

Rejected: projecting a collection to its primary entry's value, which
hides a truncation; projecting to values alone, which hides a change of
primary or label.
Cost: the refusal and the `changed` list name the whole collection, not
the entry that moves, so a caller replacing one email agrees to replace
the field.

### D-40 Custom fields reach the guard through per-call specs, with Custom and CustomOf as one pair

Custom fields reach the diff and the overwrite guard through field specs
built per call by `withCustomFields`, from the custom fields this write
names. On `guardedWrite`, the adjacent pair `Custom` and `CustomOf`
drives both halves: the table extension and the overlay of the values
onto the predicted record.

Which custom fields exist is the workspace's business, so a static list
would be a second place to add a field, the drift one table per resource
prevents (D-38). Reaching a custom field also needs two things done, and
doing one without the other is silent: the dry run reports nothing, the
write goes out, and the field changes unreported. One pair of fields
makes a resource do both or neither. `withCustomFields` builds a fresh
slice rather than appending to the package-level table, so one call's
custom fields never reach the next. A multi-select compares as a sorted
set, since two orderings of the same choices are the same value.

Rejected: a static list of custom fields per resource; extending the
table and overlaying the prediction as two edits in two functions.
Cost: every write that names a custom field allocates its own table. A
custom field named like a built-in, or two sharing a name, adds a second
spec under one name, which can only add refusals, never remove one.

### D-41 Optional scalars on an update are pointers: nil omits and a value sends

Every optional scalar on an update request is a pointer with
`omitempty`: nil omits the field, so it keeps its stored value, and a
non-nil pointer sends what it points at. The `manage_` tool inputs take
the same shape.

A bare `int64` or `string` with `omitempty` collapses "leave this alone"
and "set it to zero". An update that could not tell them apart would
either overwrite every field the caller did not mention or be unable to
send a zero or a false at all. A reopen sends `done: false` on an
activity, and a deal's status travels the same way, so the pointer is
what makes a close reversible (D-44). The tests hold both directions on
the wire: a nil field never reaches the body, a set field always does,
and `TestClient_UpdateActivity_DoneFalseIsSent` holds a false that
`omitempty` on a bare `bool` would drop.

Rejected: bare scalars with `omitempty`, which collapse "leave alone"
and "set".
Cost: input shape is a schema change and so a versioning event (D-75),
which is why it is decided once rather than per field. A pointer has no
third state, so there is no spelling for "clear this" (D-42).

### D-42 No tool clears a field

No tool clears a field once it holds a value, and no tool description
claims one can. Every optional `manage_` input reads "omit to leave it
as it is".

Pipedrive v2 has no spelling that empties a field. A PATCH carrying
`"expected_close_date": null` is rejected with
`The value is not a valid 'string'`, and one carrying `""` is accepted
and stores the zero date `0000-00-00`
([update a deal](https://developers.pipedrive.com/docs/api/v1/Deals#updateDeal)).
A deal that never had a date omits the field on a read, so the zero date
reads as a value, and a tool that called an empty string a clear would
be telling the model something false. v1's `PUT /api/v1/deals/{id}` with
a null does clear the field, which makes this a v2 gap rather than a
Pipedrive-wide one. The comment on `UpdateDealRequest` in
`internal/pipedrive/deals.go` records the behavior; it was checked by
hand, and no integration test writes either value, since it would leave
a zero date behind.

Rejected: v1's PUT, a third v1 carve-out (D-03) on an endpoint Pipedrive
has put out of support
([out of support](https://developers.pipedrive.com/changelog/post/deprecated-apiv1-endpoints-become-out-of-support));
calling an empty string a clear.
Cost: a field can be changed but not emptied. A working v2 clear means
changing the request types and the input descriptions in one commit.

### D-43 The named transitions take no overwrite and send only the field they name

The named transitions, `move_stage`, `mark_won`, `mark_lost`, `reopen`,
`archive` and `unarchive` on a deal and `complete` and `reopen` on an
activity, take no `overwrite`. Each sends a request holding only the
field it names, and drops any custom field or descriptive field passed
beside it.

A transition names both the change and the field it lands on, so the
caller already sees the whole blast radius, and asking them to
acknowledge it again adds friction without safety. Only a free-form
update needs permission. Each action says what it is where it is
declared: `dealActions` and `activityActions` are tables saying whether
an action creates, transitions or deletes, and `OverwriteAll` is set
only from them, never from caller input. `manage_person`,
`manage_organization` and `manage_note` carry no per-action facts and
keep a plain set. `SelfAuthorizingActions` derives the list from the
tables, and tests hold the server's instructions and the README to it.

Rejected: stating the rule by exclusion, `in.Action != "update"`, which
made any action added later a transition unless it happened to be called
update.
Cost: a descriptive field or `custom_fields` passed beside a transition
is ignored rather than refused, so a caller who expected `mark_won` to
also retitle the deal learns it only from the result.

### D-44 Every close has its reopen

Every action that closes a record has one that reopens it. `reopen` puts
a deal back to open from won or from lost, and puts a completed activity
back to not done.

A one-way door on a CRM with no undo turns a model's mistaken close into
a task for a person in Pipedrive's UI. The reopen is a transition like
the close (D-43), so it needs no `overwrite`, and the request shapes
carry the deal status and the activity's `done` as pointers so "reopen
this" can be told from "say nothing about it" (D-41). Pipedrive accepts
`lost_reason` only on a deal that is lost and validates the resulting
state, so a reopen sends the status alone; a reopen that also sent an
empty `lost_reason` is rejected. The integration tests
`TestWrite_DealReopensFromWonAndFromLost/mark_won`,
`TestWrite_DealReopensFromWonAndFromLost/mark_lost` and
`TestWrite_ActivityCompletesAndReopens` check both resources against a
live workspace.

Rejected: a tool that could close a record but not reopen it.
Cost: `reopen` leaves the last `lost_reason` on the deal. Pipedrive
accepts that field only while the deal is lost, so the text of the last
loss stays until the deal is lost again with another reason, and the
description says so.

### D-45 An archived deal lives in its own collection and is refused for edits until unarchived

An archived deal lives in its own collection. `list_deals` reads
`/deals`, which excludes archived deals, and reads `/deals/archived`
only when the caller passes `archived`
([archived deals](https://developers.pipedrive.com/docs/api/v1/Deals#getArchivedDeals)).
`manage_deal` refuses every action on an archived deal except
`unarchive`, with a `[refused]` naming `action: "unarchive"`.

Pipedrive refuses every edit to an archived deal except unarchiving it,
and answers with a bare 403 that reads as a permission failure. The read
every write already does can say so first and name the action that fixes
it. The guard lives in the `Get` step of `guardedWrite`, the first thing
the spine runs (D-37), so nothing runs before it. `unarchive` sends
`is_archived` alone, a create carries no `is_archived`, and an archived
deal is refused before `expect_version` is read, so neither check lets a
write past the other. Archiving is reversible and visible, so it needs
no `force` (D-28), and the tool descriptions say archiving is not
closing.

Rejected: letting the edit reach Pipedrive, whose bare 403 reads as a
permission failure and names no way out.
Cost: archived deals are absent from `list_deals` unless asked for, and
no integration test reads `/deals/archived`.

### D-46 Destructive actions register by default and guard at call time

Every `manage_` tool registers in every configuration, its delete action
included, and the guard runs at call time: `dry_run`, `overwrite`,
`expect_version` and the `PIPEDRIVE_DRY_RUN` floor (D-27, D-33). No
setting withholds a tool.

A tool that does not exist cannot explain itself. An operator who
withheld the destructive tools handed the model a missing capability to
guess at, rather than a refusal saying why a write will not run and what
would permit it (D-29). The shipped Google Workspace MCP servers take
the same posture: google-drive registers `trash_file` and google-chat
registers `delete_message`, and each guards the call. A guard is added
only where the caller cannot already see what the write would destroy,
so a delete that reads its record first and is soft takes no further
argument (D-47). The operator who wants a server that cannot write sets
the floor.

Rejected: `PIPEDRIVE_ENABLE_DESTRUCTIVE`, a registration gate, since a
tool that does not exist cannot explain itself.
Cost: a stale `PIPEDRIVE_ENABLE_DESTRUCTIVE` in a client's configuration
is not read and draws no warning. Nothing it withheld is withheld, so
setting it to `false` does nothing.

### D-47 A soft delete takes dry_run and expect_version and no permitting argument

Every delete is soft, and takes `dry_run` and `expect_version` and no
permitting argument beyond them. It reads the record first, so the
result shows what went. A deal or a note that is already deleted is
reported as it stands rather than deleted twice.

A guard is added only where the caller cannot see what they would lose,
and a soft delete is the reversible case. Pipedrive's v2 delete "Marks a
deal as deleted. After 30 days, the deal will be permanently deleted",
and the same for persons, organizations and activities
([delete a deal](https://developers.pipedrive.com/docs/api/v1/Deals#deleteDeal)).
Within that window Pipedrive's own
[Restore data](https://support.pipedrive.com/en/article/restore-data)
brings a deal, person, organization or activity back. A v1 note delete
clears `active_flag`, and `get_note` still returns the note
([delete a note](https://developers.pipedrive.com/docs/api/v1/Notes#deleteNote)).
The read puts the record on screen before it goes, and deleting a parent
reaches nothing that hangs off it (D-28). google-drive's `trash_file`
takes the same posture: `dry_run` and nothing else.

Rejected: a permitting flag on a reversible delete, which adds a step
the caller can always take and no information.
Cost: nothing in this server sets `active_flag` back or restores a v2
delete, and after 30 days nobody can. The descriptions say to treat a
delete as one-way and rehearse it with `dry_run` first.

### D-48 Annotations are advisory: reads are read-only and idempotent, and every manage_ tool is destructive

Annotations are advisory metadata for a client's interface, never the
enforcement. Every read declares `readOnlyHint` and `idempotentHint`,
and every `manage_` tool declares `destructiveHint` true and
`idempotentHint` false, through two helpers in
`internal/tools/annotations.go` so no tool invents a third posture.
`refresh_field_cache` declares only `idempotentHint`.

What stops a destructive write is the call-time guard (D-27, D-46). A
client may draw a prompt from the hints, or ignore them, and the server
cannot tell which. Each `manage_` tool can delete, so the whole tool is
destructive; MCP annotates a tool, not an action, so a finer posture
cannot be said. The tool descriptions carry the per-action truth: what
each action removes, and whether it can be undone.

Rejected: annotations as the enforcement, which would leave a client
that ignores them with an unguarded write.
Cost: a client that prompts on `destructiveHint` prompts on every
`manage_` call, a create or a dry run included. No test asserts the two
postures: `schema-ack` sees a changed annotation as a change to the
surface, and the rest is review.

### D-49 A dry-run create shows the defaults Pipedrive would fill, read with GETs alone

A dry-run create shows the defaults Pipedrive would fill in, and reads
them with GETs alone. A record created without an owner shows the
signed-in user from `/users/me`. A deal shows the user's default
currency, and the first live stage of its pipeline from
`/stages?pipeline_id=`. A rehearsal has no record id, so its `url` is
empty rather than a link to a record that does not exist.

A rehearsal exists so a caller can see what the write would do, and a
create's result is mostly what Pipedrive fills in. Zero values for the
owner, the currency and the stage told the caller something false about
the record the real create would make. Pipedrive's reference names none
of these defaults
([add a deal](https://developers.pipedrive.com/docs/api/v1/Deals#addDeal)),
so the integration test `TestWrite_CreateDefaultsMatchTheDryRun` creates
each record and compares it with its dry run. The preview reads only for
a field the call left out, and a read that fails shows no guess. A
canceled `whoami` is not remembered, so it cannot empty every later
preview.

Rejected: zero values for the owner, the currency and the stage, and a
link to record 0.
Cost: a dry-run create spends up to two reads, and the owner comes from
v1, which shows no owner if `/users/me` stops answering. A deal given
neither a pipeline nor a stage goes to the default pipeline, which no
read here names, so its stage stays unknown.

### D-50 Upstream and validation failures are tool-execution errors; JSON-RPC errors are the SDK's

An upstream failure, a business rule, a guard's refusal and this
server's own input checks come back as tool-execution errors: a
`CallToolResult` with `isError: true` and a `[class] message` text.
JSON-RPC errors are left to the SDK, for an unknown tool, arguments that
fail the input schema, or a panic.

The MCP specification gives the two channels different readers
([tools](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)).
A tool-execution error reaches the model as text it can act on; a
JSON-RPC error reads to the client as the call having gone wrong at the
protocol level. A 404 or a 429 from Pipedrive is information about the
request, not a broken call, and github-mcp-server returns its upstream
HTTP failures the same way. Reserving JSON-RPC for "the model called us
wrong" keeps the error a model can recover from on the channel it reads.

Rejected: a JSON-RPC error for an upstream HTTP failure, which a client
may show as a transport fault rather than hand to the model.
Cost: a call the SDK rejects, such as a `manage_` call with no `action`,
comes back as a JSON-RPC error with no class tag, so a caller meets two
error shapes; `TestManageNote_MissingActionIsASchemaError` holds which
one that is.

### D-51 Every tool error starts with a [class] tag from one closed set, classified by sentinel

Every tool error starts with a `[class]` tag from one closed set, and
the class comes from the sentinel the error wraps, through `errors.Is`.
The sentinels are in `internal/pipedrive/errors.go`, one per kind of
upstream answer, and `errorClass` in `internal/tools/errors.go` maps
each to its tag. A guard's refusal is its own class, `[refused]`, from a
sentinel in the tools layer.

A model can branch on a tag without parsing free text, and the class
tells it what to do next. A `[refused]` means the guard stopped the
write and named the argument that would permit it; a `[validation]`
means the input was wrong; a `[rate_limited]` or `[server_error]` means
Pipedrive did not answer usefully. Keeping a refusal apart from a
validation failure and an upstream error matters because the right next
step differs: retrying is useless for one and correct for another.
[troubleshooting.md](troubleshooting.md) documents each class by its
text, and a test holds that page to the set.

Rejected: a class the model parses out of the upstream message, whose
wording Pipedrive owns.
Cost: `errorClass` ends in a default, `[error]`, for an error that wraps
no sentinel, such as an unexpected redirect status (D-55), which tells
the caller nothing to act on. `TestEverySentinelHasItsOwnClass` keeps
every sentinel off it.

### D-52 A 403 is split into permission and business_rule by signal substrings in the upstream message

A 403 is classed `[business_rule]` when Pipedrive's message carries one
of a list of case-insensitive signal substrings, such as "locked",
"required" or "stage", and `[permission]` otherwise. The message is the
body's `error` field, or `error_info` when `error` is empty. The
request's endpoint plays no part.

Pipedrive answers both a missing permission and a business rule, such as
a locked record or a gated stage, with 403, and its status-code page
gives the model no structured way to tell them apart
([HTTP status codes](https://pipedrive.readme.io/docs/core-api-concepts-http-status-codes)).
Its v2 description declares no 403 on any operation. The two call for
different next steps: a permission failure needs a person to change the
token's rights, and a business rule needs the record or the pipeline
changed. On v2, `error_info` is free text pointing at the developer
site, so it cannot discriminate and serves only as the message when
`error` is empty. `businessRule403Signals` in
`internal/pipedrive/errors.go` is the list.

Rejected: `error_info` as the discriminator, which is free text on v2.
Cost: the signal list comes from the reference and a few observed
bodies, not from real 403s of both kinds side by side. Which bodies each
kind carries, and so whether the list is right, is open until a
permission-denied and a locked-record 403 are captured from a live
workspace and kept as test fixtures (#115). A 402, a 415 and a
limit-reached 403 are not told apart (#128).

### D-53 The model reads the upstream message alone; status and path go to the debug log

The model reads Pipedrive's own message and nothing else: an error is
`[not_found] Deal not found`, not
`[not_found] not found: HTTP 404 Deal not found [/api/v2/deals/99999]`.
The status and the path go to the debug log line (D-59). A tool that has
something to add, such as the search liveness check (D-14), wraps the
error with a hint, and the class survives the wrap.

The tag already says what the status would, and a path or a status code
repeated in the text is noise a model may read as instruction. The
response body is never formatted into an error, since Pipedrive can echo
record content back in it. `APIError` keeps the upstream message, the
status and the path, and a body that is not JSON, such as an edge's HTML
page, is reported by its length alone
([rate limiting](https://pipedrive.readme.io/docs/core-api-concepts-rate-limiting)
says a client that keeps going after 429s gets one). When the body
populates the message, the length note is discarded.

Rejected: the full `APIError.Error()` text,
`[not_found] not found: HTTP 404 Deal not found [/api/v2/deals/99999]`,
which restates the class and adds a path.
Cost: the model never sees which call or status failed. An operator who
needs them turns on `LOG_LEVEL=debug`, where each response's line
carries the path and the status.

### D-54 A read retries 429, 5xx and a lost connection; a write retries 429 and a failed dial alone

A GET is retried on a 429, a 5xx or a lost connection. A write is
retried on a 429 and on a failed dial alone. A context that ended, a
timeout included, is never retried. The attempts are bounded, and the
wait between them honors `Retry-After` as a floor and otherwise doubles
from a base, with jitter, up to a cap; the numbers are in
`internal/pipedrive/client.go`.

A write that may have reached Pipedrive must not be sent twice, since a
second create makes a second record. Pipedrive's rate-limit page says a
request over the limit is rejected, so a 429 was not applied
([rate limiting](https://pipedrive.readme.io/docs/core-api-concepts-rate-limiting)).
A 5xx can follow a commit, so only a GET repeats one. A failed dial, DNS
included, is a `*net.OpError` with `Op` "dial", and nothing was sent
([net/dial.go](https://cs.opensource.google/go/go/+/refs/tags/go1.27.3:src/net/dial.go;l=810)).
After the dial, a lost answer may follow a commit, which is also why
`net/http` replays a write only when nothing was written
([net/http/transport.go](https://cs.opensource.google/go/go/+/refs/tags/go1.27.3:src/net/http/transport.go;l=844)).

Rejected: repeating a write after a 5xx or after a failure once the
request was sent.
Cost: a write whose answer is lost comes back as an error saying the
write may have been applied, and the caller has to read the record
before trying again.

### D-55 Redirects are refused

The client refuses every redirect. Its `http.Client` has a
`CheckRedirect` that returns `http.ErrUseLastResponse`, so a 3xx comes
back as the answer rather than being followed.

Go's redirect follower forwards a request's custom headers to the
redirect target, and the token travels in one, `x-api-token` (D-62). A
followed redirect would hand the token to whatever host a `Location`
header named. Pipedrive's JSON API does not redirect in normal
operation, so refusing costs nothing a working call needs, and the token
stays on the workspace host the domain validation allows (D-65).
`TestClient_RefusesRedirects` serves a redirect to a second server and
fails if that server sees a request.

Rejected: following redirects, which `net/http` does by default and
which would carry `x-api-token` to the target.
Cost: a 3xx surfaces as an unexpected status with no class sentinel, so
the model sees `[error]` (D-51) rather than a class it can act on.

### D-56 Every outbound attempt is bounded by PIPEDRIVE_HTTP_TIMEOUT

Every outbound attempt is bounded by `PIPEDRIVE_HTTP_TIMEOUT`, a Go
duration. It is the `Timeout` of the client's own `http.Client`, so it
applies to each attempt independently, retries included. `status` probes
with the configured value, and `login`, which validates a token before
anything is stored, with the default.

An attempt with no bound is held open by a stalled upstream until the
MCP client itself gives up, and the model then learns nothing about why.
A timed-out attempt ends with a deadline error, which the retry policy
treats as an ending rather than a reason to try again, a read included
(D-54), so a timeout never repeats a write. The operator sets the bound
because the right value depends on the network between them and
Pipedrive, and `TestLoad_Defaults` and `TestLoad_AllOverrides` hold the
default and the override.

Rejected: an unbounded attempt, which a stalled connection holds until
the client gives up.
Cost: a tool call can take the timeout times the attempts, plus the
backoff between them, before it answers. Lowering the timeout ends a
stalled attempt sooner and does not change the backoff.

### D-57 The client neither pools nor rate-limits on its own

The client keeps no request queue, no worker pool and no rate limiter of
its own. Requests go out as the handlers make them, concurrently when
the MCP client sends calls in parallel, over the connection reuse
`net/http`'s transport provides. The client is safe for concurrent use.

Pipedrive sets the rate limit and enforces it, and its rate-limit page
documents the headers that report what is left
([rate limiting](https://pipedrive.readme.io/docs/core-api-concepts-rate-limiting)).
A limiter here would guess at a budget Pipedrive owns, and would still
be wrong whenever another process spends the same budget. A 429 is the
authoritative answer, and the client retries it with backoff (D-54). The
server is request and response, so the only work it starts on its own is
the field-cache warm-up at startup (D-19).

Rejected: a client-side rate limiter, which would guess at a budget
Pipedrive sets and enforces.
Cost: parallel sessions on one workspace share its rate limit, and a
burst across them meets 429s that the retries may not outlast; the error
then reaches the model as `[rate_limited]`.

### D-58 Stdout carries only MCP JSON-RPC frames, and logs go to stderr through slog

Stdout carries only MCP JSON-RPC frames. Every log line goes to stderr
through `log/slog`, as JSON or text. `main` names the process's streams
once and passes them down as an `io.Writer`, and `forbidigo` refuses
`fmt.Print*` and `os.Stdout` everywhere else.

This is the protocol, not a house preference. MCP's stdio transport says
the server "MUST NOT write anything to its `stdout` that is not a valid
MCP message", and that it may log to stderr
([transports](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports)).
A stray print corrupts the stream, and the client silently stops
working. The `smoke` gate drives the built binary over stdio and reads
every frame, and `TestTheGeneratedLauncherRefusesToStdout` holds the
bundle's launcher to stderr for its own refusal. `scripts/` is
maintainer tooling run at a terminal, and is excluded from the rule.

Rejected: printing from anywhere but `main`, which corrupts the stream
the client reads.
Cost: every function that writes takes a writer, and the rule rests on
the linter's settings loading. A `forbidigo` settings block that fails
to load leaves the linter on its defaults, still firing and looking
right, so the rule is checked by its message text.

### D-59 A log line carries path, method, status, attempt and duration, never a query string, a body or the token

A log line from the client carries the request's `path` and `method`,
the `status`, the `attempt` and the `duration`, and the warning on a
transport failure adds the `error`. No line carries a query string, a
body or the token. The startup line logs a `settings` group of
`workspace`, `domain_source`, `token_source` and `dry_run`.

A search term travels in the query string, and a body carries record
content, so either in a log would put a customer's data into stderr,
which a client may write to a file. `pathOf` strips the query and the
host before anything is logged or set as `APIError.Endpoint`, and a
transport error, which repeats the whole URL, is unwrapped to its cause
first. `TestClient_NoLogOrErrorCarriesTheSearchTerm` sends a canary term
and fails if any log line or error carries it. The token travels in a
header (D-62) and is redacted from the settings (D-61).

Rejected: logging the URL, which carries a search term in its query
string, or the body, which carries record content.
Cost: the path still carries a record's numeric id. A dial, DNS or TLS
error can name the workspace host, which the startup line names anyway.

### D-60 No tool handler logs, so the server keeps no record of tool calls

No tool handler logs. `internal/tools` holds no logger, so a read, a
write or a rehearsal produces no log line, and the server keeps no
record of tool calls. The binary's log sites are the two startup lines,
a debug line per response and a warning per transport failure (D-59),
and none carries a tool name.

The MCP transport carries the tool call and its result but not the
person's prompt or the model's reasoning, so even a complete per-call
log could say what was called and never why. Two trails hold what a log
would: Pipedrive's own change record, which is the authority on what
changed, and the MCP client's transcript, the only place the prompt and
the call sit together. A handler that logged its arguments or its result
would put record content into stderr, which D-59 keeps out. The threat
model in [security.md](security.md) relies on the absence.

Rejected: an audit trail kept by this server, which could record what
was called and never why.
Cost: the client transcript is the only record of a call or a rehearsal.
Whether to add one line per call, with the method, the path, the status
and the duration and no payload, is open (#123); adding it changes the
threat model.

### D-61 The token is unexported on Settings, and LogValue and String redact it

`Settings` holds the token in an unexported field, and implements both
`LogValue` and `String`, each rendering the settings without it. Nothing
in `internal/app` reads the token except to build the client.

Unexporting the field stops a caller reading it, but not a formatter.
`slog` calls `LogValue`, so `slog.Any("settings", s)` is safe with it
alone, while `fmt` reads unexported fields through reflection, so
without `String` a `%+v` renders `token:<the real token>`. `String`
delegates to the same redaction. `TestSettings_LogValue_RedactsTheToken`
asserts the `slog` path and the `%v`, `%+v` and `%s` verbs, and fails
without either method. The startup line logs the settings group, so this
is the one place the token sits beside a log call. The same precedent
holds in `internal/pipedrive`: a need to show a secret gets a redacting
accessor, never a field.

Rejected: `LogValue` alone, which left `%+v` printing the token.
Cost: the protection is per type, and the test covers `Settings` alone.
`pipedrive.Client` also keeps the token unexported with no `String`, so
formatting a client with `%+v` would print it; nothing does.

### D-62 The token travels only in the x-api-token header

The token travels in the `x-api-token` request header on every call and
nowhere else: never a query parameter, never a URL, never a body and
never an error. Every request sets the header from the client's token,
and no code path substitutes another.

A header stays out of the URL, so the path a log line records (D-59) and
the URL a transport error repeats carry nothing secret. A query
parameter is the place a token leaks first: into logs, proxies, browser
history and error strings. `TestClient_HappyPath` asserts the header on
the request a fake server receives. `APIError` carries the class, the
status, the upstream message and the path, so even its `Error()` string
cannot reach the token.

Rejected: the `api_token` query parameter, which puts the token into
every URL a log line, a proxy or an error repeats.
Cost: Go's redirect follower forwards custom headers to a redirect
target, so the header is safe only because the client refuses every
redirect (D-55).

### D-63 The token lives in the OS keyring, and PIPEDRIVE_API_TOKEN overrides it

The API token lives in the OS keyring, under the service `pipedrive-mcp`
with the workspace domain as the account, and `PIPEDRIVE_API_TOKEN`
overrides it when set. `pipedrive-mcp login` prompts for the token
without echo, validates it against Pipedrive with the startup probe
(D-06), and only then stores it; a bad token is refused before anything
is written. `internal/credentials` is the only writer of the keyring.

A personal API token carries every permission of the Pipedrive user
behind it, so it belongs where the operating system guards secrets. In
the keyring it is not in the MCP client's JSON config, where a screen
share, a dotfile repository, a backup or an agent coaxed into reading
files would find it, and not in the client's environment. The
environment wins, as `gh` and `aws` order theirs, so automation and a
one-off token need no `login`; with it unset, a broken keyring is
reported rather than read as no token.

Rejected: the token in the client's JSON config.
Cost: same-user malware reads an unlocked keyring as it reads any of the
user's files. The token is read once at startup, so a rotated token
takes effect only on a restart; whether to reload it on a signal or on
an auth failure is open (#122).

### D-64 The domain resolves from the environment, then a non-secret pointer file, then an error

The workspace domain resolves from `PIPEDRIVE_COMPANY_DOMAIN`, then from
the `default_domain` in a pointer file under `os.UserConfigDir()`, and
otherwise is an error that names `pipedrive-mcp login`. `login` writes
the pointer and `logout` clears it only when it names the workspace
being logged out of. The file is written 0600 in a 0700 directory,
through a temporary file renamed into place.

The domain is not a secret, so it does not belong in the keyring, and an
MCP client config that needs neither the domain nor the token is the
point of `login` (D-63). The file holds the pointer and nothing else,
the role `~/.config/gh/hosts.yml` plays for `gh`. The environment wins
so one shell or one client entry can point at another workspace without
running `login`.

Rejected: requiring `PIPEDRIVE_COMPANY_DOMAIN` on every run, which puts
the domain into every client config; storing it in the keyring beside
the token, where nothing but the token belongs.
Cost: unsetting the environment does not reach "no domain" while a
pointer file exists, so reproducing a first run means pointing the user
config directory somewhere empty.

### D-65 The domain is validated as a Pipedrive subdomain before it reaches a URL or the keyring

The workspace domain is validated as a Pipedrive subdomain before it
reaches a URL or the keyring. `config.ValidateDomain` lowers A to Z
alone and accepts one host label: letters, digits and inner hyphens,
with no hyphen at either end. Every entry point routes through it: the
environment, the pointer file, the `--domain` flag of `login` and
`logout`, and the interactive prompt.

The domain is spliced into `https://{domain}.pipedrive.com`, the host
the token is sent to, and into the keyring account. A value carrying a
dot, a slash or an `@` would move the request, and the token in its
header, to another host. A subdomain is a host label (RFC 1123).
Lowering A to Z alone means a character Unicode folds into a to z, such
as the Kelvin sign, is refused rather than read as another workspace's
letter. A bad domain exits non-zero before the server announces itself
(D-67).

Rejected: passing the value through to the URL, which lets it name any
host; lowering with Unicode case folding, which maps lookalike
characters onto real workspace names.
Cost: the pattern is a host label rather than Pipedrive's own rule, so a
workspace name it does not admit cannot be configured.

### D-66 Every setting is an environment variable registered in config.Vars and read once through config.Lookup

Every setting is an environment variable. `config.Vars` lists each one
the server reads: the variables `config.Load` reads, recorded as it
reads them, and the domain and the token, which are read outside it.
Every read goes through `config.Lookup`, which drops the spaces around a
value and reads spaces alone as unset. There is no configuration file,
and no flag configures the server; its flags are operational.

The server runs as a subprocess its client starts, and a client config
passes settings as that process's environment (D-01). One registry keeps
every reader on one list: `Load`, the bundle gate that asks for the
settings at install (D-96), and the tests that hold every name the code
spells to the registry, each spelled once in `internal/config`. A
setting added to `Load` reaches the registry without a second edit,
because the registry is recorded from the reads.

Rejected: a configuration file or configuration flags, a second source a
client config would have to name; a server variable read with
`os.Getenv` rather than `config.Lookup`, which keeps the space a cleared
form field leaves.
Cost: a variable set to spaces alone is unset, so `PIPEDRIVE_DRY_RUN`
set to spaces is off. `docs/configuration.md` is written by hand beside
a registry that lists every setting; whether to generate it from
`config.Vars` or have a gate check it is open (#124).

### D-67 A bad setting exits non-zero before the server announces itself

A setting the server cannot use stops the process before the MCP server
exists. `config.Load` validates the domain, `LOG_LEVEL`, `LOG_FORMAT`,
`PIPEDRIVE_DRY_RUN` and `PIPEDRIVE_HTTP_TIMEOUT`, and `main` exits
non-zero with the reason on stderr before it builds the server or opens
the transport. A missing domain or token stops it the same way.

A client connected to a half-configured server sees tools that look
ready and act on settings nobody chose. The dry-run floor is the sharp
case: a `PIPEDRIVE_DRY_RUN` value that does not parse as true or false,
read as the default, would start a server that writes when the operator
meant it not to (D-33). Refusing at start means no model ever talks to a
server whose configuration is in doubt.

Rejected: falling back to the default for a value that does not parse,
which turns a typo into a setting; serving and reporting the bad setting
through tool errors.
Cost: the client shows only that the server failed to start. The reason
is on stderr, which a client keeps in its own log for the server.

### D-68 status reports what resolved, and status --json is one stable object

`pipedrive-mcp status` reports the workspace domain and where it came
from, where the token came from, and whether the startup probe reached
Pipedrive (D-06). It exits zero only when all three are healthy, and
`--no-probe` skips the probe. `status --json` prints the same state as
one JSON object carrying a `schema_version`. `credentials.resolved` is
the field to branch on, `probe.ran` tells a skipped probe from a failed
one, and `reason` names what stopped the check. Every refusal is still a
whole object, and the exit code is kept, so a caller may read either.

Most first runs fail on one of those three, so one command answers them
before the server is wired into a client. A script that decides whether
to start the server needs a shape it can parse. One collector feeds both
renderers, so the text and the object cannot disagree.

Rejected: a script parsing the human lines, whose labels are free to be
reworded in any release.
Cost: the object is the contract and the labels are not, so
`schema_version` changes when a field is removed or changes meaning.
Unlike the sibling servers' status commands, this one calls Pipedrive
unless `--no-probe` is passed.

### D-69 A mistyped subcommand exits non-zero

An argument that is neither a known subcommand, `login`, `logout` or
`status`, nor a flag exits 1 and names the commands. Only an argument
with a leading dash falls through to the server's flag parsing.
`TestAnUnknownCommandIsReported` drives the dispatch through `run`,
which takes its arguments and streams as parameters, since `main` exits.

Falling through to the server on a mistyped `status` looks like a hang:
the server blocks on stdin and says nothing. The caller is then handed
exit 0 whether it meant to serve or not, and nothing downstream, a
script or a client config, can tell the two apart.

Rejected: starting the server on an unknown subcommand and reporting
success.
Cost: a leading dash is all that separates a subcommand from a flag.
Every subcommand has to be listed in the dispatch in
`cmd/pipedrive-mcp/main.go`, or it is refused as unknown.

### D-70 internal/pipedrive has no MCP imports, and internal/tools holds schemas, validation and presentation

`internal/pipedrive` is the HTTP client and imports nothing from the MCP
SDK. Schemas, input validation and presentation live in
`internal/tools`: the `jsonschema:` tags, the enums a tool closes, and
per-tool choices such as a deal's `title` against every other record's
`name`. depguard's `pipedrive-client-stdlib-only` rule in
`.golangci.yml` allows `internal/pipedrive` the standard library alone,
so it refuses an SDK import there.

The split keeps the client testable and usable without a protocol
session: its tests run against `httptest` fakes with no MCP in sight,
and a change to how a tool presents a record cannot change what the
client sends. It is also what makes each output type a shadow defined
beside its tool rather than the upstream type with tags added (D-17).

Rejected: `jsonschema:` tags and presentation on the
`internal/pipedrive` types, which would make the next upstream field
part of the tool surface.
Cost: depguard holds the import, and a `jsonschema:` tag in
`internal/pipedrive` is held by review alone. The list of v1 tools
`TestV1CarveOutStillAnswers` probes is kept by hand, because a list of
tool names beside the v1 helpers would put MCP vocabulary in the client.

### D-71 The Pipedrive client is hand-written, not generated

`internal/pipedrive` is written by hand, one file per resource, and no
client generated from Pipedrive's OpenAPI description is compiled in.
depguard's `hand-written-client` rule refuses the generated Pipedrive
client module and the runtime oapi-codegen's clients import.

The
[v2 description](https://developers.pipedrive.com/docs/api/v1/openapi-v2.yaml)
cannot produce a usable client. It declares no `$ref`: every response
shape is inlined per operation, so a generator emits an anonymous struct
per endpoint and no shared `Deal` type, with `/deals` and `/deals/{id}`
each carrying its own copy of the same fields. Generating models alone
yields only the `*Params` request types, since the response shapes exist
nowhere outside those structs. The description also knows nothing about
failure: it declares 200, 201 and 404, and no 401, 403, 429 or 5xx. The
error classes, the 403 split and the retry policy come from Pipedrive's
[status-code page](https://pipedrive.readme.io/docs/core-api-concepts-http-status-codes)
and from live behavior instead (D-51, D-52, D-54).

Rejected: a generated client, which shares no types; generated models
beside a hand-written transport, which yields no response models.
Cost: a hand-written mirror drifts from what Pipedrive returns, which
the spec check catches for responses (D-72). Every failure path is this
repository's own reading of Pipedrive's reference.

### D-72 Pipedrive's v2 description is a test oracle for the response types

`internal/pipedrive/spec_test.go` holds the upstream types to a fixture
projected from Pipedrive's v2 description: every JSON tag the package
decodes must be a field the description declares, with a type the Go
field can hold. `internal/pipedrive/testdata/v2-response-fields.json`
records its source URL, the spec's SHA256 and the capture date, and is
refreshed by hand from
[the description](https://developers.pipedrive.com/docs/api/v1/openapi-v2.yaml)
before a release. Nothing from the spec is compiled into the binary.

A hand-written mirror drifts silently. A tag under a name v2 does not
send decodes to its zero value on every record, and an integer field
where the spec says `number` fails a whole page on one fractional value.
The spec is a poor code input (D-71) and a good oracle for exactly that
drift.

Rejected: the spec as a code input.
Cost: `knownSpecOmissions` exempts two tags the description does not
declare: `is_writable`, which the `*Fields` endpoints return live and
the write guard depends on, and `people_count`, an `include_fields`
value every organization read asks for. The fixture is only as current
as its last refresh. Request bodies and query parameters are not held to
the description; whether to extend the check to them is open (#120).

### D-73 --dump-schemas prints what an in-memory client is served

`pipedrive-mcp --dump-schemas` builds the clientless server
`server.NewForSchemaDump` returns, connects an in-memory client to it,
and prints what that client is served: the instructions from initialize,
and the tools, resource templates, resources and prompts it lists, each
sorted, under a header naming the binary and SDK versions. It reads no
token, since no handler runs.

The schema-ack gate compares two of these dumps (D-76), so what it
compares is the wire rather than a list kept beside the server. A list
kept beside it holds only what it was written to hold.

Rejected: a parallel registry standing in for the listing, which left
the instructions and the resource templates invisible to the gate.
Cost: everything a client is served is in the comparison, so a reworded
description or instruction is a surface change that needs a commit
footer like any other (D-76). The header's `sdkVersion` mirrors the
`go.mod` pin by hand, and `TestSDKVersionMatchesGoMod` holds the two
together.

### D-74 Every tool is added through mcp.AddTool, which infers its schemas

Every tool is registered with the SDK's `mcp.AddTool`, from each
resource file's `Register` function in `internal/tools`. `AddTool`
infers a tool's input and output schemas from its handler's Go types and
their `jsonschema:` tags.

One source for a schema and the handler that reads it means the two
cannot disagree: the schema a client validates against is the type the
handler decodes. The SDK validates a call's arguments against that
schema before the handler runs, so a malformed call is the SDK's
JSON-RPC error rather than the handler's (D-50).

Rejected: hand-written JSON schemas beside each handler, a second
description of each input that can drift from its type.
Cost: a type the SDK cannot read panics at start, naming the tool, so a
bad tag fails every run of the binary rather than one call; `make smoke`
starts the built binary and catches it before a merge. Input shape is a
schema change and a semver event (D-75), so the Go types are part of the
contract.

### D-75 The tool surface is the versioning contract, and a break needs a MAJOR

The versioning contract is the MCP tool surface: tool names, input
schemas, output schemas and documented behavior, as the preamble of
`CHANGELOG.md` states it. A break of that surface needs a MAJOR release.
Package layout, error message wording and log formats are outside the
contract, and so are the v1 tools, which leave in a MINOR release if v1
stops answering (D-04).

A model and its client depend on the surface and on nothing else this
repository ships: a renamed tool or a removed output field breaks a
working conversation, while a reworded error does not. The contract is
what the schema-ack gate measures, so the promise and the check cover
the same thing (D-76).

Rejected: release artifacts as part of the contract, which would make an
archive or bundle rename a MAJOR.
Cost: a rename of an archive, the bundle or the checksum file breaks a
script that downloads by name, and ships in a MINOR with a note for
scripts in its CHANGELOG entry. Schema-ack counts a break only against
the last release, so narrowing something no release shipped is a plain
change.

### D-76 A pull request that changes the surface names each changed item in a commit footer

A pull request that changes what `--dump-schemas` prints names each
changed item in a footer of a commit it adds: `SCHEMA-CHANGE:` for an
addition or a change, and `BREAKING CHANGE:` for a break of the last
release. A tool is named by its name, a resource template by its URI
template, and the instructions as `instructions`. The `schema-ack` gate
builds the merge-base and the head, compares the two dumps without their
headers, and prints the footer that fixes a refusal. Annotations count,
since a flipped `destructiveHint` changes whether a client asks the
person.

Semver depends on knowing which change breaks a caller (D-75). A footer
per item makes each change to the contract an acknowledgment somebody
wrote on purpose, and a break has to say so in the words a release
reader looks for.

Rejected: a `BREAKING CHANGE:` footer on any schema diff, which marked
every addition as a break.
Cost: every reworded description needs a footer too, usually in an empty
commit. The gate builds both sides from commits and needs a merge-base,
so it runs on a pull request and stays out of `make check` (D-84).

### D-77 Every repository script is Go

Everything the repository runs on itself is Go: the gates in
`scripts/gates`, one program behind a registry that
`go run ./scripts/gates` lists, and the evals in `scripts/evals`.

A shell script is held to no gofmt, vet, lint or test. `make check` has
to run on whatever a maintainer has installed, where bash and jq are a
dependency rather than a given. A script that parses JSON with grep is
how a quote ends up inside a string, and a grep for `"tools"` passes on
a line that merely mentions the word, an error message included; the
smoke gate decodes the frame instead. The sibling servers converted
their scripts for the same reasons.

Rejected: shell scripts, held to no gofmt, vet, lint or test.
Cost: `.github/merge-base.sh` stays a shell file, which CI runs to find
the commit a pull request is measured against. The Makefile's recipes
are shell lines too.

### D-78 A live suite behind the integration tag drives the server internal/app builds, for what a fake cannot answer

`internal/integration` is the live suite. Every file in it carries
`//go:build integration`, so `go test ./...` never reaches the network,
and `make integration` runs it. It resolves the workspace through
`internal/app`, the startup assembly the binary runs, and drives the
server that assembly builds over an in-memory transport (D-34). Without
a domain and a token every test skips with the reason. A test belongs
here only when a fake cannot answer it: the wire format, custom fields
against the workspace's own definitions, cursor paging, the guards over
records somebody filled in, and the class a real status maps to.

A fake agrees with whatever was believed when it was written, so a unit
test can confirm only that the code sends what the code sends. Pipedrive
accepts `lost_reason` only on a lost deal and treats `person_id` on an
activity as read-only, and a fake accepts both;
`TestWrite_DealReopensFromWonAndFromLost` and
`TestWrite_ActivityLinksToAPerson` hold what Pipedrive does.

Rejected: unit tests against fakes alone.
Cost: the suite runs by hand against one workspace, so a custom-field or
permission setup unlike it is untested, and no CI job runs it.
`TestWrite_PersonEmailsReplaceWholesale` holds that an update replaces a
person's emails; whether to probe the phones the same way is open
(#125).

### D-79 Live write probes need a second opt-in, stay in their own pipeline, and restore and verify every write

The write probes run under `make integration-writes`, which sets
`PIPEDRIVE_INTEGRATION_WRITES=1` on top of the build tag. A probe that
creates or closes a deal also needs `PIPEDRIVE_TEST_PIPELINE_ID`, naming
a pipeline the business does not use, and there is no default. Every
probe keeps the contract written at the top of
`internal/integration/write_test.go`: capture the value first, restore
it from `t.Cleanup`, verify the restore with a fresh read, append to a
collection and trim it back, never write a field that was empty, and
assert on shapes rather than content. A restore that did not take fails
the test with `LEFT CHANGED`. Under `PIPEDRIVE_DRY_RUN` the probes skip,
since every write would rehearse.

The configured workspace is a real CRM and Pipedrive has no undo. A
restored field is not an undone write: a status change fires the
workspace's automations, and even a field that restores perfectly sits
on a live record in somebody's reports while the test holds it. So the
records the suite changes carry `mcp-test` in their names, live in the
test pipeline and are deleted when the test ends.

Rejected: probes that edit live customer records and call the restore a
success.
Cost: the note probe leaves a soft-deleted note, since a v1 delete
clears `active_flag` and nothing purges it. The other deletes are soft
too, and Pipedrive purges them after 30 days.

### D-80 Evals drive a model through the tools and score the end state and the trace

`make evals` builds the binary and runs `scripts/evals` under the `live`
tag: each task gives a model this server's tools alone, through the
`claude` CLI, and scores the result twice. The end state is read back
through the server, and the trace of tool calls is scored against the
task's rules. The fixture, an invented organization, person, deal,
activity and note, is built through the server's own tools in the
pipeline `PIPEDRIVE_TEST_PIPELINE_ID` names, and deleted afterwards.

The integration suite proves the tools work; the evals test whether a
model can use them, which is the claim that fails quietly. A model's
account of what it did is the least reliable thing in a run, hence the
read-back. A task can also be completed by a model that guessed an id
and was lucky, a pass by outcome and a failure by every rule the server
is built on, hence the trace. The task table, the stream parser and the
census build without the tag, so `go test ./scripts/evals` checks them
with no credentials.

Rejected: scoring the model's own account of what it did.
Cost: a run needs credentials, a network, the `claude` CLI and the test
pipeline, writes to the configured workspace and costs money, so it runs
by hand when text the model reads changes, never in CI.

### D-81 The eval census fails a run that created records outside its fixture, and reports edits

After an eval run, the census asks `list_deals`, `list_persons`,
`list_organizations` and `list_activities` for the records updated since
the run began, subtracts the fixture's own ids, and splits the rest into
records the run created and records that existed and moved. A created
record fails the run. An edited one is printed and does not fail it. A
listing that fails counts as a record the run cannot account for, so
silence is never read as cleanliness.

An eval drives a model, not a script: a task that says "log that we
spoke to Acme" can be answered by creating an activity nobody asked for.
The harness cannot prevent that and cannot remove what it did not
create, but it can refuse to let it pass unnoticed. Edits are not fatal
because the workspace is shared: a colleague editing a contact during
the run moves its `update_time` too, and an alarm that fires on other
people's work is one people learn to ignore.

Rejected: a one-page count either side of the run, which saturates at
the page cap and reports no drift on a workspace that large; a fatal
edit alarm on a shared workspace.
Cost: a model editing a real record is printed rather than failed, so a
run that changed somebody's record can pass.

### D-82 Coverage is held at 80% on internal/pipedrive, internal/tools, internal/credentials and internal/app

`make cover` runs the tests with a coverage profile and holds each of
`internal/pipedrive`, `internal/tools`, `internal/credentials` and
`internal/app` to 80% statement coverage, through
`go run ./scripts/gates coverage`. The packages are listed in
`floorPackages` in `scripts/gates/coverage.go`, and a listed package the
profile has no blocks for counts as 0%. CI runs the same gate (D-84),
and `make coverage` opens the HTML report.

These are the packages whose behavior reaches Pipedrive or the token:
the client, the tool handlers and their guard, the credential resolution
and the startup assembly. The floor is per package, because one figure
for the module lets a well-tested package carry an untested one. A
listed package missing from the profile was not tested by the run that
wrote it, so it fails rather than being skipped.

Rejected: one coverage figure for the whole module, which lets a
well-covered package hide an untested one.
Cost: `cmd/pipedrive-mcp`, `internal/config`, `internal/server` and the
other packages sit outside the floor, and a package added under
`internal/` stays outside it until `floorPackages` names it.

### D-83 A check is watched failing, and a gate refuses to pass on an empty input

A check is believed only after it has been seen to fail. A gate rule
ships with a test that plants the break it catches, and the rule is
broken once in the code to watch that test fail. A gate also refuses to
pass on an empty input: the release checks refuse a configuration they
read nothing from, `descriptions` fails on an empty dump, and `parity`
refuses a registry, a `check:` target, a set of recipes or the tree's
build tags when too few to be the real ones.

A gate that read nothing reports the same pass as one that read
everything. A verifier that succeeds prints little: `cosign verify-blob`
and `gh attestation verify` say almost the same after checking something
as after checking nothing. A rule nobody has seen fail may be reading
the wrong file, matching the wrong pattern or counting a number that
saturates, and every run still looks green.

Rejected: a verification that cannot fail, trusted because it exits 0.
Cost: each rule costs a test that plants its break. A verification the
release depends on, such as the cosign check, is run by hand against a
corrupted copy before its pass is believed.

### D-84 make check and CI run the same gates

`make check` and the CI workflow run the same gates, and the `parity`
gate holds them to it. It reads the recipe of each `check:` prerequisite
and finds each command in what `.github/workflows/ci.yml` runs. That is
its `run:` steps, and the recipe of each target a step runs with `make`.
CI's golangci-lint and gitleaks actions stand in for those two tools,
and a target or an action CI runs needs a `check:` prerequisite that
runs it.

A gate `scripts/gates` declares runs in both. A pull-request gate,
`changelog` or `schema-ack`, runs in CI and stays out of `make check`.
Each build tag a `//go:build` line in the tree requires has a tagged
`go vet` pass on both sides. `CLAUDE.md` names the `check` target as
the list rather than copying it.

Two lists in two files drift silently, since an edit touches one of
them. A gate CI skips can break on `main` with every pull request green,
and a gate only CI runs fails a pull request that passed `make check`.
The comparison is by command, because a target and what CI runs for it
need not share a name: `make staleness` runs `gates deps`. A hand list
of targets would be a third list, and a prerequisite on none passes
unseen.

Rejected: a hand list of the tools CI runs, which a new prerequisite
passes by being on no list; a copy of the `check` list in `CLAUDE.md`,
which needs a gate of its own to stay true.
Cost: a CI step no prerequisite runs, such as `go mod verify` or CodeQL,
is outside the comparison. A step disabled by an `if:` that is never
true is not seen, and a command is matched by its first three words.

### D-85 Every action and tool is pinned, and make check runs each tool at the version CI pins

Each tool `make check` runs, golangci-lint, govulncheck, go-licenses and
gitleaks, runs at an exact version through `go run <module>@<version>`,
so whatever is installed on the machine does not decide which version
answers. CI runs govulncheck and go-licenses through `make vuln` and
`make licenses`, so the Makefile alone names their versions. CI runs
golangci-lint and gitleaks through actions, and the `pins` gate holds
the versions `.github/workflows/ci.yml` gives those two equal to the
Makefile's. It also holds every Makefile version exact, every action to
a commit and every tool to an exact version (D-86).

A tool at another version passes what CI rejects or fails what CI
passes: a linter adds checks, and a vulnerability scanner learns
advisories. A local run that agrees with CI only by luck is not a check
anyone can lean on before a push, and a mismatch shows up as a red pull
request rather than at the keyboard.

Rejected: installing the tools at `@latest`, which is how a local run
passes something CI rejects; a check that only warns when the installed
version differs.
Cost: go-licenses cannot resolve Go's standard library from a toolchain
Go downloaded into the module cache, so `make licenses` fails under one,
as with `GOTOOLCHAIN` naming a Go other than the installed one. CI
installs a real Go and is unaffected.

### D-86 Every action is pinned by commit SHA and every installed tool by exact version

Every `uses:` in a workflow names a full commit SHA, with the version in
a trailing comment, and every tool version a workflow names is exact.
The `pins` gate reads every workflow and refuses a ref that can move, a
version that can float, an action it has not classified, and an action
that installs a tool without naming the tool's version. It also holds
every workflow to a `bash` default shell, and pre-commit, the Makefile
and CI to one gitleaks.

A tag can be moved to other code by whoever controls the action, and a
SHA cannot. A SHA pins the wrapper and not what it installs:
`sigstore/cosign-installer` with no `cosign-release` installs whatever
cosign is newest, default signing format included, so a tool's version
is pinned in its own right. An unclassified action fails rather than
passes, because a check that reads only the versions written down cannot
see one left out.

Rejected: a tag or a major-version ref on an action, which its owner can
move; a tool left at its action's default.
Cost: every bump is a SHA edit, which Dependabot proposes weekly for
actions and Go modules, and an action a workflow adds needs a row in the
gate's classification before CI passes.

### D-87 A direct dependency more than six months behind fails unless go.mod pins it with a reason

`gates deps`, run by `make staleness`, lists the module's direct
dependencies with `go list -m -u` and fails when one has an update more
than six months old, unless its `require` line in `go.mod` carries
`// pinned: <reason>`. Dependabot proposes Go module updates weekly.

A dependency left far behind collects fixes and advisories it never
receives. The server's protocol support is entirely its SDK's (D-02), so
a lagging SDK is a server a modern client cannot talk to. A pin is
allowed, but only with its reason written beside it, since a bare pin is
a decision nobody recorded.

Rejected: a bare pin with no reason, which hides the decision behind it.
Cost: the gate reaches the network, so a slow or unreachable module
proxy fails `make check`.

### D-88 Dependencies are held to a license allow-list

`make licenses` runs go-licenses over every package the module builds
and fails on any dependency whose license is not on the allow-list:
Apache-2.0, MIT, BSD-2-Clause, BSD-3-Clause, ISC and MPL-2.0. CI runs
`make licenses` (D-84).

The release archives and the bundle carry every dependency compiled into
one binary, published under the Apache License 2.0, so each dependency's
license has to allow that redistribution. An allow-list asks the
question for every license the module meets, rather than only for the
ones somebody thought to forbid.

Rejected: a deny-list of known-bad licenses, which passes a license
nobody has read.
Cost: a dependency under any license outside the list fails the gate
until the list names it, a permissive one included, so adding such a
dependency is also a change to the list in the Makefile.

### D-89 Nothing from a real Pipedrive account is committed, in the tree or the history

Nothing that identifies a real Pipedrive account is committed: no
workspace domain, token, record id, name, address or phone number, in a
file, a path, a commit message or a tag. Fixtures are invented,
addresses use reserved domains such as `example.com`, and phone numbers
come from ranges set aside for fiction. `make leaks` scans the tree,
untracked files included, and `make gitleaks` scans it and the branch's
commits for secrets.
`make leaks-history` reads everything the repository has published,
every commit message, path, blob and annotated tag message; it reads the
whole object store, so it runs by hand before a tag.
`make gitleaks-history` scans every commit for secrets, by hand too.

The repository is public, and a value committed and edited out later is
still on GitHub. A CRM holds other people's contact details, so a leak
here names third parties who never chose to be in it.

Rejected: rewriting a public history that carries released tags;
excusing a known finding by its value, which would excuse the same value
anywhere else. A known finding is accepted by its blob hash, rule and
count.
Cost: a national phone number with no international prefix is not
caught, since too much else has its shape.

### D-90 A change to watched source needs a CHANGELOG entry under [Unreleased]

A pull request that changes `internal/tools/`, `internal/pipedrive/`,
`internal/app/` or `cmd/` adds at least one line under `## [Unreleased]`
in `CHANGELOG.md`. The `changelog` gate reads the diff from the
merge-base and counts the added lines under that heading. A release cut
renames the heading to the version, so in a diff that removes
`[Unreleased]`, the version heading that replaced it counts as the
unreleased section.

The release notes are that section (D-91), so a change with no entry
ships unannounced. An entry added under a released heading is invisible
to anyone reading the release it belongs to, on the page as in the file.

Rejected: an entry under any heading, which can land under a version
already released; an entry for every change, docs and workflows
included, where a reader of the changelog expects what changes the
server.
Cost: a change outside the watched trees, such as `internal/config`,
`internal/server` or `scripts/`, needs no entry, so whether it deserves
one is left to review. The gate needs a merge-base, so it runs on a pull
request and not in `make check`.

### D-91 The release notes are the CHANGELOG section, and every version heading has its link

A release's notes are its version's section of `CHANGELOG.md`, lifted by
`gates release-notes` and handed to goreleaser as `--release-notes`;
`.goreleaser.yaml` has no changelog block. The headings inside the
section are promoted one level, since the release page's title is the
version. The `changelog-links` gate holds every version heading to its
compare-link definition in the footer.

The CHANGELOG is written for a person deciding whether to upgrade, in
Keep a Changelog's sections, while a commit list is written for the
people who made the commits. One text in both places means the page a
user lands on says what the file says.

Rejected: goreleaser's git-log changelog, a list of commit subjects;
switching it off with `changelog.disable`, which also stops goreleaser
reading `--release-notes` and leaves the page with the verification
footer alone.
Cost: a release whose section is missing, or still headed
`[Unreleased]`, fails the release workflow before anything is published,
so renaming the heading is part of preparing a release.

### D-92 One cosign signature over checksums.txt covers every archive, SBOM and the bundle, beside build provenance

A release carries one signature: the release workflow signs
`checksums.txt` with keyless cosign into `checksums.txt.bundle`, and
that file holds the SHA256 of every archive, every CycloneDX SBOM and
the `.mcpb` bundle. Beside it, `actions/attest-build-provenance` attests
the archives, the bundle and `checksums.txt` with build provenance. The
documented verification checks the signature first, against
`release.yml` on a tag ref, and only then the checksums.

A signature per artifact buys a longer asset list rather than more
assurance, since every artifact is already covered by its hash in a
signed file. The signature is checked before the checksums are read,
because until it passes `checksums.txt` is only what the release assets
say.

Rejected: per-artifact `.sig` and `.cert` files.
Cost: signing, the provenance attestation and the registry publish each
need an OIDC token that only a workflow run on a tag has.
`goreleaser check`, actionlint and a snapshot build all pass while any
of the three is wrong, and the first tag is where it shows.

### D-93 Release builds are reproducible

A release build is reproducible: the same commit builds the same bytes.
Goreleaser builds with `CGO_ENABLED=0`, `-trimpath` and the version
stamped from the tag by ldflags. Before it publishes anything, the
release workflow builds one target twice in snapshot mode and fails
unless the two binaries have the same SHA256.

The signature and the provenance say which workflow built an artifact,
not that the source produces it. A build anyone can repeat from the tag
lets a third party check the published bytes against the source. A
nondeterministic input, such as a build path, a hostname or an unpinned
dependency, is caught before a release carries it.

Rejected: trusting the signed build alone, with no check that the source
yields the same bytes again.
Cost: the check builds one target twice on one runner, so it catches
nondeterminism within a build, not a difference between machines. A
failed check stops the release, and its cause has to be traced from the
two hashes in the log.

### D-94 A release is six archives named as the sibling servers name theirs, and the bundle carries the Windows amd64 binary alone

A release is six archives, darwin, linux and windows on amd64 and arm64,
named `pipedrive-mcp_<version>_<os>_<arch>`, a zip on Windows and a
tar.gz elsewhere. That is goreleaser's own form, which the sibling
servers' releases, google-chat-mcp and google-docs-mcp among them, use
too. The Claude Desktop bundle stages the macOS universal binary, both
Linux binaries behind a launcher, and the Windows amd64 binary. Windows
on arm64 runs that one under emulation, and its archive is for an
install from the release page. `gates mcpb` holds `.goreleaser.yaml` to
that matrix both ways, every pair to a binary the bundle runs, and every
archive name in the Markdown, the workflows and the release footer to
the names it renders.

One set of names across the sibling servers lets a person or a script
that installs one install the others the same way. A bundle manifest
names one command per platform, so one Windows binary serves both
architectures.

Rejected: an ignore list or a second build in the release configuration;
the old dashed archive names.
Cost: the gate reads `.goreleaser.yaml` with regular expressions, so it
refuses a second build and any ignore list rather than read them wrong.
Nothing cross-compiles every target before a tag, so a change that
breaks windows/arm64 fails only in the release job; whether to build
every release target in CI or `make check` is open (#116).

### D-95 The bundle manifest is validated against a vendored schema pinned by SHA256

`packaging/mcpb/manifest.json` is validated against the bundle-manifest
schema for its `manifest_version`, vendored as
`packaging/mcpb/mcpb-manifest-v0.3.schema.json`. `scripts/gates/mcpb.go`
records the vendored file's SHA256 in `vendoredSchemaSHA256` and the
schema's mcpb release tag in `schemaRef`, holds `$schema` to that pinned
URL, and holds the manifest version to a floor. The gate validates
offline, and the packer validates the stamped manifest again after
writing the real version into it.

The schema closes its root with `additionalProperties: false`, so a
misspelled top-level key, a wrong type or a missing required field shows
only against it; every referential check passes such a manifest. A tag
pins bytes where a branch does not, and the hash ties the vendored copy
to the bytes somebody reviewed, so the version, the ref and the bytes
move together.

Rejected: validating against the unpinned URL that serves whatever
schema is current, which lets the declared version and the schema it is
checked against disagree silently.
Cost: the hash proves the bytes somebody reviewed, not what the URL
serves, so only a re-fetch shows an upstream retag. Nothing installs the
bundle in Claude Desktop: the generated launcher and keyring access from
a process Claude Desktop starts are unexercised by any release step, and
how to exercise them is open (#114).

### D-96 The bundle's settings come from config.Vars, and its defaults are started through config.Load

The settings the bundle asks for at install derive from `config.Vars`:
every variable the server reads, less those `bundleLeftOut` names, each
with a reason. The gate refuses a reason given for a variable the server
does not read, and requires the field for a variable the registry marks
`Secret`, the API token, to be sensitive. The bundle's defaults are
started through `config.Load` and must configure the server as no
environment does, so the gate names each field where they differ. It
refuses a default it cannot check that way: one on a required field, one
on a variable `Load` does not read, as the token and the domain are, and
one holding a `${` the host would expand.

A setting added to the server is asked for at install until the manifest
asks for it or a reason is written, so the bundle cannot fall behind the
server it installs. Starting the server's own loader is the only way to
know that a default means what the server means by it.

Rejected: a hand-kept list of settings in the manifest, which falls
behind the server it describes.
Cost: `LOG_LEVEL`, `LOG_FORMAT` and `PIPEDRIVE_HTTP_TIMEOUT` are not
asked for at install, and the token and the domain take no default, so a
person types both at install.

### D-97 The registry entry is published by its own workflow, from checksums.txt after cosign verifies it

The MCP registry entry is published by
`.github/workflows/publish-mcp.yml`, which `release.yml` calls once the
release exists and which can be dispatched by hand against a tag that
already shipped. It downloads the published `checksums.txt`, verifies
its cosign signature against `release.yml` on a tag ref, and only then
lets `gates registry-publish` lift the bundle's row into the entry's
`fileSha256`. It verifies `mcp-publisher` with cosign before unpacking
it, and holds no more privilege than publishing needs. A prerelease
publishes no entry.

The registry sends a HEAD to the bundle's URL before it accepts an
entry, so publishing runs last, and a step that runs last needs a way to
run again on its own: re-running `release.yml` would re-run goreleaser
against a release that exists. A client that trusts the registry
verifies the bundle against that hash. Somebody who can replace a
release asset can edit an unsigned checksum file to match, and the
signature is what does not move with them. An entry cannot be withdrawn.

Rejected: publishing from inside `release.yml`; reading the checksums
before verifying them.
Cost: the signature's identity accepts `release.yml` on any tag of this
repository, and the bundle's row is not checked against the tag being
published; whether to hold both to that tag is open (#118).

Next ID: D-98

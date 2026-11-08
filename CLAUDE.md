# CLAUDE.md — pipedrive-mcp

A Go MCP server for Pipedrive over stdio: one binary per person, one
workspace, the person's own API token, nothing hosted (D-01). Why the
code is the way it is lives in `docs/architecture.md`, one decision per
`D-NN`. Read the decision before changing the tool surface, the write
path, the error classes or the package layout.

## Commands

- `make check` runs every gate CI runs except the pull-request ones; its
  `check` target is the list, and `make parity` holds it to CI (D-84).
- `make integration` drives the server against the configured workspace
  and changes nothing. `make integration-writes` adds the write probes,
  and `make evals` drives a model through the tools. `CONTRIBUTING.md`
  covers all three; none runs in CI.
- `go run ./scripts/gates` lists the gates; add a name to run one.

## Hard rules

1. **Never commit anything internal**: no company domains, record or
   account ids, email addresses, company or person names, tokens, or
   content from a real Pipedrive account, in code, fixtures, docs,
   commits, pull requests or logs (D-89). Naming a sibling server, such
   as google-chat-mcp or favro-mcp, is fine. Fixtures are invented.
   `make leaks`, `make leaks-history` and gitleaks hold it.
2. **Never write to stdout outside `main`.** Stdout carries only MCP
   JSON-RPC frames; logs go to stderr through `slog` (D-58). forbidigo,
   `make smoke` and `TestTheGeneratedLauncherRefusesToStdout` hold it.
3. **Never log a payload, a query string or the token**, and never log
   from a tool handler (D-59, D-60, D-61).
   `TestClient_NoLogOrErrorCarriesTheSearchTerm` and
   `TestSettings_LogValue_RedactsTheToken` hold it.
4. **Never add a Pipedrive v1 call beyond notes and `whoami`** (D-03).
   v2 is the default, and the v1 tools sit outside the compatibility
   promise (D-04). `TestClient_WhoAmI_UsesV1` and
   `TestClient_ListNotes_FiltersAndV1Pagination` hold the two.
5. **Never send a write the guard has not passed.** Every update runs
   the `guardedWrite` spine: read, `expect_version`, diff, the
   `overwrite` refusal, the dry-run gate, then the write (D-27, D-37).
   A refusal names what it protects and the argument that permits the
   write (D-29). `TestGuardedWrite_RefusalNeverReachesPut` and
   `TestOverwrite_NamingSomeFieldsStillRefusesTheRest` hold it.
6. **Never let a call turn `PIPEDRIVE_DRY_RUN` off.** It is a floor
   every write honors; a per-call `dry_run` only turns a rehearsal on
   (D-32, D-33). `TestRuntime_NewServer_CarriesTheDryRunFloor` and
   `TestDelete_HonorsTheDryRunFloor` hold it.
7. **Never add a writable field without its row in the resource's field
   table**, which drives the diff, the guard and the request (D-38,
   D-39). `TestEveryWritableFieldHasAFieldSpec` and
   `TestEverySettableFieldReachesTheDiff` hold it.
8. **Never claim a field can be cleared, or send a null to clear one**
   (D-42). Optional update inputs are pointers whose descriptions say to
   omit them to leave a value as it is (D-41).
   `TestClient_PatchV2_NilFieldsNeverReachTheWire` and
   `TestClient_PatchV2_EmptyStringIsSentVerbatimNotAsNull` hold it.
9. **Never add `force` or a confirmation flag where the caller can see
   what it loses** (D-28). Deletes register by default and guard the
   call, and a soft delete takes `dry_run` and `expect_version` alone
   (D-46, D-47). The named transitions take no `overwrite` (D-43).
   `TestDelete_SendsTheDeleteAndReportsIt` and
   `TestREADMENamesEverySelfAuthorizingAction` hold it.
10. **Never retry a write that may have reached Pipedrive.** A write
    retries a 429 and a failed dial alone (D-54).
    `TestClient_AWriteWhoseAnswerIsLostIsSentOnce` and
    `TestClient_PatchV2_NoRetryOn5xx` hold it.
11. **Never return an error outside `[class] message`, or classify by
    its text** (D-50, D-51). A class comes from a sentinel, and a new one
    needs an entry in `docs/troubleshooting.md`.
    `TestEverySentinelHasItsOwnClass` and
    `TestTroubleshootingDocListsEveryErrorClass` hold it.
12. **Never import anything outside the standard library in
    `internal/pipedrive`, such as the MCP SDK, or generate a Pipedrive
    client** (D-70, D-71); depguard holds both. Never embed a
    `pipedrive` type in an LLM-facing type, or put a `jsonschema:` tag
    in `internal/pipedrive` (D-17); review holds that.
13. **Never register a tool outside `mcp.AddTool`** in its package's
    `Register` (D-74). `make smoke` and
    `TestRegister_ListsItsToolsWithTheirSchemas` hold it. A description
    follows the house style (D-10), and `make descriptions` checks its
    mechanical rules.
14. **Never rename or drop a released tool, input or output field, or
    make an input required, outside a MAJOR** (D-75). A pull request
    names each changed item of the surface in a commit footer, which
    `schema-ack` holds (D-76).
15. **Never read an environment variable except through
    `config.Lookup`, or add one outside `config.Vars`** (D-66).
    `TestEveryReadGoesThroughLookup` and
    `TestEveryVariableTheCodeNamesIsInEnvVars` hold it.
16. **Never point a live write at a record the run did not create, or
    outside the test pipeline.** The configured workspace is a real CRM
    (D-79, D-81). Each write probe restores what it changed and verifies
    the restore with a fresh read.
17. **Never add a shell script**: repository scripts are Go (D-77).
    **Never commit to `main`, push or tag.** The owner does; a tag
    publishes a registry entry that cannot be withdrawn (D-97).
18. **Never change a release workflow or `schema-ack`, or disable a
    gate**, such as with `--no-verify`, a lint exclusion or `t.Skip` on
    a passing test, without the owner's go-ahead (D-76, D-83, D-97).
19. **Never adopt a belief about Pipedrive without a primary source**:
    its reference or a live run (D-78). Record a verified Pipedrive
    behavior in the comment at the line that depends on it, with the
    reference link and the integration test that checks it, as
    `Checked by the integration test TestX/sub.`

## Definition of done

- `make check` passes. On a pull request CI also runs `schema-ack`, and
  the `changelog` gate, which wants an entry under `[Unreleased]` for a
  change to watched source (D-90).
- New behavior has tests, and the schema diff has been read
  (`make dump-schemas` against the last tag's).
- `/simplify` and `/code-review medium` have run, never at high. A fixed
  finding is one line in the commit body. A declined one goes in the PR
  body, or becomes a present-tense constraint comment at the line it
  protects. Never commit review output or write findings into `docs/`.
- `make integration-writes` has run for anything touching the write path
  or a response shape, and `make evals` for a change to text the model
  reads.
- `/security-review` runs before a release (`docs/release.md`).

## Traps

- **Green gates are not done.** Every fake is written from what we
  believe Pipedrive does; only the integration suite tests it (D-78).
- **`make integration-writes` and `make evals` write to a real CRM.**
  They need `PIPEDRIVE_TEST_PIPELINE_ID` naming a pipeline the business
  does not use, because closing a deal fires the workspace's automations
  (D-79, D-80).
- **An empty page is not the end.** Pipedrive's index keeps deleted
  organizations and persons, and `search` drops them after Pipedrive has
  paged, so a page can be empty and carry `next_cursor` (D-11, D-15).
- **The HTTP timeout is per attempt**, so a retried read takes longer
  (D-56).
- **A linter settings block that fails to load leaves its defaults on**,
  still firing. Check forbidigo's and depguard's message text.
- **A newer local Go passes what CI fails.** Run the gates on the version
  `go.mod` names, which CI reads through `go-version-file`.
- **Never delete a decision or reuse an ID.** Supersede or deprecate it,
  and take the next ID from the file's `Next ID:` line.

## Writing

Plain and short: lead with the outcome, one idea per sentence, no
narration, no count a gate does not check. History goes in commit
messages. `release.yml` lifts the version's CHANGELOG section into the
release notes, so the rules in `CONTRIBUTING.md` are not optional
(D-91).

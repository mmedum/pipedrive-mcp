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

1. **Keep everything internal out of the repository and all it
   publishes, because both are public**: company domains, record or account ids, email addresses,
   company or person names, tokens, and content from a real Pipedrive
   account, in code, fixtures, docs, commits, pull requests or logs
   (D-89). Naming a sibling server, such as google-chat-mcp or
   favro-mcp, is fine. Fixtures are invented. `make leaks`,
   `make leaks-history` and gitleaks hold it.
2. **Write to stdout only from `main`**, because a stray print corrupts
   the stream. Stdout carries only MCP JSON-RPC frames; logs go to
   stderr through `slog` (D-58). forbidigo, `make smoke` and
   `TestTheGeneratedLauncherRefusesToStdout` hold it.
3. **Log a request's path, method, status, attempt and duration, and a
   transport failure's error, not a payload, a query string or the
   token**, and leave tool handlers silent (D-59, D-60, D-61).
   `TestClient_NoLogOrErrorCarriesTheSearchTerm` and
   `TestSettings_LogValue_RedactsTheToken` hold it.
4. **Call Pipedrive v2, and v1 for notes, `whoami`, leads, files and goals
   alone** (D-105).
   v2 is the default, and the v1 tools sit outside the compatibility
   promise (D-04). `TestClient_WhoAmI_UsesV1` and
   `TestClient_ListNotes_FiltersAndV1Pagination` hold the two.
5. **Send a write only after the guard passes it.** Every update runs
   the `guardedWrite` spine: read, `expect_version`, diff, the
   `overwrite` refusal, the dry-run gate, then the write (D-27, D-37).
   A refusal names what it protects and the argument that permits the
   write (D-29). `TestGuardedWrite_RefusalNeverReachesPut` and
   `TestOverwrite_NamingSomeFieldsStillRefusesTheRest` hold it.
6. **Honor `PIPEDRIVE_DRY_RUN` whatever a call asks.** It is a floor
   every write honors; a per-call `dry_run` only turns a rehearsal on
   (D-32, D-33). `TestRuntime_NewServer_CarriesTheDryRunFloor` and
   `TestDelete_HonorsTheDryRunFloor` hold it.
7. **Add a writable field together with its row in the resource's field
   table**, which drives the diff, the guard and the request (D-38,
   D-39). `TestEveryWritableFieldHasAFieldSpec` and
   `TestEverySettableFieldReachesTheDiff` hold it.
8. **Describe a field as changeable, not clearable, and omit it rather
   than send a null to clear it**, because Pipedrive v2 has no spelling
   that empties a field (D-42). Optional update inputs are pointers
   whose descriptions say to omit them to leave a value as it is (D-41).
   `TestClient_PatchV2_NilFieldsNeverReachTheWire` and
   `TestClient_PatchV2_EmptyStringIsSentVerbatimNotAsNull` hold it.
9. **Add `force` or a confirmation flag only where the caller cannot see
   what it loses** (D-28). Deletes register by default and guard the
   call, and a soft delete takes `dry_run` and `expect_version` alone
   (D-46, D-47); a permanent delete also takes `confirm` (D-99). The named transitions take no `overwrite` (D-43).
   `TestDelete_SendsTheDeleteAndReportsIt` and
   `TestREADMENamesEverySelfAuthorizingAction` hold it.
10. **Retry a write only when it cannot have reached Pipedrive**, because
    a second create makes a second record. A write retries a 429 and a
    failed dial alone (D-54).
    `TestClient_AWriteWhoseAnswerIsLostIsSentOnce` and
    `TestClient_PatchV2_NoRetryOn5xx` hold it.
11. **Return every error as `[class] message`, and classify by sentinel
    rather than by text** (D-50, D-51). A new class needs an entry in
    `docs/troubleshooting.md`.
    `TestEverySentinelHasItsOwnClass` and
    `TestTroubleshootingDocListsEveryErrorClass` hold it.
12. **Keep `internal/pipedrive` on the standard library alone, rather
    than the MCP SDK or another import, and write its client by hand
    rather than generating one** (D-70, D-71); depguard holds both.
    Define each LLM-facing summary type as a parallel shadow with its
    own `jsonschema:` tags, rather than embedding a `pipedrive` type or
    tagging a type in `internal/pipedrive`; a nested type whose shape
    already fits may be used as it is (D-17). Review holds that.
13. **Register every tool through `mcp.AddTool`** in its package's
    `Register` (D-74). `make smoke` and
    `TestRegister_ListsItsToolsWithTheirSchemas` hold it. A description
    follows the house style (D-10), and `make descriptions` checks its
    mechanical rules.
14. **Save renaming or dropping a released tool, input or output field,
    and making an input required, for a MAJOR** (D-75). A pull request
    names each changed item of the surface in a commit footer, which
    `schema-ack` holds (D-76).
15. **Read every environment variable through `config.Lookup`, and add
    each one to `config.Vars`** (D-66).
    `TestEveryReadGoesThroughLookup` and
    `TestEveryVariableTheCodeNamesIsInEnvVars` hold it.
16. **Point a live write only at a record the run created, inside the
    test pipeline.** The configured workspace is a real CRM (D-79,
    D-81). Each write probe restores what it changed and verifies the
    restore with a fresh read.
17. **Write repository scripts in Go, not shell** (D-77), because a
    shell script is held to no gofmt, vet, lint or test. **Leave
    committing to `main`, pushing and tagging to the owner**; a tag
    publishes a registry entry that cannot be withdrawn (D-97).
18. **Get the owner's go-ahead before you change a release workflow or
    `schema-ack`, or disable a gate**, such as with `--no-verify`, a lint
    exclusion or `t.Skip` on a passing test (D-76, D-83, D-97).
19. **Base every belief about Pipedrive on a primary source**: its
    reference or a live run (D-78). Record a verified Pipedrive
    behavior in the comment at the line that depends on it, with the
    reference link and the integration test that checks it, as
    `Checked by the integration test TestX/sub.`

## Definition of done

- `make check` passes. On a pull request CI also runs `schema-ack`, and
  the `changelog` gate, which wants an entry under `[Unreleased]` for a
  change to watched source, or a `Changelog: none` footer (D-90).
- New behavior has tests, and the schema diff has been read
  (`make dump-schemas` against the last tag's).
- `/simplify` and `/code-review medium` have run, at medium rather than
  high. A fixed finding is one line in the commit body. A declined one
  goes in the PR body, or becomes a present-tense constraint comment at
  the line it protects. Write findings only in those places, not into
  committed review output or `docs/`.
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
- **Supersede or deprecate a decision rather than deleting it, and give
  each new one a fresh ID.** Take the next ID from the file's `Next ID:`
  line.

## Writing

Plain and short: lead with the outcome, one idea per sentence, facts
rather than narration, and a count only where a generator writes it or a gate checks it. History
goes in commit messages. `release.yml` lifts the version's CHANGELOG
section into the release notes, so follow the rules in `CONTRIBUTING.md`
exactly (D-91).

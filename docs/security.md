# Security model

The threat model and the invariants the code holds.
[`SECURITY.md`](../SECURITY.md) says how to report a vulnerability.

It applies to one binary, a subprocess of your MCP client on your
machine, with your own Pipedrive API token, against one workspace
(D-01). It talks only to `<domain>.pipedrive.com`. It opens no listener,
and has no telemetry, no crash reporting and no update check.

## Trust boundaries

| Boundary | Crosses | Assumption |
|---|---|---|
| MCP client ↔ this server | An OS pipe | Both run as the same OS user, unauthenticated. Stdout carries only protocol frames (D-58). |
| This server ↔ Pipedrive | HTTPS to the workspace's subdomain | The token travels in the `x-api-token` header alone (D-62). Redirects are refused (D-55), and every attempt has a timeout (D-56). |
| This server ↔ disk | The keyring and one pointer file | `pipedrive-mcp login` writes the token to the OS keyring and the domain to a file only you can read (D-63, D-64). No tool writes either, or the environment. |
| Record authors ↔ the model | Every record, note and field a tool returns | Written by anyone who can edit the workspace, sometimes to steer an assistant. |

## What is worth stealing

- **The API token.** It carries every permission of the user who owns
  it. It lives in the OS keyring or the environment.
- **Workspace content.** Deals, people, organizations, activities and
  notes pass through tool results to your client, and through nothing
  else.
- **The workspace itself.** A field overwritten, a deal closed or a
  record deleted. Pipedrive has no undo, and a status change fires the
  workspace's automations.

## Adversaries

**A record author writing to steer your assistant.** The server returns
record text faithfully and does not interpret it, so it cannot stop a
model being persuaded. It limits what persuasion reaches. An update
refuses to replace a field that holds a value unless `overwrite` names
it (D-27, D-30). No tool merges records or clears a field (D-08, D-42).
A delete is soft (D-47). With `PIPEDRIVE_DRY_RUN` set, the server cannot
write at all (D-33).

**A malicious MCP client, or a persuaded model driving an honest one.**
It can call any tool as you. `overwrite` and `expect_version` are
written by the model, so they inform it and are never your approval: a
model re-sends a refused write with the `overwrite` the refusal names
(D-30). Annotations are hints a client may ignore (D-48). Your client's
approval of each call is the control.

**The repository's own live runs.** `make integration-writes` and
`make evals` write to the configured workspace, a real CRM. A write probe
changes only records it created, in the pipeline
`PIPEDRIVE_TEST_PIPELINE_ID` names, and verifies each restore (D-79).
The evals fail a run that created a record outside its fixture (D-81),
and the model gets this server's tools and nothing else.

**A forged release.** One keyless cosign signature covers
`checksums.txt`, every archive carries build provenance (D-92), and the
registry entry's hash is read from that file only after the signature
verifies (D-97).

## Invariants the code holds

A test or a gate holds each of these. A way to break one is a
vulnerability.

- **Stdout carries only JSON-RPC.** forbidigo, `make smoke` and
  `TestTheGeneratedLauncherRefusesToStdout` hold it (D-58).
- **A log line carries the path, method, status, attempt and duration,
  never a query string, a body or the token.**
  `TestClient_NoLogOrErrorCarriesTheSearchTerm` and
  `TestSettings_LogValue_RedactsTheToken` hold it (D-59, D-61). No tool
  handler logs, so the server keeps no record of tool calls (D-60).
- **The token leaves only in its header, and never follows a redirect.**
  `TestClient_HappyPath` and `TestClient_RefusesRedirects` hold it (D-55,
  D-62).
- **The model reads Pipedrive's message, never a raw body.** A body that
  is not JSON is reported by its length.
  `TestClient_ErrorMessageComesFromTheBody` and
  `TestErrorText_UnhintedUpstreamIsUnchanged` hold it (D-53).
- **An update replaces a populated field only when `overwrite` names
  it.** `TestGuardedWrite_RefusalNeverReachesPut` and
  `TestOverwrite_NamingSomeFieldsStillRefusesTheRest` hold it (D-27,
  D-30).
- **A dry run sends no write, and no call can turn `PIPEDRIVE_DRY_RUN`
  off.** `TestGuardedWrite_DryRunNeverReachesPut`,
  `TestRuntime_NewServer_CarriesTheDryRunFloor` and
  `TestDelete_HonorsTheDryRunFloor` hold it (D-32, D-33).
- **A write whose answer was lost is not sent again.**
  `TestClient_AWriteWhoseAnswerIsLostIsSentOnce` and
  `TestClient_PatchV2_NoRetryOn5xx` hold it (D-54).
- **The domain is a Pipedrive subdomain before it reaches a URL or the
  keyring.** `TestLoad_Invalid` holds it (D-65).
- **Nothing in the repository comes from a real account.** `make leaks`,
  `make leaks-history` and gitleaks hold it (D-89).

## Not a vulnerability

Do not report these as vulnerabilities. Open an ordinary issue if one
surprises you.

- **The client did not prompt.** Annotations are hints, and a client can
  run a tool with nobody approving (D-48).
- **A model passed `overwrite` the person did not ask for.** The refusal
  informs the model; it cannot stop one (D-30). A write that replaces a
  field `overwrite` did not name is a vulnerability.
- **What runs as you can act as you**, the environment and the keyring
  included.
- **The token reaches everything its user can.** Pipedrive scopes a
  personal API token to its user, not to a task.
- **A tool result is not masked.** It is your data, returned to you.
- **A model following instructions in a record.** That is the client's
  concern. Content that makes the server itself break an invariant is a
  vulnerability.
- **Someone else's code.** A flaw in Pipedrive goes to Pipedrive, and
  one in a dependency to that project.

## Dependency vulnerabilities

`govulncheck` runs in `make check` and in CI, and fails when this
server's code can reach a vulnerable symbol. A CVE in a dependency is a
vulnerability here only then. It is fixed in a release whose CHANGELOG
entry names the CVE or GHSA; otherwise the dependency is updated in the
normal course. A direct dependency more than six months behind fails
`make staleness` unless `go.mod` gives a reason (D-87).

## What you are responsible for

- **The token's user.** Issue the token from a user with the least
  access you need, ideally one used only by this server, so Pipedrive's
  own history tells its changes from a person's.
- **Rehearsing.** Set `PIPEDRIVE_DRY_RUN=true` for read-only or
  speculative work.
- **Your client's approvals.** Ask before every `manage_` call in a
  client that runs with nobody watching.
- **Where the token lives.** Keep a keyring available, and
  `PIPEDRIVE_API_TOKEN` out of shared shells and logs.
  [`troubleshooting.md`](troubleshooting.md#tasks) says how to rotate
  and revoke the token, and what to do after an exposure.
- **The record of what happened.** The server keeps none (D-60).
  Pipedrive's change history says what changed, and your client's
  transcript holds each call beside the prompt behind it.
- **What you paste.** A debug log carries request paths, which hold
  record ids. A token and a tool result never belong in an issue.

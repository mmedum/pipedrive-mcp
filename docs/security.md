# Security guidance for operators

This document is the **runtime** security policy. The **reporting**
policy (vulnerability disclosure, supported versions, response targets)
is in [`../SECURITY.md`](../SECURITY.md).

## Token handling

The Pipedrive personal API token grants every permission the user
behind it has. Treat it as a password.

### Use `pipedrive-mcp login` for the binary path

The OS keyring (libsecret on Linux, Keychain on macOS, Credential
Manager on Windows) is the recommended store. `pipedrive-mcp login`
writes the token there; the MCP client config carries only
`PIPEDRIVE_COMPANY_DOMAIN`. The benefit is honest but bounded:

- The token never sits in `~/.claude.json` or `claude_desktop_config.json`,
  which means it does not appear in routine `cat`-the-config moments,
  in casual screen shares, in dotfile repositories that accidentally
  capture `~/.claude.json`, in backup tools that grab the home
  directory, or in tool-output that prompt-injection might coax an
  agent into reading.
- The token also is not in `/proc/self/environ` of the MCP client, so
  a successful prompt-injection-via-tool-output that scrapes env vars
  doesn't get it.

What the keyring does **not** protect against:

- Same-user malware. Any process running as your user can read the
  keyring through DBus / Secret Service once the login keyring is
  unlocked (the default on graphical login), exactly as it can read
  any of your files. The keyring buys disclosure-resistance, not
  depth-of-defense against compromised user accounts.
- Long-term token leakage. Pipedrive personal API tokens don't
  expire by default. Rotate them periodically the same way you'd
  rotate a password.

### Other practices

- **Never commit tokens.** `.gitignore` excludes `.env` and `.env.*`
  files. `gitleaks` runs in CI on every PR.
- **Rotate quarterly** and any time you suspect compromise.
  `pipedrive-mcp login` overwrites the keyring entry — same command,
  no extra step.
- **Use a dedicated user** in Pipedrive for the MCP server, not your
  personal account. That way you can scope its workspace permissions
  precisely and audit its actions in Pipedrive's activity log
  separately from human user activity.

## Threat model

Threats this server defends against:

- **Accidental destructive action by the LLM.** Mitigated by:
  - A per-call `dry_run` input on every reshaped write, which reports
    what the write would change and sends nothing.
  - `overwrite`, which names each field an update may replace and is
    required before it may replace content that is already there, and
    `expect_version`, which refuses a write whose record moved since it
    was read.
  - `PIPEDRIVE_DRY_RUN=true` as a server-wide floor: every write tool
    honours it, a per-call `dry_run` can only turn a rehearsal on, and
    nothing on the wire can turn one off.
  - Tool descriptions that explicitly tell the LLM not to confabulate
    values (e.g. `mark_deal_lost`'s `lost_reason`).
  - Default tool-side `limit=25` on list operations to prevent runaway
    fanout.
- **Token leakage via logs.** Mitigated by: logging never captures the
  token; only the URL, method, status and duration.

Threats this server **does not** defend against:

- **Malicious LLM tool selection.** If you give the server a token with
  write permissions, an LLM with that server in its toolset can
  legitimately create, update and delete data. The MCP transport does not carry the user's prompt to
  the server, so the server cannot tell whether a tool call is
  user-intended. Run with the smallest token scope that gets your job
  done, and use `PIPEDRIVE_DRY_RUN` for any speculative LLM work.
- **Compromised MCP client.** This server trusts whatever connects to
  its stdio. Run it only as a subprocess of a trusted MCP client.
- **Compromised host.** The token is in process memory and in the
  parent's environment. Anyone with read access to either gets the
  token. Run on a host you control.

## LLM audit trail

**This server keeps no record of tool calls.** `internal/tools` holds no
logger at all, so a successful read or write produces no log line. The
four log sites in the binary are two startup lines, a debug line per
HTTP response (suppressed at the default level) and a warning on
transport failure — none carries a tool name, and there is no request id
to correlate on.

That matters more than a missing convenience, because the MCP transport
carries tool calls and responses but NOT the user's prompt or the
model's reasoning. Even a complete per-call log could answer "what" and
never "why".

To reconstruct what happened after a surprising action, use the two
trails that do exist:

- **Pipedrive's own change record.** It is the authority on what
  actually changed, and it survives regardless of what this server did
  or did not write down.
- **Your MCP client's transcript**, which is the only place the prompt
  and the tool call sit together:
- Claude Code logs MCP calls in its session transcript.

Document this in your team's incident-response run book. The server
itself cannot do better.

## Destructive operations

Destructive tools register unconditionally, and guard the call instead.
Registration was never the right enforcement point: a tool that does not
exist cannot explain itself, so an operator who withheld it handed the
model a missing capability to guess at rather than a refusal telling it
why. This matches the Google Workspace MCP servers, which register
`trash_file` and `delete_message` and guard at call time.

Each destructive path carries the MCP `destructiveHint: true` annotation
— advisory metadata for client UX, never the enforcement — and says in
its description exactly what it removes and whether that is reversible.

The destructive surface today:

- `manage_note` with `action: delete` — soft-deletes a note. Pipedrive
  v1 implements DELETE as a soft delete, so the record persists with
  `active_flag=false`, `list_notes` filters it out, and `get_note` still
  returns it. It reads the note first so the result can show what went,
  and re-deleting an already-inactive note is a no-op rather than a
  second pointless `DELETE`.

  It takes `dry_run` and no permitting argument beyond it. That is
  deliberate: a guard is only added where the caller cannot already see
  what they would lose, and a soft delete is the reversible case
  `trash_file` handles the same way. What the description does say
  plainly is that **nothing in this server sets `active_flag` back**, so
  it should be treated as one-way.

**Deletes on the four v2 resources shipped on an explicit decision
taken 2026-09-19**, which is what this paragraph used to ask for.
`manage_deal`, `manage_person`, `manage_organization` and
`manage_activity` each gained a `delete` action.

They take `dry_run` and `expect_version` and **no permitting flag
beyond them**, for the reason the note above gives: Pipedrive's v2
delete is soft and time-boxed — its own documentation says "Marks a
<resource> as deleted. After 30 days, the <resource> will be
permanently deleted" — which is the reversible case `trash_file`
handles the same way. The read each action performs puts the record on
screen before it goes, so there is nothing the caller cannot already
see.

Two things the descriptions say plainly rather than guard:

- **Nothing here restores a deleted record.** Pipedrive's own UI can,
  within the 30-day window. After it, nobody can.
- **What becomes of the records hanging off a deleted one is not
  documented by Pipedrive and has not been verified here.** No probe
  was run, because the only workspace available is a real one. So the
  descriptions tell the caller to read `list_notes`, `list_activities`,
  `list_persons` and `list_deals` for the record first, rather than the
  code guarding against a behaviour nobody has established. If that
  behaviour is ever established, the guard to add is `force`, on the
  three parents — an activity is a leaf and would still not need one.

`detach_product_from_deal` remains off the roadmap; products are not
part of this surface at all.

## Dry-run

Two layers, and the relationship between them matters.

Per-call `dry_run: true` is the everyday mechanism: every write takes
it, reports what it would find and change, and sends nothing.

`PIPEDRIVE_DRY_RUN=true` is the operator's floor. Every write tool
honours it, so a call may turn a rehearsal *on* but nothing on the wire
can turn one *off*. That is what makes it safe to answer "use
`PIPEDRIVE_DRY_RUN` for speculative LLM work" — a flag a tool could
override would be a false promise, and the tool that can delete is
exactly the one that would override it.

Validation still runs in dry-run mode, so a rehearsal catches the same
input errors a real call would.

**Dry-run invocations are not logged either**, for the reason above: no
tool handler logs anything. A rehearsal leaves no trace here and none in
Pipedrive, so the only record of what a model attempted is the client
transcript.

## Supply chain

- All releases signed via [cosign](https://github.com/sigstore/cosign)
  keyless OIDC. Verification command in
  [`../SECURITY.md`](../SECURITY.md).
- A CycloneDX SBOM is attached to every GitHub Release.
- Dependencies refresh weekly via Dependabot. Any direct dependency
  more than 6 months out of date fails CI unless documented as `pinned:`
  in `go.mod`.

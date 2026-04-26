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

### Use `--env-file` for the Docker path

Containers can't reach the host OS keyring, so the Docker path needs
the env var. Use Docker's `--env-file` rather than `-e
PIPEDRIVE_API_TOKEN=...`. The latter exposes the token in
`docker inspect`, shell history, and `ps` output:

```sh
cat > pipedrive.env <<'EOF'
PIPEDRIVE_API_TOKEN=abc123...
PIPEDRIVE_COMPANY_DOMAIN=acme
EOF
chmod 600 pipedrive.env
docker run -i --rm --env-file pipedrive.env ghcr.io/mmedum/pipedrive-mcp:latest
```

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
  - No `DELETE` tools registered by default. `PIPEDRIVE_ENABLE_DESTRUCTIVE`
    must be explicitly set.
  - `PIPEDRIVE_DRY_RUN=true` for rehearsal mode.
  - Per-tool `dry_run: true` input.
  - Tool descriptions that explicitly tell the LLM not to confabulate
    values (e.g. `mark_deal_lost`'s `lost_reason`).
  - Default tool-side `limit=25` on list operations to prevent runaway
    fanout.
- **Token leakage via logs.** Mitigated by: logging never captures the
  token; only request IDs and metadata.
- **Token leakage via Docker.** Mitigated by `--env-file` guidance above.

Threats this server **does not** defend against:

- **Malicious LLM tool selection.** If you give the server a token with
  write permissions, an LLM with that server in its toolset can
  legitimately create, update, and (if `PIPEDRIVE_ENABLE_DESTRUCTIVE`)
  delete data. The MCP transport does not carry the user's prompt to
  the server, so the server cannot tell whether a tool call is
  user-intended. Run with the smallest token scope that gets your job
  done, and use `PIPEDRIVE_DRY_RUN` for any speculative LLM work.
- **Compromised MCP client.** This server trusts whatever connects to
  its stdio. Run it only as a subprocess of a trusted MCP client.
- **Compromised host.** The token is in process memory and in the
  parent's environment. Anyone with read access to either gets the
  token. Run on a host you control.

## LLM audit trail

The MCP transport carries tool calls and responses. It does not carry
the user's original prompt or the LLM's reasoning. This server logs
every tool call with a request ID, but the logs cannot answer "why did
the LLM call this tool?".

To reconstruct intent after a surprising action, correlate the
`request_id` field in the server's stderr with the corresponding turn in
your MCP client's prompt log:

- Claude Desktop logs MCP calls under `~/Library/Logs/Claude/mcp*.log`
  (macOS) or `%APPDATA%\Claude\logs\mcp*.log` (Windows). The request ID
  appears in both places.
- Claude Code logs MCP calls in its session transcript.

Document this in your team's incident-response run book. The server
itself cannot do better.

## Destructive operations

Destructive tools (those that issue HTTP `DELETE` against Pipedrive) are
**not registered by default**. The omission is at the registration
layer, not behind an error — the LLM cannot discover or invoke these
tools at all unless the operator has set `PIPEDRIVE_ENABLE_DESTRUCTIVE=true`.

When enabled, each destructive tool carries the MCP `destructiveHint:
true` annotation and an explicit warning in its description. Currently
the only destructive tool planned for v1.0 is:

- `detach_product_from_deal` — removes a line item from a deal.
  Reversible by re-attach, but the original line-item identity is lost.

Future destructive tools (`delete_deal`, etc.) are not on the v1.0
roadmap. Adding any will require an ADR and an explicit user decision
captured in the release that introduces them.

## Dry-run

`PIPEDRIVE_DRY_RUN=true` forces every write to short-circuit and return
a structured "would have done X" response. This is the right setting for
testing the server end-to-end against a production token without firing
writes. Per-call `dry_run: true` is also available for individual
mutations.

Validation still runs in dry-run mode, so a rehearsal catches the same
input errors a real call would.

Dry-run invocations are logged at `info` level with `dry_run=true` so an
operator can audit what the LLM tried to do.

## Supply chain

- All releases signed via [cosign](https://github.com/sigstore/cosign)
  keyless OIDC. Verification command in
  [`../SECURITY.md`](../SECURITY.md).
- A CycloneDX SBOM is attached to every GitHub Release.
- The Dockerfile pins both base images by digest before any production
  release (CI gate in `.github/workflows/release.yml` enforces this).
- Dependencies refresh weekly via Dependabot. Any direct dependency
  more than 6 months out of date fails CI unless documented as `pinned:`
  in `go.mod`.

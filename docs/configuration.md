# Configuration

The API token lives in the **OS keyring** — set it once with
`pipedrive-mcp login`, then forget about it. Everything else is
configured through environment variables. Subcommands on the binary
are `login`, `logout`, and `status`. Operational flags on the default
(server) command are `--version`, `--dump-schemas`, and `--skip-probe`
(the last for CI smoke tests; do not use in production).

## Token resolution

The binary looks for the API token in this order, first non-empty
wins (matches `gh` / `aws` / `gcloud`):

1. **`PIPEDRIVE_API_TOKEN` env var.** Explicitly overrides whatever is
   stored. Used for CI and automation, and for "use a different token
   right now" workflows without touching the keyring.
2. **OS keyring**, under service `pipedrive-mcp` and account
   `<company-domain>`. Populated by `pipedrive-mcp login`. This is the
   recommended persistent store for interactive use: the token never
   sits in `~/.claude.json` or `claude_desktop_config.json`, never
   appears in shell history, never gets archived by ordinary backup
   tools.

If env is unset and the keyring lookup fails for a reason other than
"entry not found" (no DBus, no Secret Service available, headless SSH
session before the desktop keyring is unlocked, container without a
keyring backend), the underlying error is surfaced so you can decide
to either fix the keyring or set `PIPEDRIVE_API_TOKEN` in the
environment.

If env is unset and there is no keyring entry, the server exits
non-zero with: `no API token found. Run `pipedrive-mcp login` to
store one in the OS keyring, or set PIPEDRIVE_API_TOKEN in the
environment.`

### `pipedrive-mcp login`

```sh
pipedrive-mcp login                          # interactive: prompts for domain (echo) and token (no echo)
pipedrive-mcp login --domain acme            # domain via flag; only token is prompted
PIPEDRIVE_COMPANY_DOMAIN=acme pipedrive-mcp login  # domain via env; same effect
```

The command:

1. Resolves the workspace subdomain in this order: `--domain` flag,
   `PIPEDRIVE_COMPANY_DOMAIN` env, interactive prompt (with echo —
   the domain is not a secret). The interactive path mirrors
   `aws configure` / `gh auth login`: scriptable inputs win when
   present, but the bare-hands path is fully interactive instead of
   failing.
2. Reads the token from the controlling terminal without echoing it
   (or from stdin if stdin is piped — useful for `vault read ... |
   pipedrive-mcp login --domain acme`).
3. Validates the token against Pipedrive (auth probe). A bad token
   fails immediately, before anything is stored.
4. Writes the token to the OS keyring (service `pipedrive-mcp`,
   account = company domain).
5. Records the domain in the user-config file (see "Domain
   resolution" below) so subsequent runs don't need
   `PIPEDRIVE_COMPANY_DOMAIN`.

### `pipedrive-mcp logout`

```sh
pipedrive-mcp logout
pipedrive-mcp logout --domain other-domain
```

Removes the keyring entry. No-op if no entry exists. Also clears the
recorded default-domain pointer in the user-config file (see below) if
it referenced the workspace being logged out of.

### `pipedrive-mcp status`

```sh
pipedrive-mcp status
pipedrive-mcp status --no-probe   # offline mode; skip the auth probe
```

Reports:

- The active workspace domain and where it was resolved from
  (`env` or `userconfig (<path>)`).
- The token source (`keyring (service=pipedrive-mcp, account=<domain>)`
  or `env (PIPEDRIVE_API_TOKEN)`).
- Whether an auth probe against Pipedrive succeeds.

Exit code is `0` only if all three lines report a healthy state. Use
this to verify a fresh install before wiring it into Claude Desktop.

## Domain resolution

The workspace domain (the `XXX` in `XXX.pipedrive.com`) is resolved at
startup from the first non-empty source:

1. **`PIPEDRIVE_COMPANY_DOMAIN` env var.** Highest priority; overrides
   the recorded default. Use this in CI where env supplies everything.
2. **`os.UserConfigDir()/pipedrive-mcp/config.json`'s `default_domain`
   field.** Written by `pipedrive-mcp login` and cleared by
   `pipedrive-mcp logout` (when the domains match). On Linux this is
   `~/.config/pipedrive-mcp/config.json`; macOS uses
   `~/Library/Application Support/pipedrive-mcp/config.json`; Windows
   uses `%APPDATA%\pipedrive-mcp\config.json`. The file is non-secret
   (it's a domain pointer, not a token), but is written with `0600`
   perms in a `0700` parent directory by convention.

If neither source supplies a domain, the server exits non-zero with a
clear message pointing at `pipedrive-mcp login`.

Validation runs at startup. Missing required vars or malformed values
cause a clear stderr message and a non-zero exit *before* the MCP server
announces itself, so an LLM client never sees a half-initialized server.

## Variables

### `PIPEDRIVE_API_TOKEN`

- **Required**: no — prefer `pipedrive-mcp login` (OS keyring) for
  interactive use. Set this env var for CI, for automation, or to
  override a stored credential one-off.
- **Type**: string.
- **Source**: Pipedrive → Settings → Personal preferences → API.
- **Precedence**: env wins over keyring (same as `gh` / `aws`).
- **Validation**: non-empty; the startup probe (`GET /api/v2/dealFields?limit=1`)
  must return 200. A 401 exits non-zero with:
  `auth probe failed: 401 — token rejected. Run `pipedrive-mcp login` again.`
- **Rotation**: rotate by running `pipedrive-mcp login` again (overwrites
  the keyring entry); for the env-var path, update the env and restart.
  Mid-process rotation is not supported in v1.

### `PIPEDRIVE_COMPANY_DOMAIN`

- **Required**: only when no default has been recorded by
  `pipedrive-mcp login`. See "Domain resolution" above for the full
  fallback chain.
- **Type**: string.
- **Examples**: `acme` for `acme.pipedrive.com`. Just the subdomain — no
  scheme, no path, no trailing slash.
- **Validation**: matches `^[a-z0-9-]+$` (Pipedrive workspace names).
  Other values exit non-zero with a clear message.
- **Precedence**: env wins over the userconfig default (use env to
  point one shell at a different workspace without running `login`).

### `LOG_LEVEL`

- **Required**: no.
- **Default**: `info`.
- **Allowed**: `debug`, `info`, `warn`, `error`.
- **Notes**: `debug` is verbose; do not run continuously in production.

### `LOG_FORMAT`

- **Required**: no.
- **Default**: `text`.
- **Allowed**: `text`, `json`.
- **Notes**: `json` produces structured `slog.Record`s suitable for
  ingestion into log pipelines.

### `PIPEDRIVE_ENABLE_DESTRUCTIVE`

- **Required**: no.
- **Default**: `false`.
- **Type**: boolean (`true`/`false`, case-insensitive; `1`/`0` also
  accepted).
- **Effect**: when `true`, registers the destructive tools listed in
  [`security.md`](security.md). The default install registers no tools
  that issue HTTP `DELETE` against Pipedrive.

### `PIPEDRIVE_DRY_RUN`

- **Required**: no.
- **Default**: `false`.
- **Type**: boolean.
- **Effect**: when `true`, every write/destructive tool short-circuits
  before the HTTP call and returns a structured "would have done X"
  response. Always wins over the per-call `dry_run` input. Read tools
  ignore this variable.
- **Logging**: dry-run invocations are logged at `info` level with the
  `dry_run=true` field set, regardless of `LOG_LEVEL`.

### `PIPEDRIVE_HTTP_TIMEOUT`

- **Required**: no.
- **Default**: `30s`.
- **Type**: Go duration string (`5s`, `1m`, `750ms`).
- **Effect**: per-request timeout for outbound HTTP calls to Pipedrive.
  Applies to each retry attempt independently. Total wall-clock time for
  a single tool call is roughly `timeout * (1 + retries) + backoff`.

## Examples

### Minimal

```sh
export PIPEDRIVE_API_TOKEN='abc123...'
export PIPEDRIVE_COMPANY_DOMAIN='acme'
./pipedrive-mcp
```

### Production-ish

```sh
export PIPEDRIVE_API_TOKEN='abc123...'
export PIPEDRIVE_COMPANY_DOMAIN='acme'
export LOG_LEVEL='info'
export LOG_FORMAT='json'
export PIPEDRIVE_HTTP_TIMEOUT='15s'
./pipedrive-mcp 2>>/var/log/pipedrive-mcp.log
```

### Dry-run rehearsal

```sh
export PIPEDRIVE_API_TOKEN='abc123...'
export PIPEDRIVE_COMPANY_DOMAIN='acme'
export PIPEDRIVE_DRY_RUN='true'
./pipedrive-mcp
```

Use this to rehearse multi-step LLM workflows against a production token
without firing any writes.

### Docker with `--env-file`

```sh
cat > pipedrive.env <<'EOF'
PIPEDRIVE_API_TOKEN=abc123...
PIPEDRIVE_COMPANY_DOMAIN=acme
LOG_FORMAT=json
EOF
chmod 600 pipedrive.env

docker run -i --rm --env-file pipedrive.env ghcr.io/mmedum/pipedrive-mcp:latest
```

`--env-file` keeps the token out of your shell history and out of
`docker inspect` output. See [`security.md`](security.md) for more on
token handling.

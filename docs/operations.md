# Operations run book

Day-2 guidance for anyone running `pipedrive-mcp` in production. For
configuration reference see [`configuration.md`](configuration.md). For
security guidance see [`security.md`](security.md).

## Performance and resource expectations

- **Memory**: target < 50 MB resident. The server holds no persistent
  state and no caches beyond the custom-field name map (~10 KB per
  resource type, three resources).
- **CPU**: idle most of the time; per-call CPU is dominated by JSON
  encoding/decoding and TLS.
- **Network**: outbound to `https://{domain}.pipedrive.com` only. No
  other egress.
- **Filesystem**: reads `go-licenses` data via the binary itself; writes
  nothing at runtime.
- **Background goroutines**: the MCP SDK transport reader plus a
  one-shot field-cache warm-up that fans out across the deal /
  person / organization metadata endpoints at startup, then exits.
  No periodic refresh loop today.

## Log shape

Logs are JSON when `LOG_FORMAT=json`, otherwise human-readable text.
The lines emitted today (per-tool-call structured logging is on the
roadmap; the binary doesn't emit those yet):

| When | Level | `msg` | Useful fields |
| --- | --- | --- | --- |
| startup | `INFO` | `credentials resolved` | `source` (`keyring` \| `env`), `workspace`, `domain_source` (`env` \| `userconfig`) |
| startup | `INFO` | `auth probe ok` | `workspace` |
| per-request (debug only) | `DEBUG` | `pipedrive response` | `url`, `status`, `duration` |
| transport failure | `WARN` | `pipedrive request failed` | `url`, `attempt`, `duration`, `error` |

**LLM-facing error class** — emitted as the leading `[<class>]` tag on
the tool's text response, NOT (yet) as a slog field. The classes are:

- `[auth]` — 401 from upstream.
- `[permission]` — 403 lacking permission for the resource.
- `[business_rule]` — 403 due to upstream business logic (locked
  records, required fields, stage/pipeline rules).
- `[not_found]` — 404 from upstream, or our own
  pipeline-not-visible synthetic in `list_stages`.
- `[rate_limited]` — 429 after retries exhausted.
- `[server_error]` — 5xx after retries exhausted.
- `[validation]` — 400 from upstream OR our client-side input checks
  (e.g. `deal_id` ≤ 0, unknown `status` value).

JSON example of a successful debug-level call:

```json
{"time":"2026-04-26T20:50:55Z","level":"DEBUG","msg":"pipedrive response","url":"https://acme.pipedrive.com/api/v2/deals/11","status":200,"duration":"87ms"}
```

## Common conditions

### Server exits immediately at startup

The auth probe ran and failed. Check stderr for the exact message:

- `auth probe failed: 401 — token rejected. Run pipedrive-mcp login again.` → token is wrong, revoked, or for the wrong workspace. Re-run `pipedrive-mcp login` (or update `PIPEDRIVE_API_TOKEN` in env) and restart.
- `auth probe failed: dial tcp ...` → network egress problem. Verify outbound reachability to your Pipedrive subdomain.
- `no domain configured: set PIPEDRIVE_COMPANY_DOMAIN, or run pipedrive-mcp login to record one` → no workspace configured. Run `pipedrive-mcp login` (which records the default for future runs) or set `PIPEDRIVE_COMPANY_DOMAIN` in the env.

### `429 Too Many Requests` showing up in logs

Pipedrive uses token-based rate limiting (10 req / 2 s on
`/itemSearch`; ~100 req / 10 s on most other endpoints). The client
makes up to 3 attempts with jittered backoff, honoring the
`Retry-After` header. If you're seeing repeated 429s the user
probably has multiple parallel MCP sessions or a bursty workflow.
Mitigations:

- Lower `PIPEDRIVE_HTTP_TIMEOUT` so failed calls return faster (does
  not change the upstream rate, but makes the client back off sooner).
- Reduce parallel MCP sessions on the same workspace.
- Contact Pipedrive about a higher rate limit if this is real load.

### `500/502/503` from Pipedrive

The client makes up to 3 attempts (so at most 2 backoff sleeps:
~1s and ~2s ±25% jitter, doubled per attempt up to 30s). If the call
still fails, the tool returns `[server_error]`. Try again later.
Persistent 5xx is a Pipedrive-side outage, not a client bug.

### Custom field changes not visible to the LLM

Field metadata is cached for the lifetime of the process and isn't
auto-refreshed. Call the `refresh_field_cache` tool to re-fetch the
deal / person / organization metadata in parallel without restarting
the server; the response carries a per-resource row with the live
field count after reload, plus an `errors` count if any resource
failed. Custom-field VALUES are always live — only the hash↔name
mapping is cached.

## Token rotation

The API token is read once at startup. To rotate:

1. Generate a new token in Pipedrive.
2. Update the token in your MCP client config or env file.
3. Restart the server (and the MCP client, since the server is launched
   as a subprocess).
4. Revoke the old token in Pipedrive.

Mid-process rotation via signal is not supported in the v1 line. SIGHUP
reload is on the post-1.0 backlog.

## Filing a bug

A useful bug report contains:

- Version: `pipedrive-mcp --version`.
- The exact MCP client (Claude Desktop version, OS, etc.).
- The relevant log lines from stderr (with tokens redacted).
- The `request_id` if available, so we can correlate with logs.
- Reproduction steps. If the bug requires a particular Pipedrive workspace
  configuration (custom fields, pipeline shape), describe it without
  including real customer data.

Open a [GitHub issue](https://github.com/mmedum/pipedrive-mcp/issues) for
bugs and feature requests. For security issues see [`../SECURITY.md`](../SECURITY.md).

## Pinning Docker base images before release

The `Dockerfile` ships with floating tags (`golang:1.26.2-alpine`,
`gcr.io/distroless/static-debian12:nonroot`) for development. The
release workflow refuses to build until both `FROM` lines are pinned by
SHA256 digest:

```sh
# Resolve the current digest for each base image:
docker buildx imagetools inspect golang:1.26.2-alpine | grep '^Digest:'
docker buildx imagetools inspect gcr.io/distroless/static-debian12:nonroot | grep '^Digest:'

# Edit Dockerfile, append @sha256:<digest> to each FROM line.
```

Update the digests as part of the release PR. Trivy will surface any
base-image CVEs introduced by the new digest; resolve or record them in
`security/known-cves.yaml` per the grace policy.

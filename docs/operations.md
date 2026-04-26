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
- **Background goroutines**: only what the MCP SDK transport reader
  spawns. The custom-field cache refresh tick is part of normal request
  handling, not a long-lived goroutine.

## Log shape

Logs are JSON when `LOG_FORMAT=json`, otherwise human-readable text.
JSON example:

```json
{"time":"2026-04-25T12:34:56Z","level":"INFO","msg":"tool call","tool":"get_deal","request_id":"01HZ...","duration_ms":47,"status":200}
```

Useful filters:

- `request_id` — UUID4 per tool call. Use to correlate with your MCP
  client's prompt log.
- `tool` — name of the registered tool.
- `status` — upstream Pipedrive HTTP status, or `0` if the call did not
  complete (network error, timeout, retry exhaustion).
- `dry_run` — set to `true` for rehearsal calls.
- `error` — error class on failure (`unauthorized`, `forbidden_permission`,
  `forbidden_business_rule`, `not_found`, `rate_limited`, `server_error`,
  `validation`).

## Common conditions

### Server exits immediately at startup

The auth probe ran and failed. Check stderr for the exact message:

- `auth probe failed: 401 — check PIPEDRIVE_API_TOKEN` → token is wrong,
  revoked, or for the wrong workspace. Fix and restart.
- `auth probe failed: dial tcp ...` → network egress problem. Verify
  outbound reachability to your Pipedrive subdomain.
- `config: PIPEDRIVE_COMPANY_DOMAIN is required` → set the env var.

### `429 Too Many Requests` showing up in logs

Pipedrive uses token-based rate limiting. The client retries up to 3
times with jittered backoff, honoring the `Retry-After` header. If
you're seeing repeated 429s the user probably has multiple parallel MCP
sessions or a bursty workflow. Mitigations:

- Lower `PIPEDRIVE_HTTP_TIMEOUT` so failed calls return faster (does
  not change the upstream rate, but makes the client back off sooner).
- Reduce parallel MCP sessions on the same workspace.
- Contact Pipedrive about a higher rate limit if this is real load.

### `500/502/503` from Pipedrive

The client retries up to 3 times with backoff (1s, 2s, 4s ±25% jitter).
If the call still fails, the tool returns `server_error`. Try again
later. Persistent 5xx is a Pipedrive-side outage, not a client bug.

### Custom field changes not visible to the LLM

The cache refreshes hourly and lazily on miss. If you've just added a
field in Pipedrive and want the LLM to use it immediately, call the
`refresh_field_cache` tool (Phase 1+) or restart the server.

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

The `Dockerfile` ships with floating tags (`golang:1.26.0-alpine`,
`gcr.io/distroless/static-debian12:nonroot`) for development. The
release workflow refuses to build until both `FROM` lines are pinned by
SHA256 digest:

```sh
# Resolve the current digest for each base image:
docker buildx imagetools inspect golang:1.26.0-alpine | grep '^Digest:'
docker buildx imagetools inspect gcr.io/distroless/static-debian12:nonroot | grep '^Digest:'

# Edit Dockerfile, append @sha256:<digest> to each FROM line.
```

Update the digests as part of the release PR. Trivy will surface any
base-image CVEs introduced by the new digest; resolve or record them in
`security/known-cves.yaml` per the grace policy.

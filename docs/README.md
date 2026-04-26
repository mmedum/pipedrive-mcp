# Documentation

User-facing documentation for `pipedrive-mcp`. If you're running the
server or using its tools from an MCP client, this is for you.

| File | Purpose |
| --- | --- |
| [`architecture.md`](architecture.md) | How the server is put together: module layout, transport, request lifecycle, custom-field cache, dry-run plumbing, error mapping. |
| [`configuration.md`](configuration.md) | Every environment variable: type, default, validation, examples. Single source of truth that the top-level README links into. |
| [`development.md`](development.md) | Local-run guide: prerequisites, one-time setup, `make check`, sandbox-based end-to-end verification, Claude Desktop wiring. |
| [`release.md`](release.md) | Release runbook: pre-tag checklist, the `git tag` step, post-release verification (cosign, attestations, smoke). |
| [`operations.md`](operations.md) | Run book: log shape, token rotation, how 401/429 surface, performance expectations, how to file a bug. |
| [`security.md`](security.md) | Operator security guidance: token handling, Docker `--env-file` pattern, threat model, scope of `PIPEDRIVE_ENABLE_DESTRUCTIVE` and `PIPEDRIVE_DRY_RUN`, the LLM audit-trail caveat. |
| [`v1-carveouts.md`](v1-carveouts.md) | Endpoints this server still calls on Pipedrive's API v1, with their migration plans. Updated whenever Pipedrive ships a v2 equivalent. |

For the reporting policy (vulnerabilities, supported versions, response
targets) see [`../SECURITY.md`](../SECURITY.md). For the project's
mission and quick start see [`../README.md`](../README.md). Audit
artifacts (`/security-review` outputs and Claude Desktop release-smoke
transcripts) accumulate under `../audit/`.

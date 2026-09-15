# Security policy

## Reporting a vulnerability

Please report security issues privately. Do **not** open a public GitHub
issue.

- Open a private report through GitHub's [security advisory flow](https://github.com/mmedum/pipedrive-mcp/security/advisories/new),
  which reaches the maintainer and nobody else.
- GitHub Security Advisories: use the "Report a vulnerability" button on the
  repository's Security tab.

Include enough detail for the maintainer to reproduce the issue: affected
version, steps, and any logs or stack traces. If the
issue requires a particular Pipedrive account configuration to reproduce,
describe it without including real credentials or PII.

## Response targets

These are best-effort targets, not contractual SLAs. The project is
community-maintained by a small group of maintainers. We will firm these up
as the team grows.

- Acknowledgement within **3 business days** of report.
- Patch targeted within:
  - **30 days** for high or critical severity.
  - **90 days** for medium severity.
  - Lower-severity issues are tracked but rolled into normal release cadence.

Confirmed vulnerabilities are coordinated with the reporter and disclosed
publicly only after a fix is available, unless active exploitation forces
earlier disclosure.

## Supported versions

Security patches are produced for the most recent MAJOR.MINOR release plus
the previous MAJOR.MINOR. Older versions are unsupported.

| Version       | Supported          |
| ------------- | ------------------ |
| 1.x (current) | yes (post-1.0.0)   |
| 0.9.x         | release candidates |
| < 0.9         | no                 |

This table will be updated as the project ships releases.

## Operator guidance

Operator-level guidance — token handling,
threat model, the LLM audit-trail caveat, and the scope of
`PIPEDRIVE_ENABLE_DESTRUCTIVE` and `PIPEDRIVE_DRY_RUN` — lives in
[`docs/security.md`](docs/security.md). This file is the **reporting**
policy; that one is the **runtime** policy.

## Provenance

Every release is signed via [cosign](https://github.com/sigstore/cosign)
keyless OIDC. One signature covers the release: `SHA256SUMS` is signed,
and it holds the hash of every archive and every SBOM. Verify with
(replace `X.Y.Z` with the release version — goreleaser strips the `v`
prefix from archive filenames):

```sh
sha256sum -c SHA256SUMS --ignore-missing

cosign verify-blob SHA256SUMS \
  --bundle SHA256SUMS.bundle \
  --certificate-identity-regexp 'https://github.com/mmedum/pipedrive-mcp/.*' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
```

A CycloneDX SBOM is attached per-archive (e.g.
`pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz.cdx.json`); each SBOM's hash is
in the signed `SHA256SUMS`.

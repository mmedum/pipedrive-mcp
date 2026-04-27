# Release runbook

How to cut a tagged release. Two parallel workflows trigger on a
`v*` tag push:

- `.github/workflows/release.yml` — archives, checksums, cosign-signed
  blobs, CycloneDX SBOM, GitHub Release, SLSA L2 build-provenance.
  Driven by `.goreleaser.yaml` plus a thin orchestration step that
  extracts the matching `## [X.Y.Z]` section from `CHANGELOG.md` as
  the release notes (so our Keep-a-Changelog format wins, not
  goreleaser's git-log changelog).
- `.github/workflows/docker-publish.yml` — multi-arch Docker image
  pushed to GHCR, cosign-signed by digest. Split out so the archive
  release isn't blocked by Docker registry hiccups.

This doc is the **before** and **after** checklist for the maintainer
who pushes the tag.

This is a one-person operation today. Hold yourself to the checklist —
the gates that protect production are the ones you actually run, not
the ones you list.

## Before tagging

A release lands a verified state of `main`. Don't tag from a branch.

- [ ] All exit criteria for the current phase are met (see
      `CONTRIBUTING.md` "Phases and approvals" + the phase plan the
      maintainer is working from).
- [ ] `make check` passes locally on the commit you're about to tag.
- [ ] `make smoke` passes locally (binary + Docker stdio smoke).
- [ ] Sandbox integration tests green (Phase 1+):
      `go test -tags=integration -race -count=3 -shuffle=on ./...`
- [ ] `CHANGELOG.md` `[Unreleased]` section reflects exactly the
      shipped surface — no stragglers, no missing entries.
- [ ] `make dump-schemas` output diffed against the previous tag.
      Any breaking change has a `BREAKING CHANGE:` commit footer
      somewhere in the range.
- [ ] `Dockerfile`'s `FROM` lines pinned by SHA256 digest, not
      floating tag. Procedure for resolving the digests is in
      [`operations.md`](operations.md#pinning-docker-base-images-before-release).
      The release workflow's "Verify Dockerfile pins" step fails if
      either FROM line is unpinned — fix on `main` first, don't try
      to patch in a release commit.
- [ ] `/security-review` over the cumulative diff since the previous
      tag. Output committed under `audit/security-reviews/v<tag>.md`.
- [ ] `/simplify` over the cumulative diff. Output under
      `audit/security-reviews/v<tag>-simplify.md` (or skip if you
      ran the per-PR pass diligently).
- [ ] Manual Claude Desktop / Claude Code smoke against your sandbox
      (Phase 1+). Transcript committed under
      `audit/release-smoke/v<tag>.md`.
- [ ] CHANGELOG `[Unreleased]` heading renamed to the version,
      with the date, in a release-prep commit. The release workflow's
      changelog excerpt step depends on this.
- [ ] Bump any pinned tool versions in `.github/workflows/*.yml`
      that drifted (govulncheck, go-licenses) — not strictly required
      but worth doing on the release commit.

## Tagging

```sh
git checkout main
git pull --ff-only
git tag -s "v0.X.Y" -m "v0.X.Y"
git push origin "v0.X.Y"
```

`-s` requires a configured GPG key. If you don't sign tags, drop the
flag — but cosign keyless signing covers artifact provenance, so tag
signing is optional.

The release workflow runs automatically. Watch it at
`https://github.com/mmedum/pipedrive-mcp/actions`.

## After the workflow finishes

- [ ] Open the GitHub Release page. Verify:
      - Archives present for `darwin/{amd64,arm64}`,
        `linux/{amd64,arm64}`, `windows/amd64` (`.tar.gz` for
        unix-likes, `.zip` for windows).
      - `SHA256SUMS` plus `.sig`/`.cert` for each archive and for
        `SHA256SUMS` itself.
      - `<archive>.cdx.json` (CycloneDX SBOM) per archive, plus the
        matching `.cdx.json.sig`/`.cdx.json.cert` for each.
      - SLSA build-provenance attestation visible (cli/cli-style
        "Provenance" badge on each archive).
- [ ] Pull the freshly-pushed Docker image and verify the cosign
      signature:

      cosign verify ghcr.io/mmedum/pipedrive-mcp:v0.X.Y \
        --certificate-identity-regexp 'https://github.com/mmedum/pipedrive-mcp/.+' \
        --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
- [ ] Verify a binary signature locally. Use the archive filename
      goreleaser actually produces — no `v` prefix, `.tar.gz`
      extension; the `.sig`/`.cert` sign the archive, not the
      binary inside:

      cosign verify-blob \
        --certificate pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz.cert \
        --signature   pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz.sig \
        --certificate-identity-regexp 'https://github.com/mmedum/pipedrive-mcp/.+' \
        --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
        pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz
- [ ] Verify the build attestation with `gh attestation verify`:

      gh attestation verify pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz \
        --owner mmedum
- [ ] Re-run `make smoke` against the just-released binary
      (download → smoke), not the dev binary.
- [ ] In the next PR, open `CHANGELOG.md` and add a fresh
      `## [Unreleased]` section so day-2 commits have somewhere to
      land.

## If a workflow fails

- **Reproducible-build sanity check fails (`release.yml`)**: indicates
  non-determinism in the build. Most common causes: a non-pinned dep,
  a dep that embeds build-time metadata (filepath, hostname).
  Investigate from the two SHA256 hashes in the workflow logs. Fix
  the cause, force-delete the tag (`git tag -d`, `git push --delete
  origin`), retag from the fix commit. **Only force-delete a tag if
  no consumer has pulled the release yet** — published tags are
  public history; deleting one downstream of an artifact pull breaks
  any verifier that pinned by tag.
- **Goreleaser fails on the release notes step**: the `## [X.Y.Z]`
  section in `CHANGELOG.md` is missing or unrenamed from
  `[Unreleased]`. Fix the CHANGELOG on `main`, redo the tag.
- **Cosign signing failure**: usually a transient Fulcio CA blip;
  re-running the workflow on the same tag works.
- **Docker push 403 (`docker-publish.yml`)**: `GITHUB_TOKEN` lost
  write permission to the package; check the repo's Actions → General
  → Workflow permissions.
- **Dockerfile FROM-pin guard fails**: a `FROM` line drifted to a
  floating tag. Fix on `main`, retag.
- **Trivy CVE introduced by base-image bump** (in CI on PRs): either
  fix the underlying issue or add a grace-period entry under
  `security/known-cves.yaml` per `docs/security.md`.

If you delete and recreate a tag, also delete the corresponding
draft Release on GitHub before retrying. The workflow won't overwrite
a Release that already exists.

## Phase boundary checklist

A release that crosses a phase boundary needs the additional gates
documented in `CONTRIBUTING.md` ("Phases and approvals" + "Phase 0
spike checklist") *before* the tag is pushed. The user — not the
workflow — signs off on phase progression.

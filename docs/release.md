# Release runbook

How to cut a tagged release. Two parallel workflows trigger on a
`v*` tag push:

- `.github/workflows/release.yml` — archives, checksums, cosign-signed
  blobs, CycloneDX SBOM, GitHub Release, SLSA L2 build-provenance.
  Driven by `.goreleaser.yaml` plus a thin orchestration step that
  extracts the matching `## [X.Y.Z]` section from `CHANGELOG.md` as
  the release notes (so our Keep-a-Changelog format wins, not
  goreleaser's git-log changelog).

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
- [ ] `make smoke` passes locally.
- [ ] Sandbox integration tests green (Phase 1+): `make integration-writes`.
      Not `make integration` — that skips every write probe, so the box
      gets ticked by a run that wrote nothing.
- [ ] `CHANGELOG.md` `[Unreleased]` section reflects exactly the
      shipped surface — no stragglers, no missing entries.
- [ ] `make dump-schemas` output diffed against the previous tag.
      Any breaking change has a `BREAKING CHANGE:` commit footer
      somewhere in the range.
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
- [ ] `SECURITY.md`'s supported-versions table names the version being
      tagged as current, and drops whatever fell out of the window.
      It is a published promise about which releases get patches, and
      it was written forward once and left that way for five releases,
      telling every user of the shipped release it got none.
- [ ] Bump any pinned tool versions in `.github/workflows/*.yml`
      that drifted (govulncheck, go-licenses) — not strictly required
      but worth doing on the release commit.
- [ ] Re-fetch the vendored bundle-manifest schema if `schemaRef` in
      `scripts/gates/mcpb.go` has moved, or if you want to know the pin
      is still honest. The gate validates the manifest against
      `packaging/mcpb/mcpb-manifest-v<version>.schema.json` and checks
      that file against the SHA256 recorded in `vendoredSchemaSHA256` —
      which proves the bytes are the ones somebody reviewed, NOT that
      they still match what the URL serves. Only a re-fetch shows that:

      ```sh
      ref=$(grep -oP 'schemaRef = "\K[^"]+' scripts/gates/mcpb.go)
      curl -sSL -o packaging/mcpb/mcpb-manifest-v0.3.schema.json \
        "https://raw.githubusercontent.com/anthropics/mcpb/$ref/schemas/mcpb-manifest-v0.3.schema.json"
      sha256sum packaging/mcpb/mcpb-manifest-v0.3.schema.json
      ```

      A changed hash means upstream retagged under a ref that is
      supposed to be immutable — worth understanding before updating
      the constant.
- [ ] Refresh `internal/pipedrive/testdata/v2-response-fields.json` from
      Pipedrive's v2 description, so the upstream-type check is holding
      the mirror against a current document rather than a stale one. It
      is derived from
      <https://developers.pipedrive.com/docs/api/v1/openapi-v2.yaml>;
      the file records the source URL, the spec's SHA256 and the capture
      date. A field appearing or disappearing is a finding, not a chore —
      `is_archived` and the archived-deals split were both invisible
      until somebody looked.
- [ ] The same for the vendored MCP registry schema
      (`packaging/registry/server.schema.json`, hash in
      `registrySchemaSHA256`). Its URL is dated rather than tagged, so
      it is the likelier of the two to move under you — and a registry
      entry cannot be withdrawn once published.

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
      - `checksums.txt` plus `checksums.txt.bundle`, the one signature over
        the release. Every archive and every SBOM is covered by its
        hash in that file, so there are no per-artifact `.sig`/`.cert`.
      - `<archive>.cdx.json` (CycloneDX SBOM) per archive.
      - SLSA build-provenance attestation visible (cli/cli-style
        "Provenance" badge on each archive).
- [ ] Verify the signature locally. Use the archive filename goreleaser
      actually produces — no `v` prefix, `.tar.gz` extension:

      cosign verify-blob checksums.txt \
        --bundle checksums.txt.bundle \
        --certificate-identity-regexp '^https://github\.com/mmedum/pipedrive-mcp/\.github/workflows/release\.yml@refs/tags/' \
        --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'

      sha256sum -c checksums.txt --ignore-missing   # on macOS: shasum -a 256 -c

      The signature first: until it passes, the checksums are only what
      the release assets say now.
- [ ] Verify the build attestation with `gh attestation verify`:

      gh attestation verify pipedrive-mcp-X.Y.Z-linux-amd64.tar.gz \
        --owner mmedum
- [ ] The Claude Desktop bundle, `pipedrive-mcp_X.Y.Z.mcpb`, is on the
      release page and has a row in `checksums.txt`. The bundle is not an
      archive goreleaser built — a hook drops it into `dist/` and
      `checksum.extra_files` names it — so it is the one artifact that
      can go missing without anything else looking wrong.
- [ ] The registry entry published. The `registry` job runs after
      `archives` and is skipped for prereleases on purpose, because an
      entry cannot be withdrawn. Check the job, not the release page:
      a green release with no entry looks identical to one with an
      entry.
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

### The three steps that only ever run on a real tag

Cosign signing, the build-provenance attestation and the registry
publish each need an OIDC token that only a workflow run has. So
`goreleaser check`, `actionlint` and a snapshot build all pass while any
of the three is wrong, and the first tag is where you find out. Read
that run rather than watching it go green.

They fail differently, and the difference is what you do next:

| Fails | State afterwards | Recovery |
| --- | --- | --- |
| cosign | **No release.** Signing precedes publishing. | Fix, delete the tag, re-tag. |
| attestation | Release published, unattested. | Re-run the failed job on the same tag. |
| registry publish | Release fine, no entry. | Dispatch `publish-mcp.yml` against the existing tag. |

The registry publish is its own workflow precisely so the third row is
possible: re-running `release.yml` would re-run goreleaser against a
release that already exists, and an entry for a tag that shipped weeks
ago could not be published at all.

```sh
gh workflow run publish-mcp.yml --ref main -f tag=vX.Y.Z
```

It reads the PUBLISHED release's `checksums.txt`, so the hash in the entry
is the number cosign signed rather than one from a local build. A release
cut before the checksum file was renamed carries `SHA256SUMS` and
`SHA256SUMS.bundle` instead; the workflow takes those when
`checksums.txt` is absent and verifies them the same way.

Two limits of that check, kept on purpose for now. The signature is held
to `release.yml` on any tag, not to the tag being published, so a
`checksums.txt` signed for another tag of this repository would pass.
And the entry takes the one `.mcpb` row in the file without checking its
name against the tag. Both are hardening outside the change set that
introduced them; tightening either is a change to this workflow, which
needs the maintainer's go-ahead.

**Exit 0 on empty output is not evidence.** `gh attestation verify` and
`cosign verify-blob` both say little when they succeed, and a command
that verified nothing at all also says little. Check one of them against
a deliberately corrupted copy of the artifact and confirm it exits
non-zero before believing the run that passed.

If you delete and recreate a tag, also delete the corresponding
draft Release on GitHub before retrying. The workflow won't overwrite
a Release that already exists.

## Phase boundary checklist

A release that crosses a phase boundary needs the additional gates
documented in `CONTRIBUTING.md` ("Phases and approvals" + "Phase 0
spike checklist") *before* the tag is pushed. The user — not the
workflow — signs off on phase progression.

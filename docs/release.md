# Releasing

A maintainer releases by pushing a tag, on the owner's explicit go.
`release.yml` checks that two builds are byte-identical (D-93), takes the
tag's CHANGELOG section as the release notes (D-91), and publishes with
GoReleaser. The archives, an SBOM per archive and the Claude Desktop
bundle are in `checksums.txt`, which one cosign signature covers, beside
build provenance (D-92). A tag without a pre-release suffix also
publishes the MCP registry entry through `publish-mcp.yml`, and an entry
cannot be withdrawn (D-97).

## Before the release commit

- `make check` passes, and `make leaks-history` passes over everything
  the repository has published, which `make check` does not read (D-89).
- `make integration-writes` has run if the release touches the write
  path or a response shape, and `make evals` if it changes text the
  model reads (D-79, D-80). `CONTRIBUTING.md` says what each writes.
- `/simplify`, `/code-review medium` and `/security-review` have run
  since the previous tag. Nothing they produce is committed.
- Refresh `internal/pipedrive/testdata/v2-response-fields.json` from
  <https://developers.pipedrive.com/docs/api/v1/openapi-v2.yaml>, with
  the spec's SHA256 and the capture date (D-72). A field that appears or
  goes is a finding. Check there whether v2 has gained notes or users
  (D-04).
- Re-fetch the vendored schemas: a pinned hash proves the bytes are the
  ones reviewed, not that the URL still serves them (D-95). The bundle
  manifest's ref and hash are in `scripts/gates/mcpb.go`:

  ```bash
  ref=$(grep -oP 'schemaRef = "\K[^"]+' scripts/gates/mcpb.go)
  curl -sSL -o packaging/mcpb/mcpb-manifest-v0.3.schema.json \
    "https://raw.githubusercontent.com/anthropics/mcpb/$ref/schemas/mcpb-manifest-v0.3.schema.json"
  sha256sum packaging/mcpb/mcpb-manifest-v0.3.schema.json
  ```

  The registry schema, `packaging/registry/server.schema.json`, takes the
  URL and hash in `scripts/gates/mcpregistry.go` the same way.
- Rehearse the build into `dist/`, publishing nothing, with the
  GoReleaser version `release.yml` pins. It skips signing, which needs
  the workflow's identity, and SBOMs, since syft is installed only in CI:

  ```bash
  go run github.com/goreleaser/goreleaser/v2@v2.13.1 release --snapshot --clean --skip=sign,sbom,publish
  ```

  `checksums.txt` must list the bundle. A bundle missing from it ships
  unsigned and looks no different.

## Steps

1. On a topic branch, rename `[Unreleased]` in `CHANGELOG.md` to
   `## [X.Y.Z] - YYYY-MM-DD`, leave an empty `[Unreleased]` above it, and
   add the version's link reference at the bottom.
2. Compare `make dump-schemas` with the previous tag's. A break needs a
   MAJOR (D-75) and the owner's decision before it is written; a new
   major also raises go.mod's `/vN`.
3. Commit as `Release X.Y.Z`, open a pull request, and merge once CI is
   green on that commit.
4. Tag the merge, annotated, and push the tag on its own:

   ```bash
   git switch main && git pull --ff-only
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git push origin vX.Y.Z
   ```

## What no rehearsal reaches

Signing, attestation and the registry publish need the workflow's OIDC
token, so read the first run after any change to the pipeline.

| Fails | State afterward | Recovery |
|---|---|---|
| reproducible build, release notes or cosign | no release | fix it, then release the next patch |
| attestation | release published, unattested, no entry | fix it, then release the next patch |
| registry publish | release fine, no entry | `gh workflow run publish-mcp.yml --ref main -f tag=vX.Y.Z`; do not tag again |

## Verify from outside

```bash
cd "$(mktemp -d)" && gh release download vX.Y.Z --repo mmedum/pipedrive-mcp
cosign verify-blob checksums.txt --bundle checksums.txt.bundle \
  --certificate-identity 'https://github.com/mmedum/pipedrive-mcp/.github/workflows/release.yml@refs/tags/vX.Y.Z' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum -c checksums.txt --ignore-missing
gh attestation verify pipedrive-mcp_X.Y.Z_linux_amd64.tar.gz --repo mmedum/pipedrive-mcp
```

A pass prints little, so read the exit code. The release carries
`pipedrive-mcp_X.Y.Z_darwin_amd64.tar.gz`,
`pipedrive-mcp_X.Y.Z_darwin_arm64.tar.gz`,
`pipedrive-mcp_X.Y.Z_linux_amd64.tar.gz`,
`pipedrive-mcp_X.Y.Z_linux_arm64.tar.gz`,
`pipedrive-mcp_X.Y.Z_windows_amd64.zip` and
`pipedrive-mcp_X.Y.Z_windows_arm64.zip`, each with its `.cdx.json`
SBOM, and `pipedrive-mcp_X.Y.Z.mcpb` (D-94). Check the registry job
too: a release with no entry looks like one with an entry.

Last, outside the repository, run
`go install github.com/mmedum/pipedrive-mcp/cmd/pipedrive-mcp@latest`
as a user would, and check that `pipedrive-mcp --version` names the new
release.

## A bad release

A bad release is fixed forward.

- Never move or delete a pushed tag. The Go module proxy caches it
  within minutes, and the checksum database records its hash.
- Ship a patch release with the fix.
- In that patch, add `retract vX.Y.Z // why` to `go.mod`, so the go
  command skips the bad version for `@latest`.
- Mark the bad version's CHANGELOG heading `[YANKED]`:
  `## [X.Y.Z] - YYYY-MM-DD [YANKED]`.

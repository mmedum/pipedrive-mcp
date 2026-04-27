<!--
Thanks for contributing. Every box below must be checked or have a written
exception in the PR description before merge. Reviewers will reject PRs with
unchecked boxes lacking justification.
-->

## What and why

<!-- One paragraph: what does this PR change, and why? Skip for trivial fixes. -->

## CI gates

- [ ] `go build ./...` passes
- [ ] `go vet ./...` passes
- [ ] `gofmt -l` clean
- [ ] `golangci-lint run` passes
- [ ] `go test -race ./...` passes
- [ ] Coverage ≥ 80% on `internal/pipedrive/` and `internal/tools/` (where applicable)
- [ ] `govulncheck ./...` clean
- [ ] `go-licenses check` passes
- [ ] `scripts/staleness-check.sh` passes
- [ ] Tool-schema diff is empty, OR head commit carries `SCHEMA-CHANGE:` (additive / non-breaking) or `BREAKING CHANGE:` (breaking) footer
- [ ] Docker build + trivy scan clean
- [ ] `scripts/stdio-smoke.sh` passes for both binary and Docker
- [ ] gitleaks clean

## Manual gates

- [ ] `/simplify` skill run on changed files; issues fixed or documented
- [ ] `/security-review` skill run; findings fixed or accepted with rationale below
- [ ] `README.md` updated if user-facing behavior changed
- [ ] Relevant `docs/*.md` files updated
- [ ] Design rationale captured (in the relevant `docs/` file, code comment, or CHANGELOG entry) if a non-trivial decision was made
- [ ] `CHANGELOG.md` `[Unreleased]` section updated

## Security review notes

<!-- Paste the /security-review output summary, or "no findings". -->

## Simplify review notes

<!-- Paste the /simplify output summary, or "no findings". -->

## Schema changes

<!--
If this PR changes the public tool surface, describe the change here
AND add a footer to the head commit:

  - SCHEMA-CHANGE: <description>   for additive / non-breaking changes
    (new optional input field, new tool, expanded enum)
  - BREAKING CHANGE: <description>  for breaking changes
    (tool removed/renamed, required input removed/renamed, output type
    changed, narrowed enum, etc.)

The schema-diff CI gate enforces one of these footers whenever
--dump-schemas differs from the previous tag.
-->

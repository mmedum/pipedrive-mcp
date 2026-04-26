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
- [ ] Tool-schema diff shows no breaking change, OR commit has `BREAKING CHANGE:` footer
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

## Breaking changes

<!--
If this PR breaks the public tool surface (tool removed/renamed, required
input removed/renamed, output type changed, etc.), describe the break here
AND add `BREAKING CHANGE: <description>` as a commit message footer.
-->

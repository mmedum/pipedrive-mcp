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
- [ ] `make staleness` passes
- [ ] The tool surface is unchanged, OR a commit footer names each item that changed: `SCHEMA-CHANGE:`, or `BREAKING CHANGE:` for a break of the last release
- [ ] `make smoke` passes
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
AND add an empty commit whose footer names each item that changed:

  - SCHEMA-CHANGE: <names>: what changed   for anything that breaks no
    caller of the last release (a new tool, a new optional input, a
    description, an annotation, the instructions)
  - BREAKING CHANGE: <names>: what broke   for a break of the last
    release (a tool or input removed or renamed, an input newly
    required, an output field removed, a type narrowed)

The schema-ack CI gate builds the merge-base and the head and enforces
this whenever what --dump-schemas prints differs. A refusal prints the
footer that fixes it.
-->

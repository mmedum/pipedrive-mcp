# Contributing

Thanks for your interest. This project is a production-grade MCP server, so
the bar for contributions is real — but the workflow is straightforward.

## Development setup

Detailed setup, prerequisites, the local `make check` gate, and Claude
Desktop end-to-end wiring live in
[`docs/development.md`](docs/development.md). Quick path:

```sh
git clone https://github.com/mmedum/pipedrive-mcp
cd pipedrive-mcp
go mod tidy
make check
```

`make help` lists every available target.

## Quality gates

Every PR must pass:

1. The CI gates from `.github/workflows/ci.yml` (build, vet, lint, test,
   coverage, govulncheck, license check, staleness, schema diff, Docker
   build + trivy, stdio smoke, CHANGELOG check, gitleaks).
2. The manual gates from `.github/pull_request_template.md` (`/simplify`
   and `/security-review` skill output, docs updated, design rationale captured,
   CHANGELOG updated).

The PR template has a checklist; reviewers reject PRs with unchecked boxes
lacking justification.

## Branching, commits, and breaking changes

- Branch from `main`. Use short, hyphenated branch names
  (`feature/...`, `fix/...`, `docs/...`).
- One PR per logical change; rebase rather than merge `main` into the branch.
- Commits use short, imperative subject lines. The body explains *why*.
- **Breaking changes**: any commit that changes the public tool surface in a
  backwards-incompatible way (tool removed/renamed, required input
  removed/renamed, output type changed) must include a
  `BREAKING CHANGE: <description>` footer. CI's schema-diff gate enforces
  this.
- Write commits to be reproducible: don't auto-amend pre-commit fixes into a
  prior commit, create a new one.

## Phases and approvals

The project follows a phased delivery (Phase 0 → 5, tagged `v0.0.1` →
`v1.0.0`). **Each phase boundary requires explicit maintainer approval
before the next phase starts.** Tagging `v0.x.y` does not authorize
Phase x+1; that is a separate decision recorded in the corresponding
GitHub Release.

### Phase 0 spike checklist

Three questions are pending until they are exercised against the
Pipedrive sandbox. They block Phase 2 (write tools) and must be
resolved with the rationale captured in the relevant code or doc:

1. **Field-clearing mechanism on PATCH.** Does Pipedrive v2 accept
   `null` to clear a field? `value: ""` to clear a string? Does the
   MCP Go SDK's `jsonschema:` tag grammar express the chosen
   mechanism? Outcome lands in `internal/pipedrive/types.go` and the
   tool descriptions on every `update_*` tool.
2. **403 disambiguation heuristic.** Trigger a permission-denied 403
   (token user lacks resource access) and a business-rule 403 (locked
   deal, gated stage). Tighten the `businessRule403Signals` list in
   `internal/pipedrive/errors.go` based on the recorded response
   bodies; commit the bodies as test fixtures.
3. **Hand-rolled HTTP vs. OpenAPI generator.** Inspect Pipedrive's
   OpenAPI spec coverage and run a small generator. Default remains
   hand-rolled; switch only if the spike surfaces a strong reason.

Outcomes update `internal/pipedrive/`, `docs/architecture.md`, and
`CHANGELOG.md`. There is no separate ADR file — the rationale lives
where the code lives.

## Sandbox account ownership

The integration test suite runs against a dedicated Pipedrive sandbox.

| Role | Owner |
| --- | --- |
| Sandbox account | Mark Medum Bundgaard (`mmedum@gmail.com`) |
| API token in CI secrets | Mark Medum Bundgaard |
| Token rotation cadence | Quarterly (Jan, Apr, Jul, Oct), first week of month |

If maintainership transfers, update this table in the same PR that changes
ownership of the CI secret.

## Capturing design rationale

Non-trivial design decisions are documented where the decision is
load-bearing:

- **In code comments**, when the decision shapes a specific
  function/type and the comment is what a reader needs at the call
  site.
- **In the relevant `docs/` file**, when the decision affects how
  someone runs or uses the server.
- **In `CHANGELOG.md`**, when the decision changes the public tool
  surface.

The project deliberately does not maintain a separate ADR directory.
Eight ADR files for a project at Phase 0 was ceremony, not value.
If a decision genuinely outlives a single change and needs a long-form
explanation, add a section to the appropriate `docs/` file rather than
a parallel artifact tree.

## Code of Conduct

This project follows the Contributor Covenant. See
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).

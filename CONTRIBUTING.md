# Contributing to pipedrive-mcp

Thanks for considering a contribution. This is a Go MCP server for
Pipedrive, run by people against their own workspace. Read this once
before your first pull request; CI enforces the rest.

## Before anything else

**Nothing from a real Pipedrive account may enter this repository**
(D-89). Not company domains or ids; not the names, ids or contents of
real deals, people, organizations, activities or notes; not user names,
email addresses or API tokens. This holds for code, docs, fixtures, tool
descriptions, issues, pull requests, commit and tag messages and logs.
Fixtures are invented and written inline in the tests. Refer to a real
record by its shape, such as "a deal with a dropdown field". Naming the
sibling servers, such as google-chat-mcp or favro-mcp, is fine.

`make leaks` scans the tree, and `make leaks-history` everything the
repository has published. `make gitleaks` scans the branch's commits
and the tree for credentials, and `make gitleaks-history` every commit.
If you use pre-commit, `.pre-commit-config.yaml` runs gitleaks, gofmt
and `go vet` on each commit.

## Reporting bugs and requesting features

Open a [GitHub issue](https://github.com/mmedum/pipedrive-mcp/issues)
with what
[`docs/troubleshooting.md`](docs/troubleshooting.md#reporting-a-problem)
lists. Never paste a tool result: it is your workspace's data. For
anything security-sensitive, follow [`SECURITY.md`](SECURITY.md) rather
than opening a public issue.

## Development setup

```bash
git clone https://github.com/mmedum/pipedrive-mcp
cd pipedrive-mcp
make build
make check
```

Go is the only prerequisite, at the version `go.mod` names;
`GOTOOLCHAIN=auto`, the default, fetches it. `make check` runs
golangci-lint, govulncheck, go-licenses and gitleaks with `go run`, at
the exact versions the Makefile names (D-85).
Everything the repository runs on itself is Go, in `scripts/` (D-77).
`make help` lists every target. The unit tests need no Pipedrive
account.

### Running your own build

`make build` writes `./pipedrive-mcp`. Point a client at its absolute
path, as the README's [Connect a client](README.md#connect-a-client)
shows, and restart the client. `pipedrive-mcp --version` says which
build you are running.

`make smoke` drives the built binary over stdio, and
`make dump-schemas` prints the tool surface a client is served (D-73).

## Gates that must pass

```bash
make check
```

`make check` runs the gates CI runs, except the pull-request ones, and
the `check` target in the `Makefile` is the list. `make parity` fails
when that list and `ci.yml` differ, so a check you add goes in both
(D-84). `go run ./scripts/gates` lists the checks, and
`go run ./scripts/gates <name>` runs one. A gate refuses to pass on an
empty input; before you trust one you changed, break what it holds and
watch it fail (D-83).

These are worth knowing before they fail on you:

- **The coverage floor** is 80% of statements in each of
  `internal/pipedrive`, `internal/tools`, `internal/credentials` and
  `internal/app` (D-82). `make cover` holds it, and `make coverage`
  opens the report.
- **`make vet`** vets the code three times: plain, with
  `-tags=integration` and with `-tags=live`, so the live code keeps
  compiling.
- **`make descriptions`** holds the tool descriptions to the house
  style's mechanical rules (D-10).
- **`make mcpb`** holds the bundle manifest to its schema, to the files
  the packer stages and to the server's settings, and the release to its
  archives and their names (D-94, D-95, D-96).
- **`make staleness`** fails a direct dependency more than six months
  behind, unless `go.mod` pins it with a reason (D-87).

## Tests

Unit tests use the standard library and run with the race detector. The
client's tests run against `httptest` servers, and the tool tests drive
the server through the SDK's in-memory transport, with the helpers in
`internal/server/testutil`. Every fake answers as its author believed
Pipedrive would, so it agrees with that belief. Only the live run tests
it (D-78).

## Versioning

[SemVer](https://semver.org/spec/v2.0.0.html), read through the tool
surface: names, input and output schemas, resource templates,
annotations, the instructions and documented behavior (D-75). A removed
or renamed tool or input, a newly required input, a removed output
field or a narrowed type is a breaking change, and needs a MAJOR. The
v1 tools sit outside that promise (D-04).

On a pull request, CI runs `schema-ack` (D-76). A pull request that
changes what `--dump-schemas` prints names each changed item in a footer
of a commit it adds, as an empty commit rather than an amend:

```bash
git commit --allow-empty -m "Acknowledge the tool surface change" \
  -m "SCHEMA-CHANGE: manage_deal, instructions: what changed"
```

A footer names a tool by its name, a resource template by its URI
template, and the instructions as `instructions`, each a whole word.
Only the names before the first colon followed by white space count;
the description after it names nothing. Names are separated by commas,
and a list wraps only after one. A `Token #value` footer such as
`Closes #12` acknowledges nothing. A name with a comma or a space in it
goes in backticks. A break of the last release needs
`BREAKING CHANGE: <names>: what broke` instead; narrowing something no
release shipped is a plain change. The gate builds the merge-base and
the head, and a refusal prints the footer that fixes it.
`make schema-ack` runs it locally against `origin/main`.

## Adding a tool

1. Put the wire call in `internal/pipedrive`, with tests against an
   `httptest` fake. That package imports nothing of MCP (D-70).
2. Register the tool in `internal/tools/<resource>.go`, from its
   `Register` function, through `mcp.AddTool` (D-74). Its output type is
   its own, never an embedded `pipedrive` type (D-17).
3. A write goes through `guardedWrite`, takes `dry_run`, and adds every
   field it can set to the resource's field table (D-32, D-37, D-38).
4. An error is `[class] message`, from a sentinel (D-51). A new class
   needs an entry in `docs/troubleshooting.md`.
5. Add the tool to the README's tool table, and a CHANGELOG entry.
6. Read `make dump-schemas`, and add the `SCHEMA-CHANGE:` footer above.

## The live run

Every fake in the unit suite is written from what we believe Pipedrive
does, and only a live run tests that belief (D-78). None of it runs in
CI. The configured workspace is a real CRM, not a sandbox, so read this
before you run any of it.

```bash
make integration
make integration-writes
make evals
```

- **What it needs.** The credentials the server reads: the domain from
  `PIPEDRIVE_COMPANY_DOMAIN` or the pointer `pipedrive-mcp login`
  records, and the token from `PIPEDRIVE_API_TOKEN` or the keyring.
  Without them every integration test skips, saying why.
- **`make integration`** runs the suite in `internal/integration`,
  behind the `integration` build tag, against the server `internal/app`
  builds. It reads, reads resources, and drives guard refusals and dry
  runs against the workspace's own records. It changes nothing.
- **`make integration-writes`** adds `PIPEDRIVE_INTEGRATION_WRITES=1`,
  which runs the write probes. Most need `PIPEDRIVE_TEST_PIPELINE_ID`
  naming a pipeline the business does not use, because closing a deal
  fires the workspace's automations (D-79). Without it they skip, and
  under `PIPEDRIVE_DRY_RUN` every write probe skips.
- **What the probes write.** Each probe creates its own organization,
  person, deal, activity and note, named `mcp-test <timestamp>`, and
  writes only to those. It restores what it changed from `t.Cleanup`,
  verifies the restore with a fresh read, and deletes the records when
  it ends. A restore that did not take fails with `LEFT CHANGED`, and a
  delete that failed with `LEFT BEHIND`, naming the record. Deletes are
  soft: Pipedrive purges the records after 30 days, and a deleted note
  stays readable.
- **`make evals`** drives a model through this server's tools and
  scores the end state, read back through the server, and the tool-call
  trace (D-80). It needs the `claude` command, the credentials and
  `PIPEDRIVE_TEST_PIPELINE_ID`, and spends API usage. It builds a
  fixture of invented records named `(eval <timestamp>)` through the
  tools, and deletes it at the end unless `-keep-fixture` is set. A run
  fails if the model created a record outside the fixture, and reports
  any record it edited (D-81). `-task` runs the tasks whose name contains
  its value, `-model` picks the model and `-budget` caps a task in USD:

  ```bash
  make build
  go run -tags=live ./scripts/evals -bin ./pipedrive-mcp -task refuse
  ```

  `go test ./scripts/evals` checks the task table and the stream parser
  without credentials.

A comment that relies on what a live run showed names the test:
`Checked by the integration test TestWrite_DealReopensFromWonAndFromLost/mark_lost.`
Several are "the integration tests A and B". Cite an eval task by its
name only for a fact about how a model uses the tools.

## Branch and commit conventions

- Branch from `main` with a short topic branch, such as `fix/…` or
  `docs/…`. The maintainer pushes, merges and tags.
- Write the subject in the imperative, under 72 characters. The body
  says the problem, why this change solves it, and the alternatives
  rejected.
- A fixed review finding is one line at the end of the body. A declined
  one goes in the pull request, or becomes a present-tense comment at
  the line it protects. Review output is never committed.
- Fix a commit with a new commit, never an amend of one already pushed.

## Pull requests

1. Open against `main`, with a summary and a test plan.
2. CI must be green, the `CHANGELOG entry` and
   `Schema change acknowledged` checks included.
3. The maintainer reviews and merges.

Every action is pinned to a full commit SHA, and every tool an action
installs to one exact version; `make pins` holds both (D-86).

## Changelog

A change to watched source needs an entry under `## [Unreleased]` in
`CHANGELOG.md`, in the same pull request (D-90). Leave out changes only
contributors see, unless they must act on them; such a line starts
`For contributors:`.

- **Sections** are the [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
  set: Added, Changed, Deprecated, Removed, Fixed and Security, in that
  order. Newest version first, ISO dates, link references at the bottom.
- **One line per change, 25 words or fewer, impact first.** Keep tool
  names, wire keys and versions. The investigation belongs in the commit
  message.
- **Breaking changes** start with `**Breaking:**`, name the item in
  backticks, and may add one sentence saying what to do.
- **Security fixes** name the CVE or GHSA.

The release workflow takes the version's section as the release notes,
so the entry is the release note (D-91).

## Releasing

The maintainer releases as [`docs/release.md`](docs/release.md) says.

## Code style

- Match the surrounding code. Do not reformat what you did not touch.
- Prefer the standard library. A new module needs a reason in the
  commit message and at its import.
- Comments explain why: a constraint, an invariant, something Pipedrive
  does that the code has to work around. Do not narrate what the code
  does.
- A decision in `docs/architecture.md` is `D-NN`. Cite it by ID, or a
  doc by its path and heading anchor, never by section number. What you
  verified about Pipedrive goes in the comment at the line that depends
  on it, with the reference link and the integration test that checks
  it.
- Write plainly: lead with the outcome, one idea per sentence. This
  holds for comments, commit messages, the CHANGELOG and tool
  descriptions.
- Logs record the call, never the payload (D-59).

`CLAUDE.md` has the short version of the rules, and
[`docs/architecture.md`](docs/architecture.md) the reasoning, one
decision per D-ID.

## Code of conduct

Taking part in this project means agreeing to
[`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md), the Contributor Covenant
3.0.

## License

By contributing, you agree that your contributions will be licensed
under the project's [Apache 2.0 License](./LICENSE).

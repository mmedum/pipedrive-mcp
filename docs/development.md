# Development guide

How to set up, run, and verify `pipedrive-mcp` locally. This is the
"can I actually exercise this end-to-end?" guide.

For deployment-time configuration see [`configuration.md`](configuration.md).
For the operations run book see [`operations.md`](operations.md).

## Prerequisites

| Tool | Version | Purpose |
| --- | --- | --- |
| Go | `1.26.2` | Build and test. The `toolchain` directive in `go.mod` will fetch this automatically on Go ≥ 1.21 hosts. |
| `golangci-lint` | latest | Lint gate. Install: `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`. |
| `govulncheck` | latest | Vulnerability gate. Install: `go install golang.org/x/vuln/cmd/govulncheck@latest`. |
| `go-licenses` | latest | License compatibility gate. Install: `go install github.com/google/go-licenses@latest`. |
| `gitleaks` | latest | Secret-scan gate. Install per https://github.com/gitleaks/gitleaks. Optional locally. |
| `trivy` | latest | Image-CVE gate. Install per https://github.com/aquasecurity/trivy. Optional locally. |
| `cosign` | latest | Signature verification only — release pipeline does signing. Optional. |
| `jq` | any | Pretty-printing `--dump-schemas` output. |

You do not need a Pipedrive account to run unit tests; you do need one
(production or sandbox) to run the binary or the integration suite.

## One-time setup

```sh
git clone https://github.com/mmedum/pipedrive-mcp
cd pipedrive-mcp
go mod tidy        # fetches dependencies and writes go.sum
go build ./...     # confirms everything compiles
```

If `go mod tidy` fails on a missing `github.com/modelcontextprotocol/go-sdk@v1.5.0`,
check the version pin in `go.mod` against
https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk and bump if
needed. Update `internal/tools/registry.go`'s `sdkVersion` constant in
the same change.

## Running tests

```sh
go test ./...                        # quick
go test -race ./...                  # with the race detector (CI default)
go test -race -coverprofile cov.out ./...   # with coverage
go tool cover -html=cov.out          # browse coverage in a browser
```

Coverage targets: ≥ 80% on `internal/pipedrive/` and `internal/tools/`.
The other packages are mostly wiring and are not coverage-gated.

## The evals

`make evals` drives a model through this server's tools alone and scores
what it did. It needs credentials, a network and the `claude` CLI, so it
is behind a `live` build tag and is not part of `make check`.

```sh
make evals                                  # every task
go run -tags=live ./scripts/evals -bin ./pipedrive-mcp -task refuse   # one
```

**What it is for.** `make integration` proves the tools work. The evals
prove they can be *used*, which is a different claim and the one that
fails quietly: what no driver catches is a result that is internally
consistent and wrong. Every task is therefore scored twice — the **end
state** read back through this server, because a model's account of what
it did is the least reliable thing in the run, and the **trace**, because
a task can be completed by a model that guessed an id and was lucky.

**It writes to the configured workspace.** The fixture — an
organization, a person, a deal, an activity and a note, all invented and
all named `(eval <timestamp>)` — is built through the server's own tools
and deleted afterwards. Those deletes are soft, so Pipedrive purges what
is left after 30 days.

**The census is the part to read.** An eval drives a model, not a
script: a task that says "log that we spoke to Acme" can be answered by
creating an activity nobody asked for. The harness counts deals,
persons, organizations and activities either side of the run and fails
if the workspace did not come back to where it started. It cannot
*prevent* that — nothing can, short of not running — and it cannot
remove what it did not create. It can refuse to let it pass unnoticed,
and that is what it does.

**The task table is not behind the build tag.** `go test ./scripts/evals`
walks every prompt without credentials, which is where the
unsubstituted-placeholder guard belongs — a sibling project's first full
eval run passed two tasks while sending the agent a literal `{folder}`.
The stream parser and the census live there too, for the same reason:
a parser that mis-attributes a tool result scores a refusal as a
success, and nothing about that is visible in a passing run.

## The full local check

The same gates that run in CI on every PR. Use this before pushing.

```sh
make install-tools # install govulncheck, go-licenses, golangci-lint at pinned versions
make check         # every per-PR gate, in order; `check:` in the Makefile is the list
```

`make check` includes `verify-tool-versions` as its first step. If
your local `golangci-lint` version doesn't match the pin in CI
(`GOLANGCI_LINT_VERSION` in `.github/workflows/ci.yml`), the gate
fails before doing anything else and tells you to run
`make install-tools`. This is deliberate: every CI failure on this
project so far has been a tool-version drift between local and CI,
and the verify-tool-versions step is the canary that surfaces it
before a push.

The `Makefile` is the canonical list. Inspect it to see what each
target does. To run gates one at a time:

```sh
make fmt           # gofmt -l (must be empty)
make vet           # go vet ./...
make lint          # golangci-lint run
make test          # go test -race -coverprofile cov.out ./...
make vuln          # govulncheck ./...
make licenses      # go-licenses check ./...
make staleness     # go run ./scripts/gates deps
```


Then run the server:

```sh
go run ./cmd/pipedrive-mcp
# expected stderr:
#   level=INFO msg="credentials resolved" source=keyring workspace=your-subdomain
#   level=INFO msg="auth probe ok" workspace=your-subdomain
# stdin is open and the server is waiting for MCP frames
```

`pipedrive-mcp logout` removes the keyring entry. For CI and
automation, set `PIPEDRIVE_API_TOKEN` instead of running `login`; the
keyring takes precedence when both are present.

To exercise the failure paths deliberately:

```sh
PIPEDRIVE_COMPANY_DOMAIN=acme PIPEDRIVE_API_TOKEN=invalid go run ./cmd/pipedrive-mcp
# Expected: exits non-zero within ~1s with
#   auth probe failed: 401 — token rejected. Run `pipedrive-mcp login` again.

unset PIPEDRIVE_API_TOKEN PIPEDRIVE_COMPANY_DOMAIN
go run ./cmd/pipedrive-mcp
# Expected: exits non-zero immediately with
#   config: PIPEDRIVE_COMPANY_DOMAIN is required
```

## Manual stdio test (without an LLM client)

Pipe a JSON-RPC `initialize` + `tools/list` directly into the binary:

```sh
go build -o /tmp/pipedrive-mcp ./cmd/pipedrive-mcp

cat <<'EOF' | /tmp/pipedrive-mcp --skip-probe 2>/dev/null | jq .
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"manual","version":"0"}}}
{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}
EOF
```

You should see two JSON-RPC frames — the initialize response and the
tools/list response listing the registered tools. The `--skip-probe`
flag is for this kind of test only; do not use it in production. `make
smoke` does the same sequence and is run as a gate on every PR.

## Inspecting the tool schemas

The schema-diff CI gate uses `--dump-schemas`. Run it locally:

```sh
go run ./cmd/pipedrive-mcp --dump-schemas | jq .
```

The output is a header (`binary_version`, `sdk_version`,
`tool_count`) and the tool array sorted alphabetically. The
schema-diff CI gate compares this output against the prior tag's
output to enforce semver discipline on the LLM-facing surface.

## Connecting to Claude Desktop end-to-end

1. Build a release-flavored binary (statically linked, version stamped):

   ```sh
   make build           # writes ./pipedrive-mcp
   ```

2. Edit your Claude Desktop config (macOS:
   `~/Library/Application Support/Claude/claude_desktop_config.json`,
   Windows: `%APPDATA%\Claude\claude_desktop_config.json`):

   ```json
   {
     "mcpServers": {
       "pipedrive": {
         "command": "/absolute/path/to/pipedrive-mcp",
         "env": {
           "PIPEDRIVE_API_TOKEN": "your-token",
           "PIPEDRIVE_COMPANY_DOMAIN": "your-subdomain"
         }
       }
     }
   }
   ```

3. Restart Claude Desktop. Open a new chat, click the tool icon, and
   confirm `pipedrive` is listed as connected.

4. Ask the LLM "what Pipedrive tools do you have?" — you should see
   the registered tool catalog (run `pipedrive-mcp --dump-schemas |
   jq '[.tools[].name]'` for the authoritative list).

5. Tail the server logs to confirm it's running. Claude Desktop logs
   MCP server stderr to:

   - macOS: `~/Library/Logs/Claude/mcp-server-pipedrive.log`
   - Windows: `%APPDATA%\Claude\logs\mcp-server-pipedrive.log`

If you see `auth probe failed: ...` in the log, the binary exited at
startup — the LLM will not see the server. Fix the token/domain and
restart Claude Desktop.

## Connecting to Claude Code

```sh
claude mcp add pipedrive /absolute/path/to/pipedrive-mcp \
  -e PIPEDRIVE_API_TOKEN=your-token \
  -e PIPEDRIVE_COMPANY_DOMAIN=your-subdomain
```

Then `claude` to start a session. Tools become available in the
`/tools` listing.

## Integration suite

`internal/integration/` drives the MCP server against a live Pipedrive
workspace. Every file in it carries `//go:build integration`, so a
plain `go test ./...` never touches the network.

```sh
make integration          # reads, resources, guard refusals, dry runs
make integration-writes   # the above plus the reversible write probes
```

It exists because the unit tests assert against fakes, and a fake
agrees with whatever we believed when we wrote it. What lives in the
suite is only what a fake cannot answer: the wire format the two
endpoint families actually return, custom fields resolving against the
workspace's own field definitions, cursor paging, the guards reading
records somebody really filled in, and the error class a real 404 maps
to.

The suite resolves the workspace through `internal/app`, the same
startup assembly the binary runs — the domain from
`PIPEDRIVE_COMPANY_DOMAIN` or the userconfig pointer, the token from
`PIPEDRIVE_API_TOKEN` or the keyring — so `pipedrive-mcp login` is
enough setup. With neither, every test skips with the reason instead of
failing.

Writes need `PIPEDRIVE_INTEGRATION_WRITES=1` on top of the tag, because
the workspace on the other end is a real CRM and Pipedrive has no undo.
`PIPEDRIVE_DRY_RUN` overrides that: the suite honours the dry-run floor
the way the server does, so under it the write probes skip. The safety
contract each probe keeps — capture, restore from `t.Cleanup`, verify
with a fresh read, never write a field that was empty — is written once,
at the top of `internal/integration/write_test.go`, next to the code it
binds. A restore that did not take fails the test with `LEFT CHANGED`,
naming the record.

Two residues are unavoidable and known. The note probe leaves a
soft-deleted note, because a v1 delete clears `active_flag` and nothing
purges the record. And a run is a run against one workspace: a
different custom-field or permission setup is still untested.

There is no CI job for this. The suite needs a live token, and its
natural cadence is the phase boundary below rather than every PR.

## Phase boundary verification

When tagging a release the maintainer runs the per-phase gates from
[`../CONTRIBUTING.md`](../CONTRIBUTING.md). The condensed sequence:

```sh
make check                                    # all per-PR gates
go test -race -count=3 ./...                  # 3 shuffled runs
make integration-writes                       # against the sandbox workspace
make build && cosign verify-blob ...          # only in CI; verify locally if you build a personal release

# Then manually exercise at least one tool of each category in Claude
# Desktop, copy the transcript to audit/release-smoke/v<tag>.md.
```

Per-phase outputs from `/security-review` and `/simplify` are
committed under `audit/security-reviews/v<tag>.md` and the release
notes link them.

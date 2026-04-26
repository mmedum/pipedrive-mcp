# Development guide

How to set up, run, and verify `pipedrive-mcp` locally. This is the
"can I actually exercise this end-to-end?" guide.

For deployment-time configuration see [`configuration.md`](configuration.md).
For the operations run book see [`operations.md`](operations.md).

## Prerequisites

| Tool | Version | Purpose |
| --- | --- | --- |
| Go | `1.26.0` | Build and test. The `toolchain` directive in `go.mod` will fetch this automatically on Go ≥ 1.21 hosts. |
| Docker | any recent | Local Docker build, trivy scan, Docker stdio smoke. Optional if you don't need to verify the Docker path. |
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

## The full local check

The same gates that run in CI on every PR. Use this before pushing.

```sh
make check         # runs gofmt, vet, lint, test (race), govulncheck, licenses
```

The `Makefile` is the canonical list. Inspect it to see what each
target does. To run gates one at a time:

```sh
make fmt           # gofmt -l (must be empty)
make vet           # go vet ./...
make lint          # golangci-lint run
make test          # go test -race -coverprofile cov.out ./...
make vuln          # govulncheck ./...
make licenses      # go-licenses check ./...
make staleness     # scripts/staleness-check.sh
```

The Docker-flavored gates need Docker locally:

```sh
make docker        # docker build
make smoke         # local binary + Docker stdio smoke
```

## Running against a Pipedrive sandbox

Create a personal API token in Pipedrive (Settings → Personal preferences
→ API). Use a sandbox or test workspace, not your production one — the
binary becomes a write tool from Phase 2 onwards.

Store the token once in the OS keyring:

```sh
export PIPEDRIVE_COMPANY_DOMAIN='your-subdomain'
go run ./cmd/pipedrive-mcp login
# prompts (no echo): Pipedrive API token for "your-subdomain":
# validates against the API; on success, writes to keyring
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
tools/list response with an empty `tools` array (Phase 0 has no tools
registered yet). The `--skip-probe` flag is for this kind of test only;
do not use it in production. CI's `scripts/stdio-smoke.sh` does the same
sequence and is run as a gate on every PR.

## Inspecting the tool schemas

The schema-diff CI gate uses `--dump-schemas`. Run it locally:

```sh
go run ./cmd/pipedrive-mcp --dump-schemas | jq .
```

In Phase 0 this returns a header (`binary_version`, `sdk_version`,
`tool_count: 0`) and an empty `tools` array. As tools land in Phase 1
the array fills out, sorted alphabetically.

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

4. Ask the LLM "what Pipedrive tools do you have?" — in Phase 0 the
   answer is "none registered yet"; from Phase 1 onwards you'll see
   the catalog.

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

## Phase boundary verification

When tagging a release the maintainer runs the per-phase gates from
[`../CONTRIBUTING.md`](../CONTRIBUTING.md). The condensed sequence:

```sh
make check                                    # all per-PR gates
go test -race -count=3 ./...                  # 3 shuffled runs
go test -tags=integration -race ./...         # against sandbox (Phase 1+)
make build && cosign verify-blob ...          # only in CI; verify locally if you build a personal release

# Then manually exercise at least one tool of each category in Claude
# Desktop, copy the transcript to audit/release-smoke/v<tag>.md.
```

Per-phase outputs from `/security-review` and `/simplify` are
committed under `audit/security-reviews/v<tag>.md` and the release
notes link them.

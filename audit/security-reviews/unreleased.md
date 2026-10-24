# Reviews — unreleased (`family-rules`)

**Date:** 2026-10-24
**Range:** `main...family-rules` at 88cd1f8, which is `v1.1.3..88cd1f8`; then the commit that adds this file, which applies what the reviews found and builds with Go 1.27.3.
**Reviews:** `/simplify` (one agent, four angles), `/code-review medium` and `/security-review`, each over that range.

This range is everything since the last tag, so these are the release reviews for the next one so far. When it is cut, this file becomes `v<tag>.md`, and a review of anything merged after it is added here.

## `/code-review medium`

Seven findings, each fixed:

1. **`publish-mcp.yml` found no `checksums.txt` for a release cut before the rename.** It now takes `SHA256SUMS` and `SHA256SUMS.bundle` when `checksums.txt` is absent, verifies them with the same cosign identity, and passes the file it found to `registry-publish`. The step was run against a fake `gh` for a release with each name and one with neither: it chose `checksums.txt`, then `SHA256SUMS`, then failed. `TestAReleaseBeforeTheRenameStillGivesAnEntry` holds that an old `SHA256SUMS` with a `pipedrive-mcp-1.1.3.mcpb` row gives an entry pointing at that file.
2. **The release footer ran `sha256sum` before `cosign verify-blob`,** so it read the checksums before their signature was checked, and macOS has no `sha256sum`. Cosign comes first, then `sha256sum -c` for Linux and `shasum -a 256 -c` for macOS. `goreleaser check` passes. `shasum -a 256 -c checksums.txt --ignore-missing` was run against a file with a missing row and passed.
3. **The README, `SECURITY.md` and `docs/release.md` accepted a signature from any workflow,** through an unanchored `https://github.com/mmedum/pipedrive-mcp/.*`. All three now use `publish-mcp.yml`'s identity, `release.yml` on a tag, anchored, and check the signature first, as the footer does.
4. **`docs/architecture.md` ran the gate paragraph into "Explicitly not in 0.5.0."** A blank line separates them.
5. **The packer's file was closed by `writeBundle` on success and by `packMCPB` on error, and a failed pack left a truncated `.mcpb` in `dist/`.** `finishBundle` is the one place it is closed, and its error is the pack's. The bundle is written to `<out>.*.partial` and renamed to `out` only when whole, and the partial file is removed on any failure. `TestAFailedPackLeavesNoBundle` fails on writing in place, and `TestAFailedCloseFailsThePack` on a partial file left behind or a close error dropped.
6. **The manifest was decoded four to six times.** `committedManifest` decodes it once into the struct and once into the document, and returns the document. The gate's schema check and the packer's stamp both use it, and the stamped document is validated without decoding its bytes again.
7. **The domain was trimmed by `config.Lookup`, `ResolveDomain` and `ValidateDomain`.** Each input is now trimmed once, by its reader: `config.Lookup` for the variable, `main.go` for the flag, `promptDomain` for the prompt (its comment already said so) and `ResolveDomain` for the userconfig file. `ValidateDomain` lowers and validates. `TestPromptDomain_TrimsSpaces` fails on the old prompt. The "env whitespace is treated as empty" case now goes through `ResolveDomainAtStartup`, which reads the variable, and fails when `Lookup` keeps the spaces.

Kept: `EnvVars` has only test callers. Every sibling's config package exports it, and it is the name the family's scans read.

The family's second-round fixes that apply here, each with a test that fails on the old code:

- `fieldDiff` reports a differing unexported field it cannot name whenever it differs, beside a named one too. It follows a pointer to its value rather than comparing addresses: `strictlyComparable` refuses pointers, channels, unsafe pointers and funcs, and those fields are compared as `reflect.DeepEqual` would.
- `repeatedKeys` counts two keys that differ only in case as one repeat, as Go's decode matches them. It quotes a key that holds `.` or `[` (`env["a.b"]`), and a walk that errors is a problem, not a pass.
- The name scan treats a run that another operand continues as a prefix, so `EnvPrefix + "ENABLE_" + kind` spells nothing, while `"set " + x + EnvPrefix + "NEW"` still spells `PIPEDRIVE_NEW`.
- The smoke test reads the token and the domain through `config.Lookup`, and every variable read outside `internal/config` must go through it (family item 5).
- Each variable is spelled once, in `internal/config`: error messages, the login help, the credentials error, the smoke test's environment and `bundleLeftOut` use the constants `EnvLogLevel`, `EnvLogFormat`, `EnvDryRun`, `EnvHTTPTimeout`, `DomainEnv` and `EnvAPIToken`. The messages read as before.
- A `user_config` default holding `${` is refused, since the host expands more references than the gate substitutes.
- `modulePath(root)` reads the module directive however it is written: after a tab or spaces, quoted or bare, with a trailing `//` comment, and with CRLF line ends.
- The registry entry reads the manifest through `committedManifest(root)`, so a manifest the gate refuses, a repeated key included, writes no entry.
- The API token's field must be `"sensitive": true`. Which variables are secret comes from a `Secret` flag on `config.Var`, and a bundle that asks for none of them is refused (family item 13).

Not applicable: one `DefaultProfile` (this server has no profile), `mcpb-manifest`'s output (there is no such command, and the packer prints one summary line), and the docs check (no gate reads the configuration page for variable names).

## `/simplify`

Four findings, each fixed:

8. **The defaults check could not see `company_domain` or `api_token`.** `config.Load` reads neither, so a default on either passed. `unseenDefaultProblems` refuses a default on a required field, and on a field whose variable `config.Load` does not read, found by starting it. `hostEnv`'s comment now says what is checked and what is not.
9. **The name scan needed the `PIPEDRIVE_` prefix, and `LOG_LEVEL` and `LOG_FORMAT` carry none.** `TestEachVariableIsSpelledOnce` takes the names from `EnvVars`. `TestEveryReadGoesThroughLookup` follows the argument of every `os.Getenv`, `os.LookupEnv` and `config.Lookup` outside `internal/config` to its constant. `config.Lookup` must name a variable `EnvVars` lists. `os.Getenv` must name one of the scripts' own, `SMOKE_HOLD` and `PIPEDRIVE_TEST_PIPELINE_ID`, each declared with whose it is. A read of `config.EnvLogLevel` with `os.Getenv` fails it, and so does a `config.Lookup` of an unknown name.
10. **The packer hard-coded `.` and read `LICENSE` and `README.md` from the working directory.** The root reaches `committedManifest`, the schema check and `sourceFor`, and the registry entry reads its schema from it too. `TestThePackerReadsItsRoot` packs a copied root's README.
11. **Prose the code contradicted.** `config.Lookup`'s comment said it returned the variable's name; it returns the trimmed value. `committedManifest`'s said the manifest was decoded once, which item 6 made true per shape, and the comment says so.

## `/security-review`

No finding at confidence 8 or above. Two notes, kept:

- **`publish-mcp.yml`'s cosign identity accepts `release.yml` on any tag, not only the one being published, and `bundleRow` takes the one `.mcpb` row without checking its name.** Tightening either changes the publishing path beyond what the maintainer approved for this branch. `docs/release.md` records both for the maintainer.
- **`PIPEDRIVE_DRY_RUN` set to spaces alone now means off,** by the family rule that spaces alone are unset. Before, it was refused at start. The default is off either way. A form field cleared to a space now starts the server rather than stopping it, and the CHANGELOG says so.

## Go 1.27.3

`govulncheck` v1.8.0 under the official go1.27.2 finds GO-2026-6809 reachable, in `crypto/x509`. Under go1.27.3 it finds nothing. The bump touched `go.mod`, `.github/workflows/ci.yml` and `.github/workflows/release.yml` (`GO_VERSION`), `docs/development.md`, `CLAUDE.md` (`GOTOOLCHAIN`) and `CHANGELOG.md`. `publish-mcp.yml` and `codeql.yml` read the version from `go.mod`.

## How it was checked

`make check` passes under `GOTOOLCHAIN=go1.27.3` with golangci-lint v2.14.0, `licenses` included. Each new rule was broken once in the code and its test failed. The code was then restored.

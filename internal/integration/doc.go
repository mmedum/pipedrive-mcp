// Package integration holds the end-to-end suite that drives the MCP
// server against a live Pipedrive workspace.
//
// Every test file here carries `//go:build integration`, so a plain
// `go test ./...` compiles this file and finds no tests. The suite is
// the phase-boundary gate CLAUDE.md "Phase boundaries" and
// docs/release.md name:
//
//	make integration          // the read half: nothing is mutated
//	make integration-writes   // plus the reversible write probes
//
// It exists because the unit tests assert against fakes, and a fake
// agrees with whatever we believed when we wrote it. Every serious
// defect found during the 0.4.0 work — three guard bugs, a wire-format
// error, and Pipedrive's derived-name behaviour — was found by driving
// the real API, and not one was visible to the tests that mock it.
// What lives here is therefore only what a fake cannot tell us: wire
// format, custom-field resolution against real field definitions,
// cursor paging, the guards against real records, and the error
// classes real HTTP statuses map to.
//
// # Credentials
//
// The suite resolves the workspace through internal/app, the same
// startup assembly the binary runs — the domain from
// PIPEDRIVE_COMPANY_DOMAIN, else the userconfig pointer; the token from
// PIPEDRIVE_API_TOKEN, else the OS keyring — so a maintainer who has
// run `pipedrive-mcp login` needs no extra setup and CI needs only the
// two env vars. With neither, every test skips with the reason instead
// of failing, so the tag is safe to carry in a job that holds no
// secret.
//
// # Writes
//
// Reads, resource reads, guard refusals and dry runs run on the tag
// alone; none of them mutates anything. The reversible write probes
// additionally require PIPEDRIVE_INTEGRATION_WRITES=1, because the
// workspace on the other end is somebody's real CRM and Pipedrive has
// no undo.
//
// Going through internal/app is also what makes PIPEDRIVE_DRY_RUN the
// floor here that it is everywhere else — this suite dropped it once by
// assembling the server's arguments by hand. The
// write probes skip under it rather than failing: every write would
// rehearse, and there would be nothing for the read-backs to see.
//
// Each write probe captures the original value first, restores it from
// t.Cleanup so a failure mid-test still puts it back, and verifies the
// restore with an independent read rather than trusting the write's
// echo. Collection fields are only ever appended to and then trimmed
// back, so replace-semantics cannot drop an entry that was already
// there. Nothing here writes a field that was empty: v2 has no
// spelling that empties one again, so an empty field is not a
// reversible place to write — see docs/architecture.md, "Clearing a
// field".
//
// One residue the suite cannot avoid: the note probe creates a note
// and deletes it, and a v1 delete is soft — the record stays with
// active_flag false, because nothing in the API purges it. Its content
// says which run made it.
package integration

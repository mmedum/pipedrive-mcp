//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/app"
	"github.com/mmedum/pipedrive-mcp/internal/credentials"
	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
)

// writesEnv gates the reversible write probes. The tag alone runs the
// read half, which mutates nothing; writing to a live CRM is a second,
// deliberate opt-in.
const writesEnv = "PIPEDRIVE_INTEGRATION_WRITES"

var (
	// live is the connected session every test drives, and liveClient
	// the Pipedrive client underneath it. One of each per suite: a
	// session per test would re-warm the three field caches every
	// time, which is three HTTP calls against a rate-limited API to
	// learn something the first test already knows.
	live       *mcp.ClientSession
	liveClient *pipedrive.Client
	liveDomain string

	// dryRunFloor mirrors PIPEDRIVE_DRY_RUN. The suite honours it the
	// way the binary does, so the write probes cannot run under it —
	// every write would rehearse and every read-back would fail
	// against a record that never moved. requireWrites skips instead.
	dryRunFloor bool

	// skipReason is set when the workspace could not be resolved. The
	// tests skip with it rather than fail, so the tag stays safe to
	// carry in a job that has no credentials.
	skipReason string
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

// run is TestMain's body, split out so the teardown defers actually
// run — os.Exit does not unwind them.
func run(m *testing.M) int {
	// app.Resolve is the same startup assembly the binary performs:
	// domain from env or the userconfig pointer, configuration for that
	// domain, token from env or the keyring. Going through it rather
	// than around it is what keeps PIPEDRIVE_HTTP_TIMEOUT and the
	// PIPEDRIVE_DRY_RUN floor meaning here what they mean in the
	// binary — this suite dropped the floor once by assembling the
	// arguments by hand.
	settings, err := app.Resolve()
	if err != nil {
		skipReason = skipMessage(err)
		return m.Run()
	}
	liveDomain = settings.Domain()
	dryRunFloor = settings.Config.DryRun

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Discard the client's log. `go test` would interleave it with the
	// test output, and a call that fails carries its [class] message
	// into the assertion that reads it.
	rt := settings.Connect(slog.New(slog.NewTextHandler(io.Discard, nil)))
	liveClient = rt.Client

	// rt.NewServer, not a hand-rolled registration: the point of this
	// suite is that it drives what the binary ships, cache warm-up and
	// server instructions included — and a Runtime's client and config
	// cannot describe two different workspaces.
	h, err := testutil.ConnectTo(ctx, rt.NewServer(ctx, "integration"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration: %v\n", err)
		return 1
	}
	defer h.Close()

	live = h.Client
	return m.Run()
}

// skipMessage turns a resolution failure into something a reader can
// act on. app.Resolve is terse about a missing token because main exits
// on it; a suite that skips should say how to unskip, and should name
// the variable rather than the workspace.
func skipMessage(err error) string {
	if errors.Is(err, credentials.ErrNotFound) {
		return fmt.Sprintf("no token for the configured workspace: set %s, or run `pipedrive-mcp login`", credentials.EnvVar)
	}
	return err.Error()
}

// requireLive skips the test when there is no workspace to drive.
func requireLive(t *testing.T) {
	t.Helper()
	if live == nil {
		t.Skip(skipReason)
	}
}

// requireWrites skips unless the caller has opted in to mutating the
// workspace. The messages name the variable rather than the workspace,
// because test output gets pasted into public places.
func requireWrites(t *testing.T) {
	t.Helper()
	requireLive(t)
	if dryRunFloor {
		t.Skip("PIPEDRIVE_DRY_RUN is set: the floor rehearses every write, so the probes have nothing to read back")
	}
	if os.Getenv(writesEnv) != "1" {
		t.Skipf("reversible writes against the configured workspace need %s=1", writesEnv)
	}
}

// call drives one tool. A transport error is fatal: it means the SDK
// rejected the call before any handler ran, which nothing below is
// written to cope with. Tool execution errors arrive on the result
// instead, which is what the guard assertions read.
func call(t *testing.T, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := live.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport: %v", name, err)
	}
	return res
}

// mustCall fails unless the tool succeeded, then decodes its
// structured result into out. out may be nil when only the outcome
// matters.
func mustCall(t *testing.T, name string, args map[string]any, out any) {
	t.Helper()
	res := call(t, name, args)
	if res.IsError {
		t.Fatalf("%s: %s", name, testutil.TextContent(res))
	}
	if out == nil {
		return
	}
	if res.StructuredContent == nil {
		t.Fatalf("%s: succeeded with no structured content", name)
	}
	testutil.DecodeStructured(t, res.StructuredContent, out)
}

// errClass returns the leading [class] tag of an error result — the
// same classifier the LLM branches on — or "" when the call succeeded.
func errClass(res *mcp.CallToolResult) string {
	if !res.IsError {
		return ""
	}
	if rest, ok := strings.CutPrefix(testutil.TextContent(res), "["); ok {
		if class, _, found := strings.Cut(rest, "]"); found && class != "" {
			return class
		}
	}
	return "error"
}

// pickWhere returns the first row satisfying ok, or skips with why.
// Every probe starts with "a record shaped like this, or there is
// nothing to do here", and writing the loop once keeps `&rows[i]` from
// being copy-pasted as `for _, r := range rows` — which takes the
// address of the loop variable instead of the row.
func pickWhere[T any](t *testing.T, rows []T, why string, ok func(T) bool) T {
	t.Helper()
	for i := range rows {
		if ok(rows[i]) {
			return rows[i]
		}
	}
	t.Skip(why)
	return *new(T) // unreachable: Skip does not return
}

// probeLabel marks a value this suite produced, so anything that
// escapes a failed restore — or a rehearsal that turns out not to have
// been one — is identifiable in the workspace rather than anonymous.
const probeLabel = "pipedrive-mcp probe"

// runStamp is fixed for the whole run, so every value one run wrote
// carries the same mark. Stamping per call would give a note and the
// edit to that note two different marks.
var runStamp = time.Now().Unix()

// probeValue is the marked value a probe writes or claims it would.
func probeValue(kind string) string {
	return fmt.Sprintf("%s %s %d", probeLabel, kind, runStamp)
}

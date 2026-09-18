package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// The three frames a client must send before it may ask for anything.
// The notification in the middle is required by the MCP spec after
// initialize; older versions of the Go SDK were lenient about it and
// newer ones are not, and a container's buffering is where that gap
// first showed — back when this drove one.
const (
	frameInit = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}`
	frameInitialized = `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`
	frameList        = `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`

	// The current revision, which needs no handshake at all: the
	// version and the client's capabilities ride in _meta on every
	// request. Sent as id 3 after the legacy three, so one run proves
	// the binary answers both eras.
	frameDiscover = `{"jsonrpc":"2.0","id":3,"method":"server/discover","params":{"_meta":` +
		`{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
		`"io.modelcontextprotocol/clientCapabilities":{}}}}`
)

// currentRevision is the protocol revision server/discover must list.
// A server that does not speak it is on the legacy side of the era
// boundary the spec draws, and a modern-only client fails against it.
const currentRevision = "2026-07-28"

// smokeGate drives a minimal MCP handshake and reads the reply.
//
// It asserts on the decoded frame rather than grepping the output, which
// is the reason this stopped being a shell script: `grep '"tools"'`
// passes on a line that merely mentions the word, including one inside
// an error message.
func smokeGate(w io.Writer, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: gates smoke binary TARGET")
	}
	mode, target := args[0], args[1]

	// The smoke checks stdio framing, not credentials, so the probe is
	// skipped and the token only has to exist.
	env := append(os.Environ(),
		"PIPEDRIVE_API_TOKEN="+orDefault(os.Getenv("PIPEDRIVE_API_TOKEN"), "smoke-token"),
		"PIPEDRIVE_COMPANY_DOMAIN="+orDefault(os.Getenv("PIPEDRIVE_COMPANY_DOMAIN"), "smoke"))

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if mode != "binary" {
		return fmt.Errorf("unknown mode %q (want binary)", mode)
	}
	hold := holdOpen()
	cmd := exec.CommandContext(ctx, target, "--skip-probe")
	cmd.Env = env
	// Write the frames, then hold stdin open. A reader that ends at the
	// last frame hands the server EOF immediately and it shuts down before
	// it has flushed a reply — which looks exactly like a server that
	// never answered. The shell version held it with a sleep; this holds
	// it with a pipe it closes on a timer.
	stdin, stdinW := io.Pipe()
	cmd.Stdin = stdin
	go func() {
		_, _ = io.WriteString(stdinW, frameInit+"\n"+frameInitialized+"\n"+frameList+"\n"+frameDiscover+"\n")
		time.Sleep(hold)
		_ = stdinW.Close()
	}()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	// The server is expected to close on EOF; a non-zero exit is only
	// interesting when the reply is missing, so the error is held and
	// reported with the output rather than instead of it.
	runErr := cmd.Run()

	reply, err := toolsListReply(stdout.String())
	if err != nil {
		return fmt.Errorf("%s %s: %w\nexit: %s\nstdout:\n%s\nstderr:\n%s",
			mode, target, err, exitOf(runErr), clip(stdout.String()), clip(stderr.String()))
	}
	versions, err := discoverReply(stdout.String())
	if err != nil {
		return fmt.Errorf("%s %s: %w\nexit: %s\nstdout:\n%s\nstderr:\n%s",
			mode, target, err, exitOf(runErr), clip(stdout.String()), clip(stderr.String()))
	}
	if !slices.Contains(versions, currentRevision) {
		return fmt.Errorf("%s %s: server/discover lists %v, which does not include the current revision %s",
			mode, target, versions, currentRevision)
	}

	_, _ = fmt.Fprintf(w, "stdio smoke ok (%s): tools/list answered with %d tool(s); server/discover lists %d revision(s), newest %s\n",
		mode, reply, len(versions), versions[0])
	return nil
}

// discoverReply reads the supportedVersions from the reply to id 3.
//
// Asserted rather than merely printed: the SDK answers server/discover
// with method-not-found unless the request carries the new protocol's
// _meta, so a frame sent the old way gets a clean refusal that looks
// nothing like a missing feature. Reading the list back is what tells
// the two apart.
func discoverReply(out string) ([]string, error) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var frame struct {
			ID     *int `json:"id"`
			Result *struct {
				SupportedVersions []string `json:"supportedVersions"`
			} `json:"result"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			continue
		}
		if frame.ID == nil || *frame.ID != 3 {
			continue
		}
		if frame.Error != nil {
			return nil, fmt.Errorf("server/discover was refused: %s", frame.Error.Message)
		}
		if frame.Result == nil || len(frame.Result.SupportedVersions) == 0 {
			return nil, fmt.Errorf("the reply to server/discover carried no supportedVersions")
		}
		return frame.Result.SupportedVersions, nil
	}
	return nil, fmt.Errorf("no reply to server/discover (id 3) in the output")
}

// toolsListReply finds the reply to id 2 and returns how many tools it
// carried. A frame that is not JSON, or carries no tools array, is a
// failure however much it looks like one in a terminal.
func toolsListReply(out string) (int, error) {
	if strings.TrimSpace(out) == "" {
		return 0, fmt.Errorf("no output")
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var frame struct {
			ID     *int `json:"id"`
			Result *struct {
				Tools *[]json.RawMessage `json:"tools"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			continue // not every line is a frame; the reply we want is
		}
		if frame.ID == nil || *frame.ID != 2 {
			continue
		}
		if frame.Result == nil || frame.Result.Tools == nil {
			return 0, fmt.Errorf("the reply to tools/list carried no tools array")
		}
		return len(*frame.Result.Tools), nil
	}
	return 0, fmt.Errorf("no reply to tools/list (id 2) in the output")
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func clip(s string) string {
	const limit = 2000
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}

// holdOpen is how long stdin stays open after the last frame, for a
// local binary. SMOKE_HOLD overrides it, in either mode.
func holdOpen() time.Duration {
	if v := os.Getenv("SMOKE_HOLD"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 3 * time.Second
}

// exitOf names the process's exit without %w, which renders a nil error
// as %!w(<nil>) — the first version of this printed exactly that in CI
// and said nothing useful about why the container was silent.
func exitOf(err error) string {
	if err == nil {
		return "0"
	}
	return err.Error()
}

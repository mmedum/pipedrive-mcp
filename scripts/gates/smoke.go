package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// The three frames a client must send before it may ask for anything.
// The notification in the middle is required by the MCP spec after
// initialize; older versions of the Go SDK were lenient about it and
// newer ones are not, and the Docker container's buffering is where that
// gap first showed.
const (
	frameInit = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}`
	frameInitialized = `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`
	frameList        = `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
)

// smokeGate drives a minimal MCP handshake and reads the reply.
//
// It asserts on the decoded frame rather than grepping the output, which
// is the reason this stopped being a shell script: `grep '"tools"'`
// passes on a line that merely mentions the word, including one inside
// an error message.
func smokeGate(w io.Writer, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: gates smoke (binary|docker) TARGET")
	}
	mode, target := args[0], args[1]

	// The smoke checks stdio framing, not credentials, so the probe is
	// skipped and the token only has to exist.
	env := append(os.Environ(),
		"PIPEDRIVE_API_TOKEN="+orDefault(os.Getenv("PIPEDRIVE_API_TOKEN"), "smoke-token"),
		"PIPEDRIVE_COMPANY_DOMAIN="+orDefault(os.Getenv("PIPEDRIVE_COMPANY_DOMAIN"), "smoke"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	switch mode {
	case "binary":
		cmd = exec.CommandContext(ctx, target, "--skip-probe")
	case "docker":
		cmd = exec.CommandContext(ctx, "docker", "run", "-i", "--rm",
			"-e", "PIPEDRIVE_API_TOKEN", "-e", "PIPEDRIVE_COMPANY_DOMAIN",
			target, "--skip-probe")
	default:
		return fmt.Errorf("unknown mode %q (want binary or docker)", mode)
	}
	cmd.Env = env
	// Write the frames, then hold stdin open. A reader that ends at the
	// last frame hands the server EOF immediately and it shuts down before
	// it has flushed a reply — which looks exactly like a server that
	// never answered. The shell version held it with a sleep; this holds
	// it with a pipe it closes on a timer.
	stdin, stdinW := io.Pipe()
	cmd.Stdin = stdin
	go func() {
		_, _ = io.WriteString(stdinW, frameInit+"\n"+frameInitialized+"\n"+frameList+"\n")
		time.Sleep(holdOpen())
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
		return fmt.Errorf("%s %s: %w\nstdout:\n%s\nstderr:\n%s\nexit: %w",
			mode, target, err, clip(stdout.String()), clip(stderr.String()), runErr)
	}
	_, _ = fmt.Fprintf(w, "stdio smoke ok (%s): tools/list answered with %d tool(s)\n", mode, reply)
	return nil
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

// holdOpen is how long stdin stays open after the last frame. Tuned for
// a Docker cold start on a shared runner; a tighter value works locally
// and loses the margin.
func holdOpen() time.Duration {
	if v := os.Getenv("SMOKE_HOLD"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 3 * time.Second
}

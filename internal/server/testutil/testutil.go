// Package testutil provides shared helpers for tool-handler tests.
//
// The canonical pattern: a test stands up an MCP server with the tool
// under test registered, connects an in-memory client to it via
// mcp.NewInMemoryTransports (net.Pipe-backed), and drives the handler
// through the SDK's CallTool API. This exercises the full
// schema-validate → handler → response shape end-to-end without
// spawning the binary or hitting stdio. CLAUDE.md "Where things go"
// names this as the standard test pattern for Phase 1+ tools.
package testutil

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Harness pairs a connected client and server session for tool-handler
// tests. Close() tears both down; tests should defer it.
type Harness struct {
	Client  *mcp.ClientSession
	Server  *mcp.ServerSession
	closeFn func()
}

// Close shuts down both sessions. Safe to call once.
func (h *Harness) Close() {
	if h.closeFn != nil {
		h.closeFn()
		h.closeFn = nil
	}
}

// Connect spins up an in-memory MCP server with `register` already
// called against it (typically a tool package's Register function),
// connects an in-memory client, and returns the harness. Use
// h.Client.CallTool to drive a registered tool.
func Connect(t *testing.T, register func(*mcp.Server)) *Harness {
	t.Helper()
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "pipedrive-mcp-test",
		Version: "test",
	}, nil)
	register(server)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	ss, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = ss.Close()
		t.Fatalf("client.Connect: %v", err)
	}

	return &Harness{
		Client: cs,
		Server: ss,
		closeFn: func() {
			_ = cs.Close()
			_ = ss.Close()
		},
	}
}

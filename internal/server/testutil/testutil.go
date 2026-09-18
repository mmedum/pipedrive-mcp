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
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Harness pairs a connected client and server session for tool-handler
// tests. Close() tears both down; tests should defer it.
type Harness struct {
	Client  *mcp.ClientSession
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

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "pipedrive-mcp-test",
		Version: "test",
	}, nil)
	register(server)

	h, err := ConnectTo(context.Background(), server)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// ConnectTo connects an in-memory client to a server the caller has
// already built, and is what Connect delegates to. It exists for the
// callers that cannot use Connect: one holding no *testing.T (a
// TestMain), or one whose server has to be the real thing rather than
// a bare mcp.NewServer with a tool registered on it.
func ConnectTo(ctx context.Context, server *mcp.Server) (*Harness, error) {
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	ss, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, fmt.Errorf("server.Connect: %w", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		_ = ss.Close()
		return nil, fmt.Errorf("client.Connect: %w", err)
	}

	return &Harness{
		Client: cs,
		closeFn: func() {
			_ = cs.Close()
			_ = ss.Close()
		},
	}, nil
}

// CallTool connects a server with register already applied, calls one
// tool on it, and returns the result.
//
// It exists because the connect / call / check-the-transport-error
// sequence was written out at nearly a hundred call sites, each one
// three or four lines of harness around one line of intent. A transport
// error fails the test rather than being returned: it means the harness
// broke, which is never what the caller was asserting about.
//
// A tool that answers with isError is NOT a failure here — that is a
// tool-execution error, which is a result the caller usually wants to
// assert on. Read it off the returned result.
func CallTool(t *testing.T, register func(*mcp.Server), name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()

	h := Connect(t, register)
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("calling %s: %v", name, err)
	}
	return res
}

// CallToolInto is CallTool plus decoding the structured result into
// `into`, which is what most callers do next. A result that carries no
// structured content, or that is an error, is left undecoded and handed
// back for the caller to assert on.
func CallToolInto(t *testing.T, register func(*mcp.Server), name string, args map[string]any, into any) *mcp.CallToolResult {
	t.Helper()

	res := CallTool(t, register, name, args)
	if into != nil && !res.IsError && res.StructuredContent != nil {
		DecodeStructured(t, res.StructuredContent, into)
	}
	return res
}

// Package server wires the MCP SDK to the Pipedrive client. It does not
// own the lifetime of either; cmd/pipedrive-mcp/main.go does. This
// package only provides the constructor that returns a server with
// every tool package's Register function called.
package server

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

// New constructs an MCP server, registering every tool with the given
// Pipedrive client. Each tool package exposes a Register(srv, client)
// function called from here.
func New(name, version string, _ *pipedrive.Client) *mcp.Server {
	return mcp.NewServer(&mcp.Implementation{
		Name:    name,
		Version: version,
	}, nil)
}

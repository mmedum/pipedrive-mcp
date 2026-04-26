// Package server wires the MCP SDK to the Pipedrive client. It does not
// own the lifetime of either; cmd/pipedrive-mcp/main.go does. This
// package only provides the constructor that returns a server with
// every tool package's Register function called.
package server

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/tools"
)

// New constructs an MCP server with every tool package wired up. The
// client may be nil (used by the --dump-schemas path, where tool
// handlers never execute — only their schemas are dumped). domain is
// used for URL injection in tool outputs.
func New(name, version string, client *pipedrive.Client, domain string) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    name,
		Version: version,
	}, nil)

	tools.RegisterPipelines(srv, client, domain)
	tools.RegisterDeals(srv, client, domain)

	if client != nil {
		// Warm the deal-field cache off the critical path so the
		// first user-visible get_deal/list_deals doesn't pay the
		// /dealFields round-trip. sync.Once inside the cache means
		// a real call arriving mid-warm just blocks on the same
		// fetch — never a duplicate request.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			client.WarmDealFields(ctx)
		}()
	}

	return srv
}

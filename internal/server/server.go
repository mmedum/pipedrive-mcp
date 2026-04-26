// Package server wires the MCP SDK to the Pipedrive client. It does not
// own the lifetime of either; cmd/pipedrive-mcp/main.go does. This
// package only provides the constructor that returns a server with
// every tool package's Register function called.
package server

import (
	"context"
	"sync"
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
	tools.RegisterPersons(srv, client, domain)
	tools.RegisterOrganizations(srv, client, domain)
	tools.RegisterSearch(srv, client)

	if client != nil {
		// Warm the field caches off the critical path so the first
		// user-visible get_X call doesn't pay the /XFields round-trip.
		// sync.Once inside each cache means a real call arriving
		// mid-warm just blocks on the same fetch — never duplicates.
		// Fan out across resources so a slow tail on one fetch
		// doesn't delay the others (cuts wall-clock to max(t1,t2,t3)).
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			var wg sync.WaitGroup
			wg.Add(3)
			go func() { defer wg.Done(); client.WarmDealFields(ctx) }()
			go func() { defer wg.Done(); client.WarmPersonFields(ctx) }()
			go func() { defer wg.Done(); client.WarmOrganizationFields(ctx) }()
			wg.Wait()
		}()
	}

	return srv
}

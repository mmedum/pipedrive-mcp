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
//
// dryRun mirrors PIPEDRIVE_DRY_RUN: write tools (create_note,
// delete_note) return synthetic previews instead of issuing the
// upstream mutation. enableDestructive mirrors
// PIPEDRIVE_ENABLE_DESTRUCTIVE: destructive tools (delete_note) are
// only registered when this is true. Per CLAUDE.md hard rule #3,
// destructive registration is server-build-time gating, not
// annotation-based.
//
// The parent ctx governs the cache-warm goroutine's lifetime. When
// it cancels (e.g. SIGTERM), the warm-up's in-flight HTTP requests
// cancel cleanly instead of running orphaned to completion.
func New(ctx context.Context, name, version string, client *pipedrive.Client, domain string, dryRun, enableDestructive bool) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    name,
		Version: version,
	}, nil)

	tools.RegisterPipelines(srv, client, domain)
	tools.RegisterDeals(srv, client, domain)
	tools.RegisterPersons(srv, client, domain)
	tools.RegisterOrganizations(srv, client, domain)
	tools.RegisterActivities(srv, client, domain)
	tools.RegisterNotes(srv, client, dryRun, enableDestructive)
	tools.RegisterCache(srv, client)
	tools.RegisterSearch(srv, client)

	if client != nil {
		// Warm the field caches off the critical path so the first
		// user-visible get_X call doesn't pay the /XFields round-trip.
		// sync.Once inside each cache means a real call arriving
		// mid-warm just blocks on the same fetch — never duplicates.
		// Fan out across resources so a slow tail on one fetch
		// doesn't delay the others (cuts wall-clock to max(t1,t2,t3)).
		// The 30s timeout caps the warm cycle; the parent ctx
		// shortens it further on shutdown.
		go func() {
			warmCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			var wg sync.WaitGroup
			wg.Add(3)
			go func() { defer wg.Done(); client.WarmDealFields(warmCtx) }()
			go func() { defer wg.Done(); client.WarmPersonFields(warmCtx) }()
			go func() { defer wg.Done(); client.WarmOrganizationFields(warmCtx) }()
			wg.Wait()
		}()
	}

	return srv
}

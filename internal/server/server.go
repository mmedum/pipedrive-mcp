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

	"github.com/mmedum/pipedrive-mcp/internal/config"
	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/tools"
)

// instructions is the server-level guidance an MCP client receives at
// initialize. Every shipped Google Workspace MCP server carries one,
// and it is where the things no single tool description owns belong:
// which tool to start from, what each costs, the traps that return a
// wrong answer rather than an error, and what is out of scope.
const instructions = `Pipedrive CRM tools, against one workspace's API token.

Start with search. It is the natural-language gateway: it turns a name into the numeric id every other tool needs, and it covers deals, persons, organizations, products, files and leads in one call. The list_ tools are the precision filters for when you already hold the ids. list_pipelines and list_stages name the pipeline and stage a deal is filed under, and take no paging, because a workspace rarely has more than twenty pipelines.

whoami says which account the token acts as. Call it before attributing anything to "me": its user_id is the owner_id a record gets when you create one without naming an owner, so it is what turns "my open deals" into list_deals(owner_id=...), and it reports the timezone an activity's due_time is written in.

Read one record with get_deal, get_person, get_organization, get_activity or get_note, and many with the matching list_ tool. A get_ is one call and a list_ is one call, but a list_ you then page through is as many calls as you ask for, so filter rather than sweeping. Custom fields come back resolved to the names they carry in the workspace rather than the 40-character hashes Pipedrive stores them under, and a dropdown comes back as its label rather than the option id, so you can read them and quote them directly; refresh_field_cache re-reads both after somebody adds, renames or extends a field in the Pipedrive UI.

Every mutation goes through a manage_ tool — manage_deal, manage_person, manage_organization, manage_activity, manage_note — with an action saying which.

Writing is guarded. A manage_ tool reads its target before it writes and refuses to replace ANY field that already holds a value unless you name that field in overwrite, e.g. overwrite: ["title", "value"] — the refusal lists exactly the names to pass, and naming fewer than it listed is still refused over the rest; filling a field that is empty destroys nothing and needs no permission. expect_version refuses a write whose record moved since you read it. Pipedrive has no undo, so take a refusal as information rather than an obstacle, and reach for dry_run when you are not sure what is there. overwrite carries a decision the USER made, not a way past a refusal: if a write is refused and the user has not said to replace what is there, report what the refusal named and stop. Re-sending the same write with overwrite makes the guard decorative. The named transitions — mark_won, mark_lost, move_stage, reopen, complete, archive, unarchive — take no overwrite, because the field they change is the field you named.

IMPORTANT: a list_ page holding fewer rows than the limit is NOT the end. Keep going while next_cursor comes back non-empty.

Three things no amount of retrying will fix. Activity type (call, email, meeting and so on) cannot be filtered server-side, so ask for the rows and filter them on their own type field. Activities are not indexed by search: reach them through list_activities, filtered by the deal or person they hang off. And a field that already holds a value can be changed but NOT cleared — Pipedrive v2 rejects a null and stores an empty string as a value — so omit what you do not mean to change rather than sending a blank to empty it.

Resources pipedrive://deals/{id}, and the same for persons, organizations, activities and notes, carry what the matching get_ tool returns, for attaching a record rather than calling a tool. They take no options, so include_attendees and include_notes still need the tool.

Everything here is Pipedrive v2 except notes, which v2 does not expose at all; those come from v1 and behave the same way, except that deleting one is soft — it clears active_flag, and nothing here sets it back. Custom fields are writable on deals, people and organizations: pass custom_fields keyed by the names a get_ reports, and give a dropdown its label rather than an option id. ARCHIVING IS NOT CLOSING: archive takes a deal out of the pipeline and out of list_deals — they are their own collection, so pass archived to read them — and an archived deal accepts no edit until you unarchive it. mark_lost is what 'we lost it' means. Products, leads, files, projects and goals are not here.`

// Name is the server's MCP implementation name, and the binary's.
const Name = "pipedrive-mcp"

// New constructs an MCP server with every tool package wired up.
//
// It takes the whole config.Config rather than a domain and a
// RegisterOptions, because those two are derived from it and a caller
// assembling them by hand can make them disagree — which is how the
// PIPEDRIVE_DRY_RUN floor, a promise docs/security.md makes to an
// operator, was once dropped for a whole process. cfg.CompanyDomain
// drives URL injection in tool outputs and cfg.DryRun is the
// server-wide rehearsal floor; see the RegisterOptions godoc.
//
// The parent ctx governs the cache-warm goroutine's lifetime. When
// it cancels (e.g. SIGTERM), the warm-up's in-flight HTTP requests
// cancel cleanly instead of running orphaned to completion.
func New(ctx context.Context, version string, client *pipedrive.Client, cfg config.Config) *mcp.Server {
	return newServer(ctx, version, client, cfg.CompanyDomain, tools.RegisterOptions{
		DryRun: cfg.DryRun,
	})
}

// NewForSchemaDump constructs a clientless server purely so
// --dump-schemas can walk the registry. No handler ever runs, so there
// is no workspace to name and no dry-run floor to honor — which is
// why this is a separate constructor rather than New with zero
// arguments a reader might copy.
func NewForSchemaDump() *mcp.Server {
	return newServer(context.Background(), "", nil, "", tools.RegisterOptions{})
}

func newServer(ctx context.Context, version string, client *pipedrive.Client, domain string, opts tools.RegisterOptions) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    Name,
		Version: version,
	}, &mcp.ServerOptions{Instructions: instructions})

	tools.RegisterPipelines(srv, client, domain)
	tools.RegisterDeals(srv, client, domain, opts)
	tools.RegisterPersons(srv, client, domain, opts)
	tools.RegisterOrganizations(srv, client, domain, opts)
	tools.RegisterActivities(srv, client, domain, opts)
	tools.RegisterNotes(srv, client, opts)
	tools.RegisterCache(srv, client)
	tools.RegisterSearch(srv, client)
	tools.RegisterWhoAmI(srv, client)
	tools.RegisterResources(srv, client, domain)

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

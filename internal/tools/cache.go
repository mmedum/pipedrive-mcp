package tools

import (
	"context"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type cacheClient interface {
	ReloadDealFields(ctx context.Context) (int, error)
	ReloadPersonFields(ctx context.Context) (int, error)
	ReloadOrganizationFields(ctx context.Context) (int, error)
}

// fieldCacheRefresh is one row of refreshFieldCacheOutput. Error is
// the per-resource [class] message when that resource's reload fails;
// successes return error="" and the live count.
type fieldCacheRefresh struct {
	Resource string `json:"resource" jsonschema:"deals | persons | organizations"`
	Count    int    `json:"count" jsonschema:"number of fields cached after reload; 0 on error"`
	Error    string `json:"error,omitempty" jsonschema:"per-resource error message; empty on success"`
}

type refreshFieldCacheInput struct{}

type refreshFieldCacheOutput struct {
	Refreshed []fieldCacheRefresh `json:"refreshed" jsonschema:"per-resource result, one entry each for deals / persons / organizations"`
	Errors    int                 `json:"errors,omitempty" jsonschema:"how many resources failed to reload; 0 on full success"`
}

// RegisterCache wires the refresh_field_cache tool into the MCP
// server. The tool has no inputs — it always refreshes all three
// per-resource caches (deals, persons, organizations) in parallel.
// Activities don't have a custom-field cache on Pipedrive v2 so they
// are intentionally absent.
func RegisterCache(s *mcp.Server, c cacheClient) {
	annotations := mcp.ToolAnnotations{IdempotentHint: true}

	AddTool(s, &mcp.Tool{
		Name:        "refresh_field_cache",
		Description: "Refresh Pipedrive's custom-field name caches for deals, persons, and organizations. Use after a custom field is added or renamed in the Pipedrive UI so the LLM-facing tools (get_deal, get_person, get_organization, list_X) start surfacing the new name without restarting the server. Refreshes all three resources in parallel; returns per-resource field count after reload, plus an `errors` count if any resource failed. Activities do not have custom fields on Pipedrive v2 and are intentionally skipped.",
		Annotations: &annotations,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ refreshFieldCacheInput) (*mcp.CallToolResult, refreshFieldCacheOutput, error) {
		return nil, refreshAllFieldCaches(ctx, c), nil
	})
}

// refreshAllFieldCaches fans out the three reload calls in parallel
// — Pipedrive's /XFields endpoints are independent so wall-clock cost
// is max(t1,t2,t3) rather than t1+t2+t3.
func refreshAllFieldCaches(ctx context.Context, c cacheClient) refreshFieldCacheOutput {
	rows := make([]fieldCacheRefresh, 3)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		count, err := c.ReloadDealFields(ctx)
		rows[0] = rowFor("deals", count, err)
	}()
	go func() {
		defer wg.Done()
		count, err := c.ReloadPersonFields(ctx)
		rows[1] = rowFor("persons", count, err)
	}()
	go func() {
		defer wg.Done()
		count, err := c.ReloadOrganizationFields(ctx)
		rows[2] = rowFor("organizations", count, err)
	}()
	wg.Wait()

	out := refreshFieldCacheOutput{Refreshed: rows}
	for _, r := range rows {
		if r.Error != "" {
			out.Errors++
		}
	}
	return out
}

func rowFor(resource string, count int, err error) fieldCacheRefresh {
	row := fieldCacheRefresh{Resource: resource, Count: count}
	if err != nil {
		row.Error = errorText(err)
	}
	return row
}

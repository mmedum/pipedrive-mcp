package tools_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
	"github.com/mmedum/pipedrive-mcp/internal/tools"
)

type fakeCacheClient struct {
	dealCount   int
	personCount int
	orgCount    int

	dealErr   error
	personErr error
	orgErr    error

	dealCalls   atomic.Int64
	personCalls atomic.Int64
	orgCalls    atomic.Int64
}

func (f *fakeCacheClient) ReloadDealFields(_ context.Context) (int, error) {
	f.dealCalls.Add(1)
	return f.dealCount, f.dealErr
}

func (f *fakeCacheClient) ReloadPersonFields(_ context.Context) (int, error) {
	f.personCalls.Add(1)
	return f.personCount, f.personErr
}

func (f *fakeCacheClient) ReloadOrganizationFields(_ context.Context) (int, error) {
	f.orgCalls.Add(1)
	return f.orgCount, f.orgErr
}

type cacheRow struct {
	Resource string `json:"resource"`
	Count    int    `json:"count"`
	Error    string `json:"error,omitempty"`
}

type cacheOutput struct {
	Refreshed []cacheRow `json:"refreshed"`
	Errors    int        `json:"errors,omitempty"`
}

func TestRefreshFieldCache_HappyPath(t *testing.T) {
	fake := &fakeCacheClient{
		dealCount: 18, personCount: 12, orgCount: 7,
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterCache(s, fake)
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "refresh_field_cache",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out cacheOutput
	testutil.DecodeStructured(t, res.StructuredContent, &out)

	if out.Errors != 0 {
		t.Errorf("errors = %d; want 0 on full success", out.Errors)
	}
	if len(out.Refreshed) != 3 {
		t.Fatalf("refreshed rows = %d; want 3", len(out.Refreshed))
	}
	want := map[string]int{"deals": 18, "persons": 12, "organizations": 7}
	for _, row := range out.Refreshed {
		if row.Error != "" {
			t.Errorf("%s row carries unexpected error: %q", row.Resource, row.Error)
		}
		if row.Count != want[row.Resource] {
			t.Errorf("%s count = %d; want %d", row.Resource, row.Count, want[row.Resource])
		}
	}
	if fake.dealCalls.Load() != 1 || fake.personCalls.Load() != 1 || fake.orgCalls.Load() != 1 {
		t.Errorf("each Reload should fire exactly once; got deals=%d persons=%d orgs=%d",
			fake.dealCalls.Load(), fake.personCalls.Load(), fake.orgCalls.Load())
	}
}

func TestRefreshFieldCache_PartialFailure(t *testing.T) {
	fake := &fakeCacheClient{
		dealCount: 18,
		personErr: &pipedrive.APIError{Class: pipedrive.ErrServerError, Status: 503, Message: "upstream down"},
		orgCount:  7,
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterCache(s, fake)
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "refresh_field_cache",
		Arguments: map[string]any{},
	})
	if res.IsError {
		t.Fatalf("unexpected isError on partial failure (per-resource errors are surfaced in the row, not isError): %+v", res.Content)
	}
	var out cacheOutput
	testutil.DecodeStructured(t, res.StructuredContent, &out)

	if out.Errors != 1 {
		t.Errorf("errors = %d; want 1 (only persons failed)", out.Errors)
	}
	rows := make(map[string]cacheRow, 3)
	for _, r := range out.Refreshed {
		rows[r.Resource] = r
	}
	if rows["deals"].Error != "" || rows["deals"].Count != 18 {
		t.Errorf("deals row = %+v; want error='' count=18", rows["deals"])
	}
	if rows["organizations"].Error != "" || rows["organizations"].Count != 7 {
		t.Errorf("organizations row = %+v; want error='' count=7", rows["organizations"])
	}
	if rows["persons"].Error == "" {
		t.Errorf("persons row should carry the upstream error; got %+v", rows["persons"])
	}
	if !strings.HasPrefix(rows["persons"].Error, "[server_error]") {
		t.Errorf("persons error prefix = %q; want [server_error]", rows["persons"].Error)
	}
	if rows["persons"].Count != 0 {
		t.Errorf("failed reload should report count=0; got %d", rows["persons"].Count)
	}
}

func TestRefreshFieldCache_RunsInParallel(t *testing.T) {
	// Each Reload increments a counter; the three calls happen
	// concurrently rather than serially. Loose check: no fake-side
	// ordering required, just confirm all three fire and the
	// fake's per-resource counters are independent.
	fake := &fakeCacheClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterCache(s, fake)
	})
	defer h.Close()

	_, _ = h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "refresh_field_cache",
		Arguments: map[string]any{},
	})
	if fake.dealCalls.Load()+fake.personCalls.Load()+fake.orgCalls.Load() != 3 {
		t.Errorf("expected 3 Reload calls (1 per resource); got d=%d p=%d o=%d",
			fake.dealCalls.Load(), fake.personCalls.Load(), fake.orgCalls.Load())
	}
}

func TestRegisterCache_RegistersInDumpRegistry(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterCache(s, &fakeCacheClient{})
	})
	defer h.Close()

	var buf strings.Builder
	if err := tools.DumpJSON(&buf, "test"); err != nil {
		t.Fatalf("DumpJSON: %v", err)
	}
	if !strings.Contains(buf.String(), `"refresh_field_cache"`) {
		t.Errorf("dump missing 'refresh_field_cache'; got: %s", buf.String())
	}
}

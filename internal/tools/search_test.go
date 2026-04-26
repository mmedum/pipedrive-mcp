package tools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
	"github.com/mmedum/pipedrive-mcp/internal/tools"
)

type fakeSearchClient struct {
	hits     []pipedrive.SearchHit
	next     string
	err      error
	lastOpts pipedrive.SearchOptions
}

func (f *fakeSearchClient) ItemSearch(_ context.Context, opts pipedrive.SearchOptions) ([]pipedrive.SearchHit, string, error) {
	f.lastOpts = opts
	return f.hits, f.next, f.err
}

// hitItem builds a raw SearchHit Item map of the shape Pipedrive's
// /itemSearch returns. JSON numbers come back as float64 from the
// stdlib decoder, so we mirror that here.
func hitItem(score float64, fields map[string]any) pipedrive.SearchHit {
	return pipedrive.SearchHit{Score: score, Item: fields}
}

type searchHitRow struct {
	ID      int64          `json:"id"`
	Type    string         `json:"type"`
	Name    string         `json:"name"`
	Score   float64        `json:"score"`
	Details map[string]any `json:"details,omitempty"`
}

type searchOutputRow struct {
	Hits       []searchHitRow `json:"hits"`
	Truncated  bool           `json:"truncated"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

func TestSearch_HappyPath(t *testing.T) {
	fake := &fakeSearchClient{
		hits: []pipedrive.SearchHit{
			hitItem(1.5, map[string]any{"id": float64(47), "type": "organization", "name": "Acme Inc", "country": "DK"}),
			hitItem(1.2, map[string]any{"id": float64(11), "type": "deal", "title": "Acme renewal", "value": float64(75000), "currency": "DKK"}),
		},
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterSearch(s, fake)
	})
	defer h.Close()

	res, err := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"term": "Acme"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	var out searchOutputRow
	testutil.DecodeStructured(t, res.StructuredContent, &out)

	if len(out.Hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(out.Hits))
	}
	// Org: name comes from `name`.
	if out.Hits[0].ID != 47 || out.Hits[0].Type != "organization" || out.Hits[0].Name != "Acme Inc" {
		t.Errorf("hits[0] = %+v; want id=47 type=organization name=Acme Inc", out.Hits[0])
	}
	if out.Hits[0].Details["country"] != "DK" {
		t.Errorf("org country lost: %v", out.Hits[0].Details)
	}
	// Deal: name comes from `title`.
	if out.Hits[1].Type != "deal" || out.Hits[1].Name != "Acme renewal" {
		t.Errorf("hits[1] = %+v; want type=deal name=Acme renewal", out.Hits[1])
	}
	// Top-level fields stripped from Details.
	for _, leak := range []string{"id", "type", "name", "title"} {
		if _, ok := out.Hits[0].Details[leak]; ok {
			t.Errorf("Details still contains top-level field %q: %v", leak, out.Hits[0].Details)
		}
	}
	if out.Truncated {
		t.Errorf("Truncated = true; want false (no cursor)")
	}
	if fake.lastOpts.Term != "Acme" || fake.lastOpts.Limit != 25 {
		t.Errorf("client received opts %+v; want term=Acme limit=25", fake.lastOpts)
	}
}

func TestSearch_TruncatedFlagSetWhenCursor(t *testing.T) {
	fake := &fakeSearchClient{
		hits: []pipedrive.SearchHit{hitItem(1, map[string]any{"id": float64(1), "type": "deal", "title": "x"})},
		next: "page2",
	}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterSearch(s, fake)
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"term": "xx"},
	})
	var out searchOutputRow
	testutil.DecodeStructured(t, res.StructuredContent, &out)

	if !out.Truncated {
		t.Error("Truncated = false; want true (next_cursor non-empty)")
	}
	if out.NextCursor != "page2" {
		t.Errorf("NextCursor = %q, want page2", out.NextCursor)
	}
}

func TestSearch_NotTruncatedWhenLimitFullButCursorEmpty(t *testing.T) {
	// Page is exactly `limit` deep but Pipedrive returned an empty
	// next_cursor. That's the authoritative "no more results" signal —
	// don't false-positive Truncated=true.
	hits := make([]pipedrive.SearchHit, 3)
	for i := range hits {
		hits[i] = hitItem(1, map[string]any{"id": float64(i + 1), "type": "deal", "title": "x"})
	}
	fake := &fakeSearchClient{hits: hits, next: ""}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterSearch(s, fake)
	})
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"term": "xx", "limit": 3},
	})
	var out searchOutputRow
	testutil.DecodeStructured(t, res.StructuredContent, &out)
	if out.Truncated {
		t.Error("Truncated = true; want false (cursor is empty — Pipedrive says no more)")
	}
}

func TestSearch_RejectsShortTerm(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterSearch(s, &fakeSearchClient{})
	})
	defer h.Close()
	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"term": "a"},
	})
	if !res.IsError {
		t.Fatal("expected isError on 1-char term")
	}
	if !strings.HasPrefix(contentText(res), "[validation]") {
		t.Errorf("error text = %q; want [validation] prefix", contentText(res))
	}
}

func TestSearch_OneCharOKWithExactMatch(t *testing.T) {
	fake := &fakeSearchClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterSearch(s, fake)
	})
	defer h.Close()
	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"term": "a", "exact_match": true},
	})
	if res.IsError {
		t.Fatalf("expected success with exact_match: %+v", res.Content)
	}
	if fake.lastOpts.Term != "a" {
		t.Errorf("term = %q, want a (didn't reach client)", fake.lastOpts.Term)
	}
	if !fake.lastOpts.ExactMatch {
		t.Errorf("ExactMatch = false; want true (didn't reach client)")
	}
}

func TestSearch_RejectsBadType(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterSearch(s, &fakeSearchClient{})
	})
	defer h.Close()
	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"term": "xx", "types": []string{"campaign"}},
	})
	if !res.IsError {
		t.Fatal("expected isError on unknown type")
	}
	text := contentText(res)
	if !strings.Contains(text, "campaign") {
		t.Errorf("error text = %q; want it to mention 'campaign'", text)
	}
}

func TestSearch_LimitClampedToMax(t *testing.T) {
	fake := &fakeSearchClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterSearch(s, fake)
	})
	defer h.Close()
	_, _ = h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "search",
		Arguments: map[string]any{"term": "xx", "limit": 999},
	})
	if fake.lastOpts.Limit != 100 {
		t.Errorf("client received limit=%d; want 100 (clamped)", fake.lastOpts.Limit)
	}
}

func TestSearch_PassesThroughTypesAndCursor(t *testing.T) {
	fake := &fakeSearchClient{}
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterSearch(s, fake)
	})
	defer h.Close()
	_, _ = h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search",
		Arguments: map[string]any{
			"term":   "Acme",
			"types":  []string{"organization", "person"},
			"cursor": "tok",
		},
	})
	if len(fake.lastOpts.ItemTypes) != 2 || fake.lastOpts.ItemTypes[0] != "organization" {
		t.Errorf("ItemTypes = %v; want [organization person]", fake.lastOpts.ItemTypes)
	}
	if fake.lastOpts.Cursor != "tok" {
		t.Errorf("Cursor = %q; want tok", fake.lastOpts.Cursor)
	}
}

func TestSearch_RegistersInDumpRegistry(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterSearch(s, &fakeSearchClient{})
	})
	defer h.Close()
	var buf strings.Builder
	if err := tools.DumpJSON(&buf, "test"); err != nil {
		t.Fatalf("DumpJSON: %v", err)
	}
	if !strings.Contains(buf.String(), `"search"`) {
		t.Errorf("dump missing 'search'; got: %s", buf.String())
	}
}

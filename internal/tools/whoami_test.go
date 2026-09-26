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

type fakeWhoAmIClient struct {
	user  *pipedrive.User
	err   error
	calls int
}

func (f *fakeWhoAmIClient) WhoAmI(_ context.Context) (*pipedrive.User, error) {
	f.calls++
	return f.user, f.err
}

type userRow struct {
	UserID        int64  `json:"user_id"`
	Name          string `json:"name"`
	Email         string `json:"email"`
	CompanyID     int64  `json:"company_id"`
	CompanyName   string `json:"company_name"`
	CompanyDomain string `json:"company_domain"`
	TimezoneName  string `json:"timezone_name,omitempty"`
	IsAdmin       bool   `json:"is_admin"`
	Active        bool   `json:"active"`
}

func callWhoAmI(t *testing.T, fake *fakeWhoAmIClient) (*mcp.CallToolResult, userRow) {
	t.Helper()
	h := testutil.Connect(t, func(s *mcp.Server) { tools.RegisterWhoAmI(s, fake) })
	defer h.Close()

	res, _ := h.Client.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "whoami",
		Arguments: map[string]any{},
	})
	var out struct {
		User userRow `json:"user"`
	}
	if res != nil && !res.IsError && res.StructuredContent != nil {
		testutil.DecodeStructured(t, res.StructuredContent, &out)
	}
	return res, out.User
}

func TestWhoAmI_HappyPath(t *testing.T) {
	fake := &fakeWhoAmIClient{
		user: &pipedrive.User{
			ID: 13, Name: "A User", Email: "user@example.com",
			CompanyID: 99, CompanyName: "Acme Inc", CompanyDomain: "acme",
			TimezoneName: "Europe/Copenhagen", ActiveFlag: true, IsAdmin: 1,
		},
	}
	res, got := callWhoAmI(t, fake)
	if res.IsError {
		t.Fatalf("unexpected isError: %+v", res.Content)
	}
	if got.UserID != 13 || got.CompanyDomain != "acme" {
		t.Errorf("user = %+v; want user_id=13 domain=acme", got)
	}
	if got.TimezoneName != "Europe/Copenhagen" {
		t.Errorf("timezone = %q; activities are scheduled in it, so it must survive", got.TimezoneName)
	}
	if fake.calls != 1 {
		t.Errorf("upstream calls = %d; want 1", fake.calls)
	}
}

func TestWhoAmI_IsAdminIsAnIntUpstream(t *testing.T) {
	// v1 reports is_admin as 0/1 rather than a JSON boolean; the
	// LLM-facing shadow normalizes it so the LLM never sees the int.
	for _, tc := range []struct {
		raw  int
		want bool
	}{{0, false}, {1, true}} {
		fake := &fakeWhoAmIClient{user: &pipedrive.User{ID: 1, IsAdmin: tc.raw, ActiveFlag: true}}
		_, got := callWhoAmI(t, fake)
		if got.IsAdmin != tc.want {
			t.Errorf("is_admin %d became %v; want %v", tc.raw, got.IsAdmin, tc.want)
		}
	}
}

func TestWhoAmI_UpstreamErrorSurfaces(t *testing.T) {
	fake := &fakeWhoAmIClient{err: pipedrive.ErrUnauthorized}
	res, _ := callWhoAmI(t, fake)
	if !res.IsError {
		t.Fatal("expected isError")
	}
	if !strings.HasPrefix(contentText(res), "[auth]") {
		t.Errorf("error = %q; want [auth] prefix", contentText(res))
	}
}

func TestWhoAmI_RegistersInDumpRegistry(t *testing.T) {
	h := testutil.Connect(t, func(s *mcp.Server) { tools.RegisterWhoAmI(s, &fakeWhoAmIClient{}) })
	defer h.Close()

	var buf strings.Builder
	if err := tools.DumpJSON(&buf, "test"); err != nil {
		t.Fatalf("DumpJSON: %v", err)
	}
	if !strings.Contains(buf.String(), `"whoami"`) {
		t.Error("dump missing whoami")
	}
}

package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
)

type whoamiClient interface {
	WhoAmI(ctx context.Context) (*pipedrive.User, error)
}

// userSummary is the LLM-facing shape of the authenticated account.
// Parallel shadow of pipedrive.User per CLAUDE.md — jsonschema tags
// scoped here, never on the internal/pipedrive type.
//
// The token itself is never surfaced, and neither is anything that
// would identify it.
type userSummary struct {
	UserID        int64  `json:"user_id" jsonschema:"the authenticated user's numeric id. This is the owner_id a record gets when you create one without naming an owner."`
	Name          string `json:"name" jsonschema:"the user's display name"`
	Email         string `json:"email" jsonschema:"the user's email address"`
	CompanyID     int64  `json:"company_id" jsonschema:"the workspace's numeric id"`
	CompanyName   string `json:"company_name" jsonschema:"the workspace's display name"`
	CompanyDomain string `json:"company_domain" jsonschema:"the workspace subdomain, e.g. acme for acme.pipedrive.com. Every URL this server returns is built from it."`
	Locale        string `json:"locale,omitempty" jsonschema:"the user's locale, e.g. en_US"`
	TimezoneName  string `json:"timezone_name,omitempty" jsonschema:"the user's IANA timezone, e.g. Europe/Copenhagen. Due dates and times on activities are in this zone."`
	IsAdmin       bool   `json:"is_admin" jsonschema:"true when the account is a workspace admin; a non-admin may be refused writes this server would otherwise allow"`
	Active        bool   `json:"active" jsonschema:"whether the account is active"`
}

type whoamiInput struct{}

type whoamiOutput struct {
	User userSummary `json:"user" jsonschema:"the account this server's API token authenticates as"`
}

// RegisterWhoAmI wires the whoami tool into the MCP server.
func RegisterWhoAmI(s *mcp.Server, c whoamiClient) {
	readOnly := mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}

	AddTool(s, &mcp.Tool{
		Name:        "whoami",
		Description: "WHICH ACCOUNT this server is acting as, and WHICH WORKSPACE it is pointed at. Call it before you attribute anything to \"me\" or \"my deals\": the answer's user_id is the owner_id a record gets when you create one without naming an owner, so it is what turns \"show me my open deals\" into list_deals(owner_id=...). It also reports the user's timezone, which is the zone an activity's due_date and due_time are written in — getting that wrong schedules a meeting on the wrong day. Cheap: one call, and the answer does not change during a session. Takes no arguments, because a token authenticates as exactly one user. It cannot tell you about anyone else: Pipedrive v2 exposes no users resource, so there is no tool here that lists colleagues or resolves an owner_id back to a name.",
		Annotations: &readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ whoamiInput) (*mcp.CallToolResult, whoamiOutput, error) {
		u, err := c.WhoAmI(ctx)
		if err != nil {
			return errorResult(err), whoamiOutput{}, nil
		}
		return nil, whoamiOutput{User: summarizeUser(u)}, nil
	})
}

func summarizeUser(u *pipedrive.User) userSummary {
	return userSummary{
		UserID:        u.ID,
		Name:          u.Name,
		Email:         u.Email,
		CompanyID:     u.CompanyID,
		CompanyName:   u.CompanyName,
		CompanyDomain: u.CompanyDomain,
		Locale:        u.Locale,
		TimezoneName:  u.TimezoneName,
		// v1 reports is_admin as 0/1 rather than a JSON boolean.
		IsAdmin: u.IsAdmin != 0,
		Active:  u.ActiveFlag,
	}
}

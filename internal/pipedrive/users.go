package pipedrive

import "context"

// User is the authenticated account behind the API token, as
// /api/v1/users/me reports it. Subset: enough to answer "who am I and
// which workspace am I in", which is what the tool needs to stop an
// LLM guessing at ownership.
type User struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Email         string `json:"email"`
	CompanyID     int64  `json:"company_id"`
	CompanyName   string `json:"company_name"`
	CompanyDomain string `json:"company_domain"`
	Locale        string `json:"locale,omitempty"`
	TimezoneName  string `json:"timezone_name,omitempty"`
	Created       string `json:"created,omitempty"`
	ActiveFlag    bool   `json:"active_flag"`
	IsAdmin       int    `json:"is_admin,omitempty"`
}

// WhoAmI reports the user the API token authenticates as.
//
// SECOND v1 CARVE-OUT, alongside notes. Pipedrive v2 exposes no users
// resource at all: /api/v2/users does not exist, which is also why the
// startup auth probe uses /api/v2/dealFields rather than a users call.
// v1 /users/me is the only endpoint that answers this, so a whoami tool
// either lives here or does not exist. Per CLAUDE.md hard rule 1 this
// carve-out carries a CHANGELOG entry under ### Changed and an explicit
// user go-ahead, recorded there.
//
// Note the shape difference from v2: v1 wraps single records in the
// same {success, data} envelope, so itemEnvelope still applies.
func (c *Client) WhoAmI(ctx context.Context) (*User, error) {
	var resp itemEnvelope[User]
	if err := c.doV1(ctx, "/users/me", &resp); err != nil {
		return nil, err
	}
	u := resp.Data
	return &u, nil
}

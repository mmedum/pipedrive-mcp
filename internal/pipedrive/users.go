package pipedrive

import (
	"context"
)

// User is the authenticated account behind the API token, as
// /api/v1/users/me reports it: the user, with the company's id, name and
// domain, timezone_name and locale. Locale is the date and number
// format, not the language, as Pipedrive's reference notes. Checked by
// the integration test TestWhoAmI_ReachesTheV1Carveout.
// https://developers.pipedrive.com/docs/api/v1/Users#getCurrentUser
type User struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Email         string `json:"email"`
	CompanyID     int64  `json:"company_id"`
	CompanyName   string `json:"company_name"`
	CompanyDomain string `json:"company_domain"`
	Locale        string `json:"locale,omitempty"`
	TimezoneName  string `json:"timezone_name,omitempty"`
	// DefaultCurrency is the currency a deal created without one gets.
	DefaultCurrency string `json:"default_currency,omitempty"`
	Created         string `json:"created,omitempty"`
	ActiveFlag      bool   `json:"active_flag"`
	IsAdmin         int    `json:"is_admin,omitempty"`
}

// WhoAmI reports the user the API token authenticates as.
//
// SECOND v1 CARVE-OUT, alongside notes. Pipedrive's published v2
// description has no /users and no /notes, and nothing here watches for
// either to appear. v1 /users/me is the only endpoint that answers this,
// so a whoami tool either lives here or does not exist.
// https://developers.pipedrive.com/docs/api/v1/openapi-v2.yaml
//
// v1 wraps a single record in the same {success, data} envelope as v2,
// so itemEnvelope applies.
//
// MEMOIZED for the process lifetime, behind meMu. A token authenticates
// as exactly one user, and the whoami tool's own description tells the
// model the answer does not change during a session, which invites a
// call every turn; each would otherwise be a full round trip for a value
// that cannot move. The trade: a token rotation or a rename mid-process
// goes unseen, and unlike the field caches there is no refresh tool.
// That is acceptable for a per-workspace stdio server whose token is
// fixed at startup, and a restart is the escape hatch.
//
// The first answer's error is memoized too, so a failing probe does not
// turn into a retry on every subsequent call. A call its own caller
// canceled is not an answer: it says nothing about the account, so the
// next call asks again.
func (c *Client) WhoAmI(ctx context.Context) (*User, error) {
	c.meMu.Lock()
	defer c.meMu.Unlock()
	if c.meDone {
		return c.me, c.meErr
	}
	var resp itemEnvelope[User]
	if err := c.doV1(ctx, "/users/me", &resp); err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		c.meErr, c.meDone = err, true
		return nil, err
	}
	u := resp.Data
	c.me, c.meDone = &u, true
	return c.me, nil
}

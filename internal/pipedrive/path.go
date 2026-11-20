package pipedrive

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// buildPath joins an API path with optional query values. Use this
// instead of fmt.Sprintf when the endpoint has more than one optional
// filter, so query construction stays consistent (escaping, ordering)
// across resource clients.
//
// Example:
//
//	buildPath("/stages", url.Values{"pipeline_id": {"5"}})  →  "/stages?pipeline_id=5"
//	buildPath("/stages", nil)                                →  "/stages"
func buildPath(base string, q url.Values) string {
	if len(q) == 0 {
		return base
	}
	return base + "?" + q.Encode()
}

// setLimitCursor adds the standard pagination params to q when set.
// Both ListDeals and ItemSearch share this idiom; the helper keeps
// the cursor-pagination shape uniform across resource clients.
func setLimitCursor(q url.Values, limit int, cursor string) {
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
}

// joinInt64s renders ids for Pipedrive's comma-separated `ids`
// parameter. url.Values escapes the commas; the API accepts that.
func joinInt64s(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

// maxListPages bounds readAll, so a cursor that never ends cannot keep a
// call going: 20 pages of 500 is far more pipelines, stages or fields
// than a workspace holds.
const maxListPages = 20

// readAll reads every page of a v2 list. It serves the definitions this
// server reads whole, pipelines, stages and the three field lists,
// because a field the cache never saw is neither resolved by name nor
// writable by name. Each takes `limit` up to 500 and `cursor`, per
// Pipedrive's v2 description.
// https://developers.pipedrive.com/docs/api/v1/DealFields#getDealFields
func readAll[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	if q == nil {
		q = url.Values{}
	}
	q.Set("limit", "500")
	var out []T
	for range maxListPages {
		var resp listEnvelope[T]
		if err := c.do(ctx, buildPath(path, q), &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Data...)
		if resp.AdditionalData.NextCursor == "" {
			return out, nil
		}
		q.Set("cursor", resp.AdditionalData.NextCursor)
	}
	return nil, fmt.Errorf("pipedrive: %s runs past %d pages", path, maxListPages)
}

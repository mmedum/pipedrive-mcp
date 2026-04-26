package pipedrive

import (
	"net/url"
	"strconv"
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

package pipedrive

import "net/url"

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

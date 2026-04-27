package tools

// effectiveSort encodes the recency-first override of Pipedrive's
// upstream `id asc` default — rarely what a human asking
// "what's happened with X lately" wants. The two halves are coupled
// (a blank sortBy means "user accepted the default", which also
// flips the direction), so we return them as a pair.
//
// Shared by list_activities, list_notes, list_persons, and
// list_organizations (architecture.md references this filename).
func effectiveSort(sortBy, sortDir string) (by, dir string) {
	switch {
	case sortBy == "" && sortDir == "":
		return "update_time", "desc"
	case sortBy == "":
		return "update_time", sortDir
	case sortDir == "":
		return sortBy, "asc"
	default:
		return sortBy, sortDir
	}
}

// commonV2TimestampSortFields enumerates the sort_by values Pipedrive v2
// accepts on every resource that supports the standard timestamp axis
// (/persons, /organizations, /deals, /activities, ...). Resources that
// support extra fields embed this set and add their own (e.g. activities
// adds `due_date`).
var commonV2TimestampSortFields = map[string]bool{
	"id":          true,
	"update_time": true,
	"add_time":    true,
}

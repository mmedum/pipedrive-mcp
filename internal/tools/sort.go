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

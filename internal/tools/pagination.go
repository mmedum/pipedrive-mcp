package tools

// Cursor-paginated list_X tools share a single page-size policy.
// Putting it here (rather than re-declaring `(25, 100)` in every
// resource file) prevents the caps from drifting apart silently —
// nothing else enforced that `list_deals` and `list_activities`
// matched.
const (
	defaultListLimit = 25
	maxListLimit     = 100
)

// clampLimit returns in clamped to [1, maxListLimit], with 0/negative
// defaulting to defaultListLimit.
func clampLimit(in int) int {
	if in <= 0 {
		return defaultListLimit
	}
	if in > maxListLimit {
		return maxListLimit
	}
	return in
}

// allowedSortDirections is the closed enum every list_X tool accepts
// for `sort_direction`. Pipedrive v2 list endpoints uniformly accept
// only asc | desc, so the same map is reused across resources rather
// than declared per-tool. (Per-resource sort_by enums vary — those
// stay local to each tool file.)
var allowedSortDirections = map[string]bool{
	"asc":  true,
	"desc": true,
}

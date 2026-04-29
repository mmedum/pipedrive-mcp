package tools

// RegisterOptions bundles the server-wide flags every Register
// function for a write-bearing resource takes. Bundling stops a
// future caller from swapping `DryRun` and `EnableDestructive`
// positionally — both type-check, both look fine in code review.
//
// Both fields gate *registration*: when EnableDestructive is false,
// destructive tools (currently only `delete_note`) don't register at
// all. Per CLAUDE.md hard rule #3, that's server-build-time gating,
// not annotation-based — flipping it requires a server restart.
type RegisterOptions struct {
	DryRun            bool // mirrors PIPEDRIVE_DRY_RUN
	EnableDestructive bool // mirrors PIPEDRIVE_ENABLE_DESTRUCTIVE
}

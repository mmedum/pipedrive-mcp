package tools

// RegisterOptions carries the server-wide settings a Register function
// needs.
//
// It stays a struct with one field on purpose: a named field reads
// better than a bare bool where a Register function takes it, and it
// keeps two positional bools from being swapped once a second setting
// lands.
//
// DryRun mirrors PIPEDRIVE_DRY_RUN and is a floor, not a default: a
// handler ORs it with the caller's per-call dry_run, so a call can turn
// a rehearsal on and nothing on the wire can turn one off (D-33). An
// operator sets it to make speculative LLM work safe, and a tool that
// honored only the per-call input would write anyway.
type RegisterOptions struct {
	DryRun bool // mirrors PIPEDRIVE_DRY_RUN
}

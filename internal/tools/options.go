package tools

// RegisterOptions carries the server-wide settings a Register function
// needs.
//
// DryRun mirrors PIPEDRIVE_DRY_RUN and is a floor, not a default: a
// handler ORs it with the caller's per-call dry_run, so a call can turn
// a rehearsal on and nothing on the wire can turn one off. docs/security.md
// tells an operator that setting the env var makes speculative LLM work
// safe; a tool that honoured only the per-call input would make that
// false, and the tool that can delete is exactly the one that would.
type RegisterOptions struct {
	DryRun bool // mirrors PIPEDRIVE_DRY_RUN
}

# Pipedrive API v1 carve-outs

This server commits to Pipedrive API v2 wherever possible. v1 sunsets
**2026-07-31** per Pipedrive's
[deprecation announcement](https://developers.pipedrive.com/changelog/post/deprecation-of-selected-api-v1-endpoints).
Some resources we want to support in v1.0 do not yet have v2 equivalents,
so we carve them out explicitly here. Each carve-out has a tracking link
and a migration plan.

## Active carve-outs

### Notes

- **Status**: planned for Phase 2.
- **v1 endpoints we will use**:
  - `GET /api/v1/notes` (filterable by `deal_id`, `person_id`, `org_id`)
  - `POST /api/v1/notes`
- **Why v1**: as of 2026-04-26, `/api/v2/notes` does not exist.
- **Why we still ship them**: the user's sales workflow uses the Notes
  panel as a load-bearing capture surface (call summaries, meeting
  recaps, free-form context attached to deals/persons/orgs). Removing
  notes from the tool surface would meaningfully degrade the LLM's
  ability to read or capture this context.
- **Tracking**: monitor the
  [Pipedrive changelog](https://developers.pipedrive.com/changelog) for a
  v2 announcement. Subscribe via RSS.
- **Migration plan**: when v2 ships, the migration is contained to
  `internal/pipedrive/notes.go`. The tool surface (input/output schemas
  for `get_*_notes` and `add_note_to_*`) does not change, so it's a
  PATCH-level release per semver. Add a CHANGELOG entry under `Changed`.
- **Sunset risk**: Pipedrive has not signaled that the notes v1
  endpoints will go down on 2026-07-31 specifically — the announced
  sunset list is for "selected v1 endpoints", and notes are not in the
  initial cull. We track this monthly. If notes-v1 enters the sunset
  list before notes-v2 ships, we file an issue and assess whether to
  drop the notes tools temporarily or request an extension from
  Pipedrive.

## Resolved (formerly carved out)

(none yet)

## Considered and rejected

### `/users/me`

`GET /api/v1/users/me` exists; **neither `GET /api/v2/users/me` nor
`GET /api/v2/users` exists** — the entire users surface is v1-only.
Rather than carve users out as a v1 dependency for a tool the user
described as not load-bearing ("more or less just for checking what
tool is calling and what version"), we dropped the `whoami` tool
entirely and use `GET /api/v2/dealFields?limit=1` as the startup auth
probe (every workspace has at least one deal field; the response is
small; `dealFields` is confirmed present on v2). The probe response is
discarded; the LLM never sees it.

### Leads

`/api/v2/leads` does not exist as full CRUD; v2 has `search`, `convert`,
and `convert/status` only. **Leads are out of scope for v1.0** rather
than carved out as a v1 dependency. They will be added when Pipedrive
ships v2 lead CRUD. Watch the
[Pipedrive changelog](https://developers.pipedrive.com/changelog).

### Files

`/api/v2/files` does not exist. **Files are out of scope for v1.0**
rather than carved out. Watch the
[devcommunity tracking thread](https://devcommunity.pipedrive.com/t/api-v1-to-v2-files-is-missing/19931).

## Process

When Pipedrive ships a v2 endpoint we currently carve out:

1. Verify the v2 endpoint matches our usage by reading the docs and
   testing in the sandbox.
2. Open a PR that switches the relevant `internal/pipedrive/<resource>.go`
   file from v1 to v2.
3. Update the typed structs to match v2's response shape (especially
   custom-field nesting).
4. Move the entry from "Active" to "Resolved" in this file with the
   release tag where the migration shipped.
5. Add a `Changed` entry in `CHANGELOG.md` under `[Unreleased]`.

When Pipedrive announces the sunset of an endpoint we depend on:

1. Open a tracking issue.
2. If a v2 equivalent exists, prioritize the migration before the sunset
   date.
3. If no v2 equivalent exists by sunset date minus 30 days, escalate:
   either remove the affected tools (with deprecation notice) or, if
   user impact is severe, request a sunset extension from Pipedrive.

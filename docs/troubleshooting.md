# Troubleshooting

What to do when something does not work, then the maintenance tasks.
Setting up in the first place is the README's
[Set up Pipedrive](../README.md#set-up-pipedrive). Start with this
command:

```bash
pipedrive-mcp status
```

It reports the workspace it resolved and where the domain came from,
where the token came from, and whether Pipedrive accepts it (D-68). It
exits non-zero when any of the three fails, and never prints the token.
`--no-probe` skips the call to Pipedrive, and `--json` prints one object
for a script
([configuration](configuration.md#reading-the-setup-from-a-script)). Run
it with the environment your MCP client gives the server, since it
checks what it is given.

## The client says the server failed to connect

**Cause:** the server exits non-zero at startup when a setting is bad,
when no domain or token resolves, or when Pipedrive rejects the token
(D-06, D-67). A client shows that as a failed connection. The reason is
on stderr, in the client's log for the server.

**Fix:** run `pipedrive-mcp status`, then find the message below.

## `no domain configured: set PIPEDRIVE_COMPANY_DOMAIN, or run `pipedrive-mcp login` to record one`

**Cause:** `PIPEDRIVE_COMPANY_DOMAIN` is unset, and no default domain is
recorded in the pointer file `pipedrive-mcp login` writes (D-64).

**Fix:** run `pipedrive-mcp login`, which records the domain, or set the
variable in the server's environment.

## `no API token found. Run `pipedrive-mcp login` to store one in the OS keyring, or set PIPEDRIVE_API_TOKEN in the environment.`

**Cause:** `PIPEDRIVE_API_TOKEN` is unset, and the keyring holds no token
for the resolved domain. A token is stored per domain, so one stored for
another workspace does not count (D-63).

**Fix:** run `pipedrive-mcp login` for that domain, or set the variable.

## `credentials: keyring unavailable and PIPEDRIVE_API_TOKEN is not set: …`

**Cause:** the keyring could not be read: no Secret Service on Linux, a
headless session before the desktop keyring is unlocked, or a container
without a keyring.

**Fix:** unlock or install a keyring, or set `PIPEDRIVE_API_TOKEN`. The
variable wins over the keyring whenever it is set.

## `auth probe failed: 401 — token rejected. Run `pipedrive-mcp login` again.`

**Cause:** Pipedrive rejected the token on the startup probe, a read of
one deal field (D-06). The token was revoked or replaced, or it belongs
to another workspace.

**Fix:** run `pipedrive-mcp login` again, or update
`PIPEDRIVE_API_TOKEN`, then restart the client.

## `auth probe failed: …`

**Cause:** the probe failed for another reason. `HTTP <status> — <message>`
is Pipedrive's answer. Anything else is the network: a DNS failure, a
refused connection or a timeout reaching `<domain>.pipedrive.com`.

**Fix:** check the domain with `pipedrive-mcp status`, and that this
machine reaches Pipedrive over HTTPS.

## `"…" is not a valid Pipedrive subdomain`

**Cause:** the domain is not a Pipedrive subdomain: letters, digits and
hyphens, with no hyphen at either end (D-65). The message starts with
`config: PIPEDRIVE_COMPANY_DOMAIN:` when the variable is wrong, and with
`userconfig <path>: default_domain:` when the recorded default is.

**Fix:** give the subdomain alone, such as `acme` for
`acme.pipedrive.com`, with no scheme or path.

## `config: …`

**Cause:** a setting has a value the server cannot read, such as
`LOG_LEVEL` outside `debug|info|warn|error` or a
`PIPEDRIVE_HTTP_TIMEOUT` that is not a positive duration. The server
refuses to start rather than guess (D-67).

**Fix:** correct or unset the variable it names.
[`configuration.md`](configuration.md#variables) lists the values each
one takes.

## `pipedrive-mcp: unknown command "…"`

**Cause:** a mistyped subcommand. The server exits non-zero rather than
starting (D-69).

**Fix:** use `login`, `logout` or `status`, or no command to serve.

## `[auth]`

**Cause:** Pipedrive answered 401 to a tool call. The token was revoked
or replaced while the server ran, and the server reads the token once,
at startup.

**Fix:** store the new token as [Rotating the token](#rotating-the-token)
says, and restart the client.

## `[permission]` and `[business_rule]`

**Cause:** Pipedrive answered 403. The server calls it `[business_rule]`
when the message mentions a rule, such as a locked record or a stage
setting, and `[permission]` otherwise (D-52). The word list in
`internal/pipedrive/errors.go` is unverified against real bodies of both
kinds, so a 403 can land in the wrong class.

**Fix:** read the message, which is Pipedrive's own. A permission
failure needs the token's user to have access to the record. A business
rule needs the record or the stage changed in Pipedrive.

## `[not_found]`

**Cause:** no record has that id, or the token's user cannot see it.
For a pipeline, `list_stages` says `pipeline N does not exist or is not
visible to the API token's user`.

**Fix:** find the id again with `search` or a `list_` tool.

## `[gone]`

On a notes tool or `whoami`, the message ends `this endpoint is on
Pipedrive API v1 and has no v2 equivalent — changing the request will
not help`.

**Cause:** Pipedrive answered 410: it has retired the endpoint (D-05).
`get_note`, `list_notes`, `manage_note` and `whoami` run on API v1,
because v2 has no notes and no users (D-03).

**Impact:** those tools stop working, and nothing in this server can
fall back.

**Workaround:** read and write notes in Pipedrive's own app.

**Resolution:** update to the release that removes or replaces them.
They sit outside the compatibility promise, so that is a minor release
(D-04).

## `[rate_limited]`

**Cause:** Pipedrive answered 429 on every attempt. The client tries a
call three times in all, waiting longer each time and at least as long
as a `Retry-After` asks (D-54). Pipedrive's
[rate-limiting page](https://pipedrive.readme.io/docs/core-api-concepts-rate-limiting)
gives the limits.

**Workaround:** wait, then retry.

**Resolution:** spend fewer calls. Run fewer sessions on one token,
filter a `list_` tool rather than paging through everything, and pass
`types` to `search` when you know what you want.

## `[server_error]`

**Cause:** Pipedrive answered 5xx. A read is retried, and a write is not,
because Pipedrive may have committed it before the error (D-54).

**Impact:** a write that ends here may have landed. Repeating it can
apply it twice.

**Fix:** read the record, and repeat the write only if it did not land.
A 5xx that persists is an outage at Pipedrive.

## `…; the write may have been applied, so read the record before trying again`

**Cause:** the connection was lost after a write went out. The client
retries a write only when it never left this machine (D-54).

**Impact:** repeating the call can create a second record or apply the
change twice.

**Fix:** read the record, and repeat the write only if it did not land.

## `[validation]`

**Cause:** Pipedrive answered 400, or the server refused the input
before calling: an id that is not positive, or a value outside an enum.
The message names the field and, for an enum, every allowed value.

**Fix:** correct the input the message names.

## `[refused] … already has … set, and this write would replace what is there. Pass overwrite: […] to allow it`

**Cause:** an update would replace fields that hold a value, and
`overwrite` did not name them all (D-27, D-30). The refusal names every
one. A guard that stops a write returns `[refused]` before any call to
Pipedrive, so nothing changed.

**Impact:** Pipedrive has no undo. A replaced value is gone.

**Fix:** pass `overwrite` with those names only when the person decided
to replace them. To fill the empty fields alone, leave the others out of
the call.

## `[refused] … changed since you read it (expect_version …)`

**Cause:** the record's `update_time` moved since the read that informed
the write (D-31).

**Fix:** read the record again, check the change still makes sense, and
retry with the new `update_time`.

## `[refused] deal N is archived, and Pipedrive accepts no edit to an archived deal. Pass action: "unarchive" to allow it`

**Cause:** the deal is archived, and Pipedrive refuses every edit to it
but unarchiving (D-45).

**Fix:** unarchive it first, if the person wants it edited. `list_deals`
shows archived deals only when passed `archived`.

## `the search matched, but checking whether its …s still exist failed; …`

**Cause:** `search` checks organization and person hits against
Pipedrive, because its index keeps deleted ones (D-11). That check
failed, so the search fails rather than return records that may be gone
(D-14). The class is the failed check's, often `[rate_limited]`.

**Fix:** retry later, or pass `types` without `organization` and
`person` to skip the check.

## A list page is short or empty, and `next_cursor` is set

**Cause:** one call is one page, and `search` drops deleted hits after
Pipedrive has paged (D-15).

**Fix:** keep paging while `next_cursor` is set. An empty page is not
the end.

## `this workspace has no custom field named "…"; …`

**Cause:** the server reads field names once, at startup, and someone
added or renamed the field in Pipedrive since (D-19).

**Fix:** call `refresh_field_cache`, then retry. Field values are always
read live; only names and dropdown labels are cached.

## `the workspace's custom-field definitions could not be read, …`

**Cause:** the field definitions failed to load, so a name cannot be
checked. The write is refused rather than sent unchecked (D-25).

**Fix:** call `refresh_field_cache`. Its result says, per resource,
which load failed and why.

## `a custom field cannot be cleared, so "…" cannot be set to null; …`

**Cause:** no tool clears a field (D-42). Pipedrive v2 rejects a null
and stores an empty string as a value.

**Workaround:** clear the field in Pipedrive's own app.

## Tasks

### Rotating the token

Create a new token in Pipedrive, under Settings → Personal preferences
→ API, then store it:

```bash
pipedrive-mcp login
```

This replaces the keyring entry for that domain. With the variable,
update `PIPEDRIVE_API_TOKEN` instead. Restart the client, since the
server reads the token at startup. Then revoke the old token in
Pipedrive.

### Revoking access

```bash
pipedrive-mcp logout
```

This deletes the keyring entry, and clears the recorded default domain
when it names that workspace. Pass `--domain` for another workspace. It
cannot revoke the token at Pipedrive, so do that there too. It does not
touch `PIPEDRIVE_API_TOKEN` in an environment.

### Suspected exposure

If a token may have leaked, such as in a shell history, a log or a
shared config:

1. Revoke the token in Pipedrive.
2. Create a new one, and run `pipedrive-mcp login` on each machine that
   should keep working.
3. Run `pipedrive-mcp logout` on any machine that should not.

**Impact:** until step 1, anyone holding the token acts in Pipedrive as
its user, with every permission that user has.

### Pointing a client at another workspace

Run `pipedrive-mcp login --domain <subdomain>` for it, then set
`PIPEDRIVE_COMPANY_DOMAIN` in that client's server entry. The variable
wins over the recorded default.

### Restoring a deleted record

Nothing in this server restores a record (D-47). Pipedrive keeps a
deleted deal, person, organization or activity for 30 days before it
removes it for good, and its own app can restore one within that window.
A deleted note stays readable through `get_note`.

### Reporting a problem

Open an [issue](https://github.com/mmedum/pipedrive-mcp/issues) with:

- the output of `pipedrive-mcp status` and `pipedrive-mcp --version`;
- how you installed it and which client runs it;
- the tool you called, what you expected, and the `[class] message` you
  got;
- a debug log from `LOG_LEVEL=debug` if it helps. A line carries the
  method, the path, the status and the duration (D-59). A path holds
  record ids, so replace them.

Never paste a tool result, record content, a company domain or a token.
Describe a record by its shape, such as "a deal with a dropdown field".
The server keeps no record of tool calls (D-60), so your client's
transcript is where a call and the prompt behind it sit together. A
suspected vulnerability goes through [`SECURITY.md`](../SECURITY.md),
privately.

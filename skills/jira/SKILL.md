---
name: jira
description: Query and update Jira Data Center through the `jira` CLI. Use when the user asks about Jira issues, JQL searches, projects, boards, sprints, users, comments, worklogs or transitions on a self-hosted Jira (Server/Data Center). Commands are generated from the instance's OpenAPI specification, so discover them instead of assuming them.
---

# Jira Data Center via the `jira` CLI

The `jiracli` binary is the execution interface. Every API command is generated
from an OpenAPI specification, so the available commands depend on the
compiled-in profile and the selected instance. **Discover, don't guess.**

All output is JSON by default (stdout). Errors are JSON on stderr with a
`layer`, `code`, `message`, optional `http_status` and `hint`, plus a
non-zero exit code (see "Exit codes").

## Operating rules (mandatory)

1. **Read before write.** Inspect the current state (`issue get`, `issue get-transitions`, `issue get-edit-meta`) before changing anything.
2. **Never invent identifiers.** Issue keys, project keys, transition IDs, user names, field IDs and board/sprint IDs must come from a previous command's output or from the user.
3. **Never expose credentials.** Never print, echo, log or pass tokens on the command line. Never read credential files. `jiracli auth status` shows *where* a credential comes from, never its value.
4. **Dry-run writes first.** Run any write with `--dry-run`, show the user the request, then run it for real.
5. **Destructive and administrative operations need explicit user authorization.** They fail with exit code 9 (`confirmation_required`) unless `--yes` is passed. Only add `--yes` after the user explicitly approved that exact operation on that exact target.
6. **Verify writes.** A write succeeded only if the command exited 0; then re-read the object (`issue get KEY`) to confirm the change.
7. **Do not blindly retry writes.** The CLI already retries safe requests. If a POST/PUT/DELETE fails with a network/timeout error (exit 8), re-read the state before trying again: it may have been applied.
8. **Respect permissions.** Exit 5 (HTTP 403) means the account lacks the Jira permission. Do not look for workarounds.
9. **Handle pagination.** Collections are paged. Use `--all` (bounded by `--max-pages`) or page explicitly with `--start-at`/`--max-results`. Never claim "there are N results" from a single page; use `total` when present.
10. **Respect read-only mode.** If `jiracli capabilities` shows `"read_only": true`, do not attempt writes; tell the user.

## 1. Identify the CLI and the instance

```bash
jiracli --version
jiracli capabilities            # binary, profiles, active instance, read_only, exit codes
jiracli config list             # configured instances (no secrets)
jiracli --instance NAME ...     # select another instance for one command
```

## 2. Check connectivity and authentication

```bash
jiracli auth status             # credential present? source? (no network)
jiracli auth test               # GET /rest/api/2/myself with the credential
jiracli doctor                  # layered: config, DNS, TCP, TLS, HTTP, credentials, auth
```

Or run `scripts/check-connection.sh` (prints a one-line verdict + JSON).
If authentication fails, stop and tell the user what `hint` says; do not try
to log in on their behalf. See `references/authentication.md`.

## 3. Discover commands

```bash
jiracli commands list --output json                   # all commands (command, method, path, risk, flags, args)
jiracli commands list --group issue                   # one group
jiracli commands list --risk read                     # only read-only commands
jiracli commands describe issue search                # full detail incl. body schema and provenance
jiracli issue search --help                           # human help
```

Each command reports `risk` (`read`, `write`, `destructive`, `administrative`)
and `confirmation_required`. See `references/commands.md` for frequently used
commands.

## 4. Search with JQL

```bash
jiracli issue search --jql 'project = ABC AND statusCategory != Done ORDER BY updated DESC' \
  --fields key,summary,status,assignee --max-results 50
jiracli issue search --jql '...' --all --max-pages 20      # merged pages
jiracli issue search-post --stdin <<< '{"jql":"project = ABC","maxResults":100,"fields":["summary"]}'
```

Quote JQL in single quotes. Validate a query cheaply with `--max-results 0`
(returns `total` only). See `references/jql.md`.

## 5. Read issues

```bash
jiracli issue get ABC-123 --fields summary,status,assignee,description
jiracli issue get ABC-123 --expand renderedFields,names,transitions,changelog
jiracli issue get-comments ABC-123
jiracli issue get-transitions ABC-123
jiracli issue get-edit-meta ABC-123          # which fields can be edited and how
```

## 6. Create and update issues

```bash
jiracli issue get-create-meta-project-issue-types ABC     # valid issue types
jiracli issue get-create-meta-fields ABC 10001            # fields for that type
jiracli issue create --file issue.json --dry-run          # preview
jiracli issue create --file issue.json                    # returns {"id","key","self"}
jiracli issue edit ABC-123 --stdin <<< '{"fields":{"summary":"New summary"}}'
jiracli issue add-comment ABC-123 --stdin <<< '{"body":"Comment text"}'
jiracli issue do-transition ABC-123 --stdin <<< '{"transition":{"id":"31"}}'   # id from get-transitions
jiracli issue assign ABC-123 --stdin <<< '{"name":"jdoe"}'
```

Bodies come from `--file` or `--stdin` (JSON). `commands describe` lists the
top-level properties and which are required. Successful writes that return
no body print `{"ok": true, "status": 204}`.

## 7. Projects, users, boards

```bash
jiracli project get-all                          # or: jiracli projects search-for
jiracli project get ABC
jiracli user find --username jdoe                # Data Center user search
jiracli myself get
jiracli board get-all --all
jiracli board get-all-sprints 42
jiracli sprint get-issues-for 7 --jql 'status = Open'
```

## 8. Pagination

Paged commands have `"paginated": true` in `commands list` and accept
`--all`/`--max-pages`. The merged result keeps every field of the first page,
replaces the item array with all items and sets `isLast`/`maxResults`
accordingly. A warning on stderr means `--max-pages` stopped early. See
`references/pagination.md`.

## 9. Interpreting output

- stdout: the Jira response, unmodified (JSON default; `-o yaml`, `-o table`).
- stderr: logs (`-v` for debug) and structured errors.
- `--dry-run`: `{"dry_run": true, "request": {method, url, headers, body}}`, nothing is sent and no credential is read.

## Exit codes

| Code | Meaning | What to do |
|---|---|---|
| 0 | success | verify writes by re-reading |
| 2 | usage error (bad flag/argument/body) | fix the command; check `commands describe` |
| 3 | configuration / credentials missing | tell the user; see `hint` |
| 4 | authentication failed (401) | credential invalid/expired; tell the user |
| 5 | authorization failed (403) | account lacks permission; do not work around |
| 6 | not found (404) | the identifier is wrong or not visible to this user |
| 7 | other Jira API error (400, 409, 500...) | read `message`/`details.errors` |
| 8 | network / DNS / TLS / proxy / timeout | run `jiracli doctor`; do not blindly retry writes |
| 9 | refused by safety policy | read-only mode or confirmation required; ask the user |
| 10 | rate limited (429) | wait (`details.retry_after`) and retry later |
| 130 | interrupted (Ctrl+C) | the user stopped it; do not retry on your own |

Troubleshooting: `references/troubleshooting.md`.

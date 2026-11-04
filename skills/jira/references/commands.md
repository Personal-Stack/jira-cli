# Frequently used commands

Generated from Jira Software Data Center 10.3 (profile `datacenter-10.3`).
Always confirm with `jiracli commands list` for the instance at hand.

| Task | Command | Risk |
|---|---|---|
| Who am I | `jiracli myself get` | read |
| Server version | `jiracli server-info get` | read |
| JQL search | `jiracli issue search --jql '...'` | read |
| JQL search (long query, POST) | `jiracli issue search-post --stdin` | read |
| Get issue | `jiracli issue get KEY` | read |
| Create metadata | `jiracli issue get-create-meta-project-issue-types PROJECT` / `get-create-meta-fields PROJECT TYPEID` | read |
| Edit metadata | `jiracli issue get-edit-meta KEY` | read |
| Transitions | `jiracli issue get-transitions KEY` | read |
| Comments | `jiracli issue get-comments KEY` | read |
| Worklogs | `jiracli issue get-worklogs KEY` | read |
| Create issue | `jiracli issue create --file f.json` | write |
| Edit issue | `jiracli issue edit KEY --file f.json` | write |
| Comment | `jiracli issue add-comment KEY --stdin` | write |
| Transition | `jiracli issue do-transition KEY --stdin` | write |
| Assign | `jiracli issue assign KEY --stdin` | write |
| Link issues | `jiracli issue-link link-issues --stdin` | write |
| Delete issue | `jiracli issue delete KEY` | destructive (needs `--yes`) |
| Projects | `jiracli project get-all`, `jiracli project get KEY` | read |
| Project versions | `jiracli project get-versions KEY` | read |
| Components | `jiracli project get-components KEY` | read |
| Users | `jiracli user find --username X`, `jiracli user get --username X` | read |
| Assignable users | `jiracli user find-assignable --project ABC --username x` | read |
| Fields (incl. custom field IDs) | `jiracli field list` | read |
| Boards | `jiracli board get-all`, `jiracli board get ID` | read |
| Sprints | `jiracli board get-all-sprints BOARDID`, `jiracli sprint get ID` | read |
| Sprint issues | `jiracli sprint get-issues-for SPRINTID` | read |
| Epics | `jiracli epic get KEY`, `jiracli epic get-issues-for KEY` | read |
| Filters | `jiracli filter get-favourite`, `jiracli filter get ID` | read |

## Flags

- Query parameters are kebab-case flags: `maxResults` is `--max-results`.
- Array parameters are repeatable: `--fields summary --fields status` (comma-separated strings are also accepted by Jira).
- Path parameters are positional arguments in path order (see `Usage:` in `--help`).
- Body: `--file PATH` or `--stdin` (JSON). Multipart uploads (`issue add-attachment`) take `--file`.

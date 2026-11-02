# Frequently used commands

Generated from Jira Software Data Center 10.3 (profile `datacenter-10.3`).
Always confirm with `jira commands list` for the instance at hand.

| Task | Command | Risk |
|---|---|---|
| Who am I | `jira myself get` | read |
| Server version | `jira server-info get` | read |
| JQL search | `jira issue search --jql '...'` | read |
| JQL search (long query, POST) | `jira issue search-post --stdin` | read |
| Get issue | `jira issue get KEY` | read |
| Create metadata | `jira issue get-create-meta-project-issue-types PROJECT` / `get-create-meta-fields PROJECT TYPEID` | read |
| Edit metadata | `jira issue get-edit-meta KEY` | read |
| Transitions | `jira issue get-transitions KEY` | read |
| Comments | `jira issue get-comments KEY` | read |
| Worklogs | `jira issue get-worklogs KEY` | read |
| Create issue | `jira issue create --file f.json` | write |
| Edit issue | `jira issue edit KEY --file f.json` | write |
| Comment | `jira issue add-comment KEY --stdin` | write |
| Transition | `jira issue do-transition KEY --stdin` | write |
| Assign | `jira issue assign KEY --stdin` | write |
| Link issues | `jira issue-link link-issues --stdin` | write |
| Delete issue | `jira issue delete KEY` | destructive (needs `--yes`) |
| Projects | `jira project get-all`, `jira project get KEY` | read |
| Project versions | `jira project get-versions KEY` | read |
| Components | `jira project get-components KEY` | read |
| Users | `jira user find --username X`, `jira user get --username X` | read |
| Assignable users | `jira user find-assignable --project ABC --username x` | read |
| Fields (incl. custom field IDs) | `jira field list` | read |
| Boards | `jira board get-all`, `jira board get ID` | read |
| Sprints | `jira board get-all-sprints BOARDID`, `jira sprint get ID` | read |
| Sprint issues | `jira sprint get-issues-for SPRINTID` | read |
| Epics | `jira epic get KEY`, `jira epic get-issues-for KEY` | read |
| Filters | `jira filter get-favourite`, `jira filter get ID` | read |

## Flags

- Query parameters are kebab-case flags: `maxResults` is `--max-results`.
- Array parameters are repeatable: `--fields summary --fields status` (comma-separated strings are also accepted by Jira).
- Path parameters are positional arguments in path order (see `Usage:` in `--help`).
- Body: `--file PATH` or `--stdin` (JSON). Multipart uploads (`issue add-attachment`) take `--file`.

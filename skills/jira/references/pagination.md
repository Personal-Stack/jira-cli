# Pagination

Jira DC uses offset pagination (`startAt`, `maxResults`). The CLI marks
commands with both parameters as `"paginated": true` in `commands list`.

Two response shapes exist:

| Shape | Example commands | End condition |
|---|---|---|
| Object with items array + `total` | `issue search` (`issues`), `issue get-comments` | `startAt + len(items) >= total` |
| Object with `values` + `isLast` (Agile API) | `board get-all`, `board get-all-sprints`, `sprint get-issues-for` | `isLast == true` |
| Bare array | `user find`, `user find-assignable` | a page shorter than `maxResults` |

Options:

- `--all`: follow pages and print one merged document. All fields of the first page are kept; the item array holds every item; `startAt=0`, `maxResults=<count>`, and `isLast` reflects completeness.
- `--max-pages N` (default 100) bounds the loop; if it stops early a warning is logged to stderr and `isLast` stays `false`.
- Manual: `--start-at N --max-results M` and inspect `total`/`isLast` yourself.

Jira caps `maxResults` server-side (commonly 1000 for search, 50 for Agile);
asking for more silently returns fewer. Always use the returned `maxResults`.

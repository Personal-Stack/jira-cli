# JQL quick reference (Jira Data Center)

```text
project = ABC AND status = "In Progress"
project in (ABC, DEF) AND assignee = currentUser() AND resolution = Unresolved
statusCategory != Done AND updated >= -7d ORDER BY updated DESC
issuetype = Bug AND priority in (High, Highest) AND created >= startOfMonth()
text ~ "timeout" AND project = ABC
sprint in openSprints() AND project = ABC
"Epic Link" = ABC-100            -- Jira Software DC
labels = backend AND component = API
issuekey in (ABC-1, ABC-2)
filter = 12345                   -- saved filter
```

Tips:

- Quote values with spaces. Quote the whole JQL in single quotes in the shell.
- Field names that are custom fields can be referenced by name in quotes or as `cf[10010]`; get IDs from `jiracli field list`.
- Count only: `jiracli issue search --jql '...' --max-results 0` returns `total`.
- Restrict payload size with `--fields key,summary,status`.
- Use `--validate-query strict` to get errors for unknown fields/values instead of silent warnings.
- Autocomplete help: `jiracli jql get-auto-complete` (field names, operators, functions).
- Long queries: `jiracli issue search-post --stdin <<< '{"jql":"...","fields":["summary"]}'`.

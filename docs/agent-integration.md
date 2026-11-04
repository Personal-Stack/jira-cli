# Agent integration

Agents use the CLI through the Agent Skill in `skills/jira/` (no MCP server).
The CLI is the execution layer; the skill is the operating manual.

## Install the skill

Copy or symlink `skills/jira` into the agent's skills directory (e.g.
`~/.claude/skills/jira` or a project's `.claude/skills/jira`) and make sure
the `jiracli` binary is on `PATH` with a valid config and credential.

## Machine-readable interfaces

| Command | Returns |
|---|---|
| `jiracli capabilities` | binary version, compiled profiles, active instance, read-only state, features, global flags, exit codes |
| `jiracli commands list [--group G] [--risk R]` | every command: method, path, risk, confirmation, idempotency, pagination, args, flags, body mode |
| `jiracli commands describe G N` | one command in full: descriptions, body schema name and top-level properties, provenance (spec file, version, sha256, overlays) |
| `jiracli spec status` | profile provenance, counts by risk/method, skipped operations |
| `jiracli auth status` | credential presence/source (never values) |
| `jiracli doctor` | layered connectivity diagnosis |

All default to JSON on stdout. Errors are JSON on stderr when stderr is not
a terminal:

```json
{"error": {"layer": "jira_api", "code": "http_404", "message": "Issue does not exist ...", "http_status": 404, "details": {"errorMessages": ["..."]}}}
```

## Safety model

| Mechanism | Effect |
|---|---|
| Risk metadata | each command is `read`, `write`, `destructive` or `administrative` (`specs/risk/`) |
| Confirmation | destructive/administrative commands need `--yes` or a typed confirmation; unattended runs without `--yes` exit 9 before any network call |
| Read-only mode | `read_only: true` (config), `--read-only`, or `JIRA_READ_ONLY=1` refuses every non-read command (exit 9) |
| Dry-run | `--dry-run` prints method, URL, redacted headers and body; sends nothing; needs no credential |
| Retry policy | non-idempotent requests are never retried after a possible delivery |

Recommended agent setup for a production instance: `JIRA_READ_ONLY=1` until
the user explicitly enables writes.

Risk classification counts for the current profile: 212 read, 73 write, 70
destructive, 75 administrative.

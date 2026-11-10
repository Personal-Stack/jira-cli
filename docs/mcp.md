# Local MCP server: `jiracli mcp`

`jiracli mcp` lets an AI agent (Claude Code, OpenCode, Claude Desktop, any
MCP client) use every jira-cli API command. It is **local**: the client
starts `jiracli mcp` as a child process and talks to it over stdin/stdout
(JSON-RPC 2.0, one message per line). Nothing listens on the network.

It is **read-only by default**.

## Add it to a client

Claude Code:

```bash
claude mcp add jira -- jiracli mcp
```

OpenCode (`opencode.json`):

```json
{
  "mcp": {
    "jira": { "type": "local", "command": ["jiracli", "mcp"], "enabled": true }
  }
}
```

Clients with an `mcpServers` file (Claude Desktop and others):

```json
{
  "mcpServers": {
    "jira": { "command": "jiracli", "args": ["mcp"] }
  }
}
```

Use an absolute path to `jiracli` if the client does not get your shell's
`PATH`. The server uses your normal configuration and stored token, so run
`jiracli doctor` first. Global flags work as usual, e.g.
`jiracli mcp --instance prod`.

## Modes

| Start with | The agent can run |
|---|---|
| `jiracli mcp` | read operations only (default) |
| `jiracli mcp --allow-writes` | also write operations |
| `jiracli mcp --allow-writes --allow-destructive` | also destructive and administrative operations |

- An instance with `read_only: true`, `JIRA_READ_ONLY=1` or `--read-only`
  stays read-only whatever the flags say.
- The mode is fixed when the server starts. No tool argument changes it.
- Destructive and administrative commands also need `confirm` set to the
  command's name (`"issue delete"`), as the CLI asks you to type it. This
  catches slips; it does not stop a model that is determined to run it.
- Dry runs (`dry_run: true`) are allowed in every mode: they send nothing and
  load no credentials.
- `--dry-run` on the server makes every call a dry run.

Other flags: `--max-output` (bytes per tool result, default 256 KiB; longer
results are cut, with a note) and `--max-pages` (pages one `all` call may
fetch, default 10).

## Tools

| Tool | Does |
|---|---|
| `jira_commands` | Lists the commands this mode allows (filters: `group`, `risk`, `search`) |
| `jira_describe` | One command's parameters (with types, enums, required), body and risk |
| `jira_run` | Runs one command: `command`, `params`, `body`, `dry_run`, `all`, `max_pages`, `confirm` |

Three tools instead of one per command: 430 tool definitions would fill the
agent's context before it does anything.

Example call:

```json
{"command": "issue search", "params": {"jql": "project = ABC", "fields": "summary,status", "maxResults": 20}}
```

Results are Jira's JSON, compacted. Errors are jira-cli's structured errors
(`layer`, `code`, `message`, `hint`), so the agent can correct itself.

## Security

What the server enforces:

- **The mode** (above), checked before anything is sent, then again by the
  same safety check as every CLI command.
- **No file access for the model.** Request bodies are inline (JSON, or text
  for text bodies). The model cannot pass a file path, so it cannot make the
  server read a local file. The file-upload (multipart) commands, e.g.
  `issue add-attachment`, are not available.
- **Path values are checked**: empty, `.` and `..` are refused; everything
  else is escaped, so `../..` cannot leave the API path.
- **Unknown parameters and unknown argument fields are errors**, not ignored.
- **The token never appears in results.** It is loaded by this process as for
  any command; dry runs show `[REDACTED]`.
- **Bounded results**: `--max-output` and `--max-pages`.
- **Nothing prompts.** stdin is the protocol, so interactive prompts are off.
- **Every call is logged** (command, risk, dry run, error code; no values) to
  stderr or `--log-file`.

What it cannot do:

- **It is not a boundary against an agent that has a shell.** An agent that
  can run commands as you can run `jiracli` directly, with your token, past
  this server's mode. The mode then limits what the agent does through MCP,
  not what it could do. For a real boundary, give the agent no shell, or run
  it in a Docker Sandbox ([sandbox.md](sandbox.md)), where the token never
  enters the sandbox.
- **The token's rights are the ceiling.** A PAT can do what you can do in
  Jira. The mode narrows that; it cannot narrow it below what read operations
  return.
- **Prompt injection.** Issue summaries, descriptions and comments are
  written by other people and reach the model as data. With writes allowed, a
  malicious issue can try to make the agent write elsewhere (e.g. copy data
  into a comment). Read-only mode removes Jira as a place to write to; it does
  not remove other tools the agent has (web access, other MCP servers).

Recommended: read-only, and `--allow-writes` only for sessions that need it.
Keep `--allow-destructive` off.

## Verification

- Go tests with a fake Jira: handshake, tools, read-only default (nothing but
  the read reaches Jira), writes, confirmation, instance `read_only` and
  `JIRA_READ_ONLY` winning over the flags, argument checks (paths, unknown
  fields, uploads, types), pagination cap and output limit, and that no
  operation's parameters collide.
- Checked once with the official MCP Python SDK (2.3.0) as the client against
  `scripts/testdata/fake_jira.py`: handshake (protocol 2025-11-25), tool
  listing, a paginated search, and a delete refused in read-only mode and sent
  with `--allow-writes --allow-destructive`.
- **Not tested:** Claude Code, OpenCode or Claude Desktop as the client, a real
  Jira, and the Docker wrapper (it runs `docker run -i`, which stdio needs).

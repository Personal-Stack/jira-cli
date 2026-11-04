# Logging and tracing

Logs are structured (`log/slog`), go to **stderr** (or a file), and never mix
with command output on stdout. Every record passes through a redacting
handler (`internal/logging`).

## Configuration

| Setting | Flag | Environment | Default |
|---|---|---|---|
| Level | `--log-level debug\|info\|warn\|error` | `JIRA_LOG_LEVEL` | `warn` (`-v` = debug, `-q` = error) |
| Format | `--log-format text\|json` | `JIRA_LOG_FORMAT` | `text` |
| File | `--log-file PATH` | `JIRA_LOG_FILE` | stderr |

Precedence: flag > environment > `-v`/`-q` > default. Log files are opened in
append mode with permissions `0600`. When a log file is set, stderr carries
only the error output.

`jiragen` uses the same package: `-v` (debug), `-log-format`, and
`JIRA_LOG_FILE`; its default level is `info`.

## Correlation

| Field | Scope | Use |
|---|---|---|
| `run_id` | every line of one invocation | group all lines of one command |
| `request_id` | all attempts of one HTTP request | follow retries and the 401 refresh |
| `jira_request_id` | one HTTP response (`X-AREQUESTID`) | find the request in Jira's access log |
| `instance`, `operation_id` | per operation | which instance and which API operation |

## Events

| Message | Level | Key fields |
|---|---|---|
| `command started` | info | `command`, `version` |
| `config loaded` | debug | `path`, `instances` |
| `instance selected` | debug | `instance`, `selected_by`, `auth_method`, `read_only` |
| `operation` | info | `operation_id`, `method`, `path`, `risk`, `profile`, `spec_version`, `dry_run` |
| `request validation failed` | debug | `error` |
| `refused by safety policy` | info | `error_code` (`read_only_mode`, `confirmation_required`...), `risk` |
| `credential resolved` / `credential not found` | debug | `credential_ref`, `source` / `providers` (never the value) |
| `credential file permissions` | warn | `detail` |
| `http request` | debug | `request_id`, `method`, `url`, `attempt`, `idempotent`, `body_bytes` |
| `http response` | debug | `status`, `duration_ms`, `bytes`, `content_type`, `jira_request_id`, `conn_reused`, `dns_ms`, `connect_ms`, `tls_ms`, `ttfb_ms` |
| `http error` | debug | `layer` (`dns`, `tcp`, `proxy`, `tls_certificate`...), `error`, `duration_ms` |
| `http retry` | info | `attempt`, `status` or `reason`, `wait_ms` |
| `auth refresh after 401` | info | `ok` |
| `http request failed` | debug | `status`, `layer`, `error_code` |
| `unexpected HTML response` | warn | `status`, `url` |
| `page fetched` | debug | `page`, `start_at`, `items`, `total_items`, `done` |
| `command finished` | info | `exit_code`, `duration_ms`, `error_layer`, `error_code`, `http_status` |
| jiragen: `spec loaded`, `overlay loaded`, `risk metadata loaded`, `overlays applied`, `operation skipped`, `profile generated`, `generation errors` | info/debug | sha256, counts, durations |

## Redaction

Applied to every attribute, nested group and message, regardless of call site:

- **Keys**: `authorization`, `cookie`, `set-cookie`, `password`, `secret`, `token`, `pat`, `access_token`, `refresh_token`, `id_token`, `client_secret`, `code`, `code_verifier`, `api_key`, `private_key`, `body`, `request_body`, and any key ending in `_token`, `_secret`, `_password`. Their values become `[REDACTED]`.
- **Values**: `Bearer`/`Basic` credentials, URL query parameters `code`, `state`, `token`, `access_token`, `refresh_token`, `client_secret`, `code_verifier`, `password`, `os_password`, and `user:password@` in URLs.
- `credentials.Secret` implements `slog.LogValuer` and renders as `[REDACTED]`.

Request and response bodies, and HTTP headers, are never logged.
Logs do contain hostnames, URL paths, JQL and issue keys. Treat log files as
confidential (hence mode `0600`).

## Examples

```bash
jiracli -v issue get ABC-1                                            # human-readable debug trace
jiracli --log-level debug --log-format json issue get ABC-1 2> trace.jsonl
JIRA_LOG_FILE=~/jira-trace.log JIRA_LOG_LEVEL=info jiracli issue search --jql 'project = ABC' --all
jq -c 'select(.msg=="http response") | {status, duration_ms, ttfb_ms, jira_request_id}' trace.jsonl
```

## CI

`.github/workflows/ci.yml` sets `JIRA_LOG_FORMAT=json` and stores, as the
`ci-logs-*` artifact (14 days):

| File | Content |
|---|---|
| `jiragen-validate.log`, `jiragen-check.log` | generator stage logs (spec sha256, counts) |
| `go-test.log`, `coverage.out`, `coverage.txt` | verbose race-enabled test output and coverage. A failing CLI integration test prints the CLI's stderr (logs + error JSON) |
| `smoke/jira.log` | JSON debug log of the binary smoke run (`scripts/ci-smoke.sh`) |
| `smoke/<step>.stdout/.stderr` | per-command output |
| `smoke-summary.txt`, `govulncheck.log`, `vet.log` | step reports |
| `docker-build.log`, `docker-run.log` | image build and container debug run |

`scripts/ci-smoke.sh` (also `make smoke`) fails the build if any command
exits with an unexpected code, if any log line is not JSON or lacks
`run_id`, if network failures are not attributed to a network layer, if the
read-only refusal is not traced, or if the dummy credential appears in any
stdout, stderr or log file. The job summary shows test counts, coverage and
the smoke results.

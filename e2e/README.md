# End-to-end tests: Jira Software Data Center 10.3 in Docker Compose

Runs a disposable Jira and drives it with the real CLI image.

```
 host: e2e/run.sh ─ scenario.sh (assertions, python3)
                     │  docker compose run --rm cli <args>
 compose network ────┼───────────────────────────────────────────────
   cli (jira-cli image, distroless) ──https──► jira-proxy (nginx, TLS, throwaway CA)
                                                   │ http
                                                   ▼
                                     jira (atlassian/jira-software:10.3) ──► postgres:15
```

The CLI talks to Jira through a TLS proxy signed by a throwaway CA, so
`tls.ca_bundle`, reverse-proxy headers and Jira's proxy settings
(`ATL_PROXY_NAME`, `ATL_TOMCAT_SCHEME=https`) are exercised the way they are
in enterprise deployments.

## Requirements

- Docker with Compose v2, about 4 GB free RAM, and about 3 GB of images.
- For the full run, a **Jira Software Data Center license**. Either:
  - a free 30-day evaluation license from <https://my.atlassian.com> (*New Trial > Jira Software > Data Center*; the server ID can be anything for evaluation licenses); or
  - an Atlassian *timebomb* license (valid 3 hours) from the developer documentation page "Timebomb licenses for testing server apps".

Never commit the license. Pass it as a file or environment variable.

## Run

```bash
make e2e E2E_LICENSE_FILE=~/jira-dc-eval.lic     # full run (about 6-10 min first time)
make e2e-boot                                     # no license: boot + CLI diagnostics only
e2e/run.sh --license-file lic.txt --keep         # leave the stack up afterwards
```

With `--keep`, Jira is reachable from the host at <https://localhost:8443>
(CA: `e2e/.work/certs/ca.pem`). The admin password is in
`e2e/.work/secrets/admin-password`.

## What the full run does

1. Generates a CA and server certificate (`gen-certs.sh`).
2. Builds the CLI image from the repository Dockerfile (or uses `CLI_IMAGE`).
3. Starts PostgreSQL, Jira 10.3 and the TLS proxy, and waits for `FIRST_RUN`.
4. `setup/setup_jira.py` (inside the Jira container) completes the setup wizard (application properties, license, admin account) by parsing the real forms, then creates a 1-day PAT via `/rest/pat/latest/tokens`. Basic auth is used only for this bootstrap call; the CLI never uses it.
5. `scenario.sh` runs about 45 CLI steps with expected exit codes and JSON assertions:

| Area | What is checked |
|---|---|
| Discovery and diagnostics | `commands list` (430), `doctor` (all layers ok, Jira 10.3.26), `auth status` (file source), `auth test`, `server-info get` |
| Layer attribution | no CA bundle gives exit 8 `tls_certificate`; bad PAT gives exit 4; invalid JQL gives exit 7 |
| Admin write | `project create --yes` (scrum template, creates a board) |
| Issues | create meta, `create-bulk` (55), `search` single page, `--all` across 3 pages, `search-post`, table and YAML output, edit, comment, transition (verified by re-reading), attachment upload (multipart + XSRF header) |
| Safety | `--dry-run` delete, delete without `--yes` (exit 9), read-only instance delete (exit 9), read-only search (0), delete `--yes` then get (exit 6) |
| Agile, users, fields | `board get-all --all`, `board get-all-sprints`, `user find` (bare-array pagination), `field list` |
| Logging | every HTTP response logged with Jira's `X-AREQUESTID`, at least 3 `page fetched` events, a permission warning for the 0644 secret |
| Secrets | the PAT does not appear in any step output or log |

6. Collects logs and tears everything down (`down -v`), unless `--keep`.

## Output (`e2e/.work/logs/`)

| File | Content |
|---|---|
| `scenario.log`, `e2e-results.jsonl` | step results with exit codes and durations |
| `steps/<step>.out/.err` | stdout/stderr of every CLI invocation |
| `jira-cli.log` | CLI JSON debug log of all steps (run_id, request_id, jira_request_id, timings) |
| `setup.log` | wizard and PAT bootstrap events |
| `atlassian-jira.log`, `compose.log` | Jira application log and all container logs |

Use `jira_request_id` from `jira-cli.log` to find the same request in Jira's
access log.

## Boot-only mode (no license)

Verifies the stack and the CLI's network path: config, DNS, TCP and TLS through
the proxy with the custom CA must pass. The first failure must be at the HTTP
layer (Jira answers 503 while in setup), never at network/TLS. This mode runs
in CI on every relevant pull request.

## Known limitations

- The wizard automation was verified up to and including license rejection on 10.3.26. The admin-account step was written from the form layout but has not run in this environment, which had no license. If Jira shows a step the script does not know, it stops and prints the form's field names.
- `sprints` only checks that the call succeeds; the scrum template creates a board but no sprint.

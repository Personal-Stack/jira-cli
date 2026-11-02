# Progress

Last updated: 2026-11-02. Branch: `claude/tender-bohr-rsigl2`.

## TL;DR

- Phases 1, 2, 3 and 5 are implemented and pass 72 tests (unit, `httptest` integration over TLS, generator fixtures, race detector). Total statement coverage 68.3%.
- Structured logging layer with redaction and correlation IDs is in place and exercised in CI (see "Logging" below).
- **Nothing has been run against a real Jira Data Center instance yet.** That is Phase 4 and the next step (read-only plan below).
- OAuth 2.0 is implemented to the RFC (auth code + PKCE + refresh) and tested against a fake authorization server only. Jira DC specifics (PKCE support, client secret requirement, refresh rotation) are unverified.

## Decisions taken (approved in the plan)

| Decision | Why |
|---|---|
| Commands use spec tags as-is (`jira issue get`, not `issues`) | Tags are singular in the spec; no extra mapping table |
| Hand-curated naming overlay | `_N` operationIds and duplicates made mechanical names unusable (`search search-1`) |
| Specs compiled into *profiles*; `spec update/generate/validate` moved to `cmd/jiragen` | A compiled binary cannot regenerate itself; runtime keeps `spec list/status` |
| One base spec + ordered overlays (no 3-tier precedence) | Single explicit mechanism; conflicts are errors |
| No generated response structs; raw JSON passthrough | 25 empty schemas and 6 cyclic schemas in the spec would silently drop fields |
| Go 1.27 (`toolchain go1.27.3`) | As requested |
| `jira version` removed in favour of `jira --version` | `version` is a Jira tag (project versions, 16 operations) |
| `--dry-run` allowed in read-only mode | Sends nothing; lets agents preview writes against production |

## Done

### Phase 1: CLI foundation
- [x] Go module, Cobra root, global flags (`--instance --config --output --verbose --quiet --no-color --timeout --dry-run --read-only --yes`)
- [x] Config: strict YAML (unknown keys rejected, so a `token:` key fails), multiple instances, selection flag > `JIRA_INSTANCE` > default > only instance
- [x] `config list|show|validate|set default-instance` (comment-preserving edit)
- [x] Output: JSON (order and precision preserving), YAML (order preserving), table (lossy, documented)
- [x] Structured errors with layer, code and exit codes 0-10

### Phase 2: security and authentication
- [x] Central transport: TLS 1.2+ default, CA bundle, mTLS with hot reload, proxy, timeouts, pooling
- [x] Redirect policy: exact-origin credential forwarding, no https to http, 5 hops
- [x] Retry policy: idempotent-only for 5xx/network errors; 429 for all methods; Retry-After capped at 30 s; full-jitter backoff
- [x] PAT provider (Bearer); lazy loading; header-injection guard
- [x] OAuth 2.0 provider: auth code + PKCE S256, loopback or `--manual`, state check, refresh on expiry and once after 401, keyring persistence
- [x] Credential chain: `JIRA_CREDENTIAL_FILE`, secrets dir (`/run/secrets`), `JIRA_CREDENTIAL_<REF>`, OS keyring (macOS/Windows/Secret Service); file permission checks; 5 s keyring timeout
- [x] `auth login|logout|status|test`, `doctor` (config, DNS, TCP, proxy, TLS details, serverInfo + version check, credentials, auth)
- [x] HTML-instead-of-JSON detection (SSO/reverse proxy), Seraph login reason surfaced

### Phase 3: OpenAPI generator
- [x] `cmd/jiragen generate|check|validate|update`; checksum-pinned manifest; versions derived from `info.version`
- [x] Untrusted-input handling: no external refs, 64 MiB cap, control-character stripping, quoted emission
- [x] Overlays: `rename_paths`, `exclude`, `operation_id`, `command`, `group`, parameter `in/rename/drop/flag`, `body_required`, `schema_aliases`; stale entries are errors
- [x] Risk metadata file + method/name/tag heuristics (heuristics only raise risk)
- [x] Deterministic output (tested), `REPORT.md` per profile, committed `generated/` verified by test and `make verify-generated`
- [x] Multiple specs per profile with conflict detection (custom specs)

Result for Jira Software DC 10.3.26: 433 operations, **430 commands**, 3 excluded (cookie session auth).
Risk: 212 read, 73 write, 70 destructive, 75 administrative. 40 naming warnings remain (names derived from `_N` ids that are readable enough).
Spec defects fixed by overlay: 8 duplicate operationIds, 2 conflicting path-template pairs, 4 parameter defects, `StringList` typed as object (blocked 16 operations incl. JQL search), 14 missing `body_required`.

### Phase 5: Agent Skills
- [x] `skills/jira/SKILL.md` (12 workflows, 10 operating rules, exit-code table) + 5 references + `check-connection.sh`
- [x] Every command, flag and argument count in the skill was checked against `jira commands list` output
- [x] `capabilities`, `commands list|describe`, `spec list|status`, `auth status` are machine-readable
- [x] Risk-aware execution: read-only mode (config/flag/env), confirmation (typed, or `--yes`; unattended refusal), dry-run

### Logging and CI traceability (added after the initial delivery)
- [x] `internal/logging`: slog-based, text/JSON, level/file via flags (`--log-level --log-format --log-file`) or `JIRA_LOG_*`; files created `0600`
- [x] Redacting handler on every record: sensitive keys, Bearer/Basic values, OAuth query parameters, URL userinfo (`TestRedaction`, 9 leak shapes plus over-redaction checks)
- [x] Correlation: `run_id` per invocation, `request_id` per HTTP request (shared across retries), Jira's `X-AREQUESTID` as `jira_request_id`
- [x] Trace events: command start/finish (exit code, error layer), config/instance selection, credential source (never value), operation + risk, safety refusals, HTTP request/response with DNS/connect/TLS/TTFB timings, retries, 401 refresh, pagination pages, generator stages (spec sha256, counts)
- [x] CI: verbose race tests, coverage, `scripts/ci-smoke.sh` (13 commands with expected exit codes, JSON log validation, secret-leak scan over all artifacts), job summary, `ci-logs-*` artifacts (14 days); see docs/logging.md
- [x] Bug found by the smoke test and fixed: a proxy refusing CONNECT (e.g. 403 from a corporate proxy) was classified as generic `http_transport`; now `proxy` / `proxy_connect_refused` with the proxy's status (`TestProxyConnectRefusedIsClassified`)
- [x] `clierr` classification and exit-code unit tests (was an open item)

### Real Jira 10.3 in Docker Compose (e2e/)
- [x] `e2e/docker-compose.yml`: PostgreSQL 15 + `atlassian/jira-software:10.3` (image is 10.3.26, the spec version) + nginx TLS proxy (throwaway CA) + the CLI image
- [x] `setup/setup_jira.py`: completes the setup wizard by parsing the real forms; creates a 1-day PAT. Verified on 10.3.26 through application properties and license submission (rejection detected). **Admin-account step not yet exercised** (no license available in this environment)
- [x] `scenario.sh`: ~45 CLI steps (bulk create 55 issues, `--all` over 3 pages, edit/comment/transition/attachment, safety refusals, agile, users, layer attribution for TLS/auth/API errors, log checks, PAT leak scan). All commands/flags/arg counts validated against the binary; **not yet executed against a licensed Jira**
- [x] `run.sh --boot-only` executed here (as root and as an unprivileged docker-group user): stack boots in ~75 s, the CLI image passes config/DNS/TCP/TLS through the proxy with the custom CA, Jira's setup-mode 503 is attributed to the HTTP/API layer, retries and `X-AREQUESTID` correlation observed in the CLI log
- [x] CI: `.github/workflows/e2e.yml`, boot-only on relevant PRs; full run nightly/on demand when the `JIRA_E2E_LICENSE` secret exists

### Phase 6 (partial)
- [x] Multi-stage Dockerfile (distroless, nonroot, ~20 MB). Built and run locally: headless mode with mounted secret works; no secrets in layers
- [x] GitHub Actions CI: spec-validate, verify-generated, vet/gofmt, race tests, govulncheck, build, smoke test, docker build/run
- [x] Docs: README, 9 docs in `docs/`, CONTRIBUTING, threat model in `docs/security.md`

## Verification status

| Area | How verified | Not verified |
|---|---|---|
| Config, selection, redaction | unit tests | |
| TLS custom CA, mTLS, insecure opt-in, downgrade, redirect stripping | httptest TLS with generated CA | corporate MITM proxies, TLS 1.3-only servers |
| PAT | integration tests vs fake Jira | **real Jira DC PAT** |
| OAuth 2.0 | fake auth server (PKCE verified, state, refresh, revoked token) | **real Jira DC OAuth 2.0 provider** (PKCE support, secret requirement, scopes, refresh rotation) |
| Keyring | not tested (needs desktop session) | macOS/Windows/Secret Service backends |
| Pagination (total / isLast / bare array) | fake responses | real Agile/REST pagination limits |
| Generated commands | 430 generated; tests exercise `issue get/search/search-post/create/edit/delete` | the other ~424 against a real server |
| Docker | built and run locally (with a sandbox-only CA workaround, not committed) | CI run |
| govulncheck | **could not run** (vuln.go.dev blocked by sandbox network policy) | runs in CI |

Coverage (total 68.3%): generator 88%, api 82%, pat 85%, safety 100%, transport 72%, output 71%, credentials 64%, cli 64%, oauth 63%, config 61%; `clierr` and `logging` now have direct tests.

The GitHub Actions workflow itself has not run yet (no CI run exists for this branch). Its test-job steps were executed locally with `bash -eo pipefail` and pass.

## Next: full e2e with a license (no access to your instance needed)

```bash
make e2e E2E_LICENSE_FILE=~/jira-dc-eval.lic    # evaluation license from my.atlassian.com
```

Or add the license as repository secret `JIRA_E2E_LICENSE` and run the `e2e`
workflow. Send back `e2e/.work/logs/` if anything fails. This validates PAT,
pagination limits and ~25 generated commands against real Jira 10.3.26
before touching your instance.

## Then: Phase 4, read-only verification against your Jira DC

Config (`~/.config/jira-cli/config.yaml`):

```yaml
version: 1
default_instance: dc
instances:
  dc:
    deployment: datacenter
    base_url: https://YOUR-JIRA          # include the context path if any
    auth: {type: pat, credential_ref: jira-dc}
    tls: {ca_bundle: /path/to/corporate-ca.pem}   # omit if publicly trusted
    read_only: true
```

Run with the extra guard `export JIRA_READ_ONLY=1` and capture a trace:

```bash
export JIRA_READ_ONLY=1
export JIRA_LOG_LEVEL=debug JIRA_LOG_FORMAT=json JIRA_LOG_FILE=$PWD/phase4-trace.jsonl
```

then:

```bash
make build && export PATH=$PWD/bin:$PATH
jira config validate
jira auth login                         # or: export JIRA_CREDENTIAL_FILE=~/secrets/jira-dc (chmod 600)
jira doctor                             # record: jira_version, warnings
jira auth test
jira server-info get
jira myself get
jira project get-all -o table
jira issue search --jql 'project = <KEY> ORDER BY updated DESC' --max-results 5 --fields summary,status
jira issue search --jql 'project = <KEY>' --all --max-pages 3 --max-results 50   # pagination (REST v2)
jira issue get <KEY-1> --expand names,transitions
jira issue get-transitions <KEY-1>
jira issue get-comments <KEY-1>
jira field list -o table
jira board get-all --all                 # pagination (Agile isLast)
jira board get-all-sprints <BOARD_ID>
jira user find --username <you>          # pagination (bare array)
jira issue search-post --stdin <<< '{"jql":"project = <KEY>","maxResults":3}'
jira issue delete <KEY-1>                # must fail with exit 9, no request sent
jira issue create --file examples/issue.json --dry-run
```

Please send back: the `doctor` output, any non-zero exit with its stderr JSON,
`phase4-trace.jsonl` (redacted by design; still review it for hostnames/JQL you
consider confidential), and anything where output looks wrong. Remove tokens/hostnames you consider
sensitive (the CLI never prints tokens).

## Left / open items (prioritised)

1. **Full e2e run with a license** (see above): finish the wizard admin step and the scenario on real Jira 10.3.26.
2. **Phase 4 results**: fix whatever the real instance shows (pagination caps, version differences, auth).
3. OAuth against real Jira DC: confirm endpoints (`/rest/oauth2/latest/...`), PKCE, client secret, scopes, refresh rotation; adjust docs.
4. Keyring backends: manual test on macOS/Windows/Linux desktop.
5. Release process: version tagging, goreleaser (or equivalent) for multi-OS binaries, image publishing, SBOM. Not started.
6. Risk review: a human pass over `generated/datacenter103/REPORT.md` risk column (73 `write` operations rely on heuristics).
7. 40 remaining naming warnings: rename via overlay if any read badly in practice.
8. Tests: keyring with a mock; OAuth `--manual` path.
9. First real CI run: confirm the workflow (artifact upload, job summary, govulncheck) on GitHub.
10. Optional: JSON-schema validation of nested request bodies (currently top-level type and required properties only; deeper validation is left to Jira, whose field configuration is instance-specific).
11. Optional: response filtering (`--jq`-style). The spec lists it as optional; JSON passthrough plus `--fields` covers most needs.
12. Jira Cloud: out of scope by design; the deployment abstraction is the extension point.

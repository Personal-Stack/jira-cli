# Progress

Last updated: 2026-11-02. Branch: `claude/tender-bohr-rsigl2`.

## TL;DR

- Phases 1, 2, 3 and 5 are implemented and pass 63 tests (unit, `httptest` integration over TLS, generator fixtures, race detector).
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

Coverage: generator 88%, api 82%, pat 85%, safety 100%, transport 72%, output 71%, credentials 64%, cli 64%, oauth 63%, config 61%. `clierr` has no direct tests (covered indirectly).

## Next: Phase 4, read-only verification against your Jira DC

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

Run with the extra guard `export JIRA_READ_ONLY=1`, then:

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
and anything where output looks wrong. Remove tokens/hostnames you consider
sensitive (the CLI never prints tokens).

## Left / open items (prioritised)

1. **Phase 4 results**: fix whatever the real instance shows (pagination caps, version differences, auth).
2. OAuth against real Jira DC: confirm endpoints (`/rest/oauth2/latest/...`), PKCE, client secret, scopes, refresh rotation; adjust docs.
3. Keyring backends: manual test on macOS/Windows/Linux desktop.
4. Release process: version tagging, goreleaser (or equivalent) for multi-OS binaries, image publishing, SBOM. Not started.
5. Risk review: a human pass over `generated/datacenter103/REPORT.md` risk column (73 `write` operations rely on heuristics).
6. 40 remaining naming warnings: rename via overlay if any read badly in practice.
7. Tests: `clierr` classification unit tests; proxy CONNECT integration test; keyring with a mock; OAuth `--manual` path.
8. Optional: JSON-schema validation of nested request bodies (currently top-level type and required properties only; deeper validation is left to Jira, whose field configuration is instance-specific).
9. Optional: response filtering (`--jq`-style). The spec lists it as optional; JSON passthrough plus `--fields` covers most needs.
10. Jira Cloud: out of scope by design; the deployment abstraction is the extension point.

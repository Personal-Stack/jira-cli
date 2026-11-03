# Progress

Last updated: 2026-11-03. Release: v0.1.0 (Apache-2.0).

## TL;DR

- Phases 1, 2, 3 and 5 are implemented and pass 72 tests (unit, `httptest` integration over TLS, generator fixtures, race detector). Total statement coverage 68.3%.
- Structured logging layer with redaction and correlation IDs is in place and exercised in CI (see "Logging" below).
- **Strategic note:** Atlassian is ending Data Center sales and moving customers to Cloud (user report, 2026-11-03). The CLI keeps working against existing DC instances. Jira Cloud support (different auth, REST v3 with ADF, token-based search pagination, separate spec) is out of scope until a migration date is known; the deployment/profile/auth abstractions are the extension points.
- **Phase 4 (real Jira DC) is in progress.** Confirmed on the user's production instance (via Docker wrapper and native macOS): PAT authentication, TLS with the company CA, `issue search`. Fixed from those runs: `doctor` failing on disabled anonymous access (401), bash 3.2 script failure, DER `.cert` CA files, file names with spaces. Still open: Jira version check, the three pagination shapes, more read commands, the read-only guard on prod, the licensed e2e write scenario, OAuth.
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

### Installation
- [x] `scripts/install.sh` (Linux/macOS, POSIX sh): release download (mirror, `gh`, `GITHUB_TOKEN`, public URL), SHA256 verification, `~/.local/bin`, idempotent PATH block for bash/zsh/fish/sh, completions, custom binary name, offline archive, from source, uninstall that restores rc files byte-for-byte, shadowing warning
- [x] Windows: `install.sh` from Git Bash/MSYS2/Cygwin (zip, `jira.exe`, `~/.bashrc` PATH block; `~/.bash_profile` only if it exists; `--docker` refused). The PowerShell installer and the Windows CI job were removed (CI minutes; the job never got past runner quirks). Verified only on Linux with a `uname` shim (`uname -s` = `MINGW64_NT-10.0`) and a fake `jira.exe` in a real zip: install, idempotent re-install, offline archive, wrong-platform refusal, tamper rejection, `--docker` refusal, uninstall. **Never run on real Windows.**
- [x] `scripts/dist.sh` / `make dist` (6 platforms, reproducible tarballs, SHA256SUMS), `release.yml` on `v*` tags, `make install|uninstall|test-install`
- [x] `scripts/test-install.sh`: 26 scenarios under `dash`, all passing here; found and fixed 2 installer bugs (uninstall exit code; blank-line drift in rc files)
- [ ] No release has been published yet, so the download path only works after the first tag (`git tag v0.1.0 && git push origin v0.1.0`)

### Docker wrapper (run the CLI without Go or a native binary)
- [x] `scripts/jira-docker.sh` installed as `jira` by `install.sh --docker` (builds `jira-cli:local` from a clone, or pulls `ghcr.io/personal-stack/jira-cli:<version>` with a checksum-verified wrapper)
- [x] Opt-in file credential store (`JIRA_CREDENTIAL_STORE=file`, 0600 files / 0700 dir, atomic writes); OAuth refresh persists to it. This changes the earlier guarantee "secrets are only written to the keyring"; docs/credentials.md and docs/security.md updated
- [x] Tests: 19 wrapper checks against a real container as an unprivileged user; 7 `--docker` installer checks (33 installer checks total); found and fixed 2 wrapper bugs (relative `--log-file` mount; duplicate cwd mount)
- [x] Multi-arch image (amd64/arm64 via cross-compilation) built and verified; `release.yml` pushes it to GHCR on tags (not yet run)
- [x] Measured: ~710 ms start-up per command vs milliseconds native; reaching a host-loopback Jira needs `JIRA_DOCKER_NETWORK=host`
- [ ] Not tested on macOS/Windows Docker Desktop

### TLS trust and certificate formats
- [x] `tls.ca_bundle` accepts PEM or DER (`.cer`/`.cert`/`.crt`, one or more certificates); PKCS#7 and private keys are rejected with a conversion hint (9 format cases + a real TLS test with a binary `.cert`)
- [x] `scripts/tls-diagnose.sh`: shows the server's chain and tests candidate CA files (PEM/DER/PKCS#7 auto-detected) exactly like the CLI (verdicts matched the CLI in 6/6 cases: lookalike root, full chain, leaf-only chain). Fixed a bash 3.2 (macOS) `set -u` empty-array bug, reproduced on bash 3.2.57
- [x] macOS: confirmed in the Go 1.27 source that the CLI's trust pool uses the OS verifier first on macOS/Windows (Keychain / Windows store), so `ca_bundle` is unnecessary when IT installed the CA; tokens go to the Keychain via `/usr/bin/security -i` (stdin, not argv). Installer strips the quarantine flag after checksum verification and warns against `--docker` on macOS
- [ ] `scripts/test-macos-keychain.sh` (Keychain trust + token storage) runs only on the macOS CI runner; **not executed yet** (no macOS here)

### `jira certs fetch`
- [x] Downloads a server's CA chain (no credentials; honours the instance's proxy and client certificate), completes missing issuers via AIA only when they cryptographically sign the certificate below, shows SHA-256 fingerprints, requires confirmation (`--yes`/`--dry-run`), saves CA certificates only (never the leaf) to `<config dir>/<host>-ca.pem`, and proves the file with a verified handshake
- [x] Tests: AIA completion (leaf+intermediate and leaf-only servers), same-name impostor root rejected, confirmation/dry-run/decline, self-signed server; verified through the Docker wrapper against a production-like server (tls fail, then fetch, then tls ok)

### Release pipeline (archives + GHCR image)
- [x] `release.yml` rewritten: verify, then archives and image in parallel. The image is pushed, then pulled back by digest and smoke-tested on amd64 and arm64. The GitHub release is published last, only if everything passed, with the pull command and digest in the notes
- [x] Image tags `vX.Y.Z`, `X.Y`, `latest` (pre-releases move neither); SBOM and provenance attestations; least-privilege permissions per job; manual dry-run mode (builds and tests, publishes nothing)
- [x] Verified locally: actionlint clean on all workflows; version check, archive smoke test and notes rendering run from the workflow's own `run:` blocks; both documented `docker run` forms tested against a production-like server
- [x] Fixed earlier docs: `gh auth token` cannot pull from GHCR without `gh auth refresh -s read:packages`
- [ ] Not run on GitHub yet: recommended first step is a manual dry run (Actions > release > Run workflow)

### Docker Sandboxes (sbx)
- [x] `scripts/sbx-setup.sh`: generates a local sbx kit (linux amd64/arm64 binary, agent skill, egress rule, proxy-managed `credential@1`, bash hook), `sbxenv.yaml` (read-only settings mount at `/etc/jira-cli`, env, binding) and settings (`config.yaml`, `bashrc`, created once). The PAT is an sbx secret; the sandbox only sees a sentinel. Options for a custom image (`--template`), agent kit, CA, `--kit-only`. Docs: `docs/sandbox.md`
- [x] Verified here: the kit validates against the sandbox-kit-spec v3 JSON schemas (with negative tests); the overlay builds for amd64/arm64 with the right files and modes; the binary picks up the sentinel as `env:JIRA_CREDENTIAL_JIRA_PROD`, the generated config validates, the read-only guard exits 9; re-run keeps settings; refusals (foreign sbxenv.yaml, http, credentials in URL, settings inside the project)
- [ ] Not run with a real `sbx` (none here): proxy header injection, `bash.bashrc` hook, built-in `opencode` agent name, `sandboxOptions.template`

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

Run the read-only verification script (from a clone; works with the native
binary or the Docker wrapper on PATH, bash 3.2+, no python/jq):

```bash
scripts/phase4-readonly.sh --project <KEY>
```

It forces `JIRA_READ_ONLY=1`, first proves that a write is refused locally
(stops otherwise), then runs ~25 read commands: doctor/version, auth, issue
search (single page, `--all` capped at 3x20, POST search, YAML/table), issue
get/transitions/comments/editmeta (key taken from the search), dry-run
create, project/field lists, boards (`--all` capped at 2x2) and sprints (board
taken from the results), user search (user from `myself`). It then checks the
trace: no write request sent, Jira request IDs present, retries, slowest
response. Output: `phase4-<timestamp>/summary.txt` and `trace.jsonl`.

Tested with `scripts/test-phase4.sh` against `scripts/testdata/fake_jira.py`
(15 checks incl. server-side proof that only GET + POST /search arrived, a
broken-guard stop, a bad token, an empty project), natively, through the
Docker wrapper, and under bash 3.2.57. Runs in CI.

Please send back `summary.txt` and `trace.jsonl` from the output directory. The
trace never contains tokens, but it does contain hostnames, JQL and issue keys:
review it first.

## Left / open items (prioritised)

1. **Write-path verification on real Jira**: free DC evaluation licenses are no longer issued (user report, 2026-11-03). Options: a developer license from the organisation's DC subscription (then `make e2e` works unchanged), or a staging instance with a dedicated project (needs the scenario adapted to an existing instance). Until then, writes are tested against fakes only.
2. **Phase 4, remaining read-only checks on production**: run `scripts/phase4-readonly.sh --project <KEY>` and review `summary.txt` / `trace.jsonl`. PAT auth, TLS and `issue search` are already confirmed.
3. OAuth against real Jira DC: confirm endpoints (`/rest/oauth2/latest/...`), PKCE, client secret, scopes, refresh rotation; adjust docs.
4. Keyring backends: manual test on macOS/Windows/Linux desktop.
5. Release process: pipeline complete (archives, GHCR image with SBOM/provenance, smoke tests, release last). Missing: a first dry run and first tag; image signing (cosign keyless would publish entries to the public Rekor log, which leaks private repo/image names; decide first).
6. Risk review: a human pass over `generated/datacenter103/REPORT.md` risk column (73 `write` operations rely on heuristics).
7. 40 remaining naming warnings: rename via overlay if any read badly in practice.
8. Tests: keyring with a mock; OAuth `--manual` path.
9. First real CI run: confirm the workflow (artifact upload, job summary, govulncheck) on GitHub.
10. Optional: JSON-schema validation of nested request bodies (currently top-level type and required properties only; deeper validation is left to Jira, whose field configuration is instance-specific).
11. Optional: response filtering (`--jq`-style). The spec lists it as optional; JSON passthrough plus `--fields` covers most needs.
12. Jira Cloud: out of scope by design; the deployment abstraction is the extension point.

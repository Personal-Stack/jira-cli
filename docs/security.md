# Security

## Controls (implemented centrally)

| # | Requirement | Where | Test |
|---|---|---|---|
| 1 | TLS verification on by default; insecure only by explicit config, warned | `transport/tls.go` | `TestInsecureIsOptIn`, `TestCustomCA` |
| 2 | No plaintext credentials in config | strict YAML decoding (`KnownFields`) | `TestRejectsPlaintextSecretsAndUnsafeValues` |
| 3 | No secrets in process arguments | `auth login` reads stdin/hidden prompt only | n/a (no flag exists) |
| 4 | No credentials in logs/output | `credentials.Secret` redaction (incl. `slog.LogValuer`); redacting log handler on every record (keys, Bearer/Basic values, OAuth query params, URL userinfo); headers/bodies never logged; CI scans all artifacts for a dummy token | `TestSecretNeverFormats`, `TestTokenNeverLogged`, `logging.TestRedaction`, `scripts/ci-smoke.sh`, every CLI integration test scans stdout/stderr for the token |
| 5 | No auth headers across untrusted redirects | `transport.CheckRedirect` (exact origin) | `TestRedirectStripsCredentialsAcrossOrigins` |
| 6 | No HTTPS downgrade | config validation + redirect policy | `TestRedirectRefusesDowngrade` |
| 7 | Secure OAuth token storage | keyring only; never files | `TestBareRefreshTokenFromFileIsNotPersisted` |
| 8 | Confirmation for destructive operations | `safety.Check` before credentials/network | `TestDestructiveRefusedWithoutYes` |
| 9 | Input validation for generated commands | executor: args, types, enums, JSON body, header CR/LF | `TestBodyValidation` |
| 10 | OpenAPI is untrusted input | no external refs, 64 MiB cap, checksum pinning, control chars stripped from help text, emitted strings via `strconv.Quote` | `TestExternalRefsRejected`, `TestChecksumPinned`, `TestCleanTextStripsControlChars` |
| 11 | Downloaded specs never executed | generator emits data tables only; `go/format` on output | n/a |
| 12 | No unnecessary persistence of responses | responses go to stdout only; no cache | n/a |

## Threat model

| Threat | Vector | Mitigation | Residual risk |
|---|---|---|---|
| Credential theft at rest | config file, temp files, image layers, file store | no secrets in config; keyring or mounted secrets by default; opt-in file store (`JIRA_CREDENTIAL_STORE=file`, Docker wrapper) uses 0600 files in a 0700 dir; nothing in the Docker image; mounted files permission-checked | env-var credentials readable by same-user processes (documented; prefer files) |
| Token leakage | logs, errors, `--verbose`, dry-run, crash output | `Secret` redaction type; logger never receives headers/bodies; dry-run prints `[REDACTED]`; OAuth errors carry only the error code | a user piping `--verbose` output elsewhere still sees URLs and JQL (not secrets) |
| Malicious OpenAPI spec | crafted refs, huge files, terminal escape sequences in descriptions, code injection into generated Go | external refs disabled; size cap; control characters stripped; all strings emitted via `strconv.Quote` and `go/format`; checksums pinned and reviewed via REPORT.md diffs | a spec could declare misleading summaries; review the diff |
| TLS interception | corporate MITM, misconfigured CA | verification on; explicit CA bundle; `doctor` shows issuer/subject | `tls.verify: false` is possible by explicit config (warned) |
| Redirect-based leakage | Jira/proxy redirects to another host or to http | exact-origin header forwarding; no downgrade; 5-hop limit | none known |
| Compromised/misbehaving agent | agent runs destructive commands or exfiltrates tokens | risk metadata + confirmation (`--yes` required, exit 9 otherwise); read-only mode; agent never sees secret values; skill rules | an agent with `--yes` and write permission can do what the Jira user can do; use read-only mode or a least-privilege Jira account |
| Malicious command input | header injection, path traversal, oversized bodies | header values with CR/LF rejected; path params escaped individually; bodies capped at 32 MiB; credential refs validated (no `..`, no `/`) | Jira-side validation of field values is Jira's responsibility |
| Insecure Docker configuration | root user, secrets in layers, writable config | distroless non-root image; keyring disabled; config/secrets mounted read-only; no credentials at build | operator must mount secrets read-only and restrict egress |
| Unauthorized destructive operations | accidental `delete`, misclassified POST | method baseline + name/tag heuristics + explicit risk file; confirmation required; dry-run | an operation whose risk is understated by the spec and missed by the heuristics; review REPORT.md risk column on spec updates |
| Supply chain | dependencies | 7 direct dependencies (kin-openapi only in the generator), pinned in go.sum; `make security` (govulncheck) in CI | govulncheck could not be run in the development sandbox (network policy); runs in CI |

## Reporting

Report vulnerabilities privately to the repository owner; do not open public
issues for security problems.

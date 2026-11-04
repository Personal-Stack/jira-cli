# Architecture

## Principle

OpenAPI specifications are the source of truth for API operations. They are
processed **at build time** by `cmd/jiragen` into static Go operation tables
under `generated/`. The runtime binary contains no OpenAPI parser; it builds
the Cobra command tree from those tables.

```
 specs/official/*.json ─┐
 specs/custom/*.yaml  ──┼─► jiragen: load (no external refs) ─► overlays ─► validate
 specs/overlays/*.yaml ─┤        ─► map to commands ─► risk ─► emit Go + REPORT.md
 specs/risk/*.yaml    ──┘                                         │
                                                                  ▼
                                    generated/<profile>/ops_<group>.go (committed)
                                                                  │
 cmd/jiracli ─► internal/cli ─► registry (Cobra) ─► executor ────────┘
                                   │
                    safety ◄───────┤ (read-only, confirmation, dry-run)
                                   ▼
                   internal/api (single client: auth, retries, errors, pagination)
                     │                    │
         internal/auth (pat, oauth)   internal/transport (TLS/mTLS, proxy, redirects, retry policy)
                     │
         internal/credentials (file ▸ secrets dir ▸ env ▸ keyring)
```

## Packages

| Package | Responsibility | Must not |
|---|---|---|
| `internal/operation` | Metadata model shared by generator and runtime | import anything |
| `generator`, `cmd/jiragen` | Build-time pipeline | be imported by the runtime |
| `generated/` | Emitted operation tables + `REPORT.md` | contain logic or hand edits |
| `internal/cli` | Cobra tree, global flags, executor, discovery, config/auth/doctor commands | talk to `net/http` directly |
| `internal/api` | The only HTTP client for Jira: request building, auth application, retries, 401 refresh, error mapping, pagination | know about Cobra |
| `internal/transport` | `*http.Client` construction: TLS, mTLS, CA bundle, proxy, timeouts, redirect and retry policy | know about auth |
| `internal/auth/*` | Decorate requests with credentials | log or return secrets |
| `internal/credentials` | Resolve secrets by reference | persist secrets to files |
| `internal/config` | Parse/validate/edit config (no secrets) | read credentials |
| `internal/safety` | Read-only mode, confirmation | perform I/O beyond the prompt |
| `internal/output` | JSON/YAML/table rendering | drop fields (JSON/YAML) |
| `internal/clierr` | Structured errors, layer classification, exit codes | |

## Profiles

A *profile* is one compiled-in operation table (one base spec, optional
custom specs, overlays, risk metadata). An instance selects a profile with
`specification.profile`; the CLI registers only that profile's commands.
Because generation is compile-time, an instance cannot load a new spec at
runtime: adding a spec means adding a profile and rebuilding. This is a
deliberate trade-off for reproducibility and reviewability.

## Deployment abstraction

`deployment: datacenter` is the only accepted value (config and manifest).
Cloud support would add a `cloud` deployment, its own profiles (different
base path and pagination), and an auth provider. The current code has no
Cloud-specific branches, by design.

## Request flow (`jiracli issue get ABC-1`)

1. `NewRoot` pre-scans `--instance/--config`, loads the config, picks the profile, registers its commands.
2. The executor validates arguments/flags/body against the operation metadata.
3. `safety.Check`: read-only mode, then confirmation for destructive/administrative operations.
4. `--dry-run` prints the request and stops (no credentials loaded, no connection).
5. `api.Client.Do`: build URL (`base_url` + profile base path + escaped path), apply auth, send through the transport, retry per policy, refresh once on 401 (OAuth), classify errors.
6. Output is rendered without dropping fields.

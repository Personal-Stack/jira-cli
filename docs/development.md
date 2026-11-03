# Development

## Requirements

Go 1.27 (`go.mod` pins `toolchain go1.27.3`; with `GOTOOLCHAIN=auto` the
toolchain is downloaded automatically). No CGO.

## Layout

```
cmd/jira          runtime entry point
cmd/jiragen       code generator (build time only)
generator/        generator implementation + tests
generated/        GENERATED: operation tables and REPORT.md (do not edit)
internal/         runtime packages (see docs/architecture.md)
specs/            manifest, official/custom specs, overlays, risk metadata
skills/jira/      Agent Skill
examples/         config, request body, custom spec
```

## Everyday commands

```bash
make generate           # after changing specs/overlays/risk (+ make spec-update)
make verify-generated   # CI check
make vet test           # gofmt check, go vet, tests
make test-race
make build              # bin/jira (static, trimmed)
make smoke              # run the binary with JSON debug logs + secret-leak scan (see docs/logging.md)
make e2e-boot           # real Jira 10.3 in Docker Compose, CLI diagnostics (no license)
make e2e E2E_LICENSE_FILE=lic.txt   # full e2e scenario (see e2e/README.md)
make docker
```

## Changing generated commands

1. Edit `specs/overlays/<profile>.yaml` (names, parameter fixes, exclusions) or `specs/risk/<profile>.yaml`.
2. `make spec-update generate`.
3. Review `generated/<pkg>/REPORT.md` diff (commands, risk, skipped).
4. Commit inputs and generated output together.

Never edit files under `generated/` (they start with `Code generated ... DO
NOT EDIT`). Hand-written code never goes there; the generator only deletes
files that carry the generated header.

## Tests

| Kind | Where |
|---|---|
| Unit | every `internal/*` package |
| Integration (httptest, TLS with throwaway CA) | `internal/cli/cli_test.go`, `internal/transport`, `internal/api`, `internal/auth/oauth` |
| Generation (fixture specs, determinism, conflicts, stale overlays, checksum pinning) | `generator/generator_test.go` |
| Committed output in sync | `generator/repo_test.go` |
| End-to-end, real Jira 10.3 (Docker Compose) | `e2e/` (`run.sh`, `scenario.sh`) |
| Security (token leakage, redirects, TLS defaults, confirmation) | spread across the above; see docs/security.md table |

## Contribution guidelines

- Keep changes scoped; one concern per pull request.
- `make vet test verify-generated` must pass.
- New runtime behaviour needs a test; security-relevant behaviour needs a test that fails without the fix.
- Do not add dependencies without a clear need (current direct deps: cobra, pflag, yaml.v3, go-keyring, x/oauth2, x/term, and kin-openapi for the generator only).
- Spec updates: include the manifest diff and the REPORT.md diff; call out new destructive/administrative operations.
- No Jira Cloud code until it is in scope (see docs/architecture.md, "Deployment abstraction").

## Releasing

Tag `vX.Y.Z`; build with `make build VERSION=vX.Y.Z` (the version is embedded
via `-ldflags`) and `make docker VERSION=vX.Y.Z`. Release automation is not
implemented yet (see PROGRESS.md).

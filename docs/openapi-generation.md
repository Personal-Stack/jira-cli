# OpenAPI generation

## Inputs (`specs/`)

| Path | Purpose |
|---|---|
| `specs/manifest.yaml` | Profiles; every input file is pinned by sha256; spec versions must equal `info.version` |
| `specs/official/` | Atlassian specifications (currently Jira Software DC 10.3.26) |
| `specs/custom/` | Organisation specifications (additional operations) |
| `specs/overlays/` | Explicit corrections and naming (see below) |
| `specs/risk/` | Business risk metadata |

## Commands

```bash
make spec-validate      # run the full pipeline, write nothing
make generate           # regenerate generated/ (go generate ./generated/...)
make verify-generated   # fail if generated/ is stale (CI)
make spec-update        # re-pin versions/checksums after reviewing a change
go run ./cmd/jiragen update   # also downloads `source: remote` specs over HTTPS
```

A changed spec/overlay without `spec-update` fails with `checksum mismatch`.
That is intentional: every input change is an explicit manifest diff.

## Pipeline

1. **Load** each spec with kin-openapi. External `$ref`s are disabled (specs are untrusted input). Max 64 MiB.
2. **Rename paths** (`rename_paths`), then collect operations in sorted (path, method) order.
3. **Apply overlays**: exclusions, operationId renames, parameter fixes (`in`, `rename`, `drop`), command/group names, `body_required`. Every overlay key must match an operation, so stale overlays fail.
4. **Validate** the corrected documents (kin-openapi structural validation, examples excluded).
5. **Detect cross-spec conflicts**: the same `METHOD path` in two specs is an error until one is excluded.
6. **Map** each operation (below). Unsupported constructs skip the operation and are listed in `REPORT.md`; constructs that would produce wrong behaviour fail generation.
7. **Name** commands, check uniqueness and reserved names.
8. **Classify risk**.
9. **Emit** `generated/<pkg>/ops_<group>.go`, `profile.go`, `REPORT.md`, and `generated/registry.go`, formatted with `go/format`. Output is deterministic (sorted, no timestamps); `generator/repo_test.go` and `make verify-generated` enforce it.

## Mapping rules

| OpenAPI | CLI |
|---|---|
| first tag | command group (kebab-case): `issueLinkType` becomes `issue-link-type` |
| operationId | command name with the tag noun removed: `getIssue` in `issue` becomes `get`; a `_N` suffix is dropped with a warning |
| name collision in a group | all non-overlay names in the collision fall back to the full operationId; still colliding is an error |
| path parameter | positional argument, in path order, URL-escaped |
| query parameter | `--kebab-flag`; arrays are repeatable; enums validated; integers/numbers validated |
| header parameter | flag (`Authorization`, `Cookie`, `Content-Type`... are rejected) |
| JSON body | `--file` / `--stdin`; syntax, top-level type and required top-level properties validated |
| multipart body | `--file`, sent as the declared field, with `X-Atlassian-Token: no-check` |
| other media types | raw `--file`/`--stdin` with `--content-type` |
| `startAt` + `maxResults` (GET) | `--all`, `--max-pages` |
| `security` | ignored: authentication is per instance |
| `servers` | ignored: base URL is per instance |

## Unsupported constructs (skipped and reported)

Cookie parameters, `content`-encoded parameters, object-typed query
parameters (unless aliased via `schema_aliases`), nested arrays, operations
without operationId or tags, request bodies without a media type.

## Results for Jira Software DC 10.3.26

433 operations in, **430 commands** out, 3 excluded (`/auth/1/session` cookie
login). Defects in the official spec fixed by overlay (each is a reviewed
entry in `specs/overlays/datacenter-10.3.yaml`):

- 8 duplicate operationIds (invalid OpenAPI), resolved by renaming 15 operations.
- 2 pairs of conflicting path templates (`{permission-id}`/`{permissionId}`, `{key}`/`{attributeKey}`).
- 4 parameter fixes: 2 query parameters declared `in: path`, 1 duplicated path parameter, 1 rename following a path rename.
- `StringList` declared as an empty object but sent as a comma-separated string (blocked 16 operations including JQL search until aliased).
- 14 write operations whose `requestBody.required` is missing (e.g. `createIssue`, `editIssue`, `addComment`).
- 83 command names set by overlay (4 resolve collisions, 79 improve unclear names such as `search search` becoming `issue search`); 40 names derived from `_N` operationIds remain as report warnings.

See `generated/datacenter103/REPORT.md` for the full command table.

## Adding a custom specification

See `examples/custom-spec.yaml`. Add it under `specs:` of a profile, run
`make spec-update generate`, review `REPORT.md`, and add risk entries for any
non-read operation whose HTTP method understates its risk.

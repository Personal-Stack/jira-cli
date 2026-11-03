# Changelog

## Unreleased

- `jira setup-sbx` validates before writing: checks first (sbx, binary, secrets), each answer checked when given (Jira reachable), `sbx env plan` on a scratch copy with the agent kit's name taken from sbx, the PAT secret settled; then files. A known setup is one confirmation. Through the Docker wrapper the sbx steps run on the host.
- Ctrl+C: the first press stops the command cleanly (exit 130), the second exits immediately.
- `jira setup-sbx` (guided, or with flags) and `docs/sandbox.md`: run an agent (OpenCode by default) in a Docker Sandbox with jira-cli, the agent skill and a read-only config. The Jira PAT is an sbx secret that the sandbox proxy injects, so it never enters the sandbox.
- Release workflow: the image smoke test runs each platform by its own manifest digest (the v0.1.0 run failed on arm64).

## v0.1.0 (2026-11-03)

First release.

### CLI
- 430 commands generated at build time from the official Jira Software Data Center 10.3.26 OpenAPI specification, with explicit overlays for the spec's defects and readable command names.
- Multiple instances, strict configuration (no secrets in the config file), JSON/YAML/table output.
- Authentication: personal access tokens and OAuth 2.0 (authorization code + PKCE); credentials from mounted files, environment variables, the OS keyring, or an opt-in file store.
- Transport: TLS 1.2+, custom CA bundles (PEM or DER), mTLS, proxies, same-origin redirect policy, idempotency-aware retries.
- Safety: risk classification of every operation, read-only mode, confirmation for destructive/administrative operations, `--dry-run`.
- Diagnostics: `doctor` (layered checks; anonymous 401 from production Jira treated as normal), `certs fetch` (downloads and verifies a server's CA chain), structured redacted logging with request correlation.
- Discovery: `commands list|describe`, `capabilities`, `spec status`; an Agent Skill in `skills/jira`.

### Distribution
- One installer, `install.sh`, for Linux, macOS and Windows (Git Bash): checksum-verified, user-local, PATH set up; a Docker wrapper (`install.sh --docker`, not on Git Bash).
- Multi-arch container image `ghcr.io/personal-stack/jira-cli` (linux/amd64, linux/arm64).

### Verification status
- Read-only verification against a production Jira Data Center instance: passed.
- Write operations are tested against fakes only (no Data Center license available for the full e2e run).
- Not yet exercised on real infrastructure: OAuth 2.0 against Data Center, the macOS Keychain and Windows installer CI jobs, the release workflow itself.

### License
- Apache License 2.0. The Atlassian specification is third-party material (see NOTICE).

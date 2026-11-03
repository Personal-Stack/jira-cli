# Changelog

## v0.2.0 (2026-11-03)

### Docker Sandboxes: `jira setup-sbx`
- New command `jira setup-sbx` (guided in a terminal, or with flags; `make sbx` from a clone). It sets up a Docker Sandboxes (sbx) environment in which an AI agent (OpenCode by default) uses jira-cli. The Jira PAT never enters the sandbox: it is an sbx secret, the sandbox only sees a sentinel, and the sbx proxy sets the `Authorization` header for the Jira host only. See `docs/sandbox.md`.
- Writes, in the project:
  - `tmp/.sbx/jira-cli` (`--kit-dir`): a local sbx kit with the Linux `jira`, an egress rule and the credential;
  - `sbxenv.yaml`, with jira-cli's kit as the only kit;
  - `.opencode/skills/jira`: the agent skill, with a section naming the binary's path.

  Also writes `config.yaml` and `bashrc` in `~/.config/jira-cli-sbx` (created once, then yours to edit).
- Stops at the first problem, and nothing is written before everything has passed:
  - checks first: sbx installed, a Linux binary, existing secrets;
  - each answer checked when given, including Jira reachability;
  - `sbx env plan` on a scratch copy;
  - the PAT secret settled (stored via sbx if needed).
- Answers are saved; a known setup takes one confirmation. The Linux binary is this `jira`, a build from a clone, or the matching GitHub release (SHA-256 verified).
- Docker wrapper:
  - asks for the project on the host;
  - mounts the project and the settings read-write for `setup-sbx`;
  - runs the sbx steps on the host afterwards.

### CLI
- Ctrl+C: the first press stops the command cleanly, cancelling requests, child processes and prompts; the second exits immediately. Exit code 130, error layer `interrupted`. Previously Ctrl+C was ignored while a prompt waited for input.

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

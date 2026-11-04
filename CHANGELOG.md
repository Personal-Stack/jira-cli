# Changelog

## Unreleased

### Install, upgrade, setup
- One stable install command, no GitHub CLI needed: `curl -fsSL https://github.com/Personal-Stack/jira-cli/releases/latest/download/install.sh | sh` (options after `sh -s --`). Piped, the script never mistakes the current directory for a clone.
- New `jiracli upgrade`: downloads the latest release (or `--version`) for this platform, verifies it against `SHA256SUMS`, runs it once, then replaces the installed binary in place. `--check` only reports. It refuses inside the Docker wrapper and for development builds (unless `--version`), and leaves the binary untouched when anything fails. jiracli still never checks for updates on its own.
- `jiracli config setup` (guided) now continues with the token and the connection: it offers to store the token (hidden, verified against Jira, asked again when refused) and to run `doctor`. Both are optional; end of input stops there without an error.
- In a terminal, a command that finds no stored token offers to store it, like the existing offer for a missing configuration. `auth` commands report the problem instead.
- `jiracli doctor` starts with an `installation` check (warnings only): this binary's path and version, another `jiracli` earlier on PATH, a missing PATH entry, and jira-cli's old `jira` command still on PATH.
- `install.sh --help` no longer says the default name is `jira`.
- The docs no longer describe the repository and the image as private: both are public, and `docker login` is not needed.

### Installer
- Upgrading from v0.2.0 or earlier now removes the old `jira` command (the native binary or the Docker wrapper) and its shell completions from the install folder. Before this, `jira` kept running the old version next to the new `jiracli`. Only jira-cli's own `jira` is removed: it is recognised by its module path or the wrapper's header, and a `jira` from another project is kept. The PATH block stays, because `jiracli` uses it.
- The next steps printed after installing start with `jiracli config setup`.

## v0.3.0 (2026-11-04)

### Breaking: the command is now `jiracli`
- The binary, the Docker wrapper and the image entrypoint are renamed from `jira` to `jiracli`, so commands such as `jiracli config setup` cannot be read as setting up Jira itself. Archives contain `jiracli` (`jiracli.exe` on Windows); the sbx kit puts it at `tmp/.sbx/jira-cli/bin/jiracli` and `/usr/bin/jiracli`.
- Unchanged: the configuration folder `~/.config/jira-cli`, the keyring service `jira-cli`, the `JIRA_*` environment variables, the sbx secret `jira`, the skill name `jira`, the repository and the image path `ghcr.io/personal-stack/jira-cli`. Existing configurations and stored tokens keep working.
- Migration:
  - first remove the old command: `sh install.sh --uninstall --name jira`. Do it before installing, because `--uninstall` also removes the PATH block that both installs share;
  - then install v0.3.0 (`sh install.sh`, or `sh install.sh --docker` for the wrapper);
  - re-run `jiracli setup-sbx` in each sandbox project, so the kit, the skill and the agent context name the new binary.

### Configuration: `jiracli config setup`
- New command `jiracli config setup`. It creates the configuration in the expected place, creating the folder if needed, or adds or updates an instance (`--instance`). The place is `--config`, else `$JIRA_CONFIG`, else `~/.config/jira-cli/config.yaml`.
- Guided in a terminal, with flags otherwise (`--base-url`, `--credential-ref`, `--ca-bundle`, `--allow-writes`, `--no-default`).
- Each answer is checked when given: the Jira URL must be reachable, and a CA file must hold certificates.
- Updates happen in place: comments, other instances and settings such as timeouts and proxy are kept. The result is validated before it is written.
- A command run without a configuration offers to create it in a terminal, and names `jiracli config setup` otherwise.

### Docker Sandboxes: `jiracli setup-sbx`
- The agent comes from `~/.sbxenv.yaml` when it sets one. sbx merges that file into every environment, and an agent kit there accepts no other agent name. The Docker wrapper mounts the file read-only.
- When sbx still reports `agent kit name "X"`, X is used, saved and checked again, both in setup-sbx and in the Docker wrapper's host check. v0.2.0 stopped with sbx's error instead.
- sbx's own agent and command names (`opencode`, `claude`, `codex`, `cursor`, `env`, `run`, …) are refused as sandbox names, because sbx resolves those words itself.

### Project
- The project presents itself as **Personal Stack jira-cli**, part of the Personal Stack GitHub organization: README, `LEGAL.md`, `NOTICE`, `jiracli --help`, the image title and the release title.
- `LEGAL.md`: legal notice and terms of use:
  - unofficial, not affiliated with Atlassian;
  - built from public sources only;
  - user responsibilities;
  - no telemetry;
  - no warranty.

  It is referenced from the README, `NOTICE`, `jiracli --help`, the image description and the release notes, and shipped in the archives and the image.
- `PROGRESS.md` is removed; the release notes are in this file.

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

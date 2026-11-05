# Changelog

## v0.4.1 (2026-11-05)

Installer fixes, found installing the Docker wrapper on macOS.

### Install
- **`curl … | sh -s -- --docker` installed nothing on macOS.** macOS's `sh` is bash, which reads a piped script line by line; the installer's check of the new wrapper runs `docker run -i`, which read the rest of the script from the same pipe, so the install stopped silently before replacing anything. The installer is now read to the end before it runs, and the commands it starts no longer read its stdin. Linux (dash) was not affected; the installer tests now also run it piped through bash.
- **When the new command fails its check, its error is shown.** It was hidden behind "does not run on this machine", which made a Docker or image problem look like a platform problem.
- **Reinstalling right after `--uninstall` in the same terminal** skipped the PATH block (the folder was still on that terminal's PATH), so new terminals did not find `jiracli`. The block is now written whenever no startup or profile file adds the folder.
- **Accurate error messages:** a failed image pull says that the image is public and points to `docker logout ghcr.io` and Docker Desktop's proxy settings, instead of asking to log in to a "private registry"; failed downloads no longer mention a private repository. The Docker wrapper's own errors say `jiracli:`, not `jira:`.

### Upgrading
`jiracli upgrade` from v0.4.0. With the Docker wrapper on macOS, run the installer again: the v0.4.0 installer is the one that stopped.

## v0.4.0 (2026-11-04)

From nothing to a working CLI in two commands (the install one-liner, then `jiracli config setup`), and `jiracli upgrade` to stay current.

### Install
- **One install command.** `curl -fsSL …/releases/latest/download/install.sh | sh` always gets the latest release's installer. No GitHub CLI and no login are needed: the repository and the image are public. Options go after `sh -s --` (`| sh -s -- --docker`). Piped, the script never takes the current directory for a clone.
- **Tab completion by default** for bash, zsh and fish (`--no-completions` to skip it). In zsh it now also works when `compinit` runs earlier in `.zshrc` (oh-my-zsh, prezto) and when `~/.local/bin` was already on PATH; before, the completion was installed but never loaded in those cases.
- **The old `jira` is cleaned up.** Installing over v0.2.0 or earlier removes jira-cli's old `jira` command (binary or Docker wrapper) and its completions from the install folder; before, `jira` kept running the old version next to `jiracli`. It is recognised by its module path or the wrapper's header, so a `jira` from another project is kept. The PATH block stays.
- The installer's next steps point to `jiracli config setup`, and `--help` no longer says the default name is `jira`.

### Upgrade
- **New `jiracli upgrade`.** It downloads the latest release (or `--version`, also an older one) for this platform, checks it against `SHA256SUMS`, runs the new binary once, and only then replaces the installed one, at the same path. `--check` only reports. Nothing changes when any step fails.
- It refuses inside the Docker wrapper (re-run the installer with `--docker` instead) and for development builds unless `--version` is given.
- jiracli still never checks for updates on its own; it downloads only when you run `install.sh`, `upgrade` or `setup-sbx`.

### Setup
- **`jiracli config setup` ends with a working CLI.** After writing the configuration it offers to store the token (hidden input, verified against Jira, asked again when Jira refuses it) and to run `doctor`. Both are optional; if the answers end there, it stops without an error and prints the remaining steps.
- **A missing token is offered, like a missing configuration.** In a terminal, a command that finds no stored token offers to store it. `auth` commands report the problem instead.
- **`https://` is added** to a Jira URL typed without a scheme (`jira.company.internal/jira`), in the questions and in `--base-url`. `http://` is still refused, not rewritten.
- **Tab completes paths** in the guided questions: the company CA file (folders and `.pem`, `.crt`, `.cer`, `.cert`, `.der` files; a second Tab lists the choices) and the project folder of `setup-sbx`. Shell completion completes the path flags too: `--ca-bundle` with certificate files; `--project`, `--settings`, `--kit-dir`, `--source-dir` with folders; `--config`, `--log-file`, `--binary-file`, `certs fetch --out` with files.

### Diagnostics
- **`jiracli doctor` checks the installation first** (warnings only): this binary's path and version, another `jiracli` earlier on PATH, a missing PATH entry, and jira-cli's old `jira` still on PATH, with the command to fix each.

### Releases and docs
- **Release notes like these:** what's new from this file, install and upgrade commands pinned to the version, a download table, checksum and image verification, and links to the docs at the tag. A release tag without a section here now fails before anything is built.
- The docs no longer describe the repository and the image as private, and no longer ask for `docker login`.
- The agent skill tells agents to leave `upgrade`, `config setup`, `auth login` and `setup-sbx` to the user.

### Upgrading from v0.3.0
Run the install command once (v0.3.0 has no `upgrade` yet); from then on, `jiracli upgrade`. Configuration and stored tokens are unchanged.

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

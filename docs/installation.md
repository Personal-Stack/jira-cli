# Installation

Step-by-step first setup (including exporting your company CA from the
browser) is in [getting-started.md](getting-started.md). This page is the
installer reference.

The CLI is a single static binary named `jira`. Installers put it in a
per-user directory (no admin rights) and add that directory to `PATH`.

| Platform | Installer | Default location | PATH change |
|---|---|---|---|
| Linux, macOS | `scripts/install.sh` (POSIX sh) | `~/.local/bin/jira` | marked block in `~/.bashrc` (+ `~/.bash_profile` on macOS), `~/.zshrc`, fish `conf.d/jira-cli.fish`, or `~/.profile` |
| Linux, macOS, WSL (with Docker) | `scripts/install.sh --docker` (wrapper, see docs/docker.md) | `~/.local/bin/jira` (shell script) | same as Linux/macOS |
| Windows | `scripts/install.ps1` (PowerShell 5.1 / 7) | `%LOCALAPPDATA%\Programs\jira-cli\bin\jira.exe` | user `PATH` (registry; new terminals) |

Every download is verified against the release's `SHA256SUMS` before
anything is installed. Non-HTTPS URLs are refused (except `localhost`
mirrors used in tests).

## macOS: use the native binary (Keychain, no CA files)

On macOS the native `jira` uses the **Keychain** for both:

- **TLS trust.** Certificates are verified by macOS itself (Go's platform verifier) against the System and login keychains, including company CAs that IT or MDM installed. Leave `tls.ca_bundle` **out** of the config. Any `ca_bundle` you do set is only an extra fallback.
- **Tokens.** `jira auth login` stores the PAT in the login keychain (service `jira-cli`, account `<credential_ref>`). The library writes through `/usr/bin/security -i` on stdin, so the token never appears in the process list.

```bash
sh install.sh                      # not --docker
```

```bash
jira auth login
```

```bash
jira doctor                        # "tls": ok, issuer = your company CA
```

Notes:

- The Docker wrapper cannot see the Keychain (Linux container), so it needs `ca_bundle` plus a token file. On a Mac, prefer the native binary; `install.sh --docker` warns about this.
- The installer removes the `com.apple.quarantine` flag after verifying the SHA-256, so Gatekeeper does not block the unsigned binary. Binaries are not code-signed or notarized yet.
- The Keychain item is created by the `security` tool, so other processes running as you can read it with `security find-generic-password -s jira-cli -w` without a prompt. That's the same exposure as a `0600` file, minus disk-level plaintext.
- Windows is analogous: the native binary verifies TLS with the Windows certificate store and stores tokens in Credential Manager.
- Verified by `scripts/test-macos-keychain.sh` on a real macOS runner in CI (`installers` workflow). It checks that a CA is rejected, then accepted once trusted in the System keychain with no `ca_bundle`, and that `auth login`/`status`/`logout` use the Keychain.

## The repository is private

Release assets need authentication. The installers try, in order:

1. a mirror (`--base-url` / `-BaseUrl`, or `JIRA_CLI_BASE_URL`), with layout `<url>/<tag>/<asset>` and `<url>/LATEST`;
2. the GitHub CLI, if `gh auth status` succeeds (`gh release download`);
3. `GITHUB_TOKEN` (a fine-grained token with *Contents: read* on this repository);
4. the public download URL (works only if the repository becomes public).

Getting the installer itself:

```bash
# Linux / macOS, with the GitHub CLI
gh release download -R Personal-Stack/jira-cli -p install.sh -D /tmp --clobber && sh /tmp/install.sh
# or from a clone
git clone https://github.com/Personal-Stack/jira-cli && sh jira-cli/scripts/install.sh
```

```powershell
# Windows
gh release download -R Personal-Stack/jira-cli -p install.ps1 -D $env:TEMP --clobber
powershell -ExecutionPolicy Bypass -File $env:TEMP\install.ps1
```

## Options

| install.sh | install.ps1 | Meaning |
|---|---|---|
| `--version v0.2.0` | `-Version v0.2.0` | specific release (default: latest) |
| `--dir DIR` | `-InstallDir DIR` | install directory (`JIRA_CLI_INSTALL_DIR`) |
| `--name jira-dc` | `-Name jira-dc` | binary name, if another `jira` (go-jira, ankitpokhrel/jira-cli) is installed |
| `--no-modify-path` | `-NoModifyPath` | do not touch shell files / user PATH |
| `--completions` | | bash, zsh and fish completions |
| `--base-url URL` | `-BaseUrl URL` | internal mirror (air-gapped networks) |
| `--archive FILE` | `-Archive FILE` | offline install from a downloaded archive (`SHA256SUMS` next to it is checked) |
| `--from-source [DIR]` | | build with Go from a clone (`make install`) |
| `--docker [--image IMG]` | | install the Docker wrapper instead of the binary |
| `--uninstall` | `-Uninstall` | remove binary, completions and PATH entry (config and keyring entries are kept) |

The installers warn if another `jira` earlier on `PATH` would shadow this one.

## From source

```bash
make install                 # ~/.local/bin/jira (+ PATH)
make install PREFIX=/usr/local   # /usr/local/bin/jira (needs write access)
make uninstall
```

## Releases

Pushing a tag `vX.Y.Z` runs `.github/workflows/release.yml`: tests, then
`scripts/dist.sh` builds `jira-cli_<ver>_<os>_<arch>.tar.gz|zip` for
linux/darwin/windows × amd64/arm64 plus `SHA256SUMS`, and publishes them with
`install.sh` and `install.ps1` as a GitHub release.

## Verification

| Check | Where |
|---|---|
| 26 installer scenarios (bash/zsh/fish/sh, idempotency, exact rc restore on uninstall, completions, custom name, offline archive, tampered archive, non-HTTPS refusal, from source) | `scripts/test-install.sh` under `dash`; CI on Ubuntu and macOS |
| Windows install, persisted user PATH visible to a new process, idempotency, tampered archive, uninstall | CI on `windows-latest` with PowerShell 7 and Windows PowerShell 5.1 |
| shellcheck | CI |

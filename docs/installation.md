# Installation

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

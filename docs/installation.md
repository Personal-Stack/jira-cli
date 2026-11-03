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
| Windows (Git Bash, MSYS2, Cygwin) | `scripts/install.sh` | `~/.local/bin/jira.exe` | marked block in `~/.bashrc` (+ `~/.bash_profile` if it exists); Git Bash only |

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

## Windows (Git Bash)

There is no PowerShell installer. Run `install.sh` from Git Bash (ships with
Git for Windows, including `unzip` and `sha256sum`):

```bash
sh install.sh --archive jira-cli_<version>_windows_amd64.zip
```

- It installs `jira.exe` to `~/.local/bin` (`C:\Users\<you>\.local\bin`) and adds that directory to `PATH` **for Git Bash only**. To use `jira` from cmd or PowerShell, add that folder to your user PATH yourself (Settings > System > About > Advanced system settings > Environment Variables); the installer prints the Windows path.
- `--docker` is refused on Git Bash (path and TTY translation break the wrapper). Use the native binary, or run the installer inside WSL, where it behaves as on Linux.
- The native binary verifies TLS with the Windows certificate store and stores tokens in Credential Manager.
- Not tested in CI (Windows runners cost twice the Linux minutes). The Windows branch (zip, `.exe`, platform check, tamper rejection, PATH block, uninstall) was exercised on Linux with a `uname` shim reporting `MINGW64_NT`; no real Windows machine has run it yet.

## The repository is private

Release assets need authentication. The installers try, in order:

1. a mirror (`--base-url`, or `JIRA_CLI_BASE_URL`), with layout `<url>/<tag>/<asset>` and `<url>/LATEST`;
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

## Options

| install.sh | Meaning |
|---|---|
| `--version v0.2.0` | specific release (default: latest) |
| `--dir DIR` | install directory (`JIRA_CLI_INSTALL_DIR`) |
| `--name jira-dc` | binary name, if another `jira` (go-jira, ankitpokhrel/jira-cli) is installed |
| `--no-modify-path` | do not touch shell files |
| `--completions` | bash, zsh and fish completions |
| `--base-url URL` | internal mirror (air-gapped networks) |
| `--archive FILE` | offline install from a downloaded archive (`SHA256SUMS` next to it is checked) |
| `--from-source [DIR]` | build with Go from a clone (`make install`) |
| `--docker [--image IMG]` | install the Docker wrapper instead of the binary (not on Git Bash) |
| `--uninstall` | remove binary, completions and PATH entry (config and keyring entries are kept) |

The installer warns if another `jira` earlier on `PATH` would shadow this one.

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
`install.sh` as a GitHub release.

## Verification

| Check | Where |
|---|---|
| 26 installer scenarios (bash/zsh/fish/sh, idempotency, exact rc restore on uninstall, completions, custom name, offline archive, tampered archive, non-HTTPS refusal, from source) | `scripts/test-install.sh` under `dash`; CI on Ubuntu and macOS |
| Windows (Git Bash) branch: zip extraction, `jira.exe`, platform check, tampered archive, PATH block, `--docker` refusal, uninstall | locally on Linux with a `uname` shim; **no CI job, not run on real Windows** |
| shellcheck | CI |

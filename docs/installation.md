# Installation

Step-by-step first setup (including exporting your company CA from the
browser) is in [getting-started.md](getting-started.md). This page is the
installer reference.

The CLI is a single static binary named `jiracli`. Installers put it in a
per-user directory (no admin rights) and add that directory to `PATH`.

| Platform | Installer | Default location | PATH change |
|---|---|---|---|
| Linux, macOS | `scripts/install.sh` (POSIX sh) | `~/.local/bin/jiracli` | marked block in `~/.bashrc` (+ `~/.bash_profile` on macOS), `~/.zshrc`, fish `conf.d/jira-cli.fish`, or `~/.profile` |
| Linux, macOS, WSL (with Docker) | `scripts/install.sh --docker` (wrapper, see docs/docker.md) | `~/.local/bin/jiracli` (shell script) | same as Linux/macOS |
| Windows (Git Bash, MSYS2, Cygwin) | `scripts/install.sh` | `~/.local/bin/jiracli.exe` | marked block in `~/.bashrc` (+ `~/.bash_profile` if it exists); Git Bash only |

Every download is verified against the release's `SHA256SUMS` before
anything is installed. Non-HTTPS URLs are refused (except `localhost`
mirrors used in tests).

## macOS: use the native binary (Keychain, no CA files)

On macOS the native `jiracli` uses the **Keychain** for both:

- **TLS trust.** Certificates are verified by macOS itself (Go's platform verifier) against the System and login keychains, including company CAs that IT or MDM installed. Leave `tls.ca_bundle` **out** of the config. Any `ca_bundle` you do set is only an extra fallback.
- **Tokens.** `jiracli auth login` stores the PAT in the login keychain (service `jira-cli`, account `<credential_ref>`). The library writes through `/usr/bin/security -i` on stdin, so the token never appears in the process list.

```bash
sh install.sh                      # not --docker
```

```bash
jiracli auth login
```

```bash
jiracli doctor                        # "tls": ok, issuer = your company CA
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

- It installs `jiracli.exe` to `~/.local/bin` (`C:\Users\<you>\.local\bin`) and adds that directory to `PATH` **for Git Bash only**. To use `jiracli` from cmd or PowerShell, add that folder to your user PATH yourself (Settings > System > About > Advanced system settings > Environment Variables); the installer prints the Windows path.
- `--docker` is refused on Git Bash (path and TTY translation break the wrapper). Use the native binary, or run the installer inside WSL, where it behaves as on Linux.
- The native binary verifies TLS with the Windows certificate store and stores tokens in Credential Manager.
- Not tested in CI (Windows runners cost twice the Linux minutes). The Windows branch (zip, `.exe`, platform check, tamper rejection, PATH block, uninstall) was exercised on Linux with a `uname` shim reporting `MINGW64_NT`; no real Windows machine has run it yet.

## Getting the installer

The latest release's installer is always at the same URL:

```bash
curl -fsSL https://github.com/Personal-Stack/jira-cli/releases/latest/download/install.sh | sh
```

Options go after `sh -s --`, e.g. `| sh -s -- --docker` or
`| sh -s -- --version v0.3.0`. Piped, the script never uses a clone in the
current directory.

To read the script before running it (it's ~450 lines of POSIX sh):

```bash
curl -fsSLo /tmp/install.sh https://github.com/Personal-Stack/jira-cli/releases/latest/download/install.sh && less /tmp/install.sh
```

```bash
sh /tmp/install.sh
```

What is verified: every **archive** and the Docker wrapper are checked
against the release's `SHA256SUMS` before anything is installed. The
installer script itself is not signed; it comes over HTTPS from GitHub, like
any `curl | sh` installer.

Release assets come from, in order:

1. a mirror (`--base-url`, or `JIRA_CLI_BASE_URL`), with layout `<url>/<tag>/<asset>` and `<url>/LATEST`;
2. the GitHub CLI, if `gh auth status` succeeds;
3. `GITHUB_TOKEN`, if set (avoids GitHub's anonymous API rate limit of 60 requests per hour);
4. the public download URL.

## Upgrading

```bash
jiracli upgrade
```

It downloads the latest release for this platform, checks it against
`SHA256SUMS`, runs the new binary once, and only then replaces the installed
one in place (same path, same name). `--check` only reports whether a newer
release exists; `--version vX.Y.Z` installs that release, also an older one.
The configuration and stored tokens are not touched. jiracli never checks for
updates on its own.

- **Docker wrapper:** run the installer again with `--docker`; `upgrade` refuses inside the container and says so.
- **System folder** (e.g. `/usr/local/bin`): run it as the folder's owner (`sudo jiracli upgrade`).
- **Development build** (`make install`): `upgrade` refuses unless you pass `--version`.
- **From v0.2.0 or earlier**, where the command was `jira`: there is no `upgrade` yet, so run the installer. It removes the old `jira` it finds in the install folder.

## Options

| install.sh | Meaning |
|---|---|
| `--version v0.3.0` | specific release (default: latest) |
| `--dir DIR` | install directory (`JIRA_CLI_INSTALL_DIR`) |
| `--name NAME` | command name (default `jiracli`) |
| `--no-modify-path` | do not touch shell files |
| `--no-completions` | skip the bash, zsh and fish completions (installed by default; bash needs the bash-completion package) |
| `--base-url URL` | internal mirror (air-gapped networks) |
| `--archive FILE` | offline install from a downloaded archive (`SHA256SUMS` next to it is checked) |
| `--from-source [DIR]` | build with Go from a clone (`make install`) |
| `--docker [--image IMG]` | install the Docker wrapper instead of the binary (not on Git Bash) |
| `--uninstall` | remove binary, completions and PATH entry (config and keyring entries are kept) |

The installer warns if another `jiracli` earlier on `PATH` would shadow this one.

## From source

```bash
make install                 # ~/.local/bin/jiracli (+ PATH)
make install PREFIX=/usr/local   # /usr/local/bin/jiracli (needs write access)
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
| Windows (Git Bash) branch: zip extraction, `jiracli.exe`, platform check, tampered archive, PATH block, `--docker` refusal, uninstall | locally on Linux with a `uname` shim; **no CI job, not run on real Windows** |
| shellcheck | CI |

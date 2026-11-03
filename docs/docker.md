# Running the CLI from Docker (no Go, no native binary)

`install.sh --docker` installs a small `jira` wrapper script
(`scripts/jira-docker.sh`) on your PATH. Each `jira ...` runs
`docker run --rm` of the CLI image, so the only requirement on the machine
is Docker.

> **macOS users:** prefer the native binary (`sh install.sh` without
> `--docker`). It uses the Keychain for TLS trust and tokens; this wrapper
> cannot (see docs/installation.md, "macOS").

## Install

From a clone (builds `jira-cli:local`; Go runs inside the build container):

```bash
git clone -b claude/tender-bohr-rsigl2 https://github.com/Personal-Stack/jira-cli.git
```

```bash
sh jira-cli/scripts/install.sh --docker
```

From a release (pulls `ghcr.io/personal-stack/jira-cli:<version>`; the
registry is private, so log in first):

```bash
gh auth token | docker login ghcr.io -u <github-user> --password-stdin
```

```bash
gh release download -R Personal-Stack/jira-cli -p install.sh -D /tmp --clobber && sh /tmp/install.sh --docker
```

Then use it exactly like the native CLI:

```bash
jira config validate
```

```bash
jira auth login          # token stored in ~/.config/jira-cli/credentials/<ref> (0600)
```

```bash
jira doctor
```

Uninstall with `sh install.sh --uninstall`. The image stays; remove it with
`docker rmi`.

## Updating

The wrapper runs the image built from your clone, so CLI fixes arrive only
when you rebuild it:

```bash
cd jira-cli && git pull && sh scripts/install.sh --docker
```

## Company CA certificate

The container cannot see your OS certificate store or Keychain. Export the
company root CA from your browser
([getting-started.md, section 4](getting-started.md#4-company-certificate-only-if-needed)),
put it in `~/.config/jira-cli/` (the only directory shared with the
container), and reference it by absolute path in `tls.ca_bundle`.

## What the wrapper does

| Concern | Behaviour |
|---|---|
| Config | `~/.config/jira-cli` mounted read-write **at the same path**, so `ca_bundle`/`client_cert` paths in `config.yaml` work unchanged. Keep certificates in that directory, or list them in `JIRA_DOCKER_MOUNTS` |
| Credentials | `JIRA_CREDENTIAL_STORE=file`: `auth login` writes `~/.config/jira-cli/credentials/<ref>` (0600, dir 0700). Containers have no OS keyring. See docs/credentials.md for the security trade-off |
| Files in commands | current directory mounted **read-only** at the same path (`--file issue.json` works); `--log-file`/`JIRA_LOG_FILE` directories mounted read-write |
| Ownership | runs as your uid:gid, so files it writes are yours |
| Environment | `JIRA_*`, `HTTPS_PROXY`, `NO_PROXY` forwarded **by name** (`-e NAME`), so values (e.g. `JIRA_CREDENTIAL_<REF>`) never appear on the `docker` command line or in `ps` |
| Terminal | TTY only when stdin and stdout are terminals; piped/scripted use keeps stdout (JSON) and stderr separate |
| Exit codes | the CLI's exit code is returned unchanged |

## Settings

| Variable | Use |
|---|---|
| `JIRA_CLI_IMAGE` | image to run (default baked in at install time) |
| `JIRA_DOCKER_NETWORK` | e.g. `host` (Linux): needed when Jira or your proxy is reachable only via the host's `127.0.0.1`, or for VPN split-DNS that containers don't see |
| `JIRA_DOCKER_MOUNTS` | extra read-only paths, space-separated (e.g. `/etc/ssl/company-ca.pem`) |
| `JIRA_DOCKER_MOUNT_CWD=0` | do not mount the current directory |

## Measured trade-offs

| | Native binary | Docker wrapper |
|---|---|---|
| Start-up per command | milliseconds | ~0.7 s (measured: 710 ms average over 5 runs, Linux) |
| Credential storage | OS keyring | 0600 file |
| Network | host network | Docker network (`localhost` is the container; use `JIRA_DOCKER_NETWORK=host`) |
| Corporate CA | `ca_bundle` | `ca_bundle` under `~/.config/jira-cli` or `JIRA_DOCKER_MOUNTS` (the host trust store is not visible in the container) |
| Requirements | none | Docker daemon access (on Linux, the `docker` group is root-equivalent) |

## Verified

| Check | Result |
|---|---|
| `scripts/test-docker-wrapper.sh` (real container, unprivileged user): exit codes, stderr separation, file store modes/ownership, host-path CA bundle, relative `--file`, `--log-file`, env forwarding by name (docker argv inspected), logout | 19/19 pass |
| `scripts/test-install.sh` `--docker` scenarios (clone install, mirror install with wrapper checksum, tampered wrapper rejected, uninstall) | pass |
| Against real Jira 10.3 (e2e container): default network fails at TCP (`localhost` = container); with `JIRA_DOCKER_NETWORK=host` config/DNS/TCP/TLS pass with the CA from the host path | as expected |
| Multi-arch image (`linux/amd64`, `linux/arm64`) via cross-compilation | built; both binaries checked |
| macOS / Windows Docker Desktop | **not tested** |

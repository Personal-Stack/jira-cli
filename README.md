# jira-cli

A spec-driven command-line interface for **Jira Data Center**, written in Go.
Every API command is generated at build time from OpenAPI specifications;
authentication, TLS, retries and safety checks are implemented once, centrally.

- **430 commands** generated from the official Jira Software Data Center 10.3.26 specification
- PAT and OAuth 2.0 (PKCE) authentication; credentials from mounted files, env, or the OS keyring, never from config or arguments
- Custom CA bundles, mTLS, corporate proxies, strict redirect policy
- JSON (default), YAML and table output, with no fields dropped
- Risk-aware execution: read-only mode, confirmation for destructive/administrative operations, `--dry-run`
- Structured, redacted logging with per-run and per-request correlation IDs (`--log-level`, `--log-format json`, `--log-file`)
- Machine-readable discovery (`commands list`, `capabilities`) and an [Agent Skill](skills/jira/SKILL.md)

> **Status:** phases 1-3 and 5 are complete and tested against fakes. A Docker
> Compose environment with real Jira Software DC 10.3 (`e2e/`) boots and passes
> CLI diagnostics; the full scenario needs a Jira license. See
> [PROGRESS.md](PROGRESS.md) and [e2e/README.md](e2e/README.md). See
> [PROGRESS.md](PROGRESS.md).

## Install

Linux / macOS (installs to `~/.local/bin` and adds it to `PATH`):

```bash
gh release download -R Personal-Stack/jira-cli -p install.sh -D /tmp --clobber && sh /tmp/install.sh
```

Windows (installs to `%LOCALAPPDATA%\Programs\jira-cli\bin` and adds it to the user `PATH`):

```powershell
gh release download -R Personal-Stack/jira-cli -p install.ps1 -D $env:TEMP --clobber
powershell -ExecutionPolicy Bypass -File $env:TEMP\install.ps1
```

From a clone: `make install` (needs Go 1.27).
Only Docker on the machine: `sh scripts/install.sh --docker` installs a `jira`
wrapper that runs the CLI image ([docs/docker.md](docs/docker.md)).
The repository is private, so downloads use the GitHub CLI or `GITHUB_TOKEN`;
mirrors, offline archives and uninstall are covered in
[docs/installation.md](docs/installation.md).

## Configure

```bash
mkdir -p ~/.config/jira-cli
cp examples/config.yaml ~/.config/jira-cli/config.yaml   # edit base_url, credential_ref, ca_bundle
jira config validate
```

Minimal read-only instance:

```yaml
version: 1
default_instance: prod
instances:
  prod:
    deployment: datacenter
    base_url: https://jira.company.internal
    auth: {type: pat, credential_ref: jira-prod}
    tls: {ca_bundle: /etc/ssl/company-ca.pem}
    read_only: true
```

## Authenticate

```bash
jira auth login            # PAT: hidden prompt, verified against /rest/api/2/myself, stored in the OS keyring
jira auth status
jira doctor                # config, DNS, TCP, TLS, HTTP, credentials, auth
```

Headless (CI, containers): `JIRA_CREDENTIAL_FILE=/run/secrets/jira_pat` or
`JIRA_CREDENTIAL_<REF>`; see [docs/credentials.md](docs/credentials.md).

## Use

```bash
jira issue get ABC-123 --fields summary,status
jira issue search --jql 'project = ABC AND statusCategory != Done' --all -o table
jira issue create --file examples/issue.json --dry-run
jira issue delete ABC-123            # refused without --yes (exit 9)
jira commands list --group issue     # discover generated commands
jira commands describe issue create  # parameters, body schema, risk, provenance
```

## Docker

```bash
docker run --rm \
  -v ./config:/config:ro \
  -v ./secrets/jira_pat:/run/secrets/jira-prod:ro \
  jira-cli issue get ABC-123
```

## Documentation

| Topic | Document |
|---|---|
| Installation | [docs/installation.md](docs/installation.md) |
| Docker wrapper (no Go/binary on the machine) | [docs/docker.md](docs/docker.md) |
| Architecture | [docs/architecture.md](docs/architecture.md) |
| OpenAPI generation, overlays, spec updates | [docs/openapi-generation.md](docs/openapi-generation.md) |
| Authentication (PAT, OAuth) | [docs/authentication.md](docs/authentication.md) |
| Credentials | [docs/credentials.md](docs/credentials.md) |
| TLS, proxy, redirects, retries | [docs/secure-transport.md](docs/secure-transport.md) |
| Jira Data Center specifics | [docs/jira-datacenter.md](docs/jira-datacenter.md) |
| Agent integration | [docs/agent-integration.md](docs/agent-integration.md), [skills/jira/SKILL.md](skills/jira/SKILL.md) |
| Security and threat model | [docs/security.md](docs/security.md) |
| Development and contributing | [docs/development.md](docs/development.md) |
| Logging, tracing, CI artifacts | [docs/logging.md](docs/logging.md) |
| Troubleshooting | [skills/jira/references/troubleshooting.md](skills/jira/references/troubleshooting.md) |
| Progress, open items | [PROGRESS.md](PROGRESS.md) |

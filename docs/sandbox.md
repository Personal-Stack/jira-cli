# Docker Sandboxes (sbx)

Run an AI agent (OpenCode by default) in a [Docker Sandbox](https://docs.docker.com/ai/sandboxes/)
with jira-cli available, while the **Jira personal access token stays on your
machine**.

## How it works

`scripts/sbx-setup.sh` generates a local sbx kit and an `sbxenv.yaml`:

| Piece | Where | What |
|---|---|---|
| Kit `jira-cli` (mixin) | `<project>/.sbx/jira-cli/` | `/usr/local/bin/jira` (linux amd64 + arm64), the agent skill, an egress rule for your Jira host, the credential, and a shell hook |
| Environment | `<project>/sbxenv.yaml` | the agent, the kit, your settings mounted read-only (at the same path as on the host), `JIRA_CONFIG` / `JIRA_NO_KEYRING` / `JIRA_READ_ONLY`, and the credential binding |
| Settings | `~/.config/jira-cli-sbx/` (host) | `config.yaml` and `bashrc`. Created once and never overwritten, so edit them freely |
| Token | sbx credential store (host) | `sbx secret set jira` |

**The token.** Inside the sandbox, `JIRA_CREDENTIAL_<REF>` (for example
`JIRA_CREDENTIAL_JIRA_PROD`) holds only a sentinel value. The sandbox proxy
replaces the `Authorization` header with `Bearer <your PAT>` on requests to
your Jira host. The agent can't read the real token, even with `env` or `cat`.
This follows the kit spec's `credential@1` with `proxyManaged: true`.

## Set up

From a clone of jira-cli. It builds with Go if available, or otherwise extracts
the binary from the published image:

```bash
scripts/sbx-setup.sh --project /Users/x/Projects/my-work --base-url https://jira.company.internal
```

The script does the following:

1. Builds `jira` for linux/amd64 and linux/arm64 into the kit.
2. Writes the kit, `sbxenv.yaml`, and (first run only) `config.yaml` and `bashrc`.
3. Runs `sbx secret set jira` if that secret does not exist yet. sbx prompts for the PAT.
4. Runs `sbx env plan`, which previews the setup and creates nothing.

Then start the sandbox:

```bash
cd /Users/x/Projects/my-work && sbx env run
```

Check it from the host:

```bash
sbx env exec -- bash -lc 'jira doctor'
```

Expect `credentials` to show `env:JIRA_CREDENTIAL_JIRA_PROD` and `authentication` to be `ok`.

### Options you will likely need

| Option | Use |
|---|---|
| `--template IMAGE` | Your already configured image (e.g. from JFrog) instead of the agent's default (`sandboxOptions.template`) |
| `--agent-kit REF` | Compose a published agent kit (e.g. `sbx/opencode:<tag>`) instead of the built-in `opencode` |
| `--agent NAME` | The agent kit's name. If sbx says `agent "opencode" does not match agent kit name "X"`, re-run with `--agent X` |
| `--skills-path DIR` | Where the agent reads skills (default `/home/agent/.config/opencode/skills`). The kit declares it because some agent kits don't, and sbx then refuses the bundled skill. `--no-skill` leaves the skill out |
| `--ca-bundle FILE` | Company CA. Copied into the settings folder and referenced by its path there |
| `--allow-writes` | Drop the read-only guard (default: `read_only: true` and `JIRA_READ_ONLY=1`) |
| `--binary image` | Take the binary from `ghcr.io/personal-stack/jira-cli` (no Go needed) |
| `--kit-only` | Your project already has an `sbxenv.yaml`: generate the kit and settings, then merge (below) |

`scripts/sbx-setup.sh --help` lists all options.

A private image registry needs pull credentials in `sbxenv.yaml`. For example:

```yaml
registries:
  company.jfrog.io:
    username: {value: me@company.com}
    secret: {command: "security find-generic-password -s jfrog -w"}
```

## Making changes

| You change | Then |
|---|---|
| `~/.config/jira-cli-sbx/config.yaml` or `bashrc` | Nothing. It is a live read-only mount; new shells and commands see it |
| jira-cli code (this clone) | Re-run the script (rebuilds the binary and the kit), then recreate the sandbox: `sbx env rm && sbx env run` |
| The token | `sbx secret set jira` |

The settings are read-only inside the sandbox, so the agent can't change its
own configuration.

## Existing `sbxenv.yaml`

The script refuses to overwrite an `sbxenv.yaml` it did not generate. Run it
with `--kit-only`, which writes the kit and the settings only, and merge these
entries into your file:

```yaml
kits:
  - ./.sbx/jira-cli
additionalWorkspaces:
  - {path: /Users/x/.config/jira-cli-sbx, readOnly: true}   # appears at the same path inside
env:
  JIRA_CONFIG: /Users/x/.config/jira-cli-sbx/config.yaml
  JIRA_NO_KEYRING: "1"
  JIRA_READ_ONLY: "1"
bindings:
  jira:
    apiKey:
      domains: [jira.company.internal]
```

## Verification status

**Tested in this repository:**

- The generated kit validates against the sandbox-kit-spec v3 JSON schemas, including each capability's schema. Broken variants of it are rejected.
- The overlay builds for linux/amd64 and linux/arm64 with the expected files and modes.
- With the environment the sandbox provides, the binary reports the credential as `env:JIRA_CREDENTIAL_JIRA_PROD`, `config validate` passes, and a delete is refused with exit code 9.

**Not tested:** a real `sbx` run. That covers the proxy header injection, the
`bash.bashrc` hook, the built-in `opencode` agent name, and `sandboxOptions.template`
with your image. Run the check above after the first `sbx env run`. If
`authentication` fails while `credentials` is `ok`, the header is not being
injected for your host. Check the binding domains with `sbx env plan`.

**TLS:** the sandbox proxy terminates TLS with its own CA, which the sandbox
trusts through the system store. jira-cli always keeps the system roots and
adds `ca_bundle` on top. Whether the proxy, on the host, trusts your company's
Jira certificate depends on the host; if it does not, the error is reported by
the proxy, not by jira-cli.

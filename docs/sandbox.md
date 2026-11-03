# Docker Sandboxes (sbx)

Run an AI agent (OpenCode by default) in a [Docker Sandbox](https://docs.docker.com/ai/sandboxes/)
with jira-cli available, while the **Jira personal access token stays on your
machine**.

## How it works

`jira setup-sbx` generates a local sbx kit and an `sbxenv.yaml`:

| Piece | Where | What |
|---|---|---|
| Kit `jira-cli` (mixin) | `<project>/.sbx/jira-cli/` | `/usr/local/bin/jira` (linux, for your machine's architecture; linked as `/usr/bin/jira` at startup, because some images leave `/usr/local/bin` off `PATH`), an egress rule for your Jira host, the credential, and a shell hook |
| Environment | `<project>/sbxenv.yaml` | the agent, the kit, your settings mounted read-only (at the same path as on the host), `JIRA_CONFIG` / `JIRA_NO_KEYRING` / `JIRA_READ_ONLY`, and the credential binding |
| Skill | `<project>/.opencode/skills/jira/` | the agent skill (embedded in `jira`), as an OpenCode project skill (`--no-skill` to skip). Re-runs refresh it unless you edited it |
| Settings | `~/.config/jira-cli-sbx/` (host) | `config.yaml` and `bashrc`. Created once and never overwritten, so edit them freely |
| Token | sbx credential store (host) | `sbx secret set jira` |

**The token.** Inside the sandbox, `JIRA_CREDENTIAL_<REF>` (for example
`JIRA_CREDENTIAL_JIRA_PROD`) holds only a sentinel value. The sandbox proxy
replaces the `Authorization` header with `Bearer <your PAT>` on requests to
your Jira host. The agent can't read the real token, even with `env` or `cat`.
This follows the kit spec's `credential@1` with `proxyManaged: true`.

## Set up

With `jira` installed (any install method), run the guided setup:

```bash
jira setup-sbx
```

From a clone, `make sbx` does the same with the code of the clone. Run
without flags in a terminal, it asks for:

| Question | Notes |
|---|---|
| Project | The folder the agent works in. Mounted read/write; `sbxenv.yaml` goes there |
| Jira base URL | `https://`, with the context path if any. Remembered in `config.yaml` |
| Agent kit reference | Empty for a built-in agent, e.g. `opencode` |
| Agent name | For a kit, the name sbx reports (`agent kit name "..."`) |
| Sandbox image | Your configured image, e.g. from JFrog. Empty for the agent's default |
| Custom sandbox name | Empty for sbx's default, `<agent>-<project folder>` |
| Company CA file | Usually empty |
| Read-only | Default yes |
| Copy the skill | Into `<project>/.opencode/skills/jira`; default yes |
| Advanced | sbx secret name, credential ref, settings folder, binary source |

It shows a summary and asks for confirmation before writing anything. Then:

1. It gets a Linux `jira` for the sandbox (see [The Linux binary](#the-linux-binary)).
2. It writes the files.
3. It runs `sbx secret set jira` if that secret doesn't exist yet (sbx prompts for the PAT).
4. It runs `sbx env plan` and, if you said so, `sbx env run`.

Re-running it for the same project offers the previous answers as defaults.
In each prompt, Enter keeps the value shown in brackets and `-` clears an
optional one.

Without a terminal, or for scripting, pass options instead:

```bash
jira setup-sbx --project /Users/x/Projects/my-work --base-url https://jira.company.internal --agent devex-opencode --name jira-sbx
```

Check the result from the host:

```bash
sbx env exec -- bash -lc 'jira doctor'
```

Expect `credentials` to show `env:JIRA_CREDENTIAL_JIRA_PROD` and `authentication` to be `ok`.

### With the Docker wrapper (`install.sh --docker`)

`jira setup-sbx` works through the wrapper too. The container only sees what
the wrapper mounts, so:

1. **Run it from the project folder.** The wrapper mounts the current folder
   (or `--project`) and the settings folder read-write for `setup-sbx`. In the
   prompts, keep the suggested project: other folders don't exist in the container.
2. **The binary comes from the container itself.** It is already Linux, on your
   machine's architecture, so no Go or download is needed.
3. **sbx is not in the container.** When it finishes, it prints the host
   steps. Run them on your machine:

```bash
sbx secret ls
```

If `jira` is not listed, add it (sbx prompts for the PAT):

```bash
sbx secret set jira
```

```bash
cd /Users/x/Projects/my-work && sbx env run
```

A wrapper and image from before this command don't have `setup-sbx`. Update
both from a clone with `git pull && sh scripts/install.sh --docker`.

### Options you will likely need

| Option | Use |
|---|---|
| `--template IMAGE` | Your already configured image (e.g. from JFrog) instead of the agent's default (`sandboxOptions.template`) |
| `--agent-kit REF` | Compose a published agent kit (e.g. `sbx/opencode:<tag>`) instead of the built-in `opencode` |
| `--agent NAME` | The agent kit's name. If sbx says `agent "opencode" does not match agent kit name "X"`, re-run with `--agent X` |
| `--name NAME` | Custom sandbox name (`name:` in `sbxenv.yaml`) |
| `--ca-bundle FILE` | Company CA. Copied into the settings folder and referenced by its path there |
| `--allow-writes` | Drop the read-only guard (default: `read_only: true` and `JIRA_READ_ONLY=1`) |
| `--binary SOURCE` | Where the Linux binary comes from (see below) |
| `--kit-only` | Your project already has an `sbxenv.yaml`: generate the kit and settings, then merge (below) |

`jira setup-sbx --help` lists all options. The global `--dry-run` shows the
plan and the `sbxenv.yaml` without writing anything.

### The Linux binary

The sandbox is a Linux VM with your machine's architecture, so it needs a
Linux `jira`. A macOS `jira` can't run there. With `--binary auto` (the
default), the first of these that works is used:

| Source | When |
|---|---|
| `self` | `jira` itself is Linux on the same architecture |
| `go` | You run it inside a jira-cli clone with Go installed (`--source-dir` to point at one) |
| `release` | Your `jira` is a release (e.g. `v0.1.0`): downloads `jira-cli_<version>_linux_<arch>.tar.gz` from the GitHub release and checks it against the release's `SHA256SUMS` |
| `image` | Docker can pull `ghcr.io/personal-stack/jira-cli:<version>` |

`--binary-file FILE` uses a binary you provide. Every binary is checked to be
a Linux executable for the right architecture before it is used.

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
| jira-cli (new version, or code in a clone) | Re-run `jira setup-sbx` (or `make sbx` in the clone), then recreate the sandbox: `sbx env rm && sbx env run` |
| The skill | It is a normal project file in `.opencode/skills/jira`. Edit it there, or refresh it by re-running `jira setup-sbx` (`--force` if you edited it) |
| The token | `sbx secret set jira` |

The settings are read-only inside the sandbox, so the agent can't change its
own configuration.

## Existing `sbxenv.yaml`

`jira setup-sbx` refuses to overwrite an `sbxenv.yaml` it did not generate. Run it
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

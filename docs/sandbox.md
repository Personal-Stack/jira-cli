# Docker Sandboxes (sbx)

Run an AI agent (OpenCode by default) in a [Docker Sandbox](https://docs.docker.com/ai/sandboxes/)
with jira-cli available, while the **Jira personal access token stays on your
machine**.

## Set up

```bash
jira setup-sbx
```

Run it without flags in a terminal and it asks:
- **Project:** the folder the agent works in.
- **Jira base URL:** checked for reachability as you enter it.
- **Agent:** its sbx name, e.g. `opencode`. A name only: an image or kit reference is refused, so it can never end up as a kit.
- **Sandbox name:** a new project gets `<agent>-<folder>-jira`.
- **Read-only:** default yes.

After a summary and **Proceed**, it writes the files and prints the next
steps. From a jira-cli clone, `make sbx` runs the same thing with the clone's
code.

Your answers are saved, so the next run for any project shows them and asks
one question: **Use these settings?**

For scripts, use flags instead:

```bash
jira setup-sbx --project ~/Projects/my-work --base-url https://jira.company.internal --agent devex-opencode
```

| Flag | Use |
|---|---|
| `--agent` | The agent's sbx name (default `opencode`) |
| `--kit-dir` | Where jira-cli's kit goes in the project (default `tmp/.sbx/jira-cli`) |
| `--name` | Sandbox name |
| `--secret` | sbx secret holding the PAT (default `jira`) |
| `--allow-writes` | Let the agent write to Jira (default: read-only) |
| `--no-skill` | Don't install the skill |
| `--force` | Replace an `sbxenv.yaml` jira-cli didn't generate, or an edited skill |
| `--skip-checks` | Only write the files (no sbx, reachability or secret checks) |
| `--run` | Start the sandbox at the end |
| `--binary-file`, `--source-dir` | Where the Linux `jira` for the sandbox comes from (see below) |

`jira setup-sbx --help` lists them all. The global `--dry-run` shows the
`sbxenv.yaml` it would write.

## How it makes sure it works

sbx decides what works: the agent names, the secrets and the environment
format. So setup-sbx asks sbx rather than you. It stops at the first problem,
and nothing is written before everything has passed:

1. **Checks, before any question:**
   - sbx is installed and responds;
   - a Linux `jira` for the sandbox is available;
   - which sbx secrets already exist.
2. **Each answer is checked when you give it:** the project folder exists, the
   Jira host accepts a connection, and names and references are valid. A wrong
   answer is asked again.
3. **sbx checks the complete environment** with `sbx env plan` on a scratch
   copy, before your project is touched. sbx merges your `~/.sbxenv.yaml`
   in, and that file may bring an agent kit. If sbx then reports that the agent must be the kit's name
   (`agent kit name "..."`), that name is used, saved and checked again.
   Any other sbx error is shown as is.
4. **The PAT:** the sbx secret exists, or sbx stores it now. sbx prompts for
   it, so it never passes through jira-cli.
5. **Only then** the files are written:

| What | Where |
|---|---|
| Kit: the Linux `jira` (`bin/jira`), egress to your Jira host, the credential, a `bashrc` hook | `<project>/tmp/.sbx/jira-cli/` (`--kit-dir`; binary gitignored) |
| Environment: agent, jira-cli's kit (the only kit), name, settings mount, `JIRA_*` variables, credential binding | `<project>/sbxenv.yaml` |
| The agent skill, with a "This sandbox" section naming the binary's path | `<project>/.opencode/skills/jira/` |
| `config.yaml` and `bashrc`: created once, then yours to edit | `~/.config/jira-cli-sbx/` (mounted read-only at the same path) |
| Your answers, for the next run | `~/.config/jira-cli-sbx/setup.yaml` |

**One kit.** `sbxenv.yaml` lists only jira-cli's kit. A re-run rewrites it,
which also removes entries that earlier versions added (e.g. an image given
as the agent). Saved defaults of those versions keep only the agent's name.

**The kit folder.** jira-cli's kit lives in `tmp/.sbx/jira-cli`, or wherever
`--kit-dir` points, inside the project. If it moves, the copy setup-sbx
generated at the old location is removed, including `.sbx/jira-cli` and
`temp/.sbx/jira-cli` from earlier versions.

**The binary.** The project is mounted in the sandbox at the same path, so
the agent runs `<project>/tmp/.sbx/jira-cli/bin/jira`. The skill and the agent
instructions say so, and the agent doesn't search `PATH`. (The kit also
installs it as `/usr/bin/jira`, but not every setup applies that layer.)

**No custom sandbox image.** `sandboxOptions.template` made the sandbox fail
to start ("failed to run sandbox container"), and `sbx env plan` does not
catch that. The agent's own image is used.

**The token.** Inside the sandbox, `JIRA_CREDENTIAL_<REF>` holds only a
sentinel. The sbx proxy sets `Authorization: Bearer <PAT>` on requests to
your Jira host, so the agent can't read the token, not even with `env` or `cat`.

### With the Docker wrapper (`install.sh --docker`)

`jira setup-sbx` works the same through the wrapper:
- The wrapper asks for the project on your machine first, then mounts it and
  the settings folder.
- sbx isn't in the container, so the wrapper runs the sbx steps on your
  machine right after: the secret, then `sbx env plan`.
- In the container, the binary is the container's own `jira`, which is
  already Linux.

A wrapper and image from before this command don't have it. Update both
from a clone:

```bash
git pull && sh scripts/install.sh --docker
```

### The Linux binary

The sandbox is a Linux VM with your machine's architecture. The first of
these that works is used, and each is checked to be a Linux executable for
that architecture:

1. `jira` itself, when it is Linux, e.g. in the Docker wrapper.
2. A build from the jira-cli clone you run it in (or `--source-dir`), if Go is installed.
3. The GitHub release matching your `jira` version, checked against its `SHA256SUMS`.

`--binary-file FILE` uses a binary you provide.

## First start: approvals

On the first `sbx env run`, sbx asks you to approve what the environment
requests: egress to your Jira host, the `jira` secret for that host, and the
read-only settings mount. Messages like "3 need approval" are this check, not
an error. sbx asks again only if those change.

## Making changes

| You change | Then |
|---|---|
| `~/.config/jira-cli-sbx/config.yaml` or `bashrc` | Nothing: it is a live mount |
| The jira-cli version | Re-run `jira setup-sbx` (one confirmation), then `sbx env rm; sbx env run` (`env rm` says "not found" when there is no sandbox yet; that's fine) |
| The skill | Edit `.opencode/skills/jira`; re-runs keep your edits (`--force` replaces them) |
| The token | `sbx secret set jira` |

## If the sandbox does not start

`failed to run sandbox container` doesn't say why. In the project folder:

```bash
sbx env plan
```

```bash
sbx ls
```

```bash
sbx diagnose
```

A sandbox with the same name from an earlier `sbx run` collides with the
environment; remove it with `sbx rm NAME`. `sbx ls` also shows sandboxes left
under an old name (e.g. from before a re-run changed the name); remove those
with `sbx rm NAME` as well.

`sbx env rm` answering `sandbox '...' not found` only means this
environment's sandbox does not exist (yet): nothing to remove.

## Verification status

**Tested here:**
- Go tests with a fake sbx covering every early stop (nothing is written when one triggers), defaults across projects, the cleanup of an image-as-kit left by an earlier version, and the release download with its checksum.
- The Docker wrapper test, with a fake sbx on the host.
- The generated kit validates against the sandbox-kit-spec v3 schemas, and its image layer builds.
- On a real sbx (your runs), an earlier version of the files worked: CLI, token injection and skill.

**Not tested:** this version of the command against a real `sbx`.

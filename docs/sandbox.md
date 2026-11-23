# Docker Sandboxes (sbx)

Run an AI agent (OpenCode by default) in a [Docker Sandbox](https://docs.docker.com/ai/sandboxes/)
with jira-cli available, while the **Jira personal access token stays on your
machine**.

## Set up

```bash
jiracli setup-sbx
```

Run it without flags in a terminal and it asks:
- **Project:** the folder the agent works in.
- **Jira base URL:** checked for reachability as you enter it.
- **Agent:** its sbx name, e.g. `opencode`. If your `~/.sbxenv.yaml` sets an agent, that one is used: sbx merges that file into every environment and accepts no other. A name only: an image or kit reference is refused, so it can never end up as a kit.
- **Sandbox name:** a new project gets `<agent>-<folder>-jira`. sbx's agent and command names (`opencode`, `claude`, `codex`, `cursor`, `env`, `run`, …) are refused: sbx resolves those words itself.
- **Read-only:** default yes.
- **Skills folder:** where the `jira` skill goes, relative to the project (default `.opencode/skills`, where OpenCode looks for project skills). If your agent reads skills from elsewhere, e.g. `.opencode/local/skills`, give that. The answer is saved for the next project.

After a summary and **Proceed**, it writes the files and prints the next
steps. From a jira-cli clone, `make sbx` runs the same thing with the clone's
code.

Your answers are saved, so the next run for any project shows them and asks
one question: **Use these settings?**

For scripts, use flags instead:

```bash
jiracli setup-sbx --project ~/Projects/my-work --base-url https://jira.company.internal --agent devex-opencode \
  --skills-dir .opencode/local/skills
```

| Flag | Use |
|---|---|
| `--agent` | The agent's sbx name (default `opencode`) |
| `--kit-dir` | Where jira-cli's kit goes in the project (default `tmp/.sbx/jira-cli`) |
| `--skills-dir` | Skills folder the `jira` skill goes into, relative to the project (default `.opencode/skills`) |
| `--name` | Sandbox name |
| `--secret` | sbx secret holding the PAT (default `jira`) |
| `--allow-writes` | Let the agent write to Jira (default: read-only) |
| `--no-skill` | Don't install the skill |
| `--force` | Replace an `sbxenv.yaml` jira-cli didn't generate, or an edited skill |
| `--skip-checks` | Only write the files (no sbx, reachability or secret checks) |
| `--run` | Start the sandbox at the end |
| `--binary-file`, `--source-dir` | Where the Linux `jiracli` for the sandbox comes from (see below) |

`jiracli setup-sbx --help` lists them all. The global `--dry-run` shows the
`sbxenv.yaml` it would write.

## How it makes sure it works

sbx decides what works: the agent names, the secrets and the environment
format. So setup-sbx asks sbx rather than you. It stops at the first problem,
and nothing is written before everything has passed:

1. **Checks, before any question:**
   - sbx is installed and responds;
   - a Linux `jiracli` for the sandbox is available;
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
| Kit: the Linux `jira` (`bin/jiracli`), egress to your Jira host, the credential, a `bashrc` hook | `<project>/tmp/.sbx/jira-cli/` (`--kit-dir`; binary gitignored) |
| Environment: agent, jira-cli's kit (the only kit), name, settings mount, `JIRA_*` variables, credential binding | `<project>/sbxenv.yaml` |
| The agent skill, with a "This sandbox" section naming the binary's path | `<project>/.opencode/skills/jira/` (`--skills-dir`) |
| `config.yaml` and `bashrc`: created once, then yours to edit | `~/.config/jira-cli-sbx/` (mounted read-only at the same path) |
| Your answers, for the next run | `~/.config/jira-cli-sbx/setup.yaml` |

**One kit.** `sbxenv.yaml` lists only jira-cli's kit. A re-run rewrites it,
which also removes entries that earlier versions added (e.g. an image given
as the agent). Saved defaults of those versions keep only the agent's name.

**The kit folder.** jira-cli's kit lives in `tmp/.sbx/jira-cli`, or wherever
`--kit-dir` points, inside the project. If it moves, the copy setup-sbx
generated at the old location is removed, including `.sbx/jira-cli` and
`temp/.sbx/jira-cli` from earlier versions.

**The skill folder.** If the skills folder changes, the copy an earlier
setup put at the old place (or at the default `.opencode/skills/jira`) is
removed when it is unedited. An edited one is kept, with a warning: remove it
yourself if the agent should not see two `jira` skills.

**The binary.** The project is mounted in the sandbox at the same path, so
the agent runs `<project>/tmp/.sbx/jira-cli/bin/jiracli`. The skill and the agent
instructions say so, and the agent doesn't search `PATH`. (The kit also
installs it as `/usr/bin/jiracli`, but not every setup applies that layer.)

**No custom sandbox image.** `sandboxOptions.template` made the sandbox fail
to start ("failed to run sandbox container"), and `sbx env plan` does not
catch that. The agent's own image is used.

**The token.** Inside the sandbox, `JIRA_CREDENTIAL_<REF>` holds only a
sentinel. The sbx proxy sets `Authorization: Bearer <PAT>` on requests to
your Jira host, so the agent can't read the token, not even with `env` or `cat`.

### With the Docker wrapper (`install.sh --docker`)

`jiracli setup-sbx` works the same through the wrapper:
- The wrapper asks for the project on your machine first, then mounts it and
  the settings folder.
- sbx isn't in the container, so the wrapper runs the sbx steps on your
  machine right after: the secret, then `sbx env plan`.
- In the container, the binary is the container's own `jiracli`, which is
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

1. `jiracli` itself, when it is Linux, e.g. in the Docker wrapper.
2. A build from the jira-cli clone you run it in (or `--source-dir`), if Go is installed.
3. The GitHub release matching your `jiracli` version, checked against its `SHA256SUMS`.

`--binary-file FILE` uses a binary you provide.

## Every sandbox: `jiracli sbx kit`

`setup-sbx` writes one project's `sbxenv.yaml`, and refuses a project that
already has its own. `jiracli sbx kit` instead puts jira-cli in **every**
sandbox of this machine, next to whatever the project's `sbxenv.yaml` lists:

```bash
jiracli sbx kit --base-url https://jira.company.internal
```

sbx merges `~/.sbxenv.yaml` under every environment file: lists such as `kits`
are concatenated, mappings such as `bindings` merge by key, and the project
file wins on single values. So the command adds three entries to that file and
leaves the rest of it, comments included, as it was (the previous version is
kept as `~/.sbxenv.yaml.bak`):

```yaml
kits:
  # jira-cli for jira.company.internal (jiracli sbx kit; re-run it to change)
  - source: /home/you/.config/jira-cli-sbx/kit
    args:
      host: "jira.company.internal"
      port: "443"
      secret: "jira"
      readOnly: "1"
additionalWorkspaces:
  # jira-cli: the kit's binary, config.yaml and bashrc (read-only, same path)
  - path: /home/you/.config/jira-cli-sbx
    readOnly: true
bindings:
  jira:
    apiKey:
      domains: [jira.company.internal]
```

**The kit is generic; the values are kit args.** The Jira host, port, sbx
secret and read-only mode are create-time kit args (`${{ kit.args.* }}`). sbx
expands them into the kit's network policy and credential injection before it
validates and enforces them, and the arg patterns limit what can be passed:
`host` must be a bare host name, and `readOnly` must be `"0"` or `"1"`. A
project can override an arg by listing the same kit source again in its own
`sbxenv.yaml`, because entries with the same source merge their args, later
winning. For example, to allow writes in one project:

```yaml
kits:
  - source: /home/you/.config/jira-cli-sbx/kit
    args: {readOnly: "0"}
```

For another Jira, also pass `config` (a jira-cli config whose `base_url` is
that Jira) and a binding for the host. `config.yaml` is what jiracli reads,
while `host` only opens egress and token injection for that host.

**Bindings are approvals, not secrets.** sbx copies the `bindings` entry into
`~/.config/sbx/credentials.yaml`. Without it, sbx asks on the first start, and
in `--detach` or CI the token is withheld. The PAT itself is the sbx secret
`jira` (`sbx secret set jira`), and the sandbox only sees a sentinel.

**Where things are.** The kit (`kit/`, with `bin/jiracli`, the skill and the
kit files), `config.yaml` and `bashrc` live in `~/.config/jira-cli-sbx`
(`--settings`), which is mounted read-only at the same path. The kit's image
layer also installs `/usr/bin/jiracli` and the skill
(`agent-skill@1`, optional), but earlier tests found that layer not applied,
so the agent instructions name the mounted path.

**Starting a sandbox.** `~/.sbxenv.yaml` applies to the `sbx env` commands
(`sbx env run`, `sbx env create`, ...); `sbx run <agent>` does not apply
environment files. A sandbox gets its kits when it is created: recreate one
that existed before (`sbx env rm; sbx env run`). sbx reads `~/.sbxenv.yaml`
only: a `~/.sbxenv.yml` is ignored, and both commands warn about one.

**v2 and v3 kits.** sbx does not combine v3 kits with v1/v2 kits. The built-in
agents (`opencode`, `claude`, ...) are v3; an agent kit of your own or your
company's may be v2 (`spec.yaml` with `schemaVersion: "2"`). `jiracli sbx kit`
writes the kit as v3 (`jira-cli.yaml`) and, if `sbx env plan` refuses it, as v2
(`spec.yaml`, same args and behavior, no image layer); `--kit-schema 2|3` picks
one. `setup-sbx`'s per-project kit is v3 only.

**On PATH.** A startup hook links `/usr/bin/jiracli` to the mounted binary when
the image layer did not install it, so `jiracli` works in every shell of the
sandbox either way.

**Read-only.** The `readOnly` arg sets `JIRA_READ_ONLY`. A `config.yaml`
created by `sbx kit` leaves `read_only: false` so that the arg decides. An
existing one is kept, and its `read_only: true` still wins; the command warns
about that when you pass `--allow-writes`.

**Checks.** As with `setup-sbx`, nothing is written before everything passed.
sbx checks the merged file (`sbx env plan <scratch>`; passing a path makes sbx
skip the real `~/.sbxenv.yaml`), and the token is stored as an sbx secret
before any file is written. Changing `~/.sbxenv.yaml` needs a yes, or `--yes`
without a terminal. `--print` writes the kit and prints the entries instead of
editing the file, and `--remove` takes the kit and the mount out again
(bindings are kept). Existing sandboxes get the kit only when recreated
(`sbx env rm; sbx env run`).

**Not together with `setup-sbx` in a project.** sbx refuses two kits that
provide `jira-cli`, and two kits that declare the same credential service. A
project whose `sbxenv.yaml` was generated by `setup-sbx` lists its own kit:
remove that entry (or the file) to use the global kit there. `setup-sbx`
refuses to run while `~/.sbxenv.yaml` lists the global kit. `sbx kit` does not
run in the Docker wrapper's container: it needs sbx and your home folder.

## First start: approvals

On the first `sbx env run`, sbx asks you to approve what the environment
requests: egress to your Jira host, the `jira` secret for that host, and the
read-only settings mount. Messages like "3 need approval" are this check, not
an error. sbx asks again only if those change.

## Making changes

| You change | Then |
|---|---|
| `~/.config/jira-cli-sbx/config.yaml` or `bashrc` | Nothing: it is a live mount |
| The jira-cli version | Re-run `jiracli setup-sbx` (one confirmation), then `sbx env rm; sbx env run` (`env rm` says "not found" when there is no sandbox yet; that's fine) |
| The skill | Edit `<skills folder>/jira`; re-runs keep your edits (`--force` replaces them) |
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

- `jiracli sbx kit`: Go tests with a fake sbx (editing `~/.sbxenv.yaml` keeps other entries and comments, the run is idempotent, `--print`, `--remove`, nothing is written when sbx rejects the environment or `--yes` is missing). The generated kit was loaded with the sandbox-kit-spec v3 reference library (`spec.ResolveArgs`, `ExpandCreateArgs`, `ValidateEffective`): args expand into the network policy and credential, a missing `host`, a bad host and `readOnly: "2"` are refused.

**Test it against a real sbx:** `scripts/test-sbx-real.sh --base-url https://jira.company.internal [--agent <your agent>]`
runs on your machine, because it needs local sandboxes (KVM, or Apple silicon). It
runs `jiracli sbx kit` for real, against a temporary settings folder. It edits your
`~/.sbxenv.yaml` and restores it on exit, also on failure or Ctrl+C. It starts two
sandboxes, removes them again, and sends only read requests to Jira. It answers:

| | Question | How |
|---|---|---|
| Q1 | Does sbx accept the settings mount from `~/.sbxenv.yaml`? | `sbx env plan` and `sbx env create`, then the binary at the mounted path, read-only |
| Q2 | Is the kit's image layer applied? | `/usr/bin/jiracli`, `/usr/share/jira-cli/skills/jira`, and the skill in an agent skill folder (reported, not required) |
| Q3 | Do the agent-context @1 and @2 declarations compose? | `sbx env create` succeeds, and the guidance is found in a file in the sandbox |
| | Kit args, credential, read-only | `JIRA_*` variables, `jiracli doctor` (token injection and authentication), `issue create` refused with exit 9 |
| | A project overriding an arg | A second sandbox lists the same kit source with `readOnly: "0"`: one kit, `JIRA_READ_ONLY=0`, a write `--dry-run` is allowed |

Use `--agent` with the agent you work with: the `shell` agent reads neither skills nor
agent guidance. The report ends up in `~/jcli-sbx-real-report.txt`.

**Not tested:** this version of the commands against a real `sbx`; the script above is how to do it. For `sbx kit`, that includes whether sbx accepts the settings mount from `~/.sbxenv.yaml` (the docs restrict only `workspace` there) and whether it applies the kit's image layer (`/usr/bin/jiracli`, the bundled skill).

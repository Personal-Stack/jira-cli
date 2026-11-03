#!/usr/bin/env bash
# Set up a Docker Sandboxes (sbx) environment in which an agent (OpenCode by
# default) can use jira-cli, with the Jira personal access token held by sbx.
#
# The PAT never enters the sandbox. It lives in the sbx credential store on
# the host (`sbx secret set jira`); inside the sandbox
# JIRA_CREDENTIAL_<REF> holds a sentinel, and the sandbox proxy replaces the
# Authorization header with the real token on requests to your Jira host.
#
# It writes:
#   <project>/.sbx/jira-cli/    a local sbx kit (mixin): the linux jira binary
#                               (amd64 + arm64), the network
#                               allow rule, the credential and the shell hook
#   <project>/sbxenv.yaml       the environment `sbx env run` reads
#   <project>/.opencode/skills/jira   the agent skill (OpenCode project skill)
#   <settings>/config.yaml      jira-cli config, mounted read-only (same path inside the sandbox)
#   <settings>/bashrc           your shell additions, sourced in the sandbox
#
# Usage:
#   scripts/sbx-setup.sh --project ~/Projects/my-work --base-url https://jira.company.internal
#   cd ~/Projects/my-work && sbx env run
#
# Re-run it after changing jira-cli (rebuilds the binary and the kit). The
# settings files are created once and never overwritten; edit them freely.
set -euo pipefail

say() { printf '%s\n' "$*"; }
info() { printf '  %s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)
PROJECT="$PWD"
BASE_URL=""
INSTANCE="prod"
CRED_REF="jira-prod"
SERVICE="jira"
SETTINGS="${XDG_CONFIG_HOME:-$HOME/.config}/jira-cli-sbx"
AGENT="opencode"
AGENT_SET=0
NO_SKILL=0
AGENT_KIT=""
TEMPLATE=""
CA_BUNDLE=""
READ_ONLY=1
BINARY="auto"
IMAGE="${JIRA_CLI_IMAGE:-ghcr.io/personal-stack/jira-cli:latest}"
SKIP_SECRET=0
FORCE=0
KIT_ONLY=0
RUN=0

usage() {
  sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'
  cat <<'USAGE'

Options:
  --project DIR        workspace the agent works in; sbxenv.yaml goes here (default: current dir)
  --base-url URL       Jira base URL (required unless <settings>/config.yaml exists)
  --instance NAME      instance name in config.yaml (default: prod)
  --credential-ref REF credential_ref in config.yaml (default: jira-prod)
  --service NAME       sbx secret / credential service name (default: jira)
  --settings DIR       host folder with config.yaml and bashrc (default: ~/.config/jira-cli-sbx)
  --agent NAME         sbx agent: a built-in name, or the NAME of the agent kit as sbx reports it
                       ("agent kit name ..."). Default: opencode, or derived from --agent-kit
  --agent-kit REF      compose this agent kit instead of a built-in agent (e.g. sbx/opencode:<tag>);
                       if sbx rejects the derived name, pass --agent with the name it reports
  --no-skill           do not copy the jira skill into <project>/.opencode/skills/jira
  --template IMAGE     sandbox image to use instead of the agent's default (your configured image)
  --ca-bundle FILE     company CA (PEM or DER) for config.yaml; copied into <settings>
  --allow-writes       do not set read_only / JIRA_READ_ONLY (default: read-only)
  --binary go|image    build the linux binary with Go from this clone, or extract it from
                       the published image (default: go if available, else image)
  --image REF          image for --binary image (default: ghcr.io/personal-stack/jira-cli:latest)
  --skip-secret        do not check or create the sbx secret
  --force              overwrite an existing <project>/sbxenv.yaml
  --kit-only           write the kit and settings, but not sbxenv.yaml (you merge it; see docs/sandbox.md)
  --run                run `sbx env run` at the end
  -h, --help           this help
USAGE
}

while [ $# -gt 0 ]; do
  case "$1" in
    --project) PROJECT="${2:?--project needs a value}"; shift ;;
    --base-url) BASE_URL="${2:?--base-url needs a value}"; shift ;;
    --instance) INSTANCE="${2:?--instance needs a value}"; shift ;;
    --credential-ref) CRED_REF="${2:?--credential-ref needs a value}"; shift ;;
    --service) SERVICE="${2:?--service needs a value}"; shift ;;
    --settings) SETTINGS="${2:?--settings needs a value}"; shift ;;
    --agent) AGENT="${2:?--agent needs a value}"; AGENT_SET=1; shift ;;
    --no-skill) NO_SKILL=1 ;;
    --agent-kit) AGENT_KIT="${2:?--agent-kit needs a value}"; shift ;;
    --template) TEMPLATE="${2:?--template needs a value}"; shift ;;
    --ca-bundle) CA_BUNDLE="${2:?--ca-bundle needs a value}"; shift ;;
    --allow-writes) READ_ONLY=0 ;;
    --binary) BINARY="${2:?--binary needs a value}"; shift ;;
    --image) IMAGE="${2:?--image needs a value}"; shift ;;
    --skip-secret) SKIP_SECRET=1 ;;
    --force) FORCE=1 ;;
    --kit-only) KIT_ONLY=1 ;;
    --run) RUN=1 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option: $1 (see --help)" ;;
  esac
  shift
done

# ---------- validate inputs ----------

[ -d "$PROJECT" ] || die "project directory not found: $PROJECT"
PROJECT=$(cd "$PROJECT" && pwd)
# Absolute path without creating anything yet (the settings must stay out of the project).
case "$SETTINGS" in /*) ;; *) SETTINGS="$PWD/$SETTINGS" ;; esac
case "$SETTINGS/" in "$PROJECT"/*) die "--settings must be outside the project ($SETTINGS is inside $PROJECT)" ;; esac
mkdir -p "$SETTINGS"
SETTINGS=$(cd "$SETTINGS" && pwd)
# sbx mounts extra workspaces at the same absolute path as on the host, and
# this path is written into shell snippets and YAML: keep it plain.
case "$SETTINGS" in *[!A-Za-z0-9._/-]*) die "--settings path may only contain letters, digits, '.', '_', '-' and '/': $SETTINGS" ;; esac

case "$INSTANCE" in *[!A-Za-z0-9_-]*|"") die "invalid --instance '$INSTANCE'" ;; esac
case "$CRED_REF" in *[!A-Za-z0-9._-]*|"") die "invalid --credential-ref '$CRED_REF'" ;; esac
case "$SERVICE" in *[!a-z0-9-]*|-*|"") die "invalid --service '$SERVICE' (lowercase-kebab)" ;; esac
case "$BINARY" in auto|go|image) ;; *) die "--binary must be go or image" ;; esac

if [ -n "$AGENT_KIT" ] && [ "$AGENT_SET" = 0 ]; then
  # Handle = last path segment without tag or digest (sbx/opencode:1.2@sha256:... -> opencode).
  h="${AGENT_KIT%%@*}"; h="${h##*/}"; AGENT="${h%%:*}"
fi
case "$AGENT" in *[!A-Za-z0-9._-]*|"") die "invalid agent '$AGENT'" ;; esac

CONFIG="$SETTINGS/config.yaml"
if [ -z "$BASE_URL" ]; then
  [ -f "$CONFIG" ] || die "--base-url is required (no $CONFIG yet)"
  BASE_URL=$(sed -n 's/^[[:space:]]*base_url:[[:space:]]*\([^[:space:]#]*\).*/\1/p' "$CONFIG" | head -n1)
  [ -n "$BASE_URL" ] || die "no base_url in $CONFIG; pass --base-url"
fi
case "$BASE_URL" in
  https://*) ;;
  *) die "--base-url must be https://" ;;
esac
hostport="${BASE_URL#https://}"; hostport="${hostport%%/*}"
case "$hostport" in *@*) die "base_url must not contain credentials" ;; esac
JIRA_HOST="${hostport%%:*}"
JIRA_PORT=443
[ "$hostport" != "$JIRA_HOST" ] && JIRA_PORT="${hostport##*:}"
case "$JIRA_HOST" in *[!A-Za-z0-9.-]*|"") die "cannot parse a host name from $BASE_URL" ;; esac
case "$JIRA_PORT" in *[!0-9]*|"") die "cannot parse a port from $BASE_URL" ;; esac

# JIRA_CREDENTIAL_<REF>: upper-case, everything but [A-Z0-9] becomes '_'
# (same rule as internal/credentials.EnvName).
CRED_ENV="JIRA_CREDENTIAL_$(printf '%s' "$CRED_REF" | LC_ALL=C tr '[:lower:]' '[:upper:]' | sed 's/[^A-Z0-9]/_/g')"

HAVE_SBX=0; command -v sbx >/dev/null 2>&1 && HAVE_SBX=1
[ "$HAVE_SBX" = 1 ] || warn "sbx not found: files are generated, but the secret, plan and run steps are skipped (https://docs.docker.com/ai/sandboxes/)"

KIT="$PROJECT/.sbx/jira-cli"
ENVFILE="$PROJECT/sbxenv.yaml"
if [ "$KIT_ONLY" = 0 ] && [ -f "$ENVFILE" ] && [ "$FORCE" = 0 ] && ! grep -q '^# generated by jira-cli scripts/sbx-setup.sh' "$ENVFILE"; then
  die "$ENVFILE exists and was not generated by this script; use --kit-only and merge by hand (docs/sandbox.md), or --force"
fi

say "jira-cli sbx setup"
info "project:  $PROJECT"
info "settings: $SETTINGS"
info "jira:     $BASE_URL (host $JIRA_HOST, port $JIRA_PORT)"
info "agent:    $AGENT${AGENT_KIT:+ (kit $AGENT_KIT)}${TEMPLATE:+, image $TEMPLATE}"
info "token:    sbx secret '$SERVICE' -> $CRED_ENV (sentinel in the sandbox)"

# ---------- 1. linux binaries (amd64 + arm64) ----------

mkdir -p "$KIT/bin"
if [ "$BINARY" = auto ]; then
  if command -v go >/dev/null 2>&1 && [ -f "$REPO_ROOT/go.mod" ]; then BINARY=go; else BINARY=image; fi
fi

is_linux_elf() { # is_linux_elf FILE: ELF magic, so a macOS binary is never shipped
  [ "$(head -c 4 "$1" 2>/dev/null | od -An -c | tr -d ' \n')" = '177ELF' ]
}

VERSION=""
if [ "$BINARY" = go ]; then
  command -v go >/dev/null 2>&1 || die "--binary go needs Go (https://go.dev/dl/); or use --binary image"
  VERSION=$(cd "$REPO_ROOT" && git describe --tags --always --dirty 2>/dev/null || echo dev)
  say "Building jira $VERSION for linux/amd64 and linux/arm64 (Go, from $REPO_ROOT)"
  for arch in amd64 arm64; do
    (cd "$REPO_ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath \
      -ldflags "-s -w -X main.version=$VERSION" -o "$KIT/bin/jira-$arch" ./cmd/jira)
  done
else
  command -v docker >/dev/null 2>&1 || die "--binary image needs Docker; or install Go and use --binary go"
  say "Extracting jira from $IMAGE (linux/amd64 and linux/arm64)"
  for arch in amd64 arm64; do
    cid=$(docker create --platform "linux/$arch" "$IMAGE" 2>/dev/null) \
      || die "cannot pull $IMAGE for linux/$arch (private image? docker login ghcr.io; or use --binary go)"
    docker cp "$cid:/usr/local/bin/jira" "$KIT/bin/jira-$arch" > /dev/null
    docker rm "$cid" > /dev/null
  done
  VERSION=$(docker image inspect -f '{{ index .Config.Labels "org.opencontainers.image.version" }}' "$IMAGE" 2>/dev/null || true)
fi
for arch in amd64 arm64; do
  is_linux_elf "$KIT/bin/jira-$arch" || die "$KIT/bin/jira-$arch is not a Linux binary"
  chmod 755 "$KIT/bin/jira-$arch"
done
# Kit versions are semver; anything else (dev builds, commits) becomes 0.0.0.
KIT_VERSION=$(printf '%s' "$VERSION" | sed -n 's/^v\{0,1\}\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\).*/\1/p')
[ -n "$KIT_VERSION" ] || KIT_VERSION="0.0.0"
info "binaries: $KIT/bin/jira-{amd64,arm64} ($VERSION)"

# ---------- 2. the kit ----------

rm -rf "$KIT/skill"   # older versions bundled the skill in the kit
printf 'bin/\n' > "$KIT/.gitignore"

cat > "$KIT/profile.sh" <<EOF
# Installed by the jira-cli sbx kit as /etc/profile.d/jira-cli.sh.
# Login shells source the editable host file, mounted at the same path.
if [ -r $SETTINGS/bashrc ] && [ -z "\${JIRA_CLI_BASHRC_LOADED:-}" ]; then
  JIRA_CLI_BASHRC_LOADED=1
  . $SETTINGS/bashrc
fi
EOF

cat > "$KIT/jira-cli.dockerfile" <<'EOF'
# syntax=docker/dockerfile:1
# Overlay for the jira-cli sbx kit: a static binary and a
# profile hook. Built by sbx on create; the context is this directory.
FROM scratch
ARG TARGETARCH
COPY --chmod=0755 bin/jira-${TARGETARCH} /usr/local/bin/jira
COPY --chmod=0644 profile.sh /etc/profile.d/jira-cli.sh
EOF

if [ "$READ_ONLY" = 1 ]; then
  mode_text="Read-only: every command that is not a read is refused before anything is sent (exit code 9). Do not try to work around it; ask the user."
else
  mode_text="Writes are allowed. Preview every change with --dry-run first; destructive commands need --yes, which you must not add without the user's explicit approval."
fi

cat > "$KIT/jira-cli.yaml" <<EOF
# syntax=docker/sandbox-kit:3
# Generated by jira-cli scripts/sbx-setup.sh. Re-run the script instead of
# editing this file.
schemaVersion: "3"
displayName: jira-cli
description: >-
  jira-cli for Jira Data Center at $JIRA_HOST, with the personal access token
  held by sbx (service "$SERVICE") and injected by the sandbox proxy.
sourceUrl: https://github.com/Personal-Stack/jira-cli
licenses: [Apache-2.0]

kind: mixin
version: "$KIT_VERSION"
provides: ["jira-cli@$KIT_VERSION"]
dockerfile: jira-cli.dockerfile

capabilities:
  - type: com.docker.sandbox/network-policy@1
    config:
      runtime:
        allow:
          - $JIRA_HOST:$JIRA_PORT

  # The PAT stays on the host. $CRED_ENV holds a sentinel; the proxy sets
  # the real "Authorization: Bearer <PAT>" on requests to $JIRA_HOST.
  - type: com.docker.sandbox/credential@1
    description: Jira personal access token for jira-cli
    config:
      service: $SERVICE
      phase: runtime
      apiKey:
        name: $CRED_ENV
        proxyManaged: true
        inject:
          - {domain: $JIRA_HOST, header: Authorization, format: "Bearer %s"}

  # Interactive bash shells source the editable host file as well
  # (login shells get it from /etc/profile.d/jira-cli.sh). Idempotent.
  - type: com.docker.sandbox/lifecycle@1
    config:
      startup:
        - command:
            - sh
            - -c
            - >-
              grep -qsF '$SETTINGS/bashrc' /etc/bash.bashrc ||
              printf '\n# jira-cli sbx kit\n[ -r $SETTINGS/bashrc ] && . $SETTINGS/bashrc\n' >> /etc/bash.bashrc
          user: "0"
          description: Source $SETTINGS/bashrc from interactive bash shells

  - type: com.docker.sandbox/agent-context@1
    optional: true
    config: &agent-context
      content: |
        ## Jira (jira-cli)

        \`jira\` is on PATH. Use the \`jira\` skill (.opencode/skills/jira): discover commands with
        \`jira commands list\` and \`jira commands describe <group> <command>\`
        instead of guessing them.

        - Authentication is proxy-managed: \`$CRED_ENV\` is a sentinel and the
          real token is injected on requests to $JIRA_HOST. Never print or copy
          it, and do not run \`jira auth login\`.
        - $mode_text
        - Keep queries small: --max-results 50 or less; with --all add --max-pages 3.
        - The configuration is read-only at $SETTINGS; changes are made by
          the user on the host.

  - type: com.docker.sandbox/agent-context@2
    optional: true
    config: *agent-context
EOF
info "kit:      $KIT (handle jira-cli)"

# ---------- 3. settings (created once, never overwritten) ----------

CA_LINE=""
if [ -n "$CA_BUNDLE" ]; then
  [ -f "$CA_BUNDLE" ] || die "CA bundle not found: $CA_BUNDLE"
  ca_name=$(basename "$CA_BUNDLE" | tr ' ' '_')
  cp "$CA_BUNDLE" "$SETTINGS/$ca_name"
  CA_LINE="    tls:
      ca_bundle: $SETTINGS/$ca_name"
  info "CA:       $SETTINGS/$ca_name"
fi

if [ -f "$CONFIG" ]; then
  info "config:   $CONFIG (kept; edit it on the host)"
  grep -Eq "credential_ref:[[:space:]]*$CRED_REF([[:space:]]|$)" "$CONFIG" \
    || warn "$CONFIG does not use credential_ref: $CRED_REF; the sandbox only provides $CRED_ENV"
  if [ -n "$CA_BUNDLE" ] && ! grep -qF "ca_bundle: $SETTINGS/" "$CONFIG"; then
    warn "add to $CONFIG under the instance:  tls: { ca_bundle: $SETTINGS/$ca_name }"
  fi
  # Earlier versions of this script mounted the settings at /etc/jira-cli.
  if grep -q "/etc/jira-cli/" "$CONFIG"; then
    warn "$CONFIG refers to /etc/jira-cli/, which no longer exists in the sandbox; use $SETTINGS/ instead"
  fi
else
  ro_line="    read_only: true"; [ "$READ_ONLY" = 1 ] || ro_line="    read_only: false"
  {
    printf '%s\n' "# jira-cli config for the sbx sandbox; mounted read-only at this same path."
    printf '%s\n' "# No secrets here: the token is the sbx secret \"$SERVICE\"."
    printf '%s\n' "version: 1"
    printf '%s\n' "default_instance: $INSTANCE"
    printf '%s\n' "instances:"
    printf '%s\n' "  $INSTANCE:"
    printf '%s\n' "    deployment: datacenter"
    printf '%s\n' "    base_url: $BASE_URL"
    printf '%s\n' "    auth:"
    printf '%s\n' "      type: pat"
    printf '%s\n' "      credential_ref: $CRED_REF"
    printf '%s\n' "$ro_line"
    [ -z "$CA_LINE" ] || printf '%s\n' "$CA_LINE"
  } > "$CONFIG"
  info "config:   $CONFIG (created)"
fi

if [ -f "$SETTINGS/bashrc" ]; then
  info "bashrc:   $SETTINGS/bashrc (kept)"
else
  cat > "$SETTINGS/bashrc" <<'EOF'
# Sourced by bash in the jira-cli sandbox (mounted read-only at this same path).
# Edit on the host; new shells pick it up. Keep it idempotent and secret-free.
# The variables jira-cli needs are set by sbxenv.yaml, not here.

alias jira-recent='jira issue search --max-results 20 --fields summary,status,assignee -o table --jql'
# export JIRA_LOG_LEVEL=debug
EOF
  info "bashrc:   $SETTINGS/bashrc (created)"
fi

# ---------- 4. the skill, as an OpenCode project skill ----------

SKILL_DST="$PROJECT/.opencode/skills/jira"
if [ "$NO_SKILL" = 1 ]; then
  info "skill:    skipped (--no-skill)"
elif [ -d "$SKILL_DST" ] && ! diff -rq "$REPO_ROOT/skills/jira" "$SKILL_DST" > /dev/null 2>&1 && [ "$FORCE" = 0 ]; then
  warn "$SKILL_DST differs from $REPO_ROOT/skills/jira and was kept (local edits?); --force replaces it"
else
  mkdir -p "$(dirname "$SKILL_DST")"
  rm -rf "$SKILL_DST"
  cp -R "$REPO_ROOT/skills/jira" "$SKILL_DST"
  info "skill:    $SKILL_DST"
fi

# ---------- 5. sbxenv.yaml ----------

if [ "$KIT_ONLY" = 1 ]; then
  info "env file: skipped (--kit-only); add the kit, mount, env and binding to $ENVFILE (docs/sandbox.md)"
else
{
  printf '%s\n' "# generated by jira-cli scripts/sbx-setup.sh; re-run it to regenerate."
  printf '%s\n' "# Start: sbx env run    Check: sbx env exec -- bash -lc 'jira doctor'"
  printf '%s\n' 'schemaVersion: "1"'
  printf '%s\n' "agent: $AGENT"
  printf '%s\n' "kits:"
  [ -z "$AGENT_KIT" ] || printf '%s\n' "  - $AGENT_KIT"
  printf '%s\n' "  - ./.sbx/jira-cli"
  printf '%s\n' "workspace: ."
  printf '%s\n' "additionalWorkspaces:"
  printf '%s\n' "  - path: $SETTINGS"
  printf '%s\n' "    readOnly: true"
  printf '%s\n' "env:"
  printf '%s\n' "  JIRA_CONFIG: $SETTINGS/config.yaml"
  printf '%s\n' '  JIRA_NO_KEYRING: "1"'
  [ "$READ_ONLY" = 0 ] || printf '%s\n' '  JIRA_READ_ONLY: "1"'
  if [ -n "$TEMPLATE" ]; then
    printf '%s\n' "sandboxOptions:"
    printf '%s\n' "  template: $TEMPLATE"
  fi
  printf '%s\n' "# Approves injecting the sbx secret \"$SERVICE\" into requests to $JIRA_HOST."
  printf '%s\n' "bindings:"
  printf '%s\n' "  $SERVICE:"
  printf '%s\n' "    apiKey:"
  printf '%s\n' "      domains: [$JIRA_HOST]"
} > "$ENVFILE"
info "env file: $ENVFILE"
fi

# ---------- 6. the secret ----------

if [ "$SKIP_SECRET" = 0 ] && [ "$HAVE_SBX" = 1 ]; then
  if sbx secret ls 2>/dev/null | awk '{print $1}' | grep -qx "$SERVICE"; then
    info "secret:   '$SERVICE' already in the sbx store (replace: sbx secret set $SERVICE)"
  else
    say "Storing the Jira PAT as sbx secret '$SERVICE' (sbx prompts for it; it stays on this machine)"
    sbx secret set "$SERVICE" || die "sbx secret set $SERVICE failed; run it yourself (see: sbx secret --help), then re-run with --skip-secret"
  fi
fi

# ---------- 7. plan / run ----------

if [ "$HAVE_SBX" = 1 ] && [ -f "$ENVFILE" ]; then
  say "Plan (nothing is created yet):"
  (cd "$PROJECT" && sbx env plan) || die "sbx env plan failed; see the message above"
  if [ "$RUN" = 1 ]; then (cd "$PROJECT" && exec sbx env run); fi
fi

say ""
say "Next:"
say "  cd $PROJECT && sbx env run"
say "  sbx env exec -- bash -lc 'jira doctor'          # credentials: env:$CRED_ENV, authentication: ok"
[ "$READ_ONLY" = 0 ] || say "  sbx env exec -- bash -lc 'jira issue delete X-1 --yes'; echo \$?   # expect 9 (read-only guard)"
say "Edit $CONFIG or $SETTINGS/bashrc on the host at any time. After changing jira-cli, re-run this script."

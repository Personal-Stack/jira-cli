#!/usr/bin/env bash
# Tests scripts/jira-docker.sh against a real image in an isolated HOME.
# Usage: scripts/test-docker-wrapper.sh [image]   (default jira-cli:local)
# Assertions use `cond && ok || ko`; ok() always succeeds.
# shellcheck disable=SC2015,SC2030,SC2031  # subshells scope the fake docker on purpose
set -uo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
IMAGE=${1:-jira-cli:local}
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
export HOME="$WORK/home" JIRA_CLI_IMAGE="$IMAGE"
unset XDG_CONFIG_HOME JIRA_CONFIG JIRA_CREDENTIALS_DIR JIRA_LOG_FILE JIRA_READ_ONLY
mkdir -p "$HOME/project"
BIN="$WORK/bin"; mkdir -p "$BIN"
cp "$ROOT/scripts/jira-docker.sh" "$BIN/jira"
CFG="$HOME/.config/jira-cli"
pass=0 fail=0
ok() { echo "PASS  $*"; pass=$((pass + 1)); }
ko() { echo "FAIL  $*"; fail=$((fail + 1)); }
j() { (cd "$HOME/project" && "$BIN/jira" "$@"); }

j --version | grep -q "jira version" && ok "runs the image (--version)" || ko "--version"

j config validate > "$WORK/o" 2> "$WORK/e"; rc=$?
[ $rc = 3 ] && grep -q "$CFG/config.yaml" "$WORK/e" && ok "no config: exit 3, host path in error" || { ko "no config (exit $rc)"; cat "$WORK/e"; }

# A CA bundle referenced by its host path must resolve inside the container.
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=test-ca -keyout "$WORK/k.pem" -out "$CFG/ca.pem" 2>/dev/null
cat > "$CFG/config.yaml" <<YAML
version: 1
default_instance: prod
instances:
  prod:
    deployment: datacenter
    base_url: https://jira.invalid
    auth: {type: pat, credential_ref: jira-prod}
    tls: {ca_bundle: $CFG/ca.pem}
    timeouts: {connect: 3s, request: 5s}
    retry: {max_attempts: 1}
    read_only: true
YAML
j config validate > "$WORK/o" 2>&1 && grep -q '"valid": true' "$WORK/o" && ok "config + ca_bundle at host paths validate in the container" || { ko "config validate"; cat "$WORK/o"; }

TOKEN="wrapper-test-token-$RANDOM$RANDOM"
printf '%s' "$TOKEN" | j auth login --no-verify > "$WORK/o" 2> "$WORK/e"; rc=$?
[ $rc = 0 ] && grep -q '"store": "file-store"' "$WORK/o" && ok "auth login stores in the file store" || { ko "auth login (exit $rc)"; cat "$WORK/o" "$WORK/e"; }
f="$CFG/credentials/jira-prod"
[ "$(stat -c '%a %u' "$f" 2>/dev/null)" = "600 $(id -u)" ] && ok "token file 0600, owned by the user" || ko "token file mode/owner: $(stat -c '%a %u' "$f" 2>/dev/null)"
[ "$(stat -c '%a' "$CFG/credentials")" = 700 ] && ok "credentials dir 0700" || ko "credentials dir mode $(stat -c '%a' "$CFG/credentials")"
[ "$(cat "$f")" = "$TOKEN" ] && ok "stored value matches" || ko "stored value"

j auth status > "$WORK/o" 2>&1 && grep -q "\"source\": \"file:$f\"" "$WORK/o" && ! grep -q warnings "$WORK/o" && ok "auth status: file source, no permission warning" || { ko "auth status"; cat "$WORK/o"; }

cp "$ROOT/examples/issue.json" "$HOME/project/issue.json"
j issue create --file issue.json --dry-run > "$WORK/o" 2>&1 && grep -q '"summary": "Created from jira-cli"' "$WORK/o" && ok "relative --file from the current directory" || { ko "--file"; cat "$WORK/o"; }

j issue delete ABC-1 --yes > "$WORK/o" 2> "$WORK/e"; rc=$?
[ $rc = 9 ] && [ ! -s "$WORK/o" ] && grep -q read_only_mode "$WORK/e" && ok "exit code 9 propagated; error on stderr only" || { ko "read-only guard (exit $rc)"; cat "$WORK/o" "$WORK/e"; }

j myself get --log-file trace.jsonl --log-level debug > /dev/null 2>&1; rc=$?
[ $rc = 8 ] && ok "unreachable host: exit 8" || ko "unreachable exit $rc"
[ "$(stat -c '%a %u' "$HOME/project/trace.jsonl" 2>/dev/null)" = "600 $(id -u)" ] && grep -q '"http error"\|http error' "$HOME/project/trace.jsonl" && ok "--log-file written to the host dir, 0600, user-owned" || ko "log file"

j commands list | python3 -c 'import json,sys; assert json.load(sys.stdin)["count"]==430' && ok "piped JSON stdout parses (no TTY mixing)" || ko "piped JSON"

# Secrets in the environment are forwarded by name, never on the command line.
cat > "$WORK/fake-docker" <<'SH'
#!/bin/sh
printf '%s\n' "$@" > "$ARGS_OUT"
SH
chmod +x "$WORK/fake-docker"; mkdir -p "$WORK/fakebin"; cp "$WORK/fake-docker" "$WORK/fakebin/docker"
( export PATH="$WORK/fakebin:$PATH" ARGS_OUT="$WORK/args" JIRA_CREDENTIAL_JIRA_PROD="$TOKEN" HTTPS_PROXY=http://proxy.example:3128
  cd "$HOME/project" && "$BIN/jira" issue get ABC-1 )
grep -qx -- "JIRA_CREDENTIAL_JIRA_PROD" "$WORK/args" && grep -qx HTTPS_PROXY "$WORK/args" && ok "env forwarded by name (-e NAME)" || ko "env forwarding"
! grep -qF "$TOKEN" "$WORK/args" && ok "token not on the docker command line" || ko "token visible in docker argv"
tail -4 "$WORK/args" | tr '\n' ' ' | grep -q "$IMAGE issue get ABC-1" && ok "CLI arguments passed unchanged after the image" || { ko "argv order"; cat "$WORK/args"; }
grep -q -- "$HOME/project:$HOME/project:ro" "$WORK/args" && ok "current directory mounted read-only" || ko "cwd mount"

# setup-sbx in the container: project and settings writable, binary = the
# container's own (linux), host steps printed instead of running sbx.
# A fake sbx on the host: no secret yet, and only the agent "devex-opencode".
mkdir -p "$WORK/sbxbin"
cat > "$WORK/sbxbin/sbx" <<'SH'
#!/bin/sh
case "$*" in
  "secret ls") cat "$SBX_STATE/secrets" 2>/dev/null ;;
  "secret set "*) echo "${3:-}" >> "$SBX_STATE/secrets" ;;
  "env plan")
    a=$(sed -n 's/^agent: //p' sbxenv.yaml)
    [ "$a" = devex-opencode ] || { echo "error: unknown agent \"$a\""; exit 1; } ;;
esac
SH
chmod +x "$WORK/sbxbin/sbx"; export SBX_STATE="$WORK/sbxstate"; mkdir -p "$SBX_STATE"
# setup-sbx checks that Jira is reachable: a local listener stands in for it
# (host network, so the container sees 127.0.0.1).
python3 -c 'import socket,sys; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.listen(16); sys.stdout.flush(); [s.accept() for _ in iter(int,1)]' > "$WORK/port" &
LISTENER=$!
for _ in 1 2 3 4 5 6 7 8 9 10; do [ -s "$WORK/port" ] && break; sleep 0.2; done
JIRA_URL="https://127.0.0.1:$(cat "$WORK/port")"
mkdir -p "$HOME/sbxproj"
(cd "$HOME/sbxproj" && PATH="$WORK/sbxbin:$PATH" JIRA_DOCKER_NETWORK=host "$BIN/jira" setup-sbx --base-url "$JIRA_URL" --agent devex-opencode --name sbx-test < /dev/null) > "$WORK/o" 2> "$WORK/e"; rc=$?
kill "$LISTENER" 2> /dev/null
SBXSET="$HOME/.config/jira-cli-sbx"
[ $rc = 0 ] && [ -f "$HOME/sbxproj/sbxenv.yaml" ] && [ -f "$HOME/sbxproj/.opencode/skills/jira/SKILL.md" ] && [ -f "$SBXSET/config.yaml" ] \
  && ok "setup-sbx writes the project files and settings through the container" || { ko "setup-sbx (exit $rc)"; cat "$WORK/o" "$WORK/e"; }
[ -x "$HOME/sbxproj/tmp/.sbx/jira-cli/bin/jira" ] && grep -q '"binary_source": "self"' "$WORK/o" \
  && ok "setup-sbx uses the container's own linux binary" || { ko "setup-sbx binary"; cat "$WORK/o"; }
grep -q "path: $SBXSET" "$HOME/sbxproj/sbxenv.yaml" && grep -q "JIRA_CONFIG: $SBXSET/config.yaml" "$HOME/sbxproj/sbxenv.yaml" \
  && ok "setup-sbx writes host paths into sbxenv.yaml" || { ko "setup-sbx paths"; cat "$HOME/sbxproj/sbxenv.yaml"; }
[ "$(stat -c %u "$HOME/sbxproj/sbxenv.yaml")" = "$(id -u)" ] && [ "$(stat -c %u "$SBXSET/config.yaml")" = "$(id -u)" ] \
  && ok "setup-sbx files are owned by the user" || ko "setup-sbx ownership"
grep -q "cd $HOME/sbxproj && sbx env run" "$WORK/e" && grep -q "sbx accepts the environment" "$WORK/e" \
  && [ "$(sed -n '/^kits:/,/^workspace:/p' "$HOME/sbxproj/sbxenv.yaml" | tr -d '\n')" = "kits:  - ./tmp/.sbx/jira-cli$(printf 'workspace: .')" ] \
  && grep -qx jira "$SBX_STATE/secrets" \
  && ok "setup-sbx: host checks after the container (secret stored, plan accepted, only jira-cli's kit)" || { ko "setup-sbx host checks"; cat "$WORK/e"; cat "$HOME/sbxproj/sbxenv.yaml"; }

# Guided setup-sbx: the wrapper asks for the project on the host (the
# container cannot see other folders), mounts it and passes it on.
if command -v script > /dev/null 2>&1; then
  mkdir -p "$HOME/other project"
  ( export PATH="$WORK/fakebin:$PATH" ARGS_OUT="$WORK/args2"
    cd "$HOME/project" && printf '%s\n' "$HOME/missing" "$HOME/other project" | script -qec "$BIN/jira setup-sbx" /dev/null ) > "$WORK/o" 2>&1
  grep -q "not a directory: $HOME/missing" "$WORK/o" && grep -qx -- "--project" "$WORK/args2" && grep -qx -- "$HOME/other project" "$WORK/args2" \
    && grep -qx -- "-i" "$WORK/args2" && grep -qx -- "$HOME/other project:$HOME/other project" "$WORK/args2" \
    && ok "guided setup-sbx: project asked on the host, re-asked if missing, mounted read-write" || { ko "guided setup-sbx"; cat "$WORK/o" "$WORK/args2"; }
else
  echo "SKIP  guided setup-sbx (no 'script' command for a pseudo-terminal)"
fi

j auth logout > "$WORK/o" 2>&1 && [ ! -e "$f" ] && ok "auth logout removes the token file" || { ko "logout"; cat "$WORK/o"; }

if grep -rqF "$TOKEN" "$HOME/project" "$WORK/o" "$WORK/e" 2>/dev/null; then ko "token found in outputs/logs"; else ok "token absent from outputs and logs"; fi
echo; echo "docker wrapper tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]

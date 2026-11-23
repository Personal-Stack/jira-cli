#!/usr/bin/env bash
# Tests `jiracli sbx kit` against a REAL sbx (Docker Sandboxes) on this
# machine: what the Go tests (fake sbx) and the kit-spec library cannot show.
#
# Usage:
#   scripts/test-sbx-real.sh --base-url https://jira.company.internal [options]
#
# Options:
#   --base-url URL     your Jira (required); only read requests are sent to it
#   --secret NAME      sbx secret holding the PAT (default: jira; sbx prompts
#                      once if it does not exist)
#   --agent NAME       agent for the test sandboxes (default: the agent of
#                      ~/.sbxenv.yaml, else shell). Agent guidance and skills
#                      are only checked for an agent that reads them (not shell).
#   --jiracli PATH     host jiracli to test (default: built from this clone)
#   --keep             keep the sandboxes and the work folder for inspection
#
# Needs: sbx signed in, local sandboxes working (KVM / Apple silicon), Go if
# --jiracli is not given, and a terminal the first time (approvals, secret).
#
# What it changes, and restores:
#   - ~/.sbxenv.yaml: `jiracli sbx kit` edits it (that is what is tested); the
#     original is restored on exit, also on failure or Ctrl+C;
#   - two sandboxes jcli-kit-<pid>-a/-b: created with `sbx env create --force`
#     and removed with `sbx env rm --force` on exit (unless --keep);
#   - ~/.config/sbx/credentials.yaml: sbx records the binding (approval) for
#     your Jira host there, as any first start does; `sbx env rm` keeps it.
# The kit and settings go to a temporary folder, not ~/.config/jira-cli-sbx.
#
# The open questions it answers (Q1-Q3), plus the functional checks:
#   Q1  sbx accepts additionalWorkspaces in ~/.sbxenv.yaml (the settings mount)
#   Q2  sbx applies the kit's image layer (/usr/bin/jiracli, the bundled skill)
#   Q3  a kit declaring agent-context@1 and @2 composes, and the guidance lands
# Output: PASS / FAIL / INFO lines, and a report in the work folder to share.
# Assertions use `cond && ok || ko`; ok() always succeeds.
# shellcheck disable=SC2015,SC2016,SC2088  # $ expands in the sandbox; ~ is text
set -uo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)

BASE_URL="" SECRET=jira AGENT="" JIRACLI="" KEEP=0
while [ $# -gt 0 ]; do
  case "$1" in
    --base-url) BASE_URL=${2:-}; shift 2 ;;
    --secret) SECRET=${2:-}; shift 2 ;;
    --agent) AGENT=${2:-}; shift 2 ;;
    --jiracli) JIRACLI=${2:-}; shift 2 ;;
    --keep) KEEP=1; shift ;;
    -h|--help) sed -n '2,/^# shellcheck/p' "$0" | sed -e '$d' -e 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1 (see --help)" >&2; exit 2 ;;
  esac
done
[ -n "$BASE_URL" ] || { echo "--base-url is required (see --help)" >&2; exit 2; }

die() { echo "error: $*" >&2; exit 2; }
command -v sbx >/dev/null || die "sbx is not installed (https://docs.docker.com/ai/sandboxes/install/)"
for sub in "env create" "env rm"; do
  # shellcheck disable=SC2086
  sbx $sub --help 2>&1 | grep -q -- "--force" || die "'sbx $sub' has no --force: this sbx is older than the documented one"
done
sbx env exec --help 2>&1 | grep -q -- "--no-input" || die "'sbx env exec' has no --no-input: this sbx is older than the documented one"

# Under $HOME, not $TMPDIR: on macOS /var is a symlink to /private/var, and
# the settings folder must appear at the same path inside the sandbox.
WORK=$(mktemp -d "$HOME/.jcli-sbx-real.XXXXXX")
REPORT="$WORK/report.txt"
SETTINGS="$WORK/settings"
KIT="$SETTINGS/kit"
USERENV="$HOME/.sbxenv.yaml"
NAME_A="jcli-kit-$$-a" NAME_B="jcli-kit-$$-b"
PROJ_A="$WORK/project-a" PROJ_B="$WORK/project-b"
mkdir -p "$PROJ_A" "$PROJ_B"

pass=0 fail=0
log() { echo "$*" | tee -a "$REPORT"; }
ok() { log "PASS  $*"; pass=$((pass + 1)); }
ko() { log "FAIL  $*"; fail=$((fail + 1)); }
info() { log "INFO  $*"; }

# Restore ~/.sbxenv.yaml and remove the sandboxes, whatever happens.
HAD_USERENV=0
[ -f "$USERENV" ] && { HAD_USERENV=1; cp -p "$USERENV" "$WORK/sbxenv.yaml.orig"; }
cleanup() {
  if [ "$KEEP" = 0 ]; then
    (cd "$PROJ_A" && sbx env rm --force --name "$NAME_A" >/dev/null 2>&1)
    (cd "$PROJ_B" && sbx env rm --force --name "$NAME_B" >/dev/null 2>&1)
  fi
  if [ "$HAD_USERENV" = 1 ]; then
    cp -p "$WORK/sbxenv.yaml.orig" "$USERENV"
  else
    rm -f "$USERENV"
  fi
  rm -f "$USERENV.bak.jcli-test"
  echo "~/.sbxenv.yaml restored."
  if [ "$KEEP" = 0 ]; then
    cp "$REPORT" "$HOME/jcli-sbx-real-report.txt" 2>/dev/null
    rm -rf "$WORK"
    echo "Report: $HOME/jcli-sbx-real-report.txt"
  else
    echo "Kept: $WORK (sandboxes $NAME_A, $NAME_B; remove with 'sbx rm NAME')"
  fi
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# The .bak jiracli writes would replace a .bak of yours: keep yours aside.
[ -f "$USERENV.bak" ] && cp -p "$USERENV.bak" "$USERENV.bak.jcli-test"
restore_bak() {
  if [ -f "$USERENV.bak.jcli-test" ]; then cp -p "$USERENV.bak.jcli-test" "$USERENV.bak"; else rm -f "$USERENV.bak"; fi
}

{
  echo "jiracli sbx kit against a real sbx — $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "host: $(uname -sm)"
  echo "sbx: $(sbx version 2>/dev/null | head -1 || sbx --version 2>/dev/null | head -1)"
  echo "jira: $BASE_URL"
} > "$REPORT"

# --- the host jiracli and the linux binary for the kit -----------------------
if [ -z "$JIRACLI" ]; then
  command -v go >/dev/null || die "Go is needed to build jiracli from this clone (or pass --jiracli)"
  JIRACLI="$WORK/jiracli"
  (cd "$ROOT" && go build -o "$JIRACLI" ./cmd/jiracli) || die "go build failed"
fi
"$JIRACLI" sbx kit --help >/dev/null 2>&1 || die "$JIRACLI has no 'sbx kit' command"
log "jiracli: $("$JIRACLI" --version 2>/dev/null | head -1)"

# --- the agent ---------------------------------------------------------------
if [ -z "$AGENT" ] && [ -f "$USERENV" ]; then
  AGENT=$(sed -n 's/^agent:[[:space:]]*\([A-Za-z0-9._-]*\).*/\1/p' "$USERENV" | head -1)
fi
AGENT=${AGENT:-shell}
log "agent: $AGENT"
echo

# The PAT: sbx prompts for it here, in the terminal, if it is not stored yet.
if ! sbx secret ls 2>/dev/null | awk '{print $1}' | grep -qx "$SECRET"; then
  echo "sbx secret $SECRET does not exist: sbx asks for your Jira PAT now."
  sbx secret set "$SECRET" || die "sbx secret set $SECRET failed"
fi

# --- 1. jiracli sbx kit: kit + ~/.sbxenv.yaml --------------------------------
# It runs sbx env plan on the merged file itself, and stores the secret.
"$JIRACLI" sbx kit --base-url "$BASE_URL" --secret "$SECRET" --settings "$SETTINGS" \
  --source-dir "$ROOT" --agent "$AGENT" --yes -o json > "$WORK/kit.json" 2> "$WORK/kit.err"
rc=$?
restore_bak
[ $rc = 0 ] && ok "jiracli sbx kit (its own 'sbx env plan' check passed)" || { ko "jiracli sbx kit (exit $rc)"; cat "$WORK/kit.err" | tee -a "$REPORT"; exit 1; }
grep -q "source: $KIT" "$USERENV" && grep -q "path: $SETTINGS" "$USERENV" \
  && ok "~/.sbxenv.yaml lists the kit and the settings mount" || ko "~/.sbxenv.yaml entries"
cp "$USERENV" "$WORK/sbxenv.yaml.with-kit"

# --- 2. sandbox A: a project file with only agent + workspace ----------------
# Its sbxenv.yaml plus ~/.sbxenv.yaml (merged by sbx, no path given): the real
# setup every project gets.
printf 'schemaVersion: "1"\nagent: %s\nworkspace: .\n' "$AGENT" > "$PROJ_A/sbxenv.yaml"
(cd "$PROJ_A" && sbx env plan) > "$WORK/plan-a.txt" 2>&1; rc=$?
[ $rc = 0 ] && ok "Q1 sbx env plan accepts ~/.sbxenv.yaml with the kit and the settings mount" \
  || { ko "Q1 sbx env plan (exit $rc)"; sed 's/^/      /' "$WORK/plan-a.txt" | tee -a "$REPORT"; }
(cd "$PROJ_A" && sbx env create --force --name "$NAME_A") > "$WORK/create-a.txt" 2>&1; rc=$?
if [ $rc != 0 ]; then
  ko "sbx env create (exit $rc): the sandbox did not start"
  sed 's/^/      /' "$WORK/create-a.txt" | tee -a "$REPORT"
  grep -qi "agent-context" "$WORK/create-a.txt" && ko "Q3 sbx refused the agent-context @1/@2 declarations"
  log ""; log "$pass passed, $fail failed"; exit 1
fi
ok "Q3 sbx env create: the composition (agent-context @1 and @2 included) was accepted"

# One probe in the sandbox; key=value lines.
x() { (cd "$1" && sbx env exec --no-input --name "$2" -- sh -c "$3") 2>&1; }
PROBE='
k='"'$KIT'"'
echo "mount_bin=$(test -x "$k/bin/jiracli" && echo yes || echo no)"
echo "mount_ro=$( (touch "$k/.w" 2>/dev/null && rm -f "$k/.w" && echo no) || echo yes)"
echo "layer_bin=$(test -x /usr/bin/jiracli && echo yes || echo no)"
echo "layer_skill=$(test -f /usr/share/jira-cli/skills/jira/SKILL.md && echo yes || echo no)"
echo "path_jiracli=$(command -v jiracli || echo none)"
echo "env_read_only=${JIRA_READ_ONLY-unset}"
echo "env_config=${JIRA_CONFIG-unset}"
echo "env_no_keyring=${JIRA_NO_KEYRING-unset}"
echo "cred_set=$( [ -n "${JIRA_CREDENTIAL_JIRA_PROD-}" ] && echo yes || echo no)"
# Only the generated copies name the kit path of this run; the sources in the kit are skipped.
s=$(find / -xdev \( -path /proc -o -path /sys -o -path /usr/share/jira-cli -o -path '"'$SETTINGS'"' \) -prune -o -name SKILL.md -path "*/jira/SKILL.md" -exec grep -lsF "$k/bin/jiracli" {} + 2>/dev/null | head -3 | tr "\n" " ")
echo "skill_exposed=${s:-none}"
c=$(grep -rlsF "$k/bin/jiracli" /home /root /etc /workspace /Users 2>/dev/null | grep -v -e '"'^$SETTINGS'"' -e "/SKILL.md\$" | while IFS= read -r f; do grep -qsF "## Jira (jira-cli)" "$f" && echo "$f"; done | head -3 | tr "\n" " ")
echo "context_files=${c:-none}"
if command -v curl >/dev/null; then e=$(curl -s -o /dev/null -m 8 -w "%{http_code}" https://example.com); else e=n/a; fi
echo "egress_other=$e"
'
x "$PROJ_A" "$NAME_A" "$PROBE" > "$WORK/probe-a.txt"
v() { sed -n "s/^$1=//p" "$WORK/probe-$2.txt" | tail -1; }
{ echo; echo "probe (sandbox A):"; sed 's/^/      /' "$WORK/probe-a.txt"; } >> "$REPORT"

[ "$(v mount_bin a)" = yes ] && ok "Q1 the settings mount is in the sandbox: $KIT/bin/jiracli" || ko "Q1 $KIT/bin/jiracli is not in the sandbox (mount not applied)"
[ "$(v mount_ro a)" = yes ] && ok "the settings mount is read-only" || ko "the settings mount is writable"
[ "$(v layer_bin a)" = yes ] && ok "Q2 image layer applied: /usr/bin/jiracli" || info "Q2 image layer NOT applied: no /usr/bin/jiracli (the mounted path is used instead)"
[ "$(v layer_skill a)" = yes ] && ok "Q2 image layer applied: /usr/share/jira-cli/skills/jira" || info "Q2 no bundled skill in the image layer"
if [ "$AGENT" = shell ]; then
  info "Q2/Q3 agent 'shell': skill exposure and agent guidance not checked (re-run with --agent <your agent>)"
else
  [ "$(v skill_exposed a)" != none ] && ok "Q2 skill exposed to the agent: $(v skill_exposed a)" || ko "Q2 the jira skill is not in any agent skill folder"
  [ "$(v context_files a)" != none ] && ok "Q3 agent guidance written: $(v context_files a)" || ko "Q3 the '## Jira (jira-cli)' guidance is in no file of the sandbox (searched /home /root /etc /workspace /Users)"
fi
[ "$(v env_read_only a)" = 1 ] && ok "kit arg readOnly -> JIRA_READ_ONLY=1" || ko "JIRA_READ_ONLY=$(v env_read_only a) (want 1)"
[ "$(v env_config a)" = "$SETTINGS/config.yaml" ] && ok "kit arg config -> JIRA_CONFIG" || ko "JIRA_CONFIG=$(v env_config a)"
[ "$(v env_no_keyring a)" = 1 ] && ok "kit arg noKeyring -> JIRA_NO_KEYRING=1" || ko "JIRA_NO_KEYRING=$(v env_no_keyring a)"
[ "$(v cred_set a)" = yes ] && ok "the credential sentinel JIRA_CREDENTIAL_JIRA_PROD is set" || ko "JIRA_CREDENTIAL_JIRA_PROD is not set (credential withheld? binding missing?)"
info "egress to https://example.com: HTTP $(v egress_other a) (000 = blocked; other kits may allow it)"

# jiracli itself: doctor (DNS, TLS, token injection, auth) and read-only.
x "$PROJ_A" "$NAME_A" "\"$KIT/bin/jiracli\" doctor" > "$WORK/doctor-a.txt"; rc=$?
[ $rc = 0 ] && ok "jiracli doctor in the sandbox: token injected, authenticated" \
  || { ko "jiracli doctor (exit $rc)"; sed 's/^/      /' "$WORK/doctor-a.txt" | tail -25 | tee -a "$REPORT"; }
x "$PROJ_A" "$NAME_A" "echo '{\"fields\":{}}' > /tmp/b.json; \"$KIT/bin/jiracli\" issue create --file /tmp/b.json >/dev/null 2>&1; echo rc=\$?" > "$WORK/ro-a.txt"
[ "$(sed -n 's/^rc=//p' "$WORK/ro-a.txt")" = 9 ] && ok "read-only: issue create refused before sending (exit 9)" \
  || ko "read-only probe: $(cat "$WORK/ro-a.txt")"

# --- 3. sandbox B: a project overrides an arg -------------------------------
# Same kit source with readOnly "0": sbx must merge the args (one kit), not
# compose a second jira-cli. Nothing is written to Jira (only --dry-run).
cat > "$PROJ_B/sbxenv.yaml" <<YAML
schemaVersion: "1"
agent: $AGENT
workspace: .
kits:
  - source: $KIT
    args: {readOnly: "0"}
YAML
(cd "$PROJ_B" && sbx env create --force --name "$NAME_B") > "$WORK/create-b.txt" 2>&1; rc=$?
if [ $rc = 0 ]; then
  ok "a project listing the same kit source with other args: one kit, args merged"
  x "$PROJ_B" "$NAME_B" 'echo "env_read_only=${JIRA_READ_ONLY-unset}"' > "$WORK/probe-b.txt"
  [ "$(v env_read_only b)" = 0 ] && ok "project arg override: JIRA_READ_ONLY=0" || ko "project arg override: JIRA_READ_ONLY=$(v env_read_only b) (want 0)"
  x "$PROJ_B" "$NAME_B" "echo '{\"fields\":{}}' > /tmp/b.json; \"$KIT/bin/jiracli\" issue create --file /tmp/b.json --dry-run >/dev/null 2>&1; echo rc=\$?" > "$WORK/rw-b.txt"
  [ "$(sed -n 's/^rc=//p' "$WORK/rw-b.txt")" = 0 ] && ok "writes allowed there (checked with --dry-run, nothing sent)" || ko "write dry-run: $(cat "$WORK/rw-b.txt")"
else
  ko "project arg override: sbx env create failed (exit $rc)"
  sed 's/^/      /' "$WORK/create-b.txt" | tee -a "$REPORT"
fi

log ""
log "$pass passed, $fail failed"
[ $fail = 0 ]

#!/usr/bin/env bash
# CI smoke test for the built binary with full structured logging.
#
# Runs representative commands against an unreachable instance
# (https://jira.invalid; RFC 2606 guarantees it never resolves), checks exit
# codes, validates the JSON log stream, and fails if the dummy credential
# appears anywhere in stdout, stderr or logs.
#
# Usage: scripts/ci-smoke.sh [path/to/jira] [artifact-dir]
set -euo pipefail

BIN=${1:-bin/jiracli}
ART=${2:-ci-artifacts}
mkdir -p "$ART/smoke"
ART=$(cd "$ART" && pwd)
BIN=$(cd "$(dirname "$BIN")" && pwd)/$(basename "$BIN")
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

DUMMY_TOKEN="ci-dummy-pat-$(date +%s)-DO-NOT-LEAK"
printf '%s' "$DUMMY_TOKEN" > "$WORK/pat"
chmod 600 "$WORK/pat"
cat > "$WORK/config.yaml" <<YAML
version: 1
default_instance: ci
instances:
  ci:
    deployment: datacenter
    base_url: https://jira.invalid
    auth: {type: pat, credential_ref: ci-pat}
    timeouts: {connect: 3s, request: 5s}
    retry: {max_attempts: 1}
    read_only: true
YAML

export JIRA_CONFIG="$WORK/config.yaml"
export JIRA_CREDENTIAL_FILE="$WORK/pat"
export JIRA_NO_KEYRING=1
export JIRA_LOG_LEVEL=debug
export JIRA_LOG_FORMAT=json
export JIRA_LOG_FILE="$ART/smoke/jira.log"
: > "$JIRA_LOG_FILE"

fail=0
runs=0
step() { # step <name> <expected-exit> <args...>
  local name=$1 want=$2; shift 2
  runs=$((runs + 1))
  set +e
  "$BIN" "$@" > "$ART/smoke/$name.stdout" 2> "$ART/smoke/$name.stderr"
  local got=$?
  set -e
  if [ "$got" -eq "$want" ]; then
    printf 'PASS  %-22s exit=%s\n' "$name" "$got"
  else
    printf 'FAIL  %-22s exit=%s want=%s\n' "$name" "$got" "$want"
    sed 's/^/      /' "$ART/smoke/$name.stderr" | head -20
    fail=1
  fi
}

step version            0 --version
step capabilities       0 capabilities
step commands-list      0 commands list
step commands-describe  0 commands describe issue search
step spec-status        0 spec status
step config-validate    0 config validate
step auth-status        0 auth status
step dry-run-create     0 issue create --file examples/issue.json --dry-run
step readonly-delete    9 issue delete ABC-1
step unreachable-get    8 issue get ABC-1
step doctor-unreachable 8 doctor
step bad-log-level      2 spec status --log-level loud
step usage-missing-arg  2 issue get

# --- log stream checks ---
python3 - "$JIRA_LOG_FILE" "$runs" <<'PY' || fail=1
import json, sys
path, runs = sys.argv[1], int(sys.argv[2])
recs = []
for n, line in enumerate(open(path), 1):
    try:
        recs.append(json.loads(line))
    except ValueError:
        sys.exit(f"FAIL  log line {n} is not JSON: {line[:200]!r}")
missing = [r for r in recs if not r.get("run_id")]
if missing:
    sys.exit(f"FAIL  {len(missing)} log records without run_id")
finished = [r for r in recs if r.get("msg") == "command finished"]
# --version and the invalid --log-level run do not reach the logged path.
if len(finished) < runs - 2:
    sys.exit(f"FAIL  expected >= {runs-2} 'command finished' records, got {len(finished)}")
net = [r for r in recs if r.get("msg") == "http error"]
if not net or not all(r.get("layer") in ("dns", "proxy", "tcp", "timeout") for r in net):
    sys.exit(f"FAIL  unreachable instance not traced to a network layer: {net}")
refused = [r for r in recs if r.get("msg") == "refused by safety policy"]
if not refused or refused[0].get("error_code") != "read_only_mode":
    sys.exit("FAIL  read-only refusal not traced")
by = {}
for r in recs:
    by[r.get("msg")] = by.get(r.get("msg"), 0) + 1
print(f"PASS  log stream: {len(recs)} JSON records, {len({r['run_id'] for r in recs})} runs")
for k in sorted(by):
    print(f"        {by[k]:4d}  {k}")
PY

# --- secret leak scan over everything produced ---
if grep -rl -- "$DUMMY_TOKEN" "$ART" >/dev/null 2>&1; then
  echo "FAIL  dummy credential found in:"; grep -rl -- "$DUMMY_TOKEN" "$ART"
  fail=1
else
  echo "PASS  no credential in stdout/stderr/logs ($(find "$ART" -type f | wc -l) files scanned)"
fi

exit $fail

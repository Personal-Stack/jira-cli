#!/usr/bin/env bash
# Tests scripts/phase4-readonly.sh against scripts/testdata/fake_jira.py.
# Usage: scripts/test-phase4.sh [path/to/jira]   (default: bin/jiracli)
# Assertions use `cond && ok || ko`; ok() always succeeds.
# shellcheck disable=SC2015
set -uo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN=$(cd "$(dirname "${1:-$ROOT/bin/jiracli}")" && pwd)/$(basename "${1:-$ROOT/bin/jiracli}")
WORK=$(mktemp -d)
PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')
TOKEN="phase4-test-token-$RANDOM$RANDOM"
SRV=""
cleanup() { [ -n "$SRV" ] && kill "$SRV" 2>/dev/null; rm -rf "$WORK"; }
trap cleanup EXIT
pass=0 fail=0
ok() { echo "PASS  $*"; pass=$((pass + 1)); }
ko() { echo "FAIL  $*"; fail=$((fail + 1)); }

cd "$WORK" || exit 1
openssl req -x509 -newkey rsa:2048 -nodes -days 2 -subj "/CN=phase4 test CA" -keyout ca.key -out ca.pem \
  -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign" 2>/dev/null
openssl req -newkey rsa:2048 -nodes -subj "/CN=localhost" -keyout leaf.key -out leaf.csr 2>/dev/null
printf 'subjectAltName=DNS:localhost\nextendedKeyUsage=serverAuth\n' > leaf.ext
openssl x509 -req -in leaf.csr -CA ca.pem -CAkey ca.key -CAcreateserial -days 2 -extfile leaf.ext -out leaf.pem 2>/dev/null

start_fake() { # start_fake ISSUES
  [ -n "$SRV" ] && kill "$SRV" 2>/dev/null && wait "$SRV" 2>/dev/null
  python3 -I "$ROOT/scripts/testdata/fake_jira.py" --port "$PORT" --cert leaf.pem --key leaf.key \
    --token "$TOKEN" --log "$WORK/requests.log" --issues "$1" &
  SRV=$!
  for _ in $(seq 1 50); do curl -sk -o /dev/null "https://localhost:$PORT/" && break; sleep 0.1; done
  : > requests.log   # drop the readiness probe
}

mkdir -p cfg && cat > cfg/config.yaml <<YAML
version: 1
default_instance: fake
instances:
  fake:
    deployment: datacenter
    base_url: https://localhost:$PORT
    auth: {type: pat, credential_ref: phase4}
    tls: {ca_bundle: $WORK/ca.pem}
    proxy: {disabled: true}
YAML
printf '%s' "$TOKEN" > token && chmod 600 token
export JIRA_CONFIG="$WORK/cfg/config.yaml" JIRA_CREDENTIAL_FILE="$WORK/token" JIRA_NO_KEYRING=1 JIRA_BIN="$BIN"
# For the Docker wrapper (ignored by the native binary): share the CA file and
# reach the fake server on the host's loopback.
export JIRA_DOCKER_MOUNTS="$WORK/ca.pem" JIRA_DOCKER_NETWORK=host
# Note: the config deliberately has NO read_only; the script must enforce it.

echo "== 75 issues, 5 boards"
start_fake 75
"$ROOT/scripts/phase4-readonly.sh" --project DEMO --out "$WORK/r1" > "$WORK/r1.txt" 2>&1; rc=$?
[ $rc -eq 0 ] && ok "script exits 0" || { ko "script exit $rc"; cat "$WORK/r1.txt"; }
grep -q "RESULT: [0-9]* passed, 0 failed" "$WORK/r1.txt" && ok "summary reports 0 failed" || ko "summary"
grep -q "REST v2 pagination (total-based): 60 issue(s) expected, got 60" "$WORK/r1.txt" && ok "--all capped at 3 pages x 20 = 60" || ko "search --all"
grep -q "4 board(s) over 2 page(s)" "$WORK/r1.txt" && ok "agile --all capped at 2 pages x 2 = 4" || ko "agile pagination"
grep -q "board 1 does not support sprints" "$WORK/r1.txt" && ko "scrum board treated as kanban" || ok "scrum board sprints fetched"
grep -q "PASS    user search finds 'jdoe'" "$WORK/r1.txt" && ok "user (taken from myself) found via bare-array search" || ko "user find"
grep -q "jiracli version: 10.3.26" "$WORK/r1.txt" && ok "Jira version read from doctor" || ko "version"
grep -q "first key: DEMO-1" "$WORK/r1.txt" && ok "issue key taken from search results" || ko "issue key"
[ -s "$WORK/r1/trace.jsonl" ] && [ -s "$WORK/r1/summary.txt" ] && ok "trace.jsonl and summary.txt written" || ko "outputs"
non_get=$(grep -v '^GET ' requests.log | grep -vc '^POST /rest/api/2/search$')
[ "$non_get" -eq 0 ] && ok "server side: only GET + POST /search arrived ($(wc -l < requests.log | tr -d ' ') requests)" || { ko "server received writes"; grep -v '^GET ' requests.log; }
grep -q "^DELETE" requests.log && ko "DELETE reached the server" || ok "server never saw the guarded DELETE"
grep -rqF "$TOKEN" "$WORK/r1" "$WORK/r1.txt" && ko "token found in outputs" || ok "token absent from summary, trace and step outputs"

echo "== unused broken instance in the config (user report)"
cp cfg/config.yaml cfg/config.ok
cat >> cfg/config.yaml <<'YAML'
  company-staging:
    deployment: datacenter
    base_url: https://jira-staging.company.internal
    auth: {type: pat, credential_ref: jira-staging}
    tls: {ca_bundle: /etc/ssl/company-ca.pem}
YAML
"$ROOT/scripts/phase4-readonly.sh" --project DEMO --out "$WORK/r5" > "$WORK/r5.txt" 2>&1; rc=$?
[ $rc -eq 0 ] && grep -q "WARN  other instance(s) in your config file are invalid (not used by this run): company-staging" "$WORK/r5.txt" && grep -q "RESULT: [0-9]* passed, 0 failed, 1 warning" "$WORK/r5.txt" \
  && ok "unused broken instance: warning naming it, run passes" || { ko "unused broken instance (exit $rc)"; grep -E "config-validate|WARN|RESULT" "$WORK/r5.txt"; }
cp cfg/config.ok cfg/config.yaml

echo "== empty project (no issues)"
start_fake 0
"$ROOT/scripts/phase4-readonly.sh" --project DEMO --out "$WORK/r2" > "$WORK/r2.txt" 2>&1; rc=$?
[ $rc -eq 0 ] && grep -q "SKIP  issue get" "$WORK/r2.txt" && ok "empty project: issue steps skipped, still passes" || { ko "empty project (exit $rc)"; tail -20 "$WORK/r2.txt"; }

echo "== broken guard must stop the run"
cat > fakejira <<SH
#!/bin/sh
# Simulates a CLI whose read-only guard is broken: 'issue delete' succeeds.
case "\$*" in *"issue delete"*) exit 0 ;; esac
exec "$BIN" "\$@"
SH
chmod +x fakejira
start_fake 75
JIRA_BIN="$WORK/fakejira" "$ROOT/scripts/phase4-readonly.sh" --project DEMO --out "$WORK/r3" > "$WORK/r3.txt" 2>&1; rc=$?
[ $rc -ne 0 ] && grep -q "^STOP" "$WORK/r3.txt" && [ "$(wc -l < requests.log | tr -d ' ')" -eq 0 ] && ok "broken guard: script stops before any request" || { ko "guard stop (exit $rc)"; cat "$WORK/r3.txt"; }

echo "== bad token"
start_fake 75
printf 'wrong' > token
"$ROOT/scripts/phase4-readonly.sh" --project DEMO --out "$WORK/r4" > "$WORK/r4.txt" 2>&1; rc=$?
[ $rc -ne 0 ] && grep -q "FAIL  auth-test" "$WORK/r4.txt" && ok "bad token: reported as failures (exit $rc)" || { ko "bad token"; tail -10 "$WORK/r4.txt"; }

echo; echo "phase4 script tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]

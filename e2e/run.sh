#!/usr/bin/env bash
# End-to-end test: Jira Software DC 10.3 + PostgreSQL + TLS proxy + jira-cli.
#
#   e2e/run.sh --license-file lic.txt   full run: setup wizard, PAT, CLI scenario
#   E2E_LICENSE='AAAB...' e2e/run.sh    same, license from the environment
#   e2e/run.sh --boot-only              no license: boot the stack and run CLI
#                                       diagnostics through the TLS proxy
#   --keep                              leave the stack running afterwards
#
# Environment: CLI_IMAGE (skip building), JIRA_IMAGE, POSTGRES_IMAGE,
# NGINX_IMAGE, E2E_BOOT_TIMEOUT (seconds, default 900).
# Logs and results: e2e/.work/logs/
set -euo pipefail
cd "$(dirname "$0")"

BOOT_ONLY=false KEEP=false LICENSE_FILE=${E2E_LICENSE_FILE:-}
while [ $# -gt 0 ]; do
  case $1 in
    --boot-only) BOOT_ONLY=true ;;
    --keep) KEEP=true ;;
    --license-file) LICENSE_FILE=$2; shift ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done
if [ -n "$LICENSE_FILE" ]; then E2E_LICENSE=$(tr -d '\r' < "$LICENSE_FILE"); fi
E2E_LICENSE=${E2E_LICENSE:-}
if ! $BOOT_ONLY && [ -z "$E2E_LICENSE" ]; then
  echo "A Jira Software Data Center license is required (evaluation or timebomb)." >&2
  echo "Use --license-file FILE or E2E_LICENSE, or run with --boot-only. See e2e/README.md." >&2
  exit 2
fi

E2E_UID=$(id -u)
E2E_GID=$(id -g)
export E2E_UID E2E_GID
log() { printf '%s  %s\n' "$(date -u +%H:%M:%S)" "$*"; }
DC="docker compose"
mkdir -p .work/secrets .work/logs
chmod 777 .work/logs .work/secrets   # written by container users (jira, nonroot CLI)
rm -f .work/logs/jira-cli.log .work/logs/e2e-results.jsonl

collect() {
  local rc=$?
  log "collecting logs into e2e/.work/logs"
  $DC logs --no-color > .work/logs/compose.log 2>&1 || true
  $DC exec -T jira sh -c 'cat /var/atlassian/application-data/jira/log/atlassian-jira.log' > .work/logs/atlassian-jira.log 2>/dev/null || true
  if $KEEP; then log "stack left running (--keep); stop with: docker compose -f e2e/docker-compose.yml down -v"
  else
    $DC --profile cli down -v --remove-orphans > /dev/null 2>&1 || true
    # Files created by container users may not be removable by a non-root
    # host user; remove them from inside a container, then locally.
    docker run --rm -v "$PWD/.work:/w" "${JIRA_IMAGE:-atlassian/jira-software:10.3}" rm -rf /w/secrets > /dev/null 2>&1 || true
    rm -rf .work/secrets 2>/dev/null || true
  fi
  log "exit code $rc"
}
trap collect EXIT

./gen-certs.sh > /dev/null
if [ -n "${CLI_IMAGE:-}" ] && docker image inspect "$CLI_IMAGE" > /dev/null 2>&1; then
  log "using prebuilt CLI image $CLI_IMAGE"
else
  log "building CLI image"; $DC --profile cli build cli > .work/logs/cli-build.log 2>&1
fi

log "starting postgres, jira, jira-proxy"
$DC up -d postgres jira jira-proxy > .work/logs/compose-up.log 2>&1

log "waiting for Jira to boot (first start takes several minutes)"
deadline=$(( $(date +%s) + ${E2E_BOOT_TIMEOUT:-900} ))
state=""
while [ "$(date +%s)" -lt "$deadline" ]; do
  state=$($DC exec -T jira curl -fsS http://localhost:8080/status 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin)["state"])' 2>/dev/null || true)
  case $state in RUNNING|FIRST_RUN) break ;; esac
  sleep 10
done
case $state in RUNNING|FIRST_RUN) log "Jira state: $state" ;; *) log "Jira did not boot (state='$state')"; exit 1 ;; esac

if $BOOT_ONLY; then
  log "boot-only: CLI diagnostics through the TLS proxy (no license, no credentials)"
  set +e
  $DC --profile cli run --rm -T cli doctor > .work/logs/doctor.json 2> .work/logs/doctor.err
  rc=$?
  set -e
  python3 - .work/logs/doctor.json "$rc" <<'PY'
import json, sys
d, rc = json.load(open(sys.argv[1])), int(sys.argv[2])
checks = {c["name"]: c for c in d["checks"]}
for c in d["checks"]:
    print(f'  {c["status"]:5} {c["name"]:15} {c.get("layer","")} {c.get("detail","")[:110]}')
for name in ("config", "dns", "tcp", "tls"):
    assert checks[name]["status"] == "ok", f"{name} should pass through the proxy"
tls = checks["tls"]["data"]
assert tls["issuer"] == "CN=jira-cli e2e CA", tls
# In setup mode Jira has no license/credential yet; the doctor must stop at
# a later layer, never at network/TLS.
failed = [c for c in d["checks"] if c["status"] == "fail"]
assert rc != 0 and failed and failed[0]["name"] in ("http", "credentials", "authentication"), failed
print(f'PASS  boot-only: network+TLS verified; first failing layer "{failed[0]["name"]}" as expected without setup (exit {rc})')
PY
  exit 0
fi

log "running setup wizard + creating PAT"
$DC exec -T -e E2E_LICENSE="$E2E_LICENSE" jira python3 /e2e-setup/setup_jira.py 2>&1 | tee .work/logs/setup.log

log "running CLI scenario"
./scenario.sh 2>&1 | tee .work/logs/scenario.log

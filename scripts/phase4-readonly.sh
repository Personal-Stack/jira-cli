#!/usr/bin/env bash
# Phase 4: read-only verification of jira-cli against a real Jira Data Center.
#
# Runs a fixed set of READ commands, records exit codes and timings, checks
# pagination on all three response shapes, and proves from the trace that no
# write request was sent. IDs (issue key, board id, user name) are taken from
# earlier results, never invented.
#
# Safety: JIRA_READ_ONLY=1 is forced for every command. The first step checks
# that a write is refused locally (exit 9) before any other request is made;
# if not, the script stops immediately.
#
# Usage:
#   scripts/phase4-readonly.sh --project KEY [--instance NAME] [--user NAME] [--out DIR]
#   JIRA_BIN="path/to/jiracli" scripts/phase4-readonly.sh --project KEY   (default: jiracli on PATH;
#                                                                       the Docker wrapper works)
# Requires: bash 3.2+, grep, sed, awk. No python/jq needed.
# Output: DIR (default ./phase4-<timestamp>) with summary.txt, trace.jsonl and
# per-step stdout/stderr. Send summary.txt and trace.jsonl back. Tokens are
# never logged; the trace does contain hostnames, JQL and issue keys.
set -uo pipefail

PROJECT="" INSTANCE="" USERNAME="" OUT=""
while [ $# -gt 0 ]; do
  case "$1" in
    --project) PROJECT="${2:?}"; shift ;;
    --instance) INSTANCE="${2:?}"; shift ;;
    --user) USERNAME="${2:?}"; shift ;;
    --out) OUT="${2:?}"; shift ;;
    -h|--help) sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1 (see --help)" >&2; exit 2 ;;
  esac
  shift
done
[ -n "$PROJECT" ] || { echo "--project KEY is required (a project you can browse)" >&2; exit 2; }
case "$PROJECT" in *[!A-Za-z0-9_]*) echo "invalid project key: $PROJECT" >&2; exit 2 ;; esac

JIRA_BIN="${JIRA_BIN:-jiracli}"
command -v "${JIRA_BIN%% *}" >/dev/null 2>&1 || { echo "jiracli not found ($JIRA_BIN); install it or set JIRA_BIN" >&2; exit 127; }
OUT="${OUT:-phase4-$(date +%Y%m%d-%H%M%S)}"
mkdir -p "$OUT/steps"
OUT=$(cd "$OUT" && pwd)

export JIRA_READ_ONLY=1 JIRA_LOG_LEVEL=debug JIRA_LOG_FORMAT=json JIRA_LOG_FILE="$OUT/trace.jsonl"
[ -n "$INSTANCE" ] && export JIRA_INSTANCE="$INSTANCE"
: > "$JIRA_LOG_FILE"
SUMMARY="$OUT/summary.txt"
: > "$SUMMARY"

PASS=0 FAIL=0 WARN=0 SKIP=0
say() { printf '%s\n' "$*" | tee -a "$SUMMARY"; }
# Millisecond clock via perl (present on macOS and most Linux); "" if absent.
ms() { perl -MTime::HiRes=time -e 'printf "%d\n", time*1000' 2>/dev/null || true; }
dur() { if [ -n "$1" ] && [ -n "$2" ]; then printf '%sms' "$(($2 - $1))"; else printf -- '-'; fi; }
shown() { local a out=""; for a in "$@"; do case "$a" in *[[:space:]\'\"]*) out="$out '$a'" ;; *) out="$out $a" ;; esac; done; printf '%s' "${out# }"; }

# step NAME "ALLOWED_EXITS" ARGS...   (stdin passed through)
# Sets LAST_EXIT; output is in $OUT/steps/NAME.out and .err.
step() {
  local name=$1 allowed=$2 t0 t1 rc
  shift 2
  t0=$(ms)
  # shellcheck disable=SC2086 # JIRA_BIN may contain arguments
  $JIRA_BIN "$@" > "$OUT/steps/$name.out" 2> "$OUT/steps/$name.err"
  rc=$?
  t1=$(ms)
  LAST_EXIT=$rc
  case " $allowed " in
    *" $rc "*) PASS=$((PASS + 1)); printf 'PASS  %-24s exit=%-2s %8s  jiracli %s\n' "$name" "$rc" "$(dur "$t0" "$t1")" "$(shown "$@")" | tee -a "$SUMMARY" ;;
    *) FAIL=$((FAIL + 1)); printf 'FAIL  %-24s exit=%-2s %8s  jiracli %s\n' "$name" "$rc" "$(dur "$t0" "$t1")" "$(shown "$@")" | tee -a "$SUMMARY"
       sed 's/^/      /' "$OUT/steps/$name.err" | head -12 | tee -a "$SUMMARY" ;;
  esac
}
note() { printf '      %s\n' "$*" | tee -a "$SUMMARY"; }
warn() { WARN=$((WARN + 1)); printf 'WARN  %s\n' "$*" | tee -a "$SUMMARY"; }
skip() { SKIP=$((SKIP + 1)); printf 'SKIP  %s\n' "$*" | tee -a "$SUMMARY"; }
check() { # check CONDITION-DESCRIPTION test-args...
  local d=$1; shift
  if "$@"; then PASS=$((PASS + 1)); printf 'PASS    %s\n' "$d" | tee -a "$SUMMARY"
  else FAIL=$((FAIL + 1)); printf 'FAIL    %s\n' "$d" | tee -a "$SUMMARY"; fi
}

# JSON helpers for the CLI's pretty-printed output (2-space indent, field
# order preserved). Issues/boards are objects inside a top-level array field,
# so their own keys sit at 6 spaces; users (bare array) at 4.
first() { grep -m1 -E "^$1\"$2\": " "$3" | sed -E 's/^ *"[^"]+": "?([^",]*)"?,?$/\1/'; }
count() { grep -cE "^$1\"$2\": " "$3"; }
http_requests() { grep -c '"msg":"http request"' "$JIRA_LOG_FILE"; }

say "jira-cli phase 4 (read-only) - $(date -u +%Y-%m-%dT%H:%M:%SZ)"
say "project: $PROJECT  instance: ${INSTANCE:-<default>}  cli: $($JIRA_BIN --version 2>/dev/null)"
say ""

say "== 1. Safety guard (must refuse locally, before anything else)"
before=$(http_requests)
step guard-delete "9" issue delete "$PROJECT-1" --yes
after=$(http_requests)
check "write refused with read_only_mode" grep -q read_only_mode "$OUT/steps/guard-delete.err"
check "no HTTP request sent by the refused write ($((after - before)) sent)" [ "$after" -eq "$before" ]
if [ "$LAST_EXIT" -ne 9 ] || [ "$after" -ne "$before" ]; then
  say ""
  case "$LAST_EXIT" in
    3) say "STOP: configuration or credential problem (exit 3), see $OUT/steps/guard-delete.err; run 'jiracli config validate' and 'jiracli doctor' first." ;;
    127) say "STOP: the jiracli command was not found or not executable ($JIRA_BIN)." ;;
    *) say "STOP: the read-only guard did not behave as expected (exit $LAST_EXIT); nothing else was run. Do not use this CLI against production until this is understood." ;;
  esac
  exit 1
fi

say ""; say "== 2. Connectivity, authentication, version"
# `config validate` checks every instance in the file. A broken instance that
# this run does not use is a warning, not a failure of the run.
step config-validate "0 3" config validate
if [ "$LAST_EXIT" -eq 3 ]; then
  bad=$(grep -o 'instance(s): [^"]*' "$OUT/steps/config-validate.err" | sed 's/^instance(s): //')
  if grep -q 'the selected instance' "$OUT/steps/config-validate.err"; then
    warn "other instance(s) in your config file are invalid (not used by this run): ${bad:-?}; fix or remove them (jiracli config validate)"
  else
    PASS=$((PASS - 1)); FAIL=$((FAIL + 1))
    printf 'FAIL    the selected instance is invalid: %s\n' "${bad:-see steps/config-validate.err}" | tee -a "$SUMMARY"
  fi
fi
step doctor "0" doctor
JIRA_VERSION=$(first '      ' jira_version "$OUT/steps/doctor.out")
[ -n "$JIRA_VERSION" ] || JIRA_VERSION=$(grep -m1 -o '"jira_version": "[^"]*"' "$OUT/steps/doctor.out" | sed 's/.*: "//; s/"$//')
note "jiracli version: ${JIRA_VERSION:-unknown} (CLI spec: 10.3.26)"
case "$JIRA_VERSION" in 10.3.*) ;; "") warn "could not read the Jira version from doctor" ;; *) warn "Jira $JIRA_VERSION differs from the spec version 10.3.26; some commands may differ" ;; esac
step auth-test "0" auth test
step myself "0" myself get
[ -n "$USERNAME" ] || USERNAME=$(first '  ' name "$OUT/steps/myself.out")
step server-info "0" server-info get

say ""; say "== 3. Issues"
JQL="project = $PROJECT ORDER BY key ASC"
step search-page "0" issue search --jql "$JQL" --max-results 5 --fields summary,status
TOTAL=$(first '  ' total "$OUT/steps/search-page.out")
KEY1=$(first '      ' key "$OUT/steps/search-page.out")
N=$(count '      ' key "$OUT/steps/search-page.out")
note "total issues in $PROJECT: ${TOTAL:-?}; first page: $N; first key: ${KEY1:-none}"
if [ "${TOTAL:-0}" -gt 0 ] 2>/dev/null; then
  want=5; [ "$TOTAL" -lt 5 ] && want=$TOTAL
  check "single page returns $want issue(s)" [ "$N" -eq "$want" ]
fi

pages0=$(grep -c '"msg":"page fetched"' "$JIRA_LOG_FILE")
step search-all "0" issue search --jql "$JQL" --all --max-pages 3 --max-results 20 --fields summary
pages=$(( $(grep -c '"msg":"page fetched"' "$JIRA_LOG_FILE") - pages0 ))
NA=$(count '      ' key "$OUT/steps/search-all.out")
UNIQ=$(grep -E '^      "key": ' "$OUT/steps/search-all.out" | sort -u | wc -l | tr -d ' ')
note "--all: $NA issues over $pages page(s) (cap 3 x 20)"
if [ "${TOTAL:-0}" -gt 0 ] 2>/dev/null; then
  want=60; [ "$TOTAL" -lt 60 ] && want=$TOTAL
  check "REST v2 pagination (total-based): $want issue(s) expected, got $NA" [ "$NA" -eq "$want" ]
  check "no duplicate issues across pages" [ "$UNIQ" -eq "$NA" ]
  check "page cap respected (<= 3 pages)" [ "$pages" -le 3 ]
fi

step search-post "0" issue search-post --stdin <<< "{\"jql\":\"$JQL\",\"maxResults\":3,\"fields\":[\"summary\"]}"
NP=$(count '      ' key "$OUT/steps/search-post.out")
note "search-post (POST, classified read): $NP issue(s)"
step search-yaml "0" issue search --jql "$JQL" --max-results 2 --fields summary --output yaml
step search-table "0" issue search --jql "$JQL" --max-results 3 --fields summary,status --output table

if [ -n "$KEY1" ]; then
  step issue-get "0" issue get "$KEY1" --fields summary,status,assignee
  step issue-transitions "0" issue get-transitions "$KEY1"
  step issue-comments "0" issue get-comments "$KEY1" --max-results 5
  step issue-editmeta "0" issue get-edit-meta "$KEY1"
else
  skip "issue get/transitions/comments/editmeta (no issue in $PROJECT)"
fi
step dry-run-create "0" issue create --dry-run --stdin <<< "{\"fields\":{\"project\":{\"key\":\"$PROJECT\"},\"summary\":\"phase4 dry run (never sent)\"}}"

say ""; say "== 4. Projects, fields"
step project-get "0" project get "$PROJECT"
step project-get-all "0" project get-all
step field-list "0" field list

say ""; say "== 5. Agile (isLast-based pagination)"
step boards-page "0" board get-all --project-key-or-id "$PROJECT" --max-results 10
BOARD=$(first '      ' id "$OUT/steps/boards-page.out")
NB=$(count '      ' id "$OUT/steps/boards-page.out")
note "boards for $PROJECT: $NB; first: ${BOARD:-none}"
pages0=$(grep -c '"msg":"page fetched"' "$JIRA_LOG_FILE")
step boards-all "0" board get-all --all --max-pages 2 --max-results 2
pages=$(( $(grep -c '"msg":"page fetched"' "$JIRA_LOG_FILE") - pages0 ))
note "board get-all --all: $(count '      ' id "$OUT/steps/boards-all.out") board(s) over $pages page(s) (cap 2 x 2)"
check "agile page cap respected (<= 2 pages)" [ "$pages" -le 2 ]
if [ -n "$BOARD" ]; then
  # Kanban boards have no sprints: Jira answers 400, which is fine here.
  step board-sprints "0 7" board get-all-sprints "$BOARD" --max-results 5
  [ "$LAST_EXIT" -eq 7 ] && note "board $BOARD does not support sprints (kanban?): acceptable"
else
  skip "board get-all-sprints (no board for $PROJECT)"
fi

say ""; say "== 6. Users (bare-array pagination)"
if [ -n "$USERNAME" ]; then
  step user-find "0" user find --username "$USERNAME" --max-results 5
  check "user search finds '$USERNAME'" grep -qE "^    \"name\": \"$USERNAME\"" "$OUT/steps/user-find.out"
else
  skip "user find (no user name)"
fi

say ""; say "== 7. Trace analysis"
TOTAL_REQ=$(http_requests)
WRITES=$(grep '"msg":"http request"' "$JIRA_LOG_FILE" | grep -v '"method":"GET"' | grep -v '"method":"HEAD"' | grep -vc '/rest/api/2/search"' || true)
POSTS=$(grep '"msg":"http request"' "$JIRA_LOG_FILE" | grep -c '"method":"POST"' || true)
note "HTTP requests: $TOTAL_REQ (POST: $POSTS, all to /rest/api/2/search)"
check "no write request reached Jira (non-GET other than search-post: $WRITES)" [ "$WRITES" -eq 0 ]
RESP=$(grep -c '"msg":"http response"' "$JIRA_LOG_FILE")
WITH_ID=$(grep '"msg":"http response"' "$JIRA_LOG_FILE" | grep -c '"jira_request_id"' || true)
note "responses with Jira request id: $WITH_ID/$RESP"
[ "$RESP" -gt 0 ] && [ "$WITH_ID" -lt "$RESP" ] && warn "some responses lack X-AREQUESTID (a proxy/gateway answered?)"
RETRIES=$(grep -c '"msg":"http retry"' "$JIRA_LOG_FILE" || true)
[ "$RETRIES" -gt 0 ] && warn "$RETRIES retried request(s) (rate limiting or 5xx); see trace.jsonl"
SLOW=$(grep '"msg":"http response"' "$JIRA_LOG_FILE" | grep -oE '"duration_ms":[0-9]+' | cut -d: -f2 | sort -n | tail -1)
note "slowest response: ${SLOW:-?} ms"

say ""
say "RESULT: $PASS passed, $FAIL failed, $WARN warning(s), $SKIP skipped"
say "Send back: $SUMMARY and $OUT/trace.jsonl (review the trace for hostnames/JQL first)."
[ "$FAIL" -eq 0 ]

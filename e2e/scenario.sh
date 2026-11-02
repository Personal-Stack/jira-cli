#!/usr/bin/env bash
# CLI scenario against a configured Jira 10.3 (see run.sh). Every step runs
# the CLI image inside the compose network and checks the exit code; JSON
# assertions run on the host with python3. IDs come from earlier outputs.
set -uo pipefail
cd "$(dirname "$0")"
OUT=.work/logs/steps
mkdir -p "$OUT"
RESULTS=.work/logs/e2e-results.jsonl
: > "$RESULTS"
FAILED=0

cli() { docker compose --profile cli run --rm -T cli "$@"; }

# step <name> <expected-exit> <args...>   (stdin is passed through)
step() {
  local name=$1 want=$2; shift 2
  local t0=$(date +%s%N)
  cli "$@" > "$OUT/$name.out" 2> "$OUT/$name.err"
  local got=$? ms=$(( ($(date +%s%N) - t0) / 1000000 ))
  local ok=true
  [ "$got" -eq "$want" ] || ok=false
  printf '{"step":"%s","exit":%d,"want":%d,"ok":%s,"ms":%d}\n' "$name" "$got" "$want" "$ok" "$ms" >> "$RESULTS"
  if $ok; then printf 'PASS  %-28s exit=%d  %5dms\n' "$name" "$got" "$ms"
  else
    printf 'FAIL  %-28s exit=%d want=%d\n' "$name" "$got" "$want"; sed 's/^/      /' "$OUT/$name.err" | tail -15; FAILED=1
  fi
}

# check <name> <python-expression over `d` = parsed stdout of step name>
check() {
  local name=$1 expr=$2
  if python3 - "$OUT/$name.out" "$expr" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
ok = eval(sys.argv[2], {"d": d, "any": any, "all": all, "len": len})
sys.exit(0 if ok else 1)
PY
  then printf 'PASS  %-28s %s\n' "  check" "$expr"
  else printf 'FAIL  %-28s %s\n' "  check" "$expr"; FAILED=1; fi
}

# jq-like extraction: val <name> <python-expression>
val() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(eval(sys.argv[2]))' "$OUT/$1.out" "$2"; }

echo "== discovery and diagnostics"
step version                0 --version
step commands-list          0 commands list
check commands-list         'd["count"] == 430'
step doctor                 0 doctor
check doctor                'all(c["status"] in ("ok","warn","skip") for c in d["checks"])'
check doctor                'any(c["name"]=="http" and c["data"]["jira_version"]=="10.3.26" for c in d["checks"])'
check doctor                'any(c["name"]=="tls" and c["status"]=="ok" for c in d["checks"])'
step auth-status            0 auth status
check auth-status           'd["auth"]["credential_present"] and d["auth"]["source"].startswith("file:")'
step auth-test              0 auth test
check auth-test             'd["user"]["name"] == "admin"'
step server-info            0 server-info get
check server-info           'd["version"] == "10.3.26"'

echo "== negative paths (layer attribution)"
step no-ca-bundle           8 --instance e2e-no-ca myself get
grep -q '"tls_certificate"' "$OUT/no-ca-bundle.err" && echo "PASS    check                      no-ca-bundle -> tls_certificate" || { echo "FAIL  no-ca-bundle layer"; FAILED=1; }
printf 'not-a-valid-token' > .work/secrets/jira-e2e-bad-pat; chmod 644 .work/secrets/jira-e2e-bad-pat
step bad-token              4 --instance e2e-badtoken auth test
step bad-jql                7 issue search --jql 'project = = broken'

echo "== project and issues"
if cli project get E2E > /dev/null 2>&1; then echo "SKIP  project-create (E2E exists)"; else
  step project-create       0 project create --yes --stdin <<'JSON'
{"key":"E2E","name":"E2E Test","projectTypeKey":"software","projectTemplateKey":"com.pyxis.greenhopper.jira:gh-simplified-scrum-classic","lead":"admin"}
JSON
fi
step issue-types            0 issue get-create-meta-project-issue-types E2E
TYPE_ID=$(val issue-types '[t["id"] for t in d["values"] if t["name"] in ("Task","Story")][0]')
echo "      issue type id: $TYPE_ID"

python3 - "$TYPE_ID" > .work/bulk.json <<'PY'
import json, sys
print(json.dumps({"issueUpdates": [{"fields": {"project": {"key": "E2E"}, "issuetype": {"id": sys.argv[1]},
    "summary": f"e2e issue {i:02d}"}} for i in range(55)]}))
PY
step create-bulk            0 issue create-bulk --stdin < .work/bulk.json
check create-bulk           'len(d["issues"]) == 55 and not d["errors"]'
KEY1=$(val create-bulk 'd["issues"][0]["key"]'); KEY2=$(val create-bulk 'd["issues"][1]["key"]')
echo "      created $KEY1 .. (55 issues)"

step search-page            0 issue search --jql 'project = E2E' --max-results 20 --fields summary
check search-page           'len(d["issues"]) == 20 and d["total"] >= 55'
step search-all             0 issue search --jql 'project = E2E ORDER BY key' --max-results 20 --fields summary --all
check search-all            'len(d["issues"]) == d["total"] >= 55 and len({i["key"] for i in d["issues"]}) == len(d["issues"])'
step search-post            0 issue search-post --stdin <<< '{"jql":"project = E2E","maxResults":5,"fields":["summary"]}'
check search-post           'len(d["issues"]) == 5'
step search-table           0 issue search --jql 'project = E2E' --max-results 3 --output table
grep -q "^KEY" "$OUT/search-table.out" || { echo "FAIL  table header"; FAILED=1; }
step get-yaml               0 issue get "$KEY1" --output yaml

step edit                   0 issue edit "$KEY1" --stdin <<< '{"fields":{"summary":"edited by jira-cli e2e"}}'
step get-after-edit         0 issue get "$KEY1" --fields summary
check get-after-edit        'd["fields"]["summary"] == "edited by jira-cli e2e"'
step comment                0 issue add-comment "$KEY1" --stdin <<< '{"body":"comment from jira-cli e2e"}'
step comments               0 issue get-comments "$KEY1"
check comments              'any(c["body"] == "comment from jira-cli e2e" for c in d["comments"])'

step transitions            0 issue get-transitions "$KEY1"
STATUS_BEFORE=$(cli issue get "$KEY1" --fields status 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin)["fields"]["status"]["name"])')
TRANSITION=$(val transitions '[t["id"] for t in d["transitions"] if t["to"]["name"] != "'"$STATUS_BEFORE"'"][0]')
step transition             0 issue do-transition "$KEY1" --stdin <<< "{\"transition\":{\"id\":\"$TRANSITION\"}}"
step get-after-transition   0 issue get "$KEY1" --fields status
check get-after-transition  'd["fields"]["status"]["name"] != "'"$STATUS_BEFORE"'"'

step attachment             0 issue add-attachment "$KEY1" --file /fixtures/attachment.txt
check attachment            'd[0]["filename"] == "attachment.txt"'

echo "== safety"
step delete-dry-run         0 issue delete "$KEY2" --dry-run
check delete-dry-run        'd["dry_run"] and d["request"]["method"] == "DELETE"'
step delete-no-confirm      9 issue delete "$KEY2"
step readonly-delete        9 --instance e2e-readonly issue delete "$KEY2" --yes
step readonly-search        0 --instance e2e-readonly issue search --jql 'project = E2E' --max-results 1
step get-before-delete      0 issue get "$KEY2" --fields summary
step delete                 0 issue delete "$KEY2" --yes
step get-deleted            6 issue get "$KEY2"

echo "== agile, users, fields"
step boards                 0 board get-all --all
check boards                'len(d["values"]) >= 1'
BOARD=$(val boards '[b["id"] for b in d["values"] if b.get("location",{}).get("projectKey") == "E2E"][0]')
step sprints                0 board get-all-sprints "$BOARD"
step user-find              0 user find --username admin
check user-find             'any(u["name"] == "admin" for u in d)'
step fields                 0 field list
check fields                'any(f["id"] == "summary" for f in d)'

echo "== log stream and secret scan"
python3 - .work/logs/jira-cli.log <<'PY' || FAILED=1
import json, sys
recs = [json.loads(l) for l in open(sys.argv[1])]
resp = [r for r in recs if r.get("msg") == "http response"]
with_id = [r for r in resp if r.get("jira_request_id")]
pages = [r for r in recs if r.get("msg") == "page fetched"]
print(f"PASS  log: {len(recs)} records, {len({r['run_id'] for r in recs})} runs, {len(resp)} HTTP responses, {len(with_id)} with jira_request_id, {len(pages)} pages fetched")
assert resp and len(with_id) == len(resp), "every Jira response should carry X-AREQUESTID"
assert len(pages) >= 3, "pagination should have fetched >= 3 pages"
perm = [r for r in recs if r.get("msg") == "credential file permissions"]
assert perm, "0644 secret should produce a permission warning"
PY
PAT=$(cat .work/secrets/jira-e2e-pat)
if grep -rlF -- "$PAT" .work/logs > /dev/null; then echo "FAIL  PAT found in:"; grep -rlF -- "$PAT" .work/logs; FAILED=1
else echo "PASS  PAT not present in any CLI output or log ($(find .work/logs -type f | wc -l) files)"; fi

python3 - "$RESULTS" <<'PY'
import json, sys
r = [json.loads(l) for l in open(sys.argv[1])]
print(f"\nsteps: {len(r)}  passed: {sum(x['ok'] for x in r)}  failed: {sum(not x['ok'] for x in r)}  total time: {sum(x['ms'] for x in r)/1000:.1f}s")
PY
exit $FAILED

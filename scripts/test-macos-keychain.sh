#!/usr/bin/env bash
# Verifies on macOS that the native CLI uses the Keychain:
#   1. TLS trust: a CA trusted in the System keychain is accepted with NO
#      tls.ca_bundle (Go's platform verifier), and rejected before it is trusted.
#   2. Tokens: `jiracli auth login` stores the PAT in the Keychain, `auth status`
#      reads it from there, `auth logout` removes it.
# Needs sudo (to add/remove the test CA in the System keychain). CI only.
# Usage: scripts/test-macos-keychain.sh [path/to/jira]
# Assertions use `cond && ok || ko`; ok() always succeeds, so SC2015 does not apply.
# shellcheck disable=SC2015
set -uo pipefail
[ "$(uname -s)" = Darwin ] || { echo "macOS only"; exit 2; }
BIN=$(cd "$(dirname "${1:-bin/jiracli}")" && pwd)/$(basename "${1:-bin/jiracli}")
WORK=$(mktemp -d)
CA_NAME="jira-cli keychain test CA $$"
REF="jira-cli-keychain-test-$$"
PORT=18443
pass=0 fail=0
ok() { echo "PASS  $*"; pass=$((pass + 1)); }
ko() { echo "FAIL  $*"; fail=$((fail + 1)); }
cleanup() {
  # wait: reap the server so bash does not print "Terminated: 15"
  [ -n "${SRV:-}" ] && { kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null; }
  sudo security delete-certificate -c "$CA_NAME" /Library/Keychains/System.keychain >/dev/null 2>&1
  security delete-generic-password -s jira-cli -a "$REF" >/dev/null 2>&1
  rm -rf "$WORK"
}
trap cleanup EXIT
cd "$WORK" || exit 1

# Test PKI (macOS requires SAN + serverAuth EKU and short validity for TLS leaves).
openssl req -x509 -newkey rsa:2048 -nodes -days 30 -subj "/CN=$CA_NAME" -keyout ca.key -out ca.pem \
  -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign" 2>/dev/null
openssl req -newkey rsa:2048 -nodes -subj "/CN=localhost" -keyout leaf.key -out leaf.csr 2>/dev/null
printf 'subjectAltName=DNS:localhost\nextendedKeyUsage=serverAuth\nbasicConstraints=CA:FALSE\n' > leaf.ext
openssl x509 -req -in leaf.csr -CA ca.pem -CAkey ca.key -CAcreateserial -days 30 -extfile leaf.ext -out leaf.pem 2>/dev/null
openssl s_server -accept "$PORT" -cert leaf.pem -key leaf.key -www > /dev/null 2>&1 &
SRV=$!
sleep 2

cat > config.yaml <<YAML
version: 1
default_instance: mac
instances:
  mac:
    deployment: datacenter
    base_url: https://localhost:$PORT
    auth: {type: pat, credential_ref: $REF}
    proxy: {disabled: true}
    retry: {max_attempts: 1}
YAML
export JIRA_CONFIG="$WORK/config.yaml"
unset JIRA_NO_KEYRING JIRA_CREDENTIAL_STORE JIRA_CREDENTIAL_FILE

tls_status() { "$BIN" doctor 2>/dev/null | python3 -c 'import json,sys; c=[x for x in json.load(sys.stdin)["checks"] if x["name"]=="tls"]; print(c[0]["status"]+" "+c[0].get("layer","") if c else "missing")'; }

s=$(tls_status)
case "$s" in fail*tls_certificate*) ok "before trusting the CA: tls fails ($s)" ;; *) ko "expected tls failure before trust, got: $s" ;; esac

sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ca.pem && ok "CA trusted in System keychain" || ko "could not add CA to keychain"
s=$(tls_status)
case "$s" in ok*) ok "after trusting in Keychain: tls ok with NO ca_bundle" ;; *) ko "expected tls ok after Keychain trust, got: $s" ;; esac

TOKEN="kc-test-token-$RANDOM$RANDOM"
out=$(printf '%s' "$TOKEN" | "$BIN" auth login --no-verify 2>&1)
echo "$out" | grep -q '"store": "keyring"' && ok "auth login stores in the Keychain" || { ko "auth login"; echo "$out"; }
# go-keyring stores "go-keyring-base64:" + base64(secret) on macOS (keyring_darwin.go)
# and decodes it on read; check the exact stored form.
stored=$(security find-generic-password -s jira-cli -a "$REF" -w 2>/dev/null)
[ "$stored" = "go-keyring-base64:$(printf '%s' "$TOKEN" | base64)" ] && ok "Keychain item jira-cli/$REF holds the token (go-keyring base64 encoding)" || ko "Keychain item missing or different (got ${#stored} chars)"
"$BIN" auth status 2>&1 | grep -q '"source": "keyring"' && ok "auth status reads it from the Keychain" || ko "auth status source"
"$BIN" auth logout > /dev/null 2>&1 && ! security find-generic-password -s jira-cli -a "$REF" >/dev/null 2>&1 && ok "auth logout removes the Keychain item" || ko "logout"

echo; echo "macOS keychain tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]

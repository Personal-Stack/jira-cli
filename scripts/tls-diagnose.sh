#!/usr/bin/env bash
# Usage: tls-diagnose.sh HOST[:PORT] [candidate-ca-file ...]
# Candidate files may be PEM, DER (.cer/.cert/.crt) or PKCS#7 (.p7b); the
# format is detected from the content, not the extension.
# Works with bash 3.2 (macOS) and later.
# Shows the certificate chain the server presents and tests each candidate
# CA file against it. Read-only: performs TLS handshakes, sends no data.
set -uo pipefail
target=$1; shift
host=${target%:*}; port=443; [ "$host" != "$target" ] && port=${target##*:}
work=$(mktemp -d); trap 'rm -rf "$work"' EXIT
proxy_args=()
if [ -n "${HTTPS_PROXY:-${https_proxy:-}}" ]; then
  p=${HTTPS_PROXY:-$https_proxy}; p=${p#*://}; p=${p%%/*}; p=${p##*@}
  proxy_args=(-proxy "$p")
  echo "NOTE: HTTPS_PROXY is set; connecting through $p, like the CLI does"
fi
openssl s_client -connect "$host:$port" -servername "$host" -showcerts ${proxy_args[@]+"${proxy_args[@]}"} </dev/null >"$work/raw" 2>"$work/err"
awk -v d="$work" '/-----BEGIN CERTIFICATE-----/{n++; f=sprintf("%s/c%d.pem",d,n)} f{print > f} /-----END CERTIFICATE-----/{f=""}' "$work/raw"
n=$(find "$work" -maxdepth 1 -name "c*.pem" | wc -l)
[ "$n" -gt 0 ] || { echo "no certificates received:"; cat "$work/err"; exit 1; }
echo "== Chain presented by $host:$port ($n certificate(s))"
for i in $(seq 1 "$n"); do
  f="$work/c$i.pem"
  echo "[$((i-1))] subject: $(openssl x509 -in "$f" -noout -subject -nameopt oneline | sed 's/^subject= *//')"
  echo "    issuer:  $(openssl x509 -in "$f" -noout -issuer -nameopt oneline | sed 's/^issuer= *//')"
  echo "    expires: $(openssl x509 -in "$f" -noout -enddate | cut -d= -f2)"
  aki=$(openssl x509 -in "$f" -noout -ext authorityKeyIdentifier 2>/dev/null | tail -n +2 | tr -d ' ' | sed 's/^keyid://')
  echo "    issuer key id: ${aki:-none}"
done
cat "$work"/c*.pem > "$work/chain.pem"
last="$work/c$n.pem"
if [ "$(openssl x509 -in "$last" -noout -subject -nameopt oneline | sed 's/^subject= *//')" = \
     "$(openssl x509 -in "$last" -noout -issuer -nameopt oneline | sed 's/^issuer= *//')" ]; then
  echo "NOTE: the server sends its root certificate too"
fi
need=$(openssl x509 -in "$last" -noout -issuer -nameopt oneline | sed 's/^issuer= *//')
echo; echo "== Needed: a CA certificate with subject \"$need\""
[ $# -gt 0 ] || exit 0
echo; echo "== Candidates"
# q NAME: single-quote a file name for copy-paste-safe commands (spaces, ').
q() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }
# to_pem FILE OUT: writes PEM certificates to OUT and prints the detected format.
to_pem() {
  if grep -qa -- "-----BEGIN CERTIFICATE-----" "$1"; then cp "$1" "$2"; echo pem
  elif grep -qa -- "-----BEGIN PKCS7-----" "$1" && openssl pkcs7 -print_certs -in "$1" -out "$2" 2>/dev/null; then echo pkcs7-pem
  elif openssl x509 -inform der -in "$1" -out "$2" 2>/dev/null; then echo der
  elif openssl pkcs7 -inform der -print_certs -in "$1" -out "$2" 2>/dev/null; then echo pkcs7-der
  else : > "$2"; echo unknown; fi
}
for cand in "$@"; do
  if [ ! -r "$cand" ]; then
    echo "$cand: file not found (if the file name contains spaces, put it in quotes: \"My CA 4.pem\")"
    continue
  fi
  fmt=$(to_pem "$cand" "$work/cand.pem")
  case $fmt in
    pem) echo "$cand: format PEM (usable as ca_bundle as-is)" ;;
    der) echo "$cand: format DER/binary (the CLI accepts it as-is; PEM copy: openssl x509 -inform der -in $(q "$cand") -out ca.pem)" ;;
    pkcs7-pem) echo "$cand: format PKCS#7 bundle; convert for the CLI: openssl pkcs7 -print_certs -in $(q "$cand") -out ca.pem" ;;
    pkcs7-der) echo "$cand: format PKCS#7 bundle (binary); convert for the CLI: openssl pkcs7 -inform der -print_certs -in $(q "$cand") -out ca.pem" ;;
    *) echo "$cand: not a certificate (neither PEM, DER nor PKCS#7)"; continue ;;
  esac
  # split multi-cert files so each certificate is reported separately
  rm -f "$work"/k*.pem
  awk -v d="$work" '/-----BEGIN CERTIFICATE-----/{n++; f=sprintf("%s/k%d.pem",d,n)} f{print > f} /-----END CERTIFICATE-----/{f=""}' "$work/cand.pem"
  k=$(find "$work" -maxdepth 1 -name "k*.pem" | wc -l)
  for j in $(seq 1 "$k"); do
    kf="$work/k$j.pem"
    subj=$(openssl x509 -in "$kf" -noout -subject -nameopt oneline | sed 's/^subject= *//')
    ski=$(openssl x509 -in "$kf" -noout -ext subjectKeyIdentifier 2>/dev/null | tail -n +2 | tr -d ' ')
    # -partial_chain: like the CLI (Go), accept any certificate in the CA file
    # as a trust anchor, not only self-signed roots.
    out=$(openssl verify -partial_chain -CAfile "$kf" -untrusted "$work/chain.pem" -verify_hostname "$host" "$work/c1.pem" 2>&1)
    if printf '%s' "$out" | grep -q ': OK$'; then res="OK - this file works as ca_bundle"
    else res="FAIL - $(printf '%s\n' "$out" | grep -m1 -E '^error [0-9]+|error [0-9]+ at' | sed 's/.*error [0-9]* at [0-9]* depth lookup: //; s/^error [0-9]*: //')"; fi
    echo "$cand #$j: $subj"
    echo "    key id: ${ski:-none}   expires: $(openssl x509 -in "$kf" -noout -enddate | cut -d= -f2)"
    echo "    verify: $res"
  done
done

#!/usr/bin/env bash
# Generates a throwaway CA and a server certificate for the TLS proxy.
# Output: e2e/.work/certs/{ca.pem,server.pem,server-key.pem}
set -euo pipefail
DIR=${1:-"$(dirname "$0")/.work/certs"}
mkdir -p "$DIR"
cd "$DIR"
[ -f server.pem ] && exit 0
openssl req -x509 -newkey rsa:2048 -nodes -days 30 -subj "/CN=jira-cli e2e CA" \
  -keyout ca-key.pem -out ca.pem 2>/dev/null
openssl req -newkey rsa:2048 -nodes -subj "/CN=jira-proxy" \
  -keyout server-key.pem -out server.csr 2>/dev/null
printf 'subjectAltName=DNS:jira-proxy,DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n' > ext.cnf
openssl x509 -req -in server.csr -CA ca.pem -CAkey ca-key.pem -CAcreateserial \
  -days 30 -extfile ext.cnf -out server.pem 2>/dev/null
chmod 644 ca.pem server.pem server-key.pem   # read by the nginx container user
rm -f server.csr ext.cnf ca.srl
echo "certificates written to $DIR"

#!/usr/bin/env bash
# Builds release archives for all supported platforms plus SHA256SUMS.
# Usage: scripts/dist.sh [version] [outdir]
# Output: <outdir>/jira-cli_<version>_<os>_<arch>.tar.gz (.zip on Windows)
set -euo pipefail
VERSION=${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
OUT=${2:-dist}
PLATFORMS=${PLATFORMS:-"linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"}
cd "$(dirname "$0")/.."
rm -rf "$OUT" && mkdir -p "$OUT"
for p in $PLATFORMS; do
  os=${p%/*} arch=${p#*/}
  name="jira-cli_${VERSION#v}_${os}_${arch}"
  stage="$OUT/$name"
  mkdir -p "$stage"
  exe=jira; [ "$os" = windows ] && exe=jira.exe
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" -o "$stage/$exe" ./cmd/jira
  cp README.md "$stage/"
  [ -f LICENSE ] && cp LICENSE "$stage/"
  if [ "$os" = windows ]; then
    (cd "$OUT" && zip -qr "$name.zip" "$name")
  else
    # Deterministic tarball: fixed owner/mtime/order.
    tar --sort=name --owner=0 --group=0 --numeric-owner --mtime='2000-01-01 00:00Z' \
      -C "$OUT" -czf "$OUT/$name.tar.gz" "$name"
  fi
  rm -rf "$stage"
  echo "built $name"
done
(cd "$OUT" && sha256sum jira-cli_* > SHA256SUMS)
echo "checksums: $OUT/SHA256SUMS"

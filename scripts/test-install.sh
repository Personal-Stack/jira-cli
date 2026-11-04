#!/usr/bin/env bash
# Tests scripts/install.sh end to end in isolated HOME directories, using a
# local HTTP mirror (--base-url) with real release archives from dist.sh.
# Usage: scripts/test-install.sh [dist-dir]   (builds the host platform if omitted)
# Assertions use `cond && ok || ko`; ok() always succeeds, so SC2015 does not apply.
# Release archive names are fixed ASCII, so ls in SC2012 is safe here.
# shellcheck disable=SC2015,SC2012
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/.." && pwd)
INSTALL="$ROOT/scripts/install.sh"
SH=$(command -v dash || command -v sh)   # POSIX shell, like `curl ... | sh`
WORK=$(mktemp -d)
VERSION=v0.0.0-installtest
# Scenarios use throwaway HOME directories. Keep Go's caches outside them:
# Go makes its module cache read-only, so a module cache under a temporary
# HOME cannot be deleted by a non-root user (CI runners), and a fresh cache
# would re-download every module and the toolchain for each scenario.
if command -v go >/dev/null 2>&1; then
  GOMODCACHE=$(go env GOMODCACHE) GOCACHE=$(go env GOCACHE) GOPATH=$(go env GOPATH)
  export GOMODCACHE GOCACHE GOPATH
fi
cleanup() {
  # wait returns 143 for the killed server; it must not trip set -e in the trap
  if [ -n "${SRV:-}" ]; then kill "$SRV" 2>/dev/null || true; wait "$SRV" 2>/dev/null || true; fi
  chmod -R u+w "$WORK" 2>/dev/null   # in case anything read-only was created anyway
  rm -rf "$WORK"
}
trap cleanup EXIT

# Host platform, mapped like install.sh does, so scenarios use the archive
# this machine can run (CI passes archives for every platform).
HOST_OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in x86_64|amd64) HOST_ARCH=amd64 ;; aarch64|arm64) HOST_ARCH=arm64 ;; *) HOST_ARCH=$(uname -m) ;; esac
DIST=${1:-}
if [ -z "$DIST" ]; then
  DIST="$WORK/dist"
  PLATFORMS="$(go env GOOS)/$(go env GOARCH)" "$ROOT/scripts/dist.sh" "$VERSION" "$DIST" > /dev/null
fi
MIRROR="$WORK/mirror"
mkdir -p "$MIRROR/$VERSION"
cp "$DIST"/jira-cli_* "$DIST/jira-docker.sh" "$DIST/SHA256SUMS" "$MIRROR/$VERSION/"
echo "$VERSION" > "$MIRROR/LATEST"
PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')
(cd "$MIRROR" && exec python3 -m http.server "$PORT" --bind 127.0.0.1 > "$WORK/http.log" 2>&1) &
SRV=$!
for _ in $(seq 1 50); do curl -fs "http://127.0.0.1:$PORT/LATEST" > /dev/null 2>&1 && break; sleep 0.1; done
BASE="http://127.0.0.1:$PORT"

GODIR=$(dirname "$(command -v go 2>/dev/null || echo /usr/bin/go)")
pass=0 fail=0
ok() { echo "PASS  $*"; pass=$((pass + 1)); }
ko() { echo "FAIL  $*"; fail=$((fail + 1)); }

# fresh_home <shell>: isolated HOME with a minimal PATH (no ~/.local/bin)
fresh_home() {
  local H
  H="$WORK/home-$RANDOM"; mkdir -p "$H"
  printf '# user content\nalias ll="ls -l"\n' > "$H/.bashrc"
  cp "$H/.bashrc" "$H/.bashrc.orig"
  export HOME="$H" SHELL="/bin/$1"
  unset XDG_CONFIG_HOME XDG_DATA_HOME ZDOTDIR
  PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:$GODIR"
  export PATH
}
run() { env -u GITHUB_TOKEN PATH="$PATH:/nonexistent-gh" "$SH" "$INSTALL" "$@" > "$WORK/out" 2>&1; }
blocks() { grep -c "^# >>> jira-cli >>>" "$1" 2>/dev/null || true; }

echo "== mirror install (latest), bash"
fresh_home bash
if run --base-url "$BASE"; then ok "install exits 0"; else ko "install"; cat "$WORK/out"; fi
[ -x "$HOME/.local/bin/jiracli" ] && ok "binary at ~/.local/bin/jiracli" || ko "binary missing"
"$HOME/.local/bin/jiracli" --version | grep -q "${VERSION}" && ok "version $VERSION" || ko "wrong version"
grep -q "sha256 verified" "$WORK/out" && ok "checksum verified" || ko "no checksum message"
[ "$(blocks "$HOME/.bashrc")" = 1 ] && ok "PATH block added to .bashrc" || ko "PATH block"
grep -q 'alias ll=' "$HOME/.bashrc" && ok "existing .bashrc content kept" || ko "rc content lost"
bash -c "source '$HOME/.bashrc'; command -v jiracli" | grep -q "$HOME/.local/bin/jiracli" && ok "new bash shell finds jiracli" || ko "jiracli not on PATH in new shell"
run --base-url "$BASE" && [ "$(blocks "$HOME/.bashrc")" = 1 ] && ok "re-install is idempotent (one block)" || ko "duplicate block"
run --base-url "$BASE" --uninstall && ok "uninstall exits 0" || { ko "uninstall exit"; cat "$WORK/out"; }
[ ! -e "$HOME/.local/bin/jiracli" ] && ok "uninstall removes binary" || ko "binary left"
cmp -s "$HOME/.bashrc" "$HOME/.bashrc.orig" && ok "uninstall restores .bashrc exactly" || { ko ".bashrc differs"; diff "$HOME/.bashrc.orig" "$HOME/.bashrc"; }

echo "== piped (curl ... | sh -s -- ...)"
fresh_home bash
run_piped() { (cd "$(dirname "$INSTALL")" && env -u GITHUB_TOKEN PATH="$PATH:/nonexistent-gh" "$SH" -s -- "$@" < "$INSTALL" > "$WORK/out" 2>&1); }
if run_piped --base-url "$BASE"; then ok "piped install"; else ko "piped install"; cat "$WORK/out"; fi
[ -x "$HOME/.local/bin/jiracli" ] && ok "piped: binary installed" || ko "piped: binary missing"
# Run from inside the clone, piped: the clone must not be used.
if run_piped --from-source; then ko "piped --from-source used the current directory"; else grep -q "needs a clone" "$WORK/out" && ok "piped: the current directory is not taken for a clone" || { ko "piped --from-source message"; cat "$WORK/out"; }; fi

echo "== upgrade from 'jira' (renamed in v0.3.0)"
fresh_home bash
data="$HOME/.local/share"; mkdir -p "$HOME/.local/bin" "$data/bash-completion/completions"
printf '\177ELF old build of github.com/personal-stack/jira-cli\n' > "$HOME/.local/bin/jira"
echo "# bash completion V2 for jira" > "$data/bash-completion/completions/jira"
if run --base-url "$BASE"; then ok "install over an old jira"; else ko "install over an old jira"; cat "$WORK/out"; fi
[ ! -e "$HOME/.local/bin/jira" ] && [ ! -e "$data/bash-completion/completions/jira" ] && [ -x "$HOME/.local/bin/jiracli" ] && ok "old native jira and its completion removed" || ko "old native jira kept"
printf '#!/bin/sh\n# jira: runs jira-cli from its Docker image as if it were installed natively.\n' > "$HOME/.local/bin/jira"
run --base-url "$BASE" && [ ! -e "$HOME/.local/bin/jira" ] && ok "old Docker wrapper jira removed" || { ko "old wrapper kept"; cat "$WORK/out"; }
printf '#!/bin/sh\necho another jira\n' > "$HOME/.local/bin/jira"
echo "# bash completion V2 for jira" > "$data/bash-completion/completions/jira"
run --base-url "$BASE" && [ -f "$HOME/.local/bin/jira" ] && [ -f "$data/bash-completion/completions/jira" ] && grep -q "kept .*: it is not jira-cli" "$WORK/out" && ok "a jira from another project is kept" || { ko "foreign jira touched"; cat "$WORK/out"; }
grep -c "^# >>> jira-cli >>>" "$HOME/.bashrc" | grep -qx 1 && ok "PATH block kept" || ko "PATH block"

echo "== zsh with completions, explicit version, custom name"
fresh_home zsh
if run --base-url "$BASE" --version "$VERSION" --completions --name jira-dc; then ok "install"; else ko "install"; cat "$WORK/out"; fi
[ -x "$HOME/.local/bin/jira-dc" ] && ok "installed as jira-dc" || ko "custom name"
grep -q 'fpath=' "$HOME/.zshrc" && ok "zsh fpath added" || ko "zsh fpath"
[ -s "$HOME/.local/share/zsh/site-functions/_jira-dc" ] && ok "zsh completion" || ko "zsh completion"
[ -s "$HOME/.local/share/bash-completion/completions/jira-dc" ] && ok "bash completion" || ko "bash completion"
run --uninstall --name jira-dc && ok "uninstall exits 0" || { ko "uninstall exit"; cat "$WORK/out"; }
[ ! -e "$HOME/.local/share/zsh/site-functions/_jira-dc" ] && ! grep -q jira-cli "$HOME/.zshrc" && ok "uninstall cleans completions and .zshrc" || ko "zsh cleanup"

echo "== fish"
fresh_home fish
run --base-url "$BASE" && grep -q "fish_add_path -g $HOME/.local/bin" "$HOME/.config/fish/conf.d/jira-cli.fish" && ok "fish conf.d snippet" || { ko "fish"; cat "$WORK/out"; }

echo "== already on PATH: no rc edits"
fresh_home sh
mkdir -p "$HOME/bin"; PATH="$HOME/bin:$PATH"
run --base-url "$BASE" --dir "$HOME/bin" && [ ! -f "$HOME/.profile" ] && ok "no rc change when dir already on PATH" || { ko "rc modified"; cat "$WORK/out"; }

echo "== --no-modify-path"
fresh_home bash
run --base-url "$BASE" --no-modify-path && [ "$(blocks "$HOME/.bashrc")" = 0 ] && ok "rc untouched" || ko "--no-modify-path"

echo "== offline archive"
fresh_home bash
ARCH="$MIRROR/$VERSION/jira-cli_${VERSION#v}_${HOST_OS}_${HOST_ARCH}.tar.gz"
run --archive "$ARCH" && grep -q "sha256 verified" "$WORK/out" && ok "archive install verified via adjacent SHA256SUMS" || { ko "archive"; cat "$WORK/out"; }

echo "== archive for another platform"
fresh_home bash
run --archive "$ARCH" > /dev/null 2>&1   # a working installation to protect
other_os=linux; [ "$HOST_OS" = linux ] && other_os=darwin
OTHER="$WORK/jira-cli_${VERSION#v}_${other_os}_${HOST_ARCH}.tar.gz"
cp "$ARCH" "$OTHER"   # name decides the platform check; content is irrelevant here
if run --archive "$OTHER"; then ko "wrong-platform archive accepted"
else grep -q "is built for ${other_os}/${HOST_ARCH}, but this machine is ${HOST_OS}/${HOST_ARCH}" "$WORK/out" && ok "wrong-platform archive refused with a clear message" || { ko "wrong message"; cat "$WORK/out"; }; fi
"$HOME/.local/bin/jiracli" --version > /dev/null 2>&1 && ok "existing installation left working" || ko "existing installation damaged"
# Right name, broken binary (e.g. corrupted download without SHA256SUMS):
mkdir -p "$WORK/broken/jira-cli_${VERSION#v}_${HOST_OS}_${HOST_ARCH}" "$WORK/brokenpkg"
printf 'not a program\n' > "$WORK/broken/jira-cli_${VERSION#v}_${HOST_OS}_${HOST_ARCH}/jiracli"; chmod +x "$WORK/broken/jira-cli_${VERSION#v}_${HOST_OS}_${HOST_ARCH}/jiracli"
tar -czf "$WORK/brokenpkg/jira-cli_${VERSION#v}_${HOST_OS}_${HOST_ARCH}.tar.gz" -C "$WORK/broken" "jira-cli_${VERSION#v}_${HOST_OS}_${HOST_ARCH}"
if run --archive "$WORK/brokenpkg/jira-cli_${VERSION#v}_${HOST_OS}_${HOST_ARCH}.tar.gz"; then ko "non-running binary installed"
else grep -q "does not run on this machine" "$WORK/out" && ok "non-running binary refused before replacing anything" || { ko "wrong message"; cat "$WORK/out"; }; fi
"$HOME/.local/bin/jiracli" --version > /dev/null 2>&1 && ok "existing installation still working" || ko "existing installation replaced by a broken binary"
staged=$(find "$HOME/.local/bin" -maxdepth 1 -name '.jira.tmp*' | head -n1)
[ -z "$staged" ] && ok "no staged temp file left behind" || ko "staged temp file left behind: $staged"

echo "== integrity and transport failures"
fresh_home bash
cp -r "$MIRROR/$VERSION" "$MIRROR/v9.9.9"
for f in "$MIRROR/v9.9.9"/jira-cli_*; do mv "$f" "${f/$VERSION/v9.9.9}" 2>/dev/null || true; done
for f in "$MIRROR/v9.9.9"/jira-cli_*; do n=$(basename "$f"); mv "$f" "$MIRROR/v9.9.9/${n/${VERSION#v}/9.9.9}"; done
sed "s/${VERSION#v}/9.9.9/" "$MIRROR/$VERSION/SHA256SUMS" > "$MIRROR/v9.9.9/SHA256SUMS"
for f in "$MIRROR/v9.9.9"/jira-cli_*; do printf 'tampered' >> "$f"; done   # every platform's archive
if run --base-url "$BASE" --version v9.9.9; then ko "tampered archive accepted"; else grep -q "checksum mismatch" "$WORK/out" && ok "tampered archive rejected" || { ko "wrong failure"; cat "$WORK/out"; }; fi
[ ! -e "$HOME/.local/bin/jiracli" ] && ok "nothing installed after checksum failure" || ko "binary installed despite mismatch"
if run --base-url "http://example.com/mirror" --version "$VERSION"; then ko "plain http accepted"; else grep -q "refusing non-HTTPS" "$WORK/out" && ok "non-HTTPS mirror refused" || { ko "wrong failure"; cat "$WORK/out"; }; fi

if command -v go > /dev/null; then
  echo "== from source"
  fresh_home bash
  if run --from-source "$ROOT"; then ok "built and installed from source"; else ko "from source"; cat "$WORK/out"; fi
fi

# Docker wrapper mode; needs a running daemon and an existing image
# (TEST_DOCKER_IMAGE, default jira-cli:local) so no build/pull happens here.
DIMG=${TEST_DOCKER_IMAGE:-jira-cli:local}
if command -v docker > /dev/null && docker image inspect "$DIMG" > /dev/null 2>&1; then
  echo "== --docker from a clone (image $DIMG)"
  fresh_home bash
  if run --docker --image "$DIMG"; then ok "install --docker"; else ko "install --docker"; cat "$WORK/out"; fi
  head -1 "$HOME/.local/bin/jiracli" | grep -q '^#!/bin/sh' && grep -q "JIRA_CLI_IMAGE:-$DIMG" "$HOME/.local/bin/jiracli" && ok "wrapper installed with the image baked in" || ko "wrapper content"
  "$HOME/.local/bin/jiracli" --version | grep -q "jiracli version" && ok "wrapper runs the container" || ko "wrapper run"
  [ "$(blocks "$HOME/.bashrc")" = 1 ] && ok "PATH block added" || ko "PATH block"
  run --uninstall && [ ! -e "$HOME/.local/bin/jiracli" ] && cmp -s "$HOME/.bashrc" "$HOME/.bashrc.orig" && ok "uninstall restores everything" || ko "docker uninstall"

  echo "== --docker from a release mirror (wrapper checksum-verified)"
  fresh_home bash
  cp "$INSTALL" "$WORK/install-copy.sh"   # outside the repo: forces the release path
  if ( cd "$WORK" && env -u GITHUB_TOKEN "$SH" "$WORK/install-copy.sh" --docker --image "$DIMG" --base-url "$BASE" > "$WORK/out" 2>&1 ); then
    grep -q "sha256 verified" "$WORK/out" && ok "wrapper downloaded and verified" || { ko "no verification"; cat "$WORK/out"; }
  else ko "mirror --docker install"; cat "$WORK/out"; fi
  sed "s/^#!/# tampered\n#!/" "$MIRROR/$VERSION/jira-docker.sh" > "$WORK/t" && cp "$WORK/t" "$MIRROR/v9.9.9/jira-docker.sh" && grep -v jira-docker.sh "$MIRROR/v9.9.9/SHA256SUMS" > "$WORK/s" && grep jira-docker.sh "$MIRROR/$VERSION/SHA256SUMS" >> "$WORK/s" && cp "$WORK/s" "$MIRROR/v9.9.9/SHA256SUMS"
  fresh_home bash
  if ( cd "$WORK" && env -u GITHUB_TOKEN "$SH" "$WORK/install-copy.sh" --docker --image "$DIMG" --base-url "$BASE" --version v9.9.9 > "$WORK/out" 2>&1 ); then ko "tampered wrapper accepted"
  else grep -q "checksum mismatch for jira-docker.sh" "$WORK/out" && ok "tampered wrapper rejected" || { ko "wrong failure"; cat "$WORK/out"; }; fi
else
  echo "SKIP  --docker scenarios (no docker daemon or image $DIMG)"
fi

echo
echo "installer tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]

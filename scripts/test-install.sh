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
cleanup() { [ -n "${SRV:-}" ] && kill "$SRV" 2>/dev/null; rm -rf "$WORK"; }
trap cleanup EXIT

DIST=${1:-}
if [ -z "$DIST" ]; then
  DIST="$WORK/dist"
  PLATFORMS="$(go env GOOS)/$(go env GOARCH)" "$ROOT/scripts/dist.sh" "$VERSION" "$DIST" > /dev/null
fi
MIRROR="$WORK/mirror"
mkdir -p "$MIRROR/$VERSION"
cp "$DIST"/jira-cli_* "$DIST/SHA256SUMS" "$MIRROR/$VERSION/"
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
[ -x "$HOME/.local/bin/jira" ] && ok "binary at ~/.local/bin/jira" || ko "binary missing"
"$HOME/.local/bin/jira" --version | grep -q "${VERSION}" && ok "version $VERSION" || ko "wrong version"
grep -q "sha256 verified" "$WORK/out" && ok "checksum verified" || ko "no checksum message"
[ "$(blocks "$HOME/.bashrc")" = 1 ] && ok "PATH block added to .bashrc" || ko "PATH block"
grep -q 'alias ll=' "$HOME/.bashrc" && ok "existing .bashrc content kept" || ko "rc content lost"
bash -c "source '$HOME/.bashrc'; command -v jira" | grep -q "$HOME/.local/bin/jira" && ok "new bash shell finds jira" || ko "jira not on PATH in new shell"
run --base-url "$BASE" && [ "$(blocks "$HOME/.bashrc")" = 1 ] && ok "re-install is idempotent (one block)" || ko "duplicate block"
run --base-url "$BASE" --uninstall && ok "uninstall exits 0" || { ko "uninstall exit"; cat "$WORK/out"; }
[ ! -e "$HOME/.local/bin/jira" ] && ok "uninstall removes binary" || ko "binary left"
cmp -s "$HOME/.bashrc" "$HOME/.bashrc.orig" && ok "uninstall restores .bashrc exactly" || { ko ".bashrc differs"; diff "$HOME/.bashrc.orig" "$HOME/.bashrc"; }

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
ARCH=$(ls "$MIRROR/$VERSION"/jira-cli_*.tar.gz | head -n1)
run --archive "$ARCH" && grep -q "sha256 verified" "$WORK/out" && ok "archive install verified via adjacent SHA256SUMS" || { ko "archive"; cat "$WORK/out"; }

echo "== integrity and transport failures"
fresh_home bash
cp -r "$MIRROR/$VERSION" "$MIRROR/v9.9.9"
for f in "$MIRROR/v9.9.9"/jira-cli_*; do mv "$f" "${f/$VERSION/v9.9.9}" 2>/dev/null || true; done
for f in "$MIRROR/v9.9.9"/jira-cli_*; do n=$(basename "$f"); mv "$f" "$MIRROR/v9.9.9/${n/${VERSION#v}/9.9.9}"; done
sed "s/${VERSION#v}/9.9.9/" "$MIRROR/$VERSION/SHA256SUMS" > "$MIRROR/v9.9.9/SHA256SUMS"
printf 'tampered' >> "$(ls "$MIRROR/v9.9.9"/jira-cli_*.tar.gz | head -n1)"
if run --base-url "$BASE" --version v9.9.9; then ko "tampered archive accepted"; else grep -q "checksum mismatch" "$WORK/out" && ok "tampered archive rejected" || { ko "wrong failure"; cat "$WORK/out"; }; fi
[ ! -e "$HOME/.local/bin/jira" ] && ok "nothing installed after checksum failure" || ko "binary installed despite mismatch"
if run --base-url "http://example.com/mirror" --version "$VERSION"; then ko "plain http accepted"; else grep -q "refusing non-HTTPS" "$WORK/out" && ok "non-HTTPS mirror refused" || { ko "wrong failure"; cat "$WORK/out"; }; fi

if command -v go > /dev/null; then
  echo "== from source"
  fresh_home bash
  if run --from-source "$ROOT"; then ok "built and installed from source"; else ko "from source"; cat "$WORK/out"; fi
fi

echo
echo "installer tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]

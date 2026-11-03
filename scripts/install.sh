#!/bin/sh
# jira-cli installer for Linux and macOS.
#
# Installs the `jira` binary to ~/.local/bin (no sudo) and adds that directory
# to PATH in your shell startup file. Every download is verified against the
# release's SHA256SUMS.
#
#   sh install.sh                        latest release
#   sh install.sh --version v0.2.0       a specific release
#   sh install.sh --dir /usr/local/bin   system-wide (may need sudo)
#   sh install.sh --from-source          build from this repository clone (needs Go)
#   sh install.sh --archive FILE         offline install from a downloaded archive
#   sh install.sh --docker               no Go/binary: install a `jira` wrapper that runs the Docker image
#   sh install.sh --uninstall            remove binary, completions and PATH entry
#
# Run `sh install.sh --help` for all options. Windows: use install.ps1.
set -eu

REPO="Personal-Stack/jira-cli"
VERSION="${JIRA_CLI_VERSION:-}"
INSTALL_DIR="${JIRA_CLI_INSTALL_DIR:-$HOME/.local/bin}"
BASE_URL="${JIRA_CLI_BASE_URL:-}"
BIN_NAME="jira"
MODIFY_PATH=1
COMPLETIONS=0
FROM_SOURCE=0
ARCHIVE=""
UNINSTALL=0
DOCKER=0
IMAGE="${JIRA_CLI_IMAGE:-}"
MARK_BEGIN="# >>> jira-cli >>>"
MARK_END="# <<< jira-cli <<<"

say() { printf '%s\n' "$*"; }
info() { printf '  %s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

usage() {
  sed -n '2,15p' "$0" 2>/dev/null | sed 's/^# \{0,1\}//'
  cat <<USAGE
Options:
  --version TAG        release tag to install (default: latest)
  --dir DIR            install directory (default: \$HOME/.local/bin; env JIRA_CLI_INSTALL_DIR)
  --name NAME          binary name (default: jira; e.g. jira-dc if another 'jira' exists)
  --no-modify-path     do not edit shell startup files
  --completions        install shell completions (bash, zsh, fish)
  --base-url URL       mirror with layout URL/<tag>/<asset> (env JIRA_CLI_BASE_URL)
  --from-source [DIR]  build with Go from a repository clone (default: this script's repo)
  --archive FILE       install from a local release archive (SHA256SUMS next to it, if present, is checked)
  --docker             install a 'jira' wrapper that runs the CLI in Docker (no Go needed).
                       From a clone it builds jira-cli:local; otherwise it pulls
                       ghcr.io/personal-stack/jira-cli:<version>
  --image IMAGE        image for --docker (env JIRA_CLI_IMAGE)
  --uninstall          remove the binary, completions and the PATH block
  -h, --help           show this help

Private repository: downloads use the GitHub CLI ('gh auth login') or GITHUB_TOKEN.
USAGE
}

SRC_DIR=""
while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="${2:?--version needs a value}"; shift ;;
    --dir) INSTALL_DIR="${2:?--dir needs a value}"; shift ;;
    --name) BIN_NAME="${2:?--name needs a value}"; shift ;;
    --no-modify-path) MODIFY_PATH=0 ;;
    --completions) COMPLETIONS=1 ;;
    --base-url) BASE_URL="${2:?--base-url needs a value}"; shift ;;
    --from-source)
      FROM_SOURCE=1
      if [ $# -gt 1 ] && [ "${2#-}" = "$2" ]; then SRC_DIR="$2"; shift; fi ;;
    --archive) ARCHIVE="${2:?--archive needs a value}"; shift ;;
    --uninstall) UNINSTALL=1 ;;
    --docker) DOCKER=1 ;;
    --image) IMAGE="${2:?--image needs a value}"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option: $1 (see --help)" ;;
  esac
  shift
done

case "$BIN_NAME" in *[!A-Za-z0-9._-]*|"") die "invalid --name '$BIN_NAME'" ;; esac
TARGET="$INSTALL_DIR/$BIN_NAME"

# ---------- shell integration ----------

rc_files() {
  # Startup files that get the PATH block, based on the user's login shell.
  case "$(basename "${SHELL:-sh}")" in
    zsh) printf '%s\n' "${ZDOTDIR:-$HOME}/.zshrc" ;;
    bash)
      printf '%s\n' "$HOME/.bashrc"
      # macOS Terminal starts login shells, which read .bash_profile.
      [ "$(uname -s)" = Darwin ] && printf '%s\n' "$HOME/.bash_profile"
      ;;
    fish) printf '%s\n' "${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d/jira-cli.fish" ;;
    *) printf '%s\n' "$HOME/.profile" ;;
  esac
}

remove_block() { # remove_block FILE
  [ -f "$1" ] || return 0
  if grep -qF "$MARK_BEGIN" "$1"; then
    tmp="$1.jira-cli.tmp"
    # Drop the block and the single blank line add_block put before it.
    awk -v b="$MARK_BEGIN" -v e="$MARK_END" '
      $0 == b { held = 0; skip = 1; next }
      $0 == e { skip = 0; next }
      skip { next }
      $0 == "" { if (held) print ""; held = 1; next }
      { if (held) print ""; held = 0; print }
      END { if (held) print "" }' "$1" > "$tmp"
    cat "$tmp" > "$1" && rm -f "$tmp"   # keep the original file's mode/owner
    info "removed jira-cli block from $1"
  fi
}

add_block() { # add_block FILE
  f="$1"
  mkdir -p "$(dirname "$f")"
  remove_block "$f"
  case "$f" in
    *.fish)
      {
        printf '%s\n' "$MARK_BEGIN"
        printf 'fish_add_path -g %s\n' "$INSTALL_DIR"
        printf '%s\n' "$MARK_END"
      } >> "$f" ;;
    *)
      {
        printf '\n%s\n' "$MARK_BEGIN"
        # shellcheck disable=SC2016 # $PATH must stay literal in the rc file
        printf 'case ":$PATH:" in *":%s:"*) ;; *) export PATH="%s:$PATH" ;; esac\n' "$INSTALL_DIR" "$INSTALL_DIR"
        if [ "$COMPLETIONS" = 1 ] && [ "${f##*/}" = .zshrc ]; then
          # shellcheck disable=SC2016 # $fpath must stay literal in the rc file
          printf 'fpath=("%s" $fpath)\n' "${XDG_DATA_HOME:-$HOME/.local/share}/zsh/site-functions"
        fi
        printf '%s\n' "$MARK_END"
      } >> "$f" ;;
  esac
  info "added $INSTALL_DIR to PATH in $f"
}

completion_paths() {
  data="${XDG_DATA_HOME:-$HOME/.local/share}"
  printf '%s\n' \
    "bash $data/bash-completion/completions/$BIN_NAME" \
    "zsh $data/zsh/site-functions/_$BIN_NAME" \
    "fish ${XDG_CONFIG_HOME:-$HOME/.config}/fish/completions/$BIN_NAME.fish"
}

install_completions() {
  completion_paths | while read -r sh path; do
    mkdir -p "$(dirname "$path")"
    if "$TARGET" completion "$sh" > "$path.tmp" 2>/dev/null; then
      mv "$path.tmp" "$path"; info "$sh completion: $path"
    else
      rm -f "$path.tmp"; warn "could not generate $sh completion"
    fi
  done
}

# ---------- uninstall ----------

if [ "$UNINSTALL" = 1 ]; then
  say "Uninstalling jira-cli"
  if [ -f "$TARGET" ]; then rm -f "$TARGET"; info "removed $TARGET"; else info "no binary at $TARGET"; fi
  completion_paths | while read -r _ path; do
    if [ -f "$path" ]; then rm -f "$path"; info "removed $path"; fi
  done
  for f in "${ZDOTDIR:-$HOME}/.zshrc" "$HOME/.bashrc" "$HOME/.bash_profile" "$HOME/.profile" \
           "${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d/jira-cli.fish"; do
    remove_block "$f"
  done
  say "Done. Configuration (~/.config/jira-cli) and keyring entries were left untouched."
  exit 0
fi

# ---------- platform ----------

os=$(uname -s); arch=$(uname -m)
case "$os" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  MINGW*|MSYS*|CYGWIN*) die "Windows detected: use install.ps1 (PowerShell)" ;;
  *) die "unsupported operating system: $os" ;;
esac
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "unsupported architecture: $arch (supported: amd64, arm64)" ;;
esac

TMP=$(mktemp -d 2>/dev/null || mktemp -d -t jira-cli)
trap 'rm -rf "$TMP"' EXIT INT TERM

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
  else die "need sha256sum or shasum to verify downloads"; fi
}

verify() { # verify FILE SUMSFILE
  want=$(awk -v n="$(basename "$1")" '$2==n || $2=="*"n {print $1}' "$2")
  [ -n "$want" ] || die "$(basename "$1") is not listed in SHA256SUMS"
  got=$(sha256 "$1")
  [ "$want" = "$got" ] || die "checksum mismatch for $(basename "$1") (expected $want, got $got)"
  info "sha256 verified: $got"
}

http_get() { # http_get URL OUT [extra curl args...]
  url="$1"; out="$2"; shift 2
  case "$url" in
    https://*) ;;
    http://127.0.0.1*|http://localhost*) ;;   # local mirrors / tests only
    *) die "refusing non-HTTPS download URL: $url" ;;
  esac
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 --connect-timeout 15 -o "$out" "$@" "$url"
  elif command -v wget >/dev/null 2>&1; then
    [ $# -eq 0 ] || die "wget fallback does not support authenticated downloads; install curl"
    wget -q -O "$out" "$url"
  else
    die "need curl or wget"
  fi
}

# ---------- release downloads ----------

have_gh() { command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; }

resolve_version() {
  [ -z "$VERSION" ] || return 0
  say "Resolving latest release"
  if [ -n "$BASE_URL" ]; then
    http_get "$BASE_URL/LATEST" "$TMP/LATEST"; VERSION=$(tr -d ' \r\n' < "$TMP/LATEST")
  elif have_gh; then
    VERSION=$(gh release view -R "$REPO" --json tagName -q .tagName)
  else
    auth=""; [ -n "${GITHUB_TOKEN:-}" ] && auth="Authorization: Bearer $GITHUB_TOKEN"
    http_get "https://api.github.com/repos/$REPO/releases/latest" "$TMP/latest.json" ${auth:+-H "$auth"} \
      || die "cannot query releases (private repository: run 'gh auth login' or set GITHUB_TOKEN)"
    VERSION=$(sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' "$TMP/latest.json" | head -n1)
  fi
  [ -n "$VERSION" ] || die "could not determine the latest release; pass --version"
}

# fetch_release NAME...: downloads release assets of $VERSION into $TMP.
fetch_release() {
  if [ -n "$BASE_URL" ]; then
    for f in "$@"; do http_get "$BASE_URL/$VERSION/$f" "$TMP/$f"; done
  elif have_gh; then
    for f in "$@"; do gh release download "$VERSION" -R "$REPO" -D "$TMP" -p "$f" --clobber; done
  elif [ -n "${GITHUB_TOKEN:-}" ]; then
    [ -f "$TMP/release.json" ] || http_get "https://api.github.com/repos/$REPO/releases/tags/$VERSION" "$TMP/release.json" -H "Authorization: Bearer $GITHUB_TOKEN"
    for f in "$@"; do
      # Asset API URLs in document order, paired with the following "name".
      u=$(grep -oE '"(url|name)": *"[^"]*"' "$TMP/release.json" | sed 's/^"\([a-z]*\)": *"\(.*\)"$/\1 \2/' |
          awk -v n="$f" '$1=="url" && $2 ~ /\/releases\/assets\// {u=$2} $1=="name" && $2==n {print u; exit}')
      [ -n "$u" ] || die "release $VERSION has no asset $f"
      http_get "$u" "$TMP/$f" -H "Authorization: Bearer $GITHUB_TOKEN" -H "Accept: application/octet-stream"
    done
  else
    for f in "$@"; do
      http_get "https://github.com/$REPO/releases/download/$VERSION/$f" "$TMP/$f" \
        || die "download failed (private repository: run 'gh auth login' or set GITHUB_TOKEN)"
    done
  fi
}

# ---------- obtain what to install ----------

BIN_SRC=""
REPO_ROOT=$(cd "$(dirname "$0")/.." 2>/dev/null && pwd)
if [ "$DOCKER" = 1 ]; then
  if [ "$os" = darwin ]; then
    warn "on macOS the native binary uses the Keychain for both TLS trust (company CAs installed by IT) and tokens; the Docker image cannot see the Keychain, so you will need tls.ca_bundle and a 0600 token file. Prefer installing without --docker."
  fi
  command -v docker >/dev/null 2>&1 || die "--docker needs Docker (https://docs.docker.com/get-docker/)"
  docker info >/dev/null 2>&1 || die "cannot reach the Docker daemon (is it running? is your user in the 'docker' group?)"
  case "$IMAGE" in *[!A-Za-z0-9._/:@-]*) die "invalid --image '$IMAGE'" ;; esac
  if [ -f "$REPO_ROOT/Dockerfile" ] && [ -f "$REPO_ROOT/scripts/jira-docker.sh" ] && [ -z "$VERSION" ]; then
    # Running from a clone: use its wrapper; build the image unless one was given.
    cp "$REPO_ROOT/scripts/jira-docker.sh" "$TMP/jira-docker.sh"
    if [ -z "$IMAGE" ]; then
      IMAGE="jira-cli:local"
      ver=$(cd "$REPO_ROOT" && git describe --tags --always --dirty 2>/dev/null || echo dev)
      say "Building image $IMAGE ($ver) from $REPO_ROOT (Go runs inside the build; not needed on this machine)"
      docker build --build-arg VERSION="$ver" -t "$IMAGE" "$REPO_ROOT" || die "docker build failed"
    fi
  else
    resolve_version
    [ -n "$IMAGE" ] || IMAGE="ghcr.io/personal-stack/jira-cli:$VERSION"
    say "Downloading the Docker wrapper for $VERSION"
    fetch_release jira-docker.sh SHA256SUMS
    verify "$TMP/jira-docker.sh" "$TMP/SHA256SUMS"
  fi
  if ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
    say "Pulling $IMAGE"
    docker pull "$IMAGE" || die "cannot pull $IMAGE (private registry: gh auth refresh -h github.com -s read:packages && gh auth token | docker login ghcr.io -u <github-user> --password-stdin)"
  fi
  sed "s|__JIRA_CLI_IMAGE__|$IMAGE|" "$TMP/jira-docker.sh" > "$TMP/jira-wrapper"
  BIN_SRC="$TMP/jira-wrapper"
elif [ "$FROM_SOURCE" = 1 ]; then
  [ -n "$SRC_DIR" ] || SRC_DIR="$REPO_ROOT"
  [ -f "$SRC_DIR/go.mod" ] || die "$SRC_DIR is not a jira-cli checkout (no go.mod)"
  command -v go >/dev/null 2>&1 || die "Go is required for --from-source (https://go.dev/dl/); or use --docker"
  ver=$(cd "$SRC_DIR" && git describe --tags --always --dirty 2>/dev/null || echo dev)
  say "Building jira-cli $ver from $SRC_DIR"
  (cd "$SRC_DIR" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$ver" -o "$TMP/jira" ./cmd/jira)
  BIN_SRC="$TMP/jira"
else
  if [ -n "$ARCHIVE" ]; then
    [ -f "$ARCHIVE" ] || die "archive not found: $ARCHIVE"
    asset=$(basename "$ARCHIVE")
    cp "$ARCHIVE" "$TMP/$asset"
    sums="$(dirname "$ARCHIVE")/SHA256SUMS"
    if [ -f "$sums" ]; then verify "$TMP/$asset" "$sums"; else warn "no SHA256SUMS next to the archive; integrity not verified"; fi
  else
    resolve_version
    asset="jira-cli_${VERSION#v}_${os}_${arch}.tar.gz"
    say "Downloading jira-cli $VERSION ($os/$arch)"
    fetch_release "$asset" SHA256SUMS
    verify "$TMP/$asset" "$TMP/SHA256SUMS"
  fi
  mkdir -p "$TMP/x"
  case "$asset" in
    *.tar.gz) tar -xzf "$TMP/$asset" -C "$TMP/x" ;;
    *.zip) die "zip archives are for Windows; use install.ps1" ;;
    *) die "unknown archive type: $asset" ;;
  esac
  BIN_SRC=$(find "$TMP/x" -type f -name jira | head -n1)
  [ -n "$BIN_SRC" ] || die "archive does not contain a 'jira' binary"
fi

# ---------- install ----------

mkdir -p "$INSTALL_DIR" 2>/dev/null || die "cannot create $INSTALL_DIR (use --dir or sudo)"
[ -w "$INSTALL_DIR" ] || die "$INSTALL_DIR is not writable (use --dir or run with sudo)"
cp "$BIN_SRC" "$INSTALL_DIR/.$BIN_NAME.tmp.$$"
chmod 755 "$INSTALL_DIR/.$BIN_NAME.tmp.$$"
mv -f "$INSTALL_DIR/.$BIN_NAME.tmp.$$" "$TARGET"   # atomic replace
# macOS: browser downloads carry com.apple.quarantine, which tar copies to the
# extracted binary; Gatekeeper then refuses to run the unsigned binary. The
# archive's SHA-256 was verified above, so the flag is removed.
if [ "$os" = darwin ] && [ "$DOCKER" = 0 ] && command -v xattr >/dev/null 2>&1; then
  xattr -d com.apple.quarantine "$TARGET" 2>/dev/null || true
fi
installed=$("$TARGET" --version 2>/dev/null) || die "installed binary does not run: $TARGET"
if [ "$DOCKER" = 1 ]; then
  say "Installed $installed (Docker image $IMAGE) -> $TARGET"
  info "credentials: \${XDG_CONFIG_HOME:-~/.config}/jira-cli/credentials (0600 files; containers have no keyring)"
else
  say "Installed $installed -> $TARGET"
fi

if [ "$COMPLETIONS" = 1 ]; then install_completions; fi

case ":$PATH:" in
  *":$INSTALL_DIR:"*) on_path=1 ;;
  *) on_path=0 ;;
esac
if [ "$on_path" = 0 ]; then
  if [ "$MODIFY_PATH" = 1 ]; then
    for f in $(rc_files); do add_block "$f"; done
    say "Open a new terminal, or run:  export PATH=\"$INSTALL_DIR:\$PATH\""
  else
    say "$INSTALL_DIR is not on PATH; add it to use '$BIN_NAME' directly."
  fi
fi

# Another 'jira' earlier on PATH (e.g. go-jira, ankitpokhrel/jira-cli) would
# shadow this one.
first=$(PATH="$INSTALL_DIR:$PATH" command -v "$BIN_NAME" 2>/dev/null || true)
other=$(command -v "$BIN_NAME" 2>/dev/null || true)
if [ "$on_path" = 1 ] && [ -n "$other" ] && [ "$other" != "$TARGET" ]; then
  warn "'$BIN_NAME' resolves to $other, which shadows $TARGET; reinstall with --name jira-dc or reorder PATH"
elif [ -n "$first" ] && [ "$first" != "$TARGET" ]; then
  warn "'$BIN_NAME' resolves to $first; reinstall with --name jira-dc to avoid the clash"
fi

say ""
say "Next steps:"
say "  $BIN_NAME config validate      # create ~/.config/jira-cli/config.yaml first (see examples/config.yaml)"
say "  $BIN_NAME auth login           # store your personal access token in the OS keyring"
say "  $BIN_NAME doctor               # check connectivity, TLS and authentication"

#!/bin/sh
# jira: runs jira-cli from its Docker image as if it were installed natively.
#
# Installed as `jira` by `install.sh --docker`. Every invocation is a
# `docker run --rm` of the CLI image with:
#   - your config dir (~/.config/jira-cli) mounted read-write at the SAME path,
#     so paths in config.yaml (ca_bundle, client_cert) work unchanged;
#   - the current directory mounted read-only at the same path, so
#     `--file issue.json` works;
#   - for `jira setup-sbx`: the project (current directory or --project) and
#     the settings folder read-write, --ca-bundle / --binary-file read-only;
#   - credentials stored as 0600 files in ~/.config/jira-cli/credentials
#     (JIRA_CREDENTIAL_STORE=file), because containers have no OS keyring;
#   - your uid/gid, so files it writes are yours;
#   - JIRA_* and proxy variables forwarded by NAME only (values never appear
#     on the docker command line / in the process list);
#   - a TTY only when interactive, so scripts keep stdout/stderr separate;
#   - the CLI's exit code.
#
# Environment:
#   JIRA_CLI_IMAGE        image to run (default set at install time)
#   JIRA_DOCKER_NETWORK   docker network, e.g. "host" on Linux (VPN DNS, proxy on 127.0.0.1)
#   JIRA_DOCKER_MOUNTS    extra paths mounted read-only at the same path,
#                         space-separated (e.g. /etc/ssl/company-ca.pem)
#   JIRA_DOCKER_MOUNT_CWD 0 = do not mount the current directory
set -eu

IMAGE="${JIRA_CLI_IMAGE:-__JIRA_CLI_IMAGE__}"
case "$IMAGE" in __*) IMAGE="jira-cli:local" ;; esac

command -v docker >/dev/null 2>&1 || { echo "jira: docker is not installed or not on PATH" >&2; exit 127; }

CFG_HOME="${XDG_CONFIG_HOME:-$HOME/.config}"
CFG_DIR="$CFG_HOME/jira-cli"
CRED_DIR="${JIRA_CREDENTIALS_DIR:-$CFG_DIR/credentials}"
mkdir -p "$CFG_DIR"
[ -d "$CRED_DIR" ] || (umask 077 && mkdir -p "$CRED_DIR")

# 1. Inspect the CLI arguments (they stay untouched at the end of "$@").
LOG_DIRS=""
prev=""
for a in "$@"; do
  [ "$prev" = --log-file ] && LOG_DIRS="$LOG_DIRS
$(dirname "$a")"
  case "$a" in --log-file=*) LOG_DIRS="$LOG_DIRS
$(dirname "${a#--log-file=}")" ;; esac
  prev="$a"
done

# 2. Prepend docker options. Order does not matter to docker; prepending keeps
#    the CLI arguments intact (no word splitting, no arrays needed).
set -- "$IMAGE" "$@"

# Mounts are tracked as "path:mode" lines. Paths are made absolute and
# deduplicated; read-write mounts are added first and win over read-only.
MOUNTS=""
# abs: absolute, normalised path (resolves ".", "..", trailing "/").
abs() {
  if [ -d "$1" ]; then (cd "$1" && pwd)
  elif [ -d "$(dirname "$1")" ]; then printf '%s/%s' "$(cd "$(dirname "$1")" && pwd)" "$(basename "$1")"
  else case "$1" in /*) printf '%s' "$1" ;; *) printf '%s/%s' "$PWD" "$1" ;; esac
  fi
}
has() { case "
$MOUNTS
" in *"
$1:"*) return 0 ;; esac; return 1; }
rw() { mkdir -p "$1" 2>/dev/null || true; p=$(abs "$1"); has "$p" || MOUNTS="$MOUNTS
$p:rw"; }
ro() { p=$(abs "$1"); [ -e "$p" ] || return 0; has "$p" || MOUNTS="$MOUNTS
$p:ro"; }

rw "$CFG_DIR"
case "$CRED_DIR" in "$CFG_DIR"/*) ;; *) rw "$CRED_DIR" ;; esac
if [ -n "${JIRA_LOG_FILE:-}" ]; then rw "$(dirname "$JIRA_LOG_FILE")"; fi
OLDIFS=$IFS; IFS='
'
for d in $LOG_DIRS; do [ -n "$d" ] && rw "$d"; done
IFS=$OLDIFS
if [ -n "${JIRA_CONFIG:-}" ]; then ro "$(dirname "$JIRA_CONFIG")"; fi
if [ -n "${JIRA_CREDENTIAL_FILE:-}" ]; then ro "$JIRA_CREDENTIAL_FILE"; fi
# `jira setup-sbx` writes into the project and the settings folder; sbx itself
# runs on the host afterwards (the command prints those steps).
if [ "${2:-}" = setup-sbx ]; then   # $1 is the image (set above)
  # Guided run (`jira setup-sbx` alone, in a terminal): ask for the project
  # here, on the host, because the container only sees mounted folders.
  if [ $# -eq 2 ] && [ -t 0 ] && [ -t 2 ]; then
    while :; do
      printf 'Project the agent works in (mounted read/write) [%s]: ' "$PWD" >&2
      IFS= read -r ans || exit 1
      [ -n "$ans" ] || ans=$PWD
      # shellcheck disable=SC2088 # a literal ~ typed by the user
      case "$ans" in "~") ans=$HOME ;; "~/"*) ans="$HOME/${ans#"~/"}" ;; esac
      [ -d "$ans" ] && break
      echo "  not a directory: $ans" >&2
    done
    set -- "$@" -i --project "$(cd "$ans" && pwd)"
  fi
  sbx_project="$PWD" sbx_settings="$CFG_HOME/jira-cli-sbx" prev=""
  for a in "$@"; do
    case "$prev" in
      --project) sbx_project="$a" ;;
      --settings) sbx_settings="$a" ;;
      --ca-bundle|--binary-file) ro "$a" ;;
    esac
    case "$a" in
      --project=*) sbx_project="${a#--project=}" ;;
      --settings=*) sbx_settings="${a#--settings=}" ;;
      --ca-bundle=*|--binary-file=*) ro "${a#*=}" ;;
    esac
    prev="$a"
  done
  [ -d "$sbx_project" ] && rw "$sbx_project"
  rw "$sbx_settings"
  set -- -e JIRA_CLI_CONTAINER=1 "$@"
fi
WORKDIR=""
if [ "${JIRA_DOCKER_MOUNT_CWD:-1}" != 0 ] && [ "$PWD" != / ]; then ro "$PWD"; WORKDIR="$PWD"; fi
for m in ${JIRA_DOCKER_MOUNTS:-}; do ro "$m"; done

IFS='
'
for m in $MOUNTS; do
  [ -n "$m" ] || continue
  path=${m%:*} mode=${m##*:}
  if [ "$mode" = ro ]; then set -- -v "$path:$path:ro" "$@"; else set -- -v "$path:$path" "$@"; fi
done
IFS=$OLDIFS
[ -n "$WORKDIR" ] && set -- -w "$WORKDIR" "$@"

# Forward variables by name: `-e NAME` takes the value from this process.
for name in $(env | sed -n -e 's/^\(JIRA_[A-Za-z0-9_]*\)=.*/\1/p' \
                           -e 's/^\([Hh][Tt][Tt][Pp][Ss]\{0,1\}_[Pp][Rr][Oo][Xx][Yy]\)=.*/\1/p' \
                           -e 's/^\([Nn][Oo]_[Pp][Rr][Oo][Xx][Yy]\)=.*/\1/p'); do
  case "$name" in
    JIRA_CLI_IMAGE|JIRA_DOCKER_*|JIRA_CONFIG|JIRA_CREDENTIALS_DIR|JIRA_CREDENTIAL_STORE|JIRA_NO_KEYRING) ;;
    *) set -- -e "$name" "$@" ;;
  esac
done

# The image defaults JIRA_CONFIG to /config/config.yaml; use the host path.
set -- -e "JIRA_CONFIG=${JIRA_CONFIG:-$CFG_DIR/config.yaml}" "$@"
set -- -e HOME="$HOME" -e XDG_CONFIG_HOME="$CFG_HOME" \
       -e JIRA_NO_KEYRING=1 -e JIRA_CREDENTIAL_STORE=file -e JIRA_CREDENTIALS_DIR="$CRED_DIR" "$@"
[ -n "${JIRA_DOCKER_NETWORK:-}" ] && set -- --network "$JIRA_DOCKER_NETWORK" "$@"

# A TTY merges stderr into stdout; only use one for interactive sessions.
if [ -t 0 ] && [ -t 1 ]; then set -- -t "$@"; fi

exec docker run --rm -i --user "$(id -u):$(id -g)" "$@"

#!/usr/bin/env bash
# Writes the GitHub release notes for a version to stdout.
#
# Usage: scripts/release-notes.sh VERSION [DIST_DIR] [IMAGE] [DIGEST]
#   VERSION   tag, e.g. v0.4.0; its "## VERSION" section of CHANGELOG.md is
#             the "What's new" part (required for a release, see --check)
#   DIST_DIR  folder with the archives and SHA256SUMS (download table)
#   IMAGE     image without tag (default ghcr.io/personal-stack/jira-cli)
#   DIGEST    pushed image index digest (sha256:...)
#
# scripts/release-notes.sh --check VERSION only checks that CHANGELOG.md has
# a section for VERSION (the release workflow runs it before building).
set -euo pipefail
cd "$(dirname "$0")/.."

REPO_URL="https://github.com/Personal-Stack/jira-cli"
INSTALL_URL="$REPO_URL/releases/latest/download/install.sh"

# changelog_section VERSION: the body of "## VERSION ..." up to the next "## ".
changelog_section() {
  awk -v v="$1" '
    /^## / { if (found) exit; if ($2 == v) { found = 1; next } }
    found { print }
  ' CHANGELOG.md | sed -e '/./,$!d' | sed -e ':a' -e '/^\n*$/{$d;N;ba' -e '}'
}

if [ "${1:-}" = --check ]; then
  v=${2:?usage: release-notes.sh --check VERSION}
  if [ -z "$(changelog_section "$v")" ]; then
    echo "::error::CHANGELOG.md has no '## $v' section: add the release notes before tagging" >&2
    exit 1
  fi
  echo "CHANGELOG.md has a section for $v"
  exit 0
fi

VERSION=${1:?usage: release-notes.sh VERSION [DIST_DIR] [IMAGE] [DIGEST]}
DIST=${2:-dist}
IMAGE=${3:-ghcr.io/personal-stack/jira-cli}
DIGEST=${4:-}
TAG_URL="$REPO_URL/blob/$VERSION"

new=$(changelog_section "$VERSION")
[ -n "$new" ] || new="_No CHANGELOG.md entry for $VERSION._"

human_size() { awk -v b="$1" 'BEGIN { if (b >= 1048576) printf "%.1f MB", b / 1048576; else printf "%d KB", b / 1024 }'; }
platform() {
  case "$1" in
    *_linux_amd64.*) echo "Linux x86-64" ;;
    *_linux_arm64.*) echo "Linux ARM64" ;;
    *_darwin_amd64.*) echo "macOS Intel" ;;
    *_darwin_arm64.*) echo "macOS Apple silicon" ;;
    *_windows_amd64.*) echo "Windows x86-64 (Git Bash)" ;;
    *_windows_arm64.*) echo "Windows ARM64 (Git Bash)" ;;
    *) echo "" ;;
  esac
}

cat <<EOF
## What's new

$new

## Install

Linux and macOS (installs \`jiracli\` to \`~/.local/bin\`, with Tab completion):

\`\`\`bash
curl -fsSL $INSTALL_URL | sh -s -- --version $VERSION
\`\`\`

Then set it up in one guided session (configuration, token, connection check):

\`\`\`bash
jiracli config setup
\`\`\`

**Upgrading:** \`jiracli upgrade\` (available from v0.4.0; it verifies the
download and replaces the binary in place). From an older version, run the
install command above. On Windows, run \`install.sh\` from Git Bash.

**Docker** (only Docker on the machine): the same command with \`--docker\`
installs a \`jiracli\` wrapper that runs the image:

\`\`\`bash
curl -fsSL $INSTALL_URL | sh -s -- --docker --version $VERSION
\`\`\`

\`\`\`bash
docker pull $IMAGE:$VERSION
\`\`\`
EOF
if [ -n "$DIGEST" ]; then
  cat <<EOF

Image digest, for pinning: \`$IMAGE@$DIGEST\`.
The image is public (no login needed) and covers linux/amd64 and linux/arm64.
EOF
fi

if compgen -G "$DIST/jira-cli_${VERSION#v}_*" > /dev/null; then
  printf '\n## Downloads\n\n| Platform | File | Size |\n|---|---|---|\n'
  for f in "$DIST"/jira-cli_"${VERSION#v}"_*; do
    n=$(basename "$f")
    printf '| %s | [%s](%s/releases/download/%s/%s) | %s |\n' "$(platform "$n")" "$n" "$REPO_URL" "$VERSION" "$n" "$(human_size "$(wc -c < "$f")")"
  done
  # shellcheck disable=SC2016 # Markdown backticks, not command substitution
  printf '\nAlso attached: `install.sh`, `jira-docker.sh` (the Docker wrapper) and `SHA256SUMS`.\n'
fi

cat <<EOF

## Verify

\`install.sh\` and \`jiracli upgrade\` check every archive against \`SHA256SUMS\`
before installing anything. To check a download by hand:

\`\`\`bash
grep jira-cli_${VERSION#v}_linux_amd64.tar.gz SHA256SUMS | sha256sum -c    # macOS: shasum -a 256 -c
\`\`\`
EOF
if [ -n "$DIGEST" ]; then
  cat <<EOF

The image is built by this release's workflow, with an SBOM and build
provenance attached, and smoke-tested by digest on both architectures before
this release was published:

\`\`\`bash
docker buildx imagetools inspect $IMAGE@$DIGEST --format '{{json .Provenance}}'
\`\`\`
EOF
fi

cat <<EOF

## Documentation

[Getting started]($TAG_URL/docs/getting-started.md) ·
[Installation and upgrades]($TAG_URL/docs/installation.md) ·
[Docker]($TAG_URL/docs/docker.md) ·
[Agents in Docker Sandboxes]($TAG_URL/docs/sandbox.md) ·
[Full changelog]($TAG_URL/CHANGELOG.md)

---

Unofficial: Personal Stack jira-cli is not affiliated with or endorsed by
Atlassian. "Jira" is a trademark of Atlassian. See [LEGAL.md]($TAG_URL/LEGAL.md).
EOF

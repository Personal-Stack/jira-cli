#!/bin/sh
# Verifies that the jira CLI can reach and authenticate to the selected
# instance. Prints a one-line verdict to stderr and the doctor JSON to stdout.
# Usage: check-connection.sh [--instance NAME]
# Exit status: 0 healthy, otherwise the doctor's exit code.
set -u

if ! command -v jira >/dev/null 2>&1; then
  echo "jira CLI not found on PATH" >&2
  exit 127
fi

out=$(jira doctor --output json "$@" 2>/dev/null)
code=$?
printf '%s\n' "$out"
if [ "$code" -eq 0 ]; then
  echo "OK: jira $(jira --version 2>/dev/null | awk '{print $NF}') is connected and authenticated" >&2
else
  echo "FAILED (exit $code): see the first check with \"status\": \"fail\"" >&2
fi
exit "$code"

# Credentials

Credentials are referenced by `credential_ref` (`[A-Za-z0-9][A-Za-z0-9._-]{0,127}`,
no `..`). The config file never contains secrets: unknown keys such as
`token:` are rejected by strict YAML decoding.

## Resolution order (first match wins)

| # | Source | Intended for | Notes |
|---|---|---|---|
| 1 | `JIRA_CREDENTIAL_FILE=/path` | containers, CI | applies only to the selected instance's `credential_ref` |
| 2 | `$JIRA_CREDENTIALS_DIR/<ref>` (default `/run/secrets/<ref>`) | Docker/Swarm/Kubernetes secrets | |
| 3 | `JIRA_CREDENTIAL_<REF>` | CI | `jira-prod` becomes `JIRA_CREDENTIAL_JIRA_PROD`; visible to same-user processes |
| 4 | OS keyring (service `jira-cli`) | workstations | macOS Keychain, Windows Credential Manager, Linux Secret Service; disable with `JIRA_NO_KEYRING=1` |

## File checks

Mounted secret files are re-read every invocation (rotation needs no restart) and:

- must be regular files (symlinks are followed, so Kubernetes projected secrets work);
- are refused if world-writable;
- produce a warning if group/world-readable (Docker secrets default to `0444`, hence a warning, not an error);
- are limited to 64 KiB; trailing whitespace/newlines are trimmed; empty files are refused.

## Guarantees

- Secrets never come from command-line arguments (visible in `ps`, shell history).
- Secrets are never written to temporary files. The only write path is the OS keyring (`auth login`, OAuth refresh).
- The `Secret` type renders as `[REDACTED]` in every `fmt` verb, JSON and text marshalling (tested).
- Keyring calls time out after 5 s (no session bus in containers).

## Docker

```bash
docker run --rm \
  -v ./config:/config:ro \
  -v ./secrets/jira_pat:/run/secrets/jira-production:ro \
  jira-cli issue get ABC-123
```

or with an explicit path:

```bash
docker run --rm -v ./config:/config:ro -v /run/secrets:/run/secrets:ro \
  -e JIRA_CREDENTIAL_FILE=/run/secrets/jira_pat jira-cli issue get ABC-123
```

## External secret managers

Implement `credentials.Provider` (`Name`, `Get(ref)`) and add it to the
chain in `credentials.Default`. Auth providers depend only on the chain, so
they need no change.

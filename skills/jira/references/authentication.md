# Authentication

The CLI supports two methods per instance (`auth.type` in the config):

| Method | Header | Requirements |
|---|---|---|
| `pat` | `Authorization: Bearer <token>` | Jira Data Center 8.14+, PAT created by the user (Profile > Personal Access Tokens) |
| `oauth` | `Authorization: Bearer <access token>` | Jira DC OAuth 2.0 provider (incoming application link); token refreshed automatically |

Basic authentication is intentionally not supported.

## Where credentials come from (first match wins)

1. `JIRA_CREDENTIAL_FILE`: a file holding the selected instance's credential (containers).
2. `$JIRA_CREDENTIALS_DIR/<credential_ref>` (default `/run/secrets/<credential_ref>`, i.e. Docker secrets).
3. `JIRA_CREDENTIAL_<REF>` environment variable (`jira-prod` becomes `JIRA_CREDENTIAL_JIRA_PROD`).
4. The OS keyring (`jira auth login`).

`jira auth status` shows which source was used (never the value).

## Agent rules

- Do not run `jira auth login` unless the user asks you to; it needs the user to type a token or approve in a browser.
- Never ask the user to paste a token into the chat. Ask them to run `jira auth login` themselves or configure a secret file.
- A 401 (exit 4) usually means an expired/revoked PAT, or a revoked OAuth refresh token. Report it; do not retry.
- `details.login_reason: AUTHENTICATION_DENIED` means Jira requires a CAPTCHA after failed logins: the user must log in via the browser once.
- An `unexpected_html` error means a reverse proxy/SSO gateway returned a login page: the base URL or network path is wrong for API access.

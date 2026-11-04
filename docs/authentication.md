# Authentication

Authentication is configured per instance (`auth.type`) and implemented as a
provider (`internal/auth`) that only decorates requests. Generated commands
never handle credentials.

```go
type Provider interface {
    Method() string
    Apply(ctx context.Context, req *http.Request) error
    Status(ctx context.Context) Status // no network, no secret values
}
type Refresher interface { Refresh(ctx context.Context) error } // optional
```

Basic authentication is not implemented, by design.

## Personal Access Tokens (`type: pat`)

- Jira Data Center 8.14+ (PATs are created under *Profile > Personal Access Tokens*; admins can restrict or expire them).
- Sent as `Authorization: Bearer <token>`. `username` is optional and informational; Jira resolves the user from the token.
- Loaded lazily on the first real request: `--dry-run`, `--help` and discovery never read the secret.
- Rejected locally if it contains whitespace or control characters (prevents header injection).
- Rotation: credentials are re-read on every invocation; replace the file/env/keyring entry.

```bash
jiracli auth login --method pat              # hidden prompt; verifies via /rest/api/2/myself, stores in keyring
printf %s "$TOKEN" | jiracli auth login      # non-interactive (still never an argument)
jiracli auth status                          # source + presence, no network
jiracli auth test                            # GET /rest/api/2/myself
jiracli auth logout                          # remove from keyring
```

## OAuth 2.0 (`type: oauth`)

Targets Jira Data Center's OAuth 2.0 provider (*Administration >
Applications > Application links > Create link > External application >
Incoming*). Everything is configurable because installations differ:

```yaml
auth:
  type: oauth
  credential_ref: jira-staging              # token set stored here
  oauth:
    authorization_url: https://jira.example/rest/oauth2/latest/authorize
    token_url: https://jira.example/rest/oauth2/latest/token
    client_id: ...
    client_secret_ref: jira-staging-client  # optional; resolved like any credential
    redirect_uri: http://127.0.0.1:8765/callback   # loopback with an explicit port; must match the link
    scopes: [READ]
    pkce: true                              # default
```

- Authorization-code flow with PKCE (S256) and a random `state` checked in constant time.
- Loopback listener on `redirect_uri`, or `jiracli auth login --manual` to paste the redirect URL (remote shells).
- Tokens are stored in the keyring as JSON (`access_token`, `refresh_token`, `expiry`) and refreshed automatically when expired, and once after a 401. Refreshed tokens are written back only to the keyring.
- Headless: mount either the token JSON or a bare refresh token as the credential (`JIRA_CREDENTIAL_FILE`). Refreshed tokens then live only in memory for that process; if the server rotates refresh tokens, the mounted one eventually becomes invalid and must be re-issued.
- SSO is not a separate protocol: the authorization page redirects to the configured identity provider as usual.
- If the server does not support the configured flow (e.g. rejects PKCE, or OAuth 2.0 provider is unavailable on older versions), login fails with the server's error and a hint; nothing is bypassed.

### Unverified against a real instance (Phase 4)

- PKCE acceptance by the Jira DC OAuth 2.0 provider of your version (set `pkce: false` if rejected).
- Whether a client secret is required for the incoming link (set `client_secret_ref`).
- Refresh-token rotation behaviour and token lifetimes.

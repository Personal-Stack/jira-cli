# Jira Data Center notes

## Compatibility

| Item | Status |
|---|---|
| Specification | Jira Software Data Center **10.3.26** (official, `specs/official/`) |
| Tested against a real instance | **Read-only: yes**, a production Data Center (`scripts/phase4-readonly.sh`). Writes: no, `httptest` fakes only |
| PAT | Jira DC 8.14+ |
| OAuth 2.0 provider | Jira DC with OAuth 2.0 incoming application links (not verified against a real instance) |
| Version check | `jira doctor` compares `serverInfo.version` (major.minor) with the profile spec version and warns on mismatch |

Other Jira versions: most `api/2` endpoints are stable across 8.x-10.x, but
commands may be missing or behave differently. For a materially different
version, add a profile built from that version's specification.

## Base URL and context path

`base_url` may include a context path (`https://host/jira`). Requests go to
`<base_url>/rest/<path>`. `doctor` warns if Jira's own configured base URL
(`serverInfo.baseUrl`) differs, which commonly indicates a reverse-proxy
mismatch.

## Behaviour specific to Data Center

- **Seraph login reasons**: `X-Seraph-LoginReason` is reported in error `details.login_reason`; `AUTHENTICATION_DENIED` means CAPTCHA is required after failed logins.
- **SSO / reverse proxies**: an HTML response to an API call is reported as `unexpected_html` (authentication layer) instead of being printed as data.
- **XSRF**: multipart uploads (attachments, avatars) send `X-Atlassian-Token: no-check`, which Jira requires.
- **Rate limiting** (Jira 8.6+ admin feature): 429 with `Retry-After` is honoured (see secure-transport.md).
- **Pagination**: REST v2 uses `total`; the Agile API uses `isLast`; some user endpoints return bare arrays. All three are handled by `--all`.
- **Hidden 404s**: Jira returns 404 instead of 403 for issues the user cannot browse.

## Specification caveats (Jira Software DC 10.3.26)

The official spec declares `basic` security and `http://localhost:8090/rest`
as server; both are ignored. It also contains defects fixed by overlay; see
docs/openapi-generation.md for the list.

# Troubleshooting

Run `jira doctor` first. It reports checks in order and stops at the first
failing layer:

| Check | Fails when | Typical fix |
|---|---|---|
| `config` | file missing/invalid, CA bundle/cert unreadable | `jira config validate`; fix paths |
| `dns` | host cannot be resolved | VPN/internal DNS; check `base_url` |
| `tcp` | port unreachable | firewall, wrong port, proxy needed |
| `proxy_tcp` | proxy unreachable | `proxy.url` / `HTTPS_PROXY` |
| `tls` | certificate unknown/expired/mismatched | set `tls.ca_bundle` to the corporate CA |
| `http` | `/rest/api/2/serverInfo` (sent without credentials) fails or returns HTML | wrong base URL/context path, SSO gateway in front of the API. A 401/403 **from Jira** (has `X-AREQUESTID`) is `ok`: anonymous access is disabled, which is normal in production; the version is then checked in the `version` step with credentials |
| `version` | authenticated `serverInfo` fails | only runs when anonymous access is disabled |
| `credentials` | no credential found | `jira auth login` or `JIRA_CREDENTIAL_FILE` |
| `authentication` | `/myself` returns 401 | token expired/revoked; CAPTCHA (`login_reason`) |

Warnings worth reporting: the Jira version differs from the profile's spec
version (commands may be missing), Jira's own base URL differs from
`base_url` (links in responses point elsewhere), the server certificate
expires within 14 days, or a secret file is group/world-readable.

## Error layers

`configuration`, `credentials`, `dns`, `tcp`, `proxy`, `tls`,
`tls_certificate`, `timeout`, `http_transport`, `authentication`,
`authorization`, `jira_api`, `response_parsing`, `safety`, `usage`.

## Common Jira API errors (exit 7)

- `400` with `details.errors` keyed by field: a field is not on the screen, has an invalid value, or is required. Check `issue get-edit-meta` / create meta.
- `400 "Field 'x' cannot be set"`: field not on the create/edit screen for that project/issue type.
- `409`: concurrent modification or conflicting state; re-read and retry once if appropriate.
- `404` (exit 6) also occurs when the user lacks *browse* permission: Jira hides existence.

Use `-v` for debug logs (never headers, bodies or secrets). For a
machine-readable trace: `jira --log-level debug --log-format json <command> 2> trace.jsonl`.
Every line carries `run_id`; HTTP lines carry `request_id`, timings
(`dns_ms`, `connect_ms`, `tls_ms`, `ttfb_ms`) and `jira_request_id`, which a
Jira administrator can look up in Jira's access log. See docs/logging.md.

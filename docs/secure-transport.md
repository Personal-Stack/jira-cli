# Secure transport

All Jira traffic goes through one `*http.Client` built by
`internal/transport.New` and used only by `internal/api.Client`.

## TLS

| Setting | Default | Notes |
|---|---|---|
| verification | **on** | `tls.verify: false` must be explicit; every invocation logs a warning and `doctor` reports `warn` |
| minimum version | TLS 1.2 | `tls.min_version: "1.3"` |
| roots | system pool + `tls.ca_bundle` | bundle must contain at least one PEM certificate |
| mTLS | off | `tls.client_cert` + `tls.client_key` (key must be `0600`); reloaded when either file's mtime changes |
| SNI override | host of `base_url` | `tls.server_name` |

## URL scheme

`https` only. `http` is rejected unless `allow_insecure_http: true` (local
development; warned on every invocation). URLs with credentials, queries or
fragments are rejected. Other schemes are rejected.

## Proxy

`proxy.url` (http/https/socks5), `proxy.disabled`, or the standard
`HTTPS_PROXY`/`NO_PROXY`. Proxy URLs with passwords are rejected in the config
(put them in the environment from a secret instead).

## Redirects

- At most 5.
- `https` to `http` is refused.
- `Authorization`, `Cookie` and `Proxy-Authorization` are forwarded only to the exact same origin (scheme, host, port). Go's default also forwards to subdomains; this is stricter. (Tested.)

## Timeouts and pooling

`timeouts.connect` (default 10 s; dial + TLS handshake), `timeouts.request`
(default 60 s; `--timeout` overrides). Keep-alive pool of 8 idle connections
per host; HTTP/2 when offered. Context cancellation (Ctrl-C/SIGTERM) aborts
in-flight requests.

## Retries

| Condition | GET/HEAD (and operations marked `idempotent` in risk metadata) | Other methods |
|---|---|---|
| 429 | retried, honouring `Retry-After` up to 30 s | retried (request was rejected before processing) |
| 502/503/504 | retried | **not** retried |
| network error | retried | **not** retried (it may have been applied) |
| TLS/certificate error, cancellation | not retried | not retried |

Backoff is exponential with full jitter (base 500 ms), 3 attempts by default
(`retry.max_attempts`, 0-10). A `Retry-After` above 30 s is not waited out;
the 429 is returned (exit 10).

## Diagnostics

`jira doctor` checks config, DNS, TCP, TLS (version, cipher, certificate
subject/issuer/expiry), HTTP (`/rest/api/2/serverInfo`, unauthenticated),
credentials, and authentication, and attributes failures to a layer: `dns`,
`tcp`, `proxy`, `tls`, `tls_certificate`, `timeout`, `http_transport`,
`authentication`, `authorization`, `jira_api`.

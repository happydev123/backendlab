# Security model

BackendLab sends real HTTP requests, reads upstream responses that may
contain personal or confidential data, and (in server mode) stores results
in PostgreSQL. This page states what the tool protects against, what it
does not, and which settings matter.

## Trust boundaries

| Mode | Who chooses destinations | Who supplies suites | Credentials |
|---|---|---|---|
| CLI | The local user (config file or `--base-url`) | The local user | Environment variables of the local shell |
| Server | The operator, in server configuration | Approved files in `suites_dir`, selected by the operator | Environment variables listed in `credentials.allowed_env` |

The CLI acts with the authority of the person running it, so it does not
restrict destinations. The server executes work on behalf of a logged-in
operator but treats suites as data: a suite cannot name a host, a scheme, an
environment variable or a credential, and it cannot grant itself write
access.

## Write safety

- Only `GET`, `HEAD` and `OPTIONS` run by default.
- Other methods need `--allow-writes` on the CLI. On the server they need
  `allow_writes: true` in configuration **and** an explicit write request
  on the submitted run.
- Requests are never retried, mutating or not.
- Cancelling a run or hitting a timeout stops waiting; it does not undo a
  request the upstream already received.
- Background runs containing mutating cases are never replayed after an
  uncertain interruption. They are marked `interrupted` for operator review.
  A stale worker may still have a request in flight when its lease expires;
  fencing tokens stop it from overwriting newer results, not from finishing
  that request.

## Outbound requests in server mode

`internal/egress` checks every destination:

- Host names must match `egress.allow_hosts` (exact or `*.suffix`, optional
  port) when that list is set.
- Every resolved address is checked. Loopback, link-local (including cloud
  metadata addresses), private, CGNAT, unique-local IPv6, multicast,
  unspecified and translated forms that embed such addresses are refused
  unless an operator allows a range in `egress.allow_cidrs` (loopback only via
  `allow_loopback`). Metadata addresses cannot be allowed.
- The connection is made to the checked IP literal, so a DNS answer cannot
  change between the check and the dial.
- Redirects are not followed unless the target enables it, and each hop is
  checked again. Credentials are dropped when a redirect changes origin.
- Proxy environment variables are ignored by server transports.

Application checks do not replace network controls. Run the worker where
outbound traffic is limited by firewall or egress gateway rules as well;
see [egress.md](egress.md).

## Credentials and secrets

- Target credential headers come from environment variables at run time.
  Literal credential headers in configuration are rejected, and suites
  cannot set `Authorization`, `Proxy-Authorization` or `Cookie`.
- Resolved values are registered with the redactor for that run, so they are
  scrubbed from error text, previews and reports even if an upstream echoes
  them back.
- Suite snapshots stored with runs contain no resolved secrets because
  suites cannot hold them.
- The database URL and the operator password hash are read from the
  environment and never logged.

## Redaction

Applied before anything is printed, logged, persisted or rendered:

- Headers: `Authorization`, `Proxy-Authorization`, `Cookie`, `Set-Cookie`,
  common API-key headers, plus configured names.
- JSON: values under keys such as `password`, `token`, `secret`, `api_key`
  (case-insensitive), plus configured JSON Pointer patterns
  (`redact.json_paths`, `*` matches one segment).
- URLs: user info and sensitive query parameters.
- Free text: any registered secret value of four bytes or more.

The marker is always `[REDACTED]`. Redaction depends on configuration and on
knowing which values are sensitive. It does not guarantee that arbitrary
upstream payloads contain no sensitive data; a secret under an unexpected key
or inside prose passes through.

## Stored data

| Data | Stored | Notes |
|---|---|---|
| Suite snapshot | yes | The approved YAML at submission time. |
| Case observations | yes | Status, sizes, timings, sanitized errors. |
| Findings | yes | Redacted previews, bounded in size. |
| Reports | yes | Bounded by `limits.max_report_bytes`. |
| Response bodies | no by default | `retention.retain_raw_bodies: true` stores redacted bodies up to `raw_body_max_bytes`. Enable only with database access restricted to operators. |
| Session tokens | hash only | SHA-256 of the cookie value. |
| Login throttle keys | hash only | SHA-256 of the client address. |

`backendlab retention cleanup` deletes finished runs older than
`retention.run_max_age`.

## Operator authentication

- One operator account. The password hash (bcrypt, cost 12) comes from
  `BACKENDLAB_ADMIN_PASSWORD_HASH`; `backendlab hash-password` reads the
  password from stdin.
- Sessions live in PostgreSQL with absolute expiry. Cookies are `HttpOnly`,
  `SameSite`, and `Secure` unless `auth.secure_cookies: false` is set for
  local plain-HTTP use.
- State-changing requests authenticated by cookie need the session's CSRF
  token.
- Login attempts are throttled per client address in PostgreSQL.

There is no multi-tenant isolation and no role model. Anyone with the
operator password can see all runs.

## Output escaping

- Text reports escape control characters, ANSI sequences, invalid UTF-8 and
  bidirectional overrides.
- Markdown reports escape punctuation that would create links, emphasis,
  tables or HTML.
- Dashboard templates use `html/template` contextual escaping and a
  Content-Security-Policy without inline scripts.

## Not provided

Multi-tenant isolation, SSO, encryption of stored results beyond what
PostgreSQL provides, automatic secret detection in arbitrary payloads, and
any guarantee of exactly-once execution.

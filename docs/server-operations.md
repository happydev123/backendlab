# Server operations

`backendlab serve` is an optional, single-operator server. It accepts runs of
approved suites against targets defined in its configuration, shows their
results in a dashboard and a JSON API, and compares uploaded query plans.
Runs are executed by separate `backendlab worker` processes that share the
same PostgreSQL database (see [jobdock.md](jobdock.md)). Every offline
command (`run`, `compare`, `stream`, `validate`, `plans compare`) works
without the server.

Code: `internal/server` (assembly, timeouts, shutdown), `internal/httpapi`
(middleware and JSON API), `internal/dashboard` with `web/` (HTML pages and
assets), `internal/catalog` (approved suites and submission policy).

## Processes

| Command | Purpose |
|---|---|
| `backendlab migrate [status] --config FILE` | Apply pending schema migrations, or list their state. Never run implicitly. |
| `backendlab serve --config FILE` | HTTP server: dashboard, JSON API, `/healthz`, `/readyz`. |
| `backendlab worker --config FILE` | Claims queued runs and executes them. Run one or more. |
| `backendlab hash-password` | Reads the operator password from stdin and prints a bcrypt hash. |
| `backendlab retention cleanup --config FILE [--dry-run]` | Deletes finished runs older than `retention.run_max_age`, expired sessions and old login attempts. |

`--config` may be replaced by the `BACKENDLAB_CONFIG` environment variable.

## Quick start (local)

```sh
# 1. PostgreSQL connection for every server command.
export BACKENDLAB_DATABASE_URL='postgres://backendlab:...@127.0.0.1:5432/backendlab?sslmode=disable'

# 2. Operator password hash. hash-password reads the password from stdin;
#    typing it at the prompt keeps it out of shell history. End with Ctrl-D.
backendlab hash-password
export BACKENDLAB_ADMIN_PASSWORD_HASH='$2a$12$...'   # single quotes: the hash contains $

# 3. Schema.
backendlab migrate --config examples/server/backendlab-server.yaml

# 4. Mock upstream, server and one worker (separate terminals).
go run ./cmd/mockservice
backendlab serve  --config examples/server/backendlab-server.yaml
backendlab worker --config examples/server/backendlab-server.yaml
```

Open `http://127.0.0.1:8080/` and sign in. The example configuration sets
`auth.secure_cookies: false` because it serves plain HTTP on loopback; keep
the default (`true`) everywhere else.

## Configuration

The server file is YAML, decoded strictly: unknown keys, duplicate keys and
multiple documents are errors. Durations use Go syntax (`30s`, `15m`, `12h`).
[examples/server/backendlab-server.yaml](../examples/server/backendlab-server.yaml)
is an annotated example that the test suite loads.

| Key | Default | Meaning |
|---|---|---|
| `version` | required | Must be `1`. |
| `listen` | `127.0.0.1:8080` | Address of the HTTP server. |
| `database.url_env` | `BACKENDLAB_DATABASE_URL` | Name of the variable holding the PostgreSQL URL. The URL itself never appears in the file or in logs. |
| `database.max_conns` | `10` | Pool size per process, 1 to 100. |
| `database.min_conns` | `0` | Connections kept open, at most `max_conns`. |
| `database.max_conn_lifetime` | `1h` | Connections are replaced after this age. |
| `database.max_conn_idle_time` | `10m` | Idle connections are closed after this time. |
| `suites_dir` | required | Directory of approved suites. Relative paths resolve against the working directory. |
| `targets.<name>.base_url` | required, at least one target | Absolute `http` or `https` URL without credentials, query or fragment. Names match `^[a-z][a-z0-9_-]{0,31}$`. |
| `targets.<name>.headers` | none | Literal values, or `{env: NAME, prefix: "Bearer "}`. Credential headers (`Authorization`, `Cookie`, `X-Api-Key`, ...) must use `env`. |
| `targets.<name>.follow_redirects` | `false` | Follow up to five redirects; egress checks apply to each hop. |
| `targets.<name>.ca_file` | none | Not supported in server mode; install CAs in the system trust store. |
| `credentials.allowed_env` | empty | Variables target headers may read. Must not contain the database or password-hash variables. |
| `egress.allow_hosts` | empty (no name restriction) | Host patterns runs may reach. See [egress.md](egress.md). |
| `egress.allow_cidrs` | empty | Private ranges explicitly allowed. |
| `egress.allow_loopback` | `false` | Allow 127.0.0.0/8 and ::1 (development only). |
| `allow_writes` | `false` | Permit suites with mutating cases. Each submission must also request writes. |
| `worker.concurrency` | `2` | Runs executed in parallel per worker, 1 to 32. |
| `worker.lease_duration` | `30s` | Lease length, at least 5s. |
| `worker.heartbeat_interval` | `10s` | At most half of the lease. |
| `worker.poll_interval` | `2s` | Delay between claims when the queue is empty. |
| `worker.max_read_only_attempts` | `2` | Automatic attempts for read-only runs after a lost lease, 1 to 5. Mutating runs always get 1. |
| `auth.password_hash_env` | `BACKENDLAB_ADMIN_PASSWORD_HASH` | Variable holding the operator's bcrypt hash (cost 12 or more). |
| `auth.session_ttl` | `12h` | Absolute session lifetime, 1m to 168h. Activity does not extend it. |
| `auth.secure_cookies` | `true` | Mark the session cookie `Secure`. |
| `auth.login_max_attempts` | `5` | Password attempts per client address per window. |
| `auth.login_window` | `15m` | Throttle window. |
| `retention.run_max_age` | `720h` | Finished runs older than this are deleted by `retention cleanup`. At least 1h. |
| `retention.retain_raw_bodies` | `false` | Store bounded, redacted response bodies with case results. |
| `retention.raw_body_max_bytes` | `65536` | Bound per retained body, 0 to 1048576. |
| `limits.*` | see below | Resource bounds for suites, requests, reports and plans. |
| `redact.headers`, `redact.keys`, `redact.json_paths` | empty | Extend the built-in redaction lists. |
| `log.level` | `info` | `debug`, `info`, `warn` or `error`. |
| `log.format` | `json` | `json` or `text`. Logs go to stderr. |

Limits that matter most for the server (defaults; each has a hard maximum,
see `internal/limits`): `max_suite_bytes` 1 MiB, `max_cases` 200,
`request_timeout` 10s, `run_timeout` 10m, `max_response_bytes` 2 MiB,
`max_findings` 2000, `max_report_bytes` 4 MiB, `max_plan_bytes` 16 MiB.

HTTP server bounds are fixed in code (`server.Limits`): read header timeout
10s, read timeout 60s, write timeout 60s, idle timeout 120s, header size
64 KiB, graceful shutdown 15s. JSON request bodies are limited to 64 KiB,
dashboard forms to 64 KiB, and plan uploads to two plans of
`max_plan_bytes` each plus 64 KiB.

## Environment variables

| Variable | Used by | Content |
|---|---|---|
| `BACKENDLAB_DATABASE_URL` (or the name in `database.url_env`) | all server commands | PostgreSQL URL. |
| `BACKENDLAB_ADMIN_PASSWORD_HASH` (or `auth.password_hash_env`) | `serve` | bcrypt hash from `hash-password`. |
| Names in `credentials.allowed_env` | `worker` | Target credentials referenced by `headers.<name>.env`. |
| `BACKENDLAB_CONFIG` | all server commands | Configuration path when `--config` is omitted. |

Credential values are resolved by the worker at execution time and are
registered with the redactor, so they are scrubbed from error text and
stored results. They are never stored in runs, suites or reports.

## Database, migrations and pools

Apply migrations explicitly before starting new binaries:

```sh
backendlab migrate --config server.yaml          # apply pending migrations
backendlab migrate status --config server.yaml   # list applied, pending, modified, unknown
```

`serve` and `worker` do not migrate on start. `/readyz` reports 503 while a
migration is pending, an applied file was modified, or the database records
a migration the binary does not know (a newer schema).

Each process has its own pool of `database.max_conns` connections. `serve`
needs few (one per concurrent request at most, plus readiness checks); a
worker needs about `worker.concurrency + 2` (heartbeats and recovery run
alongside executions). Keep the sum over all processes below PostgreSQL's
`max_connections`.

## Approved suites

The catalog is read once when `serve` starts; restart it after changing the
suites directory.

- Only `*.yaml` and `*.yml` files directly in `suites_dir` are considered.
  Subdirectories are not searched and hidden files are ignored. At most 200
  suite files are allowed; more is a startup error.
- Symlinks are resolved. A file whose target lies outside `suites_dir` is
  rejected, as is anything that is not a regular file.
- Each file is read once (bounded by `limits.max_suite_bytes`), validated
  with the strict suite loader, and kept in memory. The bytes that passed
  validation are the snapshot stored with a run, together with their
  SHA-256. Workers execute the snapshot, not the file on disk.
- Schema and rules files named by a suite (`expect.schema`, `expect.rules`,
  `stream.schema`) must resolve inside `suites_dir` and exist.
- Files that fail to load do not stop the server. They are listed with the
  reason on the submission page and under `problems` in `GET /api/v1/suites`.

The suites directory should be writable only by the operator: whoever can
write there decides what the server runs.

## Submission policy

A submission names an approved suite file, a mode and target names. It has
no field for URLs, environment variables or suite content; unknown JSON
fields are rejected.

- `run` mode takes exactly one target; `compare` takes exactly two different
  targets, baseline then candidate. Every target must exist in the server
  configuration.
- A suite with mutating cases (any method other than GET, HEAD, OPTIONS)
  needs both `allow_writes: true` in the server configuration and
  `allow_writes: true` in the submission. Requesting writes while the server
  disallows them is refused even for read-only suites.
- Read-only runs get `worker.max_read_only_attempts` attempts; mutating runs
  get one and are never retried automatically.

**Resubmission** (`POST /api/v1/runs/{id}/resubmit`) is allowed for
interrupted, failed and cancelled runs only. It creates a new run from the
stored snapshot; the original row is unchanged. Before resubmitting, the
server checks that the suite file is still in the catalog with the same
SHA-256 as the snapshot. If the file was edited or removed, resubmission is
refused with `409 suite_changed`, because the operator approved the file as
it is now and resubmission would run the old content; submit a new run
instead. When the digest matches, the same checks as for a new submission
apply (targets still configured, write permission still granted).

## Sessions, cookies and CSRF

- `POST /api/v1/session` (or the sign-in form) checks the password and sets
  the `backendlab_session` cookie: `HttpOnly`, `SameSite=Lax`, `Path=/`,
  expiring with the session, and `Secure` unless `auth.secure_cookies` is
  false. Only the SHA-256 of the token is stored.
- API clients may send the token as `Authorization: Bearer <token>` instead
  of the cookie. The login response contains it; treat it like the password.
  When an `Authorization` header is present the cookie is ignored.
- Every state-changing request authenticated by the cookie must carry the
  session's CSRF token: header `X-CSRF-Token` for the API, form field
  `csrf_token` for the dashboard (the plan upload form sends it as its first
  part). Tokens are compared in constant time. Bearer-authenticated requests
  need no CSRF token because browsers never attach that header on their own.
- Login attempts are throttled per client address (the TCP peer address)
  with `auth.login_max_attempts` per `auth.login_window`, enforced in
  PostgreSQL so parallel requests cannot exceed it. Throttled attempts get
  `429` with `Retry-After` set to the window length.
- `X-Forwarded-For` and similar headers are ignored; there is no
  trusted-proxy setting. Behind a reverse proxy all clients share the
  proxy's address and therefore one throttle budget.
- The server speaks plain HTTP. For anything beyond loopback, put a TLS
  reverse proxy in front of it and keep `secure_cookies` on.

Every response carries `Content-Security-Policy: default-src 'self'; ...;
frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`,
`Referrer-Policy: no-referrer`, `X-Frame-Options: DENY` and
`Cache-Control: no-store` (static assets: `max-age=300`). The dashboard has
no inline scripts or styles and works without JavaScript; `app.js` only
asks for confirmation before cancel and resubmit and reloads pages of active
runs every five seconds.

## API reference

All endpoints except `/healthz`, `/readyz` and login require a session.
Errors use one envelope:

```json
{"error": {"code": "writes_not_permitted", "message": "writes not permitted: ..."}}
```

| Status | Codes | When |
|---|---|---|
| 400 | `invalid_request`, `invalid_submission`, `unknown_suite` | Malformed body or parameters, policy violations of the request itself. |
| 401 | `unauthenticated`, `invalid_credentials` | No valid session; wrong password. |
| 403 | `csrf_failed`, `writes_not_permitted` | Missing CSRF token; write policy refused the run. |
| 404 | `not_found`, `no_report` | Unknown run or endpoint; finished run without a stored report. |
| 405 | `method_not_allowed` | Wrong method for an existing endpoint. |
| 409 | `invalid_state`, `suite_changed` | Cancel of a finished run, resubmit of a queued, running or succeeded run, report of an unfinished run, edited suite file. |
| 413 | `too_large` | Body over its limit. |
| 415 | `unsupported_media_type` | Wrong `Content-Type`. |
| 429 | `throttled` | Login throttled; see `Retry-After`. |
| 500 | `internal` | Logged with the request ID; the message is generic. |
| 503 | `not_ready`, `unavailable` | Readiness failed; session store unreachable. |

Messages never contain secrets, database error text or upstream bodies.

The examples use a cookie jar for the browser-style flow and `$TOKEN` for
the bearer flow.

```sh
B=http://127.0.0.1:8080

# Sign in. The response holds token, csrf_token and expires_at.
curl -s -c jar -H 'Content-Type: application/json' \
  -d '{"password":"..."}' $B/api/v1/session
CSRF=...    # csrf_token from the response
TOKEN=...   # token from the response

# Approved suites, configured target names and load problems.
curl -s -H "Authorization: Bearer $TOKEN" $B/api/v1/suites

# Submit a compare run (cookie flow: CSRF header required).
curl -s -b jar -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -d '{"suite":"invoice-compare.yaml","mode":"compare","targets":["baseline","candidate"]}' \
  $B/api/v1/runs

# Submit a mutating suite: the server must allow writes and the request must ask.
curl -s -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"suite":"counter-writes.yaml","mode":"run","targets":["candidate"],"allow_writes":true}' \
  $B/api/v1/runs

# List runs: status may repeat or be comma-separated; limit 1-200 (default 50); offset 0-100000.
curl -s -H "Authorization: Bearer $TOKEN" "$B/api/v1/runs?status=failed,interrupted&limit=20&offset=0"

# Run details with case results (findings, observations, retained bodies if enabled).
curl -s -H "Authorization: Bearer $TOKEN" $B/api/v1/runs/run_abcdefghijklmnop

# Cancel: queued runs are cancelled at once (202, status cancelled); running
# runs are flagged (202, status running, cancel_requested true) and stop at
# the worker's next heartbeat.
curl -s -X POST -H "Authorization: Bearer $TOKEN" $B/api/v1/runs/run_abcdefghijklmnop/cancel

# Resubmit an interrupted, failed or cancelled run as a new run (201).
curl -s -X POST -H "Authorization: Bearer $TOKEN" $B/api/v1/runs/run_abcdefghijklmnop/resubmit

# Download the stored report (attachment): json (default), markdown or text.
curl -s -OJ -H "Authorization: Bearer $TOKEN" "$B/api/v1/runs/run_abcdefghijklmnop/report?format=markdown"

# Compare two EXPLAIN (FORMAT JSON) plans. Stateless: nothing is stored and
# no SQL runs. fail_on is optional (scan, join, estimate, spill, temp_io,
# structure, all).
curl -s -H "Authorization: Bearer $TOKEN" \
  -F before=@examples/plans/before.json -F after=@examples/plans/after.json -F fail_on=scan \
  $B/api/v1/plans/compare

# Sign out (cookie flow needs the CSRF header).
curl -s -b jar -X DELETE -H "X-CSRF-Token: $CSRF" $B/api/v1/session
```

`POST /api/v1/runs` and resubmit answer `201` with a `Location` header and
the run; run IDs have the form `run_` plus 16 base32 characters.

## Liveness and readiness

- `GET /healthz` answers `200 {"status":"ok"}` while the process serves
  HTTP. It does not touch the database, so a database outage does not get
  the process restarted by an orchestrator.
- `GET /readyz` answers `200 {"status":"ready"}` when a database ping
  succeeds within 2 seconds and every migration in the binary is applied
  unmodified, otherwise `503 not_ready` with a short reason. Use it to gate
  traffic, not to restart the process.

## Logs and identifiers

Server processes log structured lines (JSON by default) to stderr.

- Every request gets an ID, returned in `X-Request-Id`. A client-supplied
  `X-Request-Id` is kept only if it matches `^[A-Za-z0-9._-]{8,64}$`;
  otherwise the server assigns a new `req_...` ID.
- One access log line per request: `method`, `route` (the route pattern such
  as `/api/v1/runs/{id}`, not the raw path), `status`, `duration_ms`,
  `bytes`, `request_id`. Headers, query strings and bodies are never logged,
  so cookies, bearer tokens and passwords do not reach the log.
- Operations log `run_id` (the public `run_...` ID): `run submitted`,
  `run cancellation requested`, `run resubmitted`. Worker logs carry the same
  `run_id` plus `worker_id` and `attempt`, so a run can be followed from
  submission to completion.
- Internal errors are logged with `request_id` and the redacted error;
  panics are logged with their stack and answered with a generic 500.

## Retention and backups

```sh
backendlab retention cleanup --config server.yaml --dry-run   # print the cutoff
backendlab retention cleanup --config server.yaml             # delete
```

Cleanup deletes runs that finished before `now - retention.run_max_age`
(with their case results), expired sessions, and login attempts outside the
throttle window. Queued and running runs are never deleted. Schedule it with
cron or a systemd timer; the server does not run it on its own.

All state lives in PostgreSQL: runs with their suite snapshots and reports,
case results, sessions and login attempts. Back up with the usual tools
(`pg_dump`, or base backups with WAL archiving). Restoring an older backup
loses runs submitted since and may resurrect deleted sessions; sessions
expire on their own. Retained response bodies, if enabled, are upstream data
and the backup inherits their sensitivity.

## Limitations

- Single operator. There is one password and no user accounts, roles or
  per-user data. There is no multi-tenant isolation: anyone with the
  password sees and controls every run.
- No exactly-once execution. A worker that lost its lease may still have
  requests in flight, and read-only runs may be sent more than once within
  their attempt bound.
- Cancellation is cooperative and takes up to one heartbeat interval. It
  does not undo upstream side effects of requests that were already sent.
- Raw response bodies are not retained by default. With retention enabled
  they are redacted and bounded, but redaction is best effort and cannot
  recognize every secret embedded in free text.
- The suite catalog is read at startup; changes need a restart.
- Login throttling keys on the TCP peer address only; behind a proxy all
  clients share one budget.
- The server speaks plain HTTP; TLS must be terminated by a proxy.
- The run list counts and pages with separate statements, so totals can be
  off by runs submitted in between.

# Implementation report

This report covers the state of the repository at the final commit: what
is implemented, the main design decisions, the checks that were run and
their actual results, how to start, and what remains limited.

## Implemented features

### Shared core

- One finding type for every module (module, code, kind, severity, case ID,
  location, explanation, sanitized expected and observed values, details)
  with deterministic ordering and bounded collection.
- Versioned report document (`backendlab.report/v1`) with tool version,
  input identifiers and digests, completion status, summary counts,
  findings, limitations, and optional runtime timing. Renderers for text
  (terminal-safe escaping, optional color), JSON and Markdown (escaped).
- Exit codes: 0 passed, 1 checks failed, 2 invalid input, configuration or
  execution error, cancellation or incomplete run.
- Strict YAML for suites and configuration (unknown and duplicate keys
  rejected), resource limits with defaults and hard maximums, redaction of
  headers, JSON paths, URLs, error text and resolved secrets.

### ReplayLab

- YAML suites with cases (method, relative path, path parameters, query,
  headers, JSON or text body, timeout), assertions (status, headers, JSON
  Pointer `equals`/`exists`/`type`/`matches`, explicit duration threshold),
  captures and `${name}` substitution limited to path parameters, query
  values, header values and JSON body strings. References to values no
  earlier case captures are rejected when the suite loads.
- Targets defined separately from suites; credentials resolved from the
  environment and never stored.
- Write gate: methods other than GET, HEAD and OPTIONS need
  `--allow-writes`. No automatic retries anywhere.
- HTTP execution with TLS verification always on, redirects off by default
  (credentials dropped on origin change when enabled), bounded request and
  response sizes, per-request and whole-run timeouts, cancellation, bounded
  concurrency for independent read-only cases, httptrace timings.
- Structural JSON comparison: key order ignored, array order kept, absent
  distinct from null, decimal number comparison with absolute, relative
  and per-path tolerances, exclusions applied before reporting, keyed
  unordered arrays with inconclusive handling of missing or duplicate keys,
  selected-header and status comparison, bounded difference lists.

### StreamProbe

- Incremental SSE parser that handles chunk boundaries inside lines, CRLF
  pairs and UTF-8 sequences; LF, CR and CRLF; comments; multi-line data;
  `event`, `data`, `id`, `retry`; bounds on line, event, stream size and
  event count.
- Live reading with time to first event, inter-event gaps and duration;
  saved SSE text without invented timing; a timed recording format and a
  recorder for live streams.
- Termination classes kept apart: EOF, terminal event, timeout, cancellation,
  invalid data, limits, read errors. EOF is not malformed unless a terminal
  marker is required.
- Expectations: required events, terminal marker, JSON payloads, JSON Schema,
  maximum gap and duration. Optional provider-neutral `chat-delta` adapter.

### ExtractCheck

- Declarative rules: `required`, `allowed_values`, `pattern`, `decimal`,
  `sum` (explicit signed terms), `line_items_sum`, `line_item_product`,
  `product`, `date`, `date_order`, `unique` (within a batch), plus JSON
  Schema. All money arithmetic uses decimals with explicit tolerance and
  rounding.
- Each failure carries rule ID, document or case ID, field location,
  expected condition, sanitized observed value and explanation.
- Usable as `backendlab validate` on files and as `expect.schema` /
  `expect.rules` assertions in suites.

### QueryLens

- Parsing of PostgreSQL `EXPLAIN (FORMAT JSON)` output, estimated and
  analyzed, with missing metrics kept as unknown; clear rejection of other
  input.
- Documented node matching heuristic with a recorded confidence and reason
  per match.
- Findings for scan and join strategy changes, estimate versus actual row
  discrepancies (per-loop semantics), temporary I/O and spills, structure
  changes; observations for cost, rows, time, buffers and loops; opt-in
  `--fail-on` classes.

### JobDock and server mode

- Versioned SQL migrations with checksums and an advisory lock, applied only
  by `backendlab migrate`.
- Run states `queued`, `running`, `succeeded`, `failed`, `cancelled`,
  `interrupted` with transitions enforced in Go, in SQL predicates and by a
  database trigger.
- Claims with `FOR UPDATE SKIP LOCKED` in short transactions, leases with
  heartbeats, fencing tokens on every worker write, cancellation requests,
  recovery that requeues read-only runs a bounded number of times and marks
  runs with mutating cases `interrupted`.
- Worker with bounded concurrency and graceful shutdown; executor that runs
  the stored suite snapshot through an egress policy (host allowlist,
  address classification, rebinding-safe dialing).
- Operator authentication (bcrypt hash from the environment, PostgreSQL
  sessions, HttpOnly/SameSite/Secure cookies, CSRF tokens, login
  throttling), JSON API, server-rendered dashboard, readiness and liveness,
  report downloads, stateless plan comparison page, retention cleanup.

### Demonstration and operations

- `mockservice` with baseline and candidate variants, volatile fields, a
  changed required field, a configurable invoice total mismatch, valid,
  slow-gap and truncated SSE streams written in fragments, a slow endpoint
  and a mutating counter.
- Automated scenario in `demo/` covering all ten required behaviors.
- Makefile, multi-stage Dockerfile with a non-root runtime user, Docker
  Compose stack (database health check, explicit migration service,
  persistent volume, nothing published except the server on loopback), and
  a GitHub Actions workflow with read-only permissions.

## Architecture decisions

- **Modular monolith.** One module, two executables, internal packages with
  downward dependencies only. `replay` exposes hooks; `runner` connects the
  stream and validation modules, so the CLI and the worker share one
  assembly path and modules stay independently testable.
- **Exact numbers.** JSON is decoded with `json.Number` everywhere and
  compared with `shopspring/decimal`; no value passes through float64 on
  its way to a comparison or a money check.
- **Deterministic output.** Findings are sorted, cases keep suite order,
  offline reports carry no timestamps; golden tests pin the output.
- **Errors versus findings.** A problem with the suite, configuration or
  referenced files is an error (exit 2) detected before any request. A
  problem observed while running is a finding (exit 1 when failing).
- **Conservative recovery.** The system never claims exactly-once execution.
  Fencing tokens prevent stale writes; mutating runs are not replayed after
  uncertain interruption.
- **Server treats suites as data.** Destinations, credentials and write
  permission come only from server configuration; files referenced by
  suites must stay inside the approved suites directory.
- **Minimal dependencies.** chi (routing), pgx (PostgreSQL), yaml.v3,
  santhosh-tekuri/jsonschema, shopspring/decimal, x/crypto (bcrypt).

## Verification

All commands ran on Windows 11 with Go 1.27.3 (module directive `go 1.26.0`)
and PostgreSQL 17.6 on 127.0.0.1. Environment for test commands:
`BACKENDLAB_TEST_DATABASE_URL=postgres://...@127.0.0.1:55432/backendlab_test`
and `BACKENDLAB_TEST_ASYNC_CLOSE=1` (see "Host-specific notes").

| Check | Command | Result |
|---|---|---|
| Formatting | `gofmt -l cmd internal migrations web demo` | No files listed. |
| Vet | `go vet ./...` and `go vet -tags demo ./demo/` | Clean. |
| Build | `go build ./cmd/backendlab ./cmd/mockservice` | Both executables built. |
| Unit and integration tests | `go test -count=1 ./...` (three consecutive runs) | Each run: 25 of 26 packages passed. The one failure per run was environmental and in a different package each time: `replay` and `stream` binaries exited with native exception `0xc0000417` (no test failure reported), and one `store` test lost its PostgreSQL connection ("forcibly closed by the remote host"). Every package passed in at least one of the runs and in the race run. |
| Race detector | `CGO_ENABLED=1 go test -race -count=1 ./...` | All packages passed (about 260 s). |
| PostgreSQL integration | `go test -count=1 ./internal/store/... ./internal/jobs/... ./internal/auth/... ./internal/httpapi/... ./internal/server/...` | Passed against PostgreSQL 17.6 (about 96 s). |
| Fuzz smoke | each of the 7 fuzz targets with `-fuzztime 10s` | All passed, no failing inputs. |
| Demonstration | `go test -tags demo -count=1 -v ./demo/` | All 10 steps passed (3 of 3 consecutive runs). |
| Per-commit history | export each commit, `go build ./...`, `go vet ./...`, `go test` of packages changed by the commit | 121 of 121 earlier commits passed (one test step was re-run after a native crash with no test failure); this report's commit adds documentation only. |
| History metadata | script checking count, dates, offsets, order, identity, parents, message text | See "Commit history". |

Demonstration results observed (from the test log):

1. Structural comparison: compare exits 1 with differences only on
   `invoice-detail` at `/total`, `/customer/email` (missing) and
   `/customer/email_address` (added).
2. Volatile fields: `request_id` differed between the two responses and was
   not reported.
3. Numeric tolerance: `exchange_rate` 1.08 versus 1.0800004 absorbed by an
   absolute tolerance of 0.00001.
4. Invoice totals: the candidate fails `validate.sum`; the baseline passes;
   the batch file reports `validate.sum` and `validate.unique`.
5. SSE across fragmented writes: the chat stream (split inside lines, CRLF
   pairs and UTF-8 characters) passes all expectations.
6. Stream classification: truncated stream reports `stream.missing_terminal`,
   the gap stream `stream.max_gap_exceeded`, a slow request `replay.timeout`.
7. Plan comparison: `--fail-on scan,spill` exits 1 with
   `plans.scan_strategy_changed` and `plans.sort_spill_introduced`.
8. Background run: submitted through the API, completed by a worker with
   status `failed` (the environments differ), Markdown report downloaded.
9. Cancellation: a run with a slow request was cancelled while in flight.
10. Interrupted mutating run: one upstream increment, status `interrupted`
    pending operator review, not claimable again, still one increment after
    recovery.

### Host-specific notes and blocked checks

- On this machine a Winsock layered service provider (installed by a VPN
  product) blocks socket close while a read is pending and occasionally
  crashes processes inside `WSASend`. Tests that use sockets therefore run
  with `BACKENDLAB_TEST_ASYNC_CLOSE=1`, which makes test listeners and
  client connections close in the background; production code does not
  read this variable.
- The crash appears as a process exit with `0xc0000417` or an access
  violation `0xc0000005` in native code, with no Go test failure reported.
  It hit the `replay` test binary in about one run in three, occasionally
  other socket-heavy test binaries, and once ended a demo run inside a
  combined check run. Local PostgreSQL connections are affected too: one
  test connection was reset by the host during setup. Repeated runs pass; the race
  detector run, which serializes more, passed every package.
- `TestWorkerBoundsConcurrency` once exceeded its 10-second wait while every
  package ran in parallel against the slow local database; its waits are now
  30 seconds.
- End-to-end runs of the compiled `backendlab` binary against a local HTTP
  server hang or crash on this host for the same reason (exit 124 after a
  60-second timeout). The same paths are exercised in-process by the CLI
  integration tests and the demonstration scenario.
- **Not run:** `docker build` and `docker compose` (Docker is not installed
  here) and the GitHub Actions workflow (nothing was pushed). Both are
  configured but unverified.

## Quick start

```sh
make build
bin/mockservice &
cd examples/invoice-api && ../../bin/backendlab compare compare-suite.yaml
```

Offline checks without any service:

```sh
backendlab validate examples/invoice/invoices-batch.json --rules examples/invoice/invoice-rules.yaml
backendlab plans compare examples/plans/before.json examples/plans/after.json --fail-on scan,spill
backendlab stream inspect internal/stream/testdata/chat.sse --terminal-data '[DONE]'
```

Server mode: see the README and `docs/server-operations.md`
(`backendlab migrate`, `backendlab serve`, `backendlab worker`, or
`docker compose up -d --build`).

## Known limitations

- Single-run timings are observations; only explicit thresholds fail.
- Validation shows consistency with configured rules, not authenticity.
- Plan node matching is heuristic; cost is not time; plans do not explain
  HTTP regressions; no index advice.
- Redaction is configuration-driven and not a guarantee.
- Single operator, no multi-tenant isolation, no exactly-once execution;
  cancellation does not undo upstream side effects; a stale worker may still
  finish a request it started.
- Egress checks complement network controls and do not replace them.
- JSON Schemas must be single files (internal `$defs` references only).
- The CLI does not use proxy environment variables.

## Commit history

- Commits: 122, all on `main`, linear, author and committer
  `happydev123 <michaelmu2222@gmail.com>`.
- First commit: 2025-11-01 10:14 -07:00. Last commit (this report):
  2026-05-31 17:22 -07:00.
- Per month: November 15, December 20, January 14, February 17, March 15,
  April 22, May 19.
- Author and committer dates increase strictly along the history and use
  America/Los_Angeles offsets (-07:00 before 2025-11-02 and from
  2026-03-08, -08:00 in between).

Commit timestamps were assigned retrospectively to organize development into
a November 2025–May 2026 timeline; they do not represent the actual dates the
work was performed. See [development-history.md](development-history.md).

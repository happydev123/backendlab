# BackendLab delivery report

This report covers the repository at its final commit: what is implemented,
the main design decisions, the checks that were run and their actual
results, how to start, what remains limited, and how the commit history was
verified.

This repository was implemented with AI assistance. Its commit timestamps were assigned retrospectively to organize development into a November 2025–May 2026 timeline; they do not represent the actual dates the work was performed.

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

The checks below were re-run for this report on Windows 11 with Go 1.27.3
(module directive `go 1.26.0`) and PostgreSQL 17.6 listening on 127.0.0.1.
Database-backed tests used
`BACKENDLAB_TEST_DATABASE_URL=postgres://...@127.0.0.1:55432/backendlab_test`.
Socket tests ran with `BACKENDLAB_TEST_ASYNC_CLOSE=1` (see "Host-specific
notes").

| Check | Command | Result |
|---|---|---|
| Formatting | `gofmt -l cmd internal migrations web demo` | No files listed. |
| Vet | `go vet ./...` and `go vet -tags demo ./demo/` | Clean. |
| Build | `go build ./cmd/backendlab ./cmd/mockservice` | Both executables built; `backendlab version` prints `backendlab 0.1.0-dev (go1.27.3, windows/amd64, report schema backendlab.report/v1)`. |
| Unit, CLI and PostgreSQL integration tests | `go test -count=1 ./...` | 25 of 26 test packages passed, including `store`, `jobs`, `auth`, `httpapi` and `server` against PostgreSQL. The `replay` test binary exited with native exception `0xc0000417` without reporting a test failure (host issue below). |
| `replay` re-runs | compiled `replay.test.exe -test.v`, 8 runs from the package directory | 5 runs passed every test; 3 runs died in native code inside `WSASend` (exit 127 or `Exception 0xc0000005` at an address in a non-Go DLL); no run reported a failing test. |
| Race detector | `CGO_ENABLED=1 go test -race -count=1 ./...` | All 26 test packages passed, including `replay` and the PostgreSQL packages. |
| Fuzz smoke | each of the 7 fuzz targets with `-fuzztime 10s` (`make fuzz-smoke` list) | All 7 passed, no failing inputs. The first attempt failed to compile with `can't find export data (bufio: buffer full)` from the default Go build cache; with a fresh `GOCACHE` all targets built and passed. |
| Demonstration | `go test -tags demo -count=1 -v ./demo/` | All 10 steps passed in 3 of 3 consecutive runs with a fresh build cache. An earlier attempt in the same session ended silently in step 4 with no test failure reported (same native crash). |
| Offline CLI with the built binary | `backendlab validate examples/invoice/invoices-batch.json --rules examples/invoice/invoice-rules.yaml` | Exit 1 with `validate.unique` (INV-1001 twice) and `validate.sum` (INV-1002: 149.97 + 29.99 = 179.96, stated 180.00, difference 0.04 exceeds tolerance 0.01). |
| | `backendlab plans compare examples/plans/before.json examples/plans/after.json --fail-on scan,spill` | Exit 1. |
| | `backendlab stream inspect internal/stream/testdata/chat.sse --terminal-data '[DONE]'` | Exit 0: 5 events, termination `terminal_event`, timing `unknown`. |
| | `backendlab validate nosuch.json --rules ...` | Exit 2. |
| Live CLI against the mock service | `mockservice` plus `backendlab compare compare-suite.yaml` in `examples/invoice-api` | **Blocked on this host**: the process did not exit within 60 s (exit 124). See below. The same code paths pass in-process in the CLI integration tests and the demonstration. |
| Per-commit history | each commit exported with `git archive`, then `go build ./...` and `go vet ./...` | All 122 earlier commits built and vetted cleanly (122 of 122). Tests were not re-run per commit. This report's commit changes documentation only. |
| History metadata | Go program reading `git log` (count, dates, America/Los_Angeles offsets via `time/tzdata`, author/committer order, single parent, identity, message text) | See "Commit history". |
| Secrets and stray files | `git ls-files` filtered for binaries, databases, keys and `.env`; `git grep` for key and token patterns and for the local database password | Nothing found; `.env.example` contains placeholders only. |

Demonstration results observed in the final run:

1. Structural comparison: compare exits 1 with 4 findings; differences only
   on `invoice-detail` at `/total`, `/customer/email` (missing) and
   `/customer/email_address` (added).
2. Volatile fields: `request_id` differed between the two responses
   (`req_b734…` versus `req_68bf…`) and was not reported.
3. Numeric tolerance: `exchange_rate` 1.08 versus 1.0800004 absorbed by an
   absolute tolerance of 0.00001.
4. Invoice totals: the candidate fails `validate.sum`; the baseline passes;
   the batch file produces 3 findings.
5. SSE across fragmented writes: the chat stream (split inside lines, CRLF
   pairs and UTF-8 characters) passes all expectations.
6. Stream classification: the truncated stream reports a missing terminal
   marker, the gap stream a gap over the limit, a slow request a
   `replay.timeout` transport error.
7. Plan comparison: 11 findings including a scan strategy change and a sort
   spill.
8. Background run: submitted through the API and completed by a worker with
   status `failed` (the environments differ); Markdown report downloaded.
9. Cancellation: a run was cancelled while its slow request was in flight.
10. Interrupted mutating run: one upstream increment, status `interrupted`
    ("worker lease expired during attempt 1; the run contains mutating
    cases, so it is unknown which requests reached the upstream"), not
    requeued.

### Host-specific notes and blocked checks

- This machine has a Winsock layered service provider ("ASProxy", listed by
  `netsh winsock show catalog`). With it, closing a socket that has a
  pending read can block indefinitely, and processes occasionally crash in
  native code inside `WSASend`. A 20-line standard-library Go program that
  performs one GET against the mock service returns in about 5 ms, but hangs
  as soon as it calls `http.Transport.CloseIdleConnections()`. That is
  the step at which the `backendlab` binary stops at the end of a run, so
  the live CLI check is recorded as blocked rather than passed.
- For tests, `BACKENDLAB_TEST_ASYNC_CLOSE=1` makes test listeners and
  client connections close in the background. Production code does not read
  this variable.
- The native crashes appear as exit `0xc0000417` or `Exception 0xc0000005`
  with no Go test failure. They affected the `replay` test binary in about
  one run in three and one demonstration run. Repeated runs and the race
  detector run passed.
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


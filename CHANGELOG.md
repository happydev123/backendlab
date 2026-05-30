# Changelog

All notable changes are recorded here. BackendLab has no tagged release
yet; everything below is unreleased work on the main branch.

## Unreleased

### Durable runs and dashboard

- PostgreSQL schema with versioned, checksummed migrations and an explicit
  `backendlab migrate` command.
- JobDock: run state machine, `FOR UPDATE SKIP LOCKED` claims, leases with
  heartbeats, fencing tokens on every worker write, cancellation requests,
  and recovery that retries read-only runs a bounded number of times and
  marks runs with mutating cases `interrupted`.
- Worker with bounded concurrency and graceful shutdown (`backendlab worker`).
- Operator authentication: bcrypt password hash from the environment,
  PostgreSQL-backed sessions, CSRF tokens, login throttling, and
  `backendlab hash-password`.
- Egress policy for server-executed requests (host allowlist, address
  classification, rebinding-safe dialing).
- JSON API and server-rendered dashboard for approved suites, run status,
  cancellation, case results, findings and report downloads, plus a
  stateless plan comparison page.
- `backendlab retention cleanup`, Dockerfile, Docker Compose stack, CI
  workflow and an automated demonstration scenario.

### Query plans

- QueryLens: parsing of PostgreSQL `EXPLAIN (FORMAT JSON)` output, estimated
  and analyzed, with missing metrics kept as unknown.
- Node matching heuristic with recorded reasons; scan and join strategy
  changes, estimate mismatches, spills, temporary I/O and structure changes;
  cost, time and buffer observations.
- `backendlab plans compare` with opt-in `--fail-on` classes.

### Streams and validation

- StreamProbe: incremental SSE parser tolerant of arbitrary chunk
  boundaries, termination classification, expectations, timed recordings,
  an optional chat-delta adapter, `backendlab stream inspect` and
  `backendlab stream probe`.
- ExtractCheck: declarative rules (required, allowed values, patterns,
  decimals, sums, products, line items, dates, uniqueness) with decimal
  arithmetic and explicit tolerance and rounding; `backendlab validate`.
- Suites can use `expect.schema`, `expect.rules` and `stream` sections.

### HTTP replay and comparison

- Shared finding and report model with text, JSON and Markdown output.
- YAML suites with strict validation, captures and restricted substitution.
- Targets with environment-backed credentials and redaction.
- Structural JSON comparison with exclusions, tolerances and keyed
  unordered arrays.
- `backendlab run` and `backendlab compare` with write gating, timeouts,
  bounded concurrency and documented exit codes.
- Demonstration service for local experiments.

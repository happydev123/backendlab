# Implementation plan

This plan splits BackendLab into four stages that each end with a usable
tool. Later stages build on the shared model from the first stage and do not
change its meaning. The plan lists commit-sized tasks; each task should
build, pass its tests and leave documentation consistent with the code.

## Shared model and compatibility assumptions

These definitions are fixed before any module is written.

| Term | Meaning |
|---|---|
| Suite | A versioned YAML file with a name and an ordered list of cases. It holds no destinations and no credentials. |
| Case | One HTTP request plus assertions, comparison settings and optional captured values. Case IDs are stable and unique within a suite. |
| Target | A named environment (base URL plus header configuration). Credentials are referenced by environment variable name and resolved at runtime. |
| Run | One execution of a suite against one target (`run`) or two targets (`compare`). |
| Case result | The observations (status, timings, sizes) and findings for one case. |
| Artifact | A sanitized report or a supplied query plan. |
| Finding | A structured result: module, code, kind, severity, case ID, location, explanation, expected and observed values, details. |

Finding kinds keep four outcomes apart: `transport_error`, `assertion_failure`,
`difference`, and `inconclusive`/`unsupported`. `observation` records facts
such as timings that are not judged. Only findings with severity `error`
fail a run.

Compatibility assumptions:

- Suites carry `version: 1`. Unknown keys are errors, so a newer suite read
  by an older binary fails loudly instead of being half-applied.
- Reports carry `schema: backendlab.report/v1`. Fields may be added within
  v1; removing or redefining a field requires v2.
- JSON numbers are kept as their literal text (`json.Number`) and compared
  as decimals. No value passes through float64 on its way to a comparison or
  a money check.
- JSON Pointer (RFC 6901) is the only path syntax. A pattern form allows a
  single `*` segment for exclusions and redaction.
- Query plans are PostgreSQL `EXPLAIN (FORMAT JSON)` output from supported
  PostgreSQL versions. Other formats are rejected.
- PostgreSQL is required only for `serve`, `worker`, `migrate` and the
  administrative commands. Every offline command works without it.

Exit codes: 0 when the command completed and its checks passed, 1 when it
completed but checks failed (assertions, differences, thresholds, or case
transport errors), 2 for invalid input, configuration errors, unsupported
input, cancellation and failures to execute a suite at all.

## Stage 1: HTTP replay and comparison

Goal: `backendlab run` and `backendlab compare` work offline against any
reachable HTTP service, with deterministic reports.

1. Shared core: findings, report document and renderers (text, JSON,
   Markdown) with terminal and Markdown escaping.
2. JSON Pointer and bounded JSON decoding helpers.
3. Suite model, strict YAML loading and validation.
4. Variable templates restricted to explicit locations; static check that
   every reference is captured by an earlier case.
5. Configuration: CLI file, targets with environment-backed credentials,
   limits with defaults and hard maximums.
6. Redaction of headers, JSON paths, URLs, error text and resolved secrets.
7. Structural JSON comparison: key order ignored, array order kept,
   absent distinct from null, type changes.
8. Numeric tolerances (absolute, relative, per path) using decimals.
9. Exclusions of volatile fields applied before reporting; bounded
   differences and previews.
10. Keyed unordered array comparison with inconclusive handling of
    missing or duplicate keys.
11. Status and selected-header comparison; text body comparison by digest.
12. HTTP transport with TLS verification, bounded sizes, no redirects by
    default.
13. Request building: path parameter encoding, query encoding, header
    merging, JSON and text bodies.
14. Write gate: non-read-only methods need `--allow-writes`; no retries.
15. Per-request and whole-run timeouts, cancellation, case status rules.
16. Assertions: status, headers, JSON pointer checks, explicit duration
    threshold.
17. Captures and sequential execution; skipped dependents.
18. Bounded concurrency for independent read-only cases.
19. Compare mode and report assembly.
20. CLI: argument parsing, output selection, `run`, `compare`, `version`.
21. Demonstration service: baseline/candidate JSON, volatile fields,
    changed required field.

## Stage 2: streaming inspection and structured validation

Goal: inspect SSE streams live and from files; validate extracted JSON
against schemas and declarative rules; use both as assertions in suites.

1. Incremental SSE parser handling split lines, CR/LF/CRLF and split UTF-8.
2. Parser limits (line, event, stream size, event count).
3. Live reading with time to first event, gaps and duration from an
   injectable clock.
4. Termination classification: EOF, terminal event, timeout, cancellation,
   invalid data, limits.
5. Expectations: required events, terminal marker, JSON payloads, schema,
   maximum gap and duration.
6. Saved-file inspection without invented timing; timed recording format.
7. Optional chat-delta adapter, independent from the core parser.
8. `backendlab stream inspect` and `backendlab stream probe`.
9. JSON Schema wrapper with remote and file references disabled.
10. Rules file format and strict loading.
11. Required fields, allowed values, patterns.
12. Decimal fields with scale and range checks.
13. Sum, product and line-item rules with explicit tolerance and rounding.
14. Date parsing and ordering.
15. Duplicate identifiers within a batch.
16. Document loading (object, array, JSON Lines) and reports.
17. `backendlab validate` and suite integration (`expect.schema`,
    `expect.rules`, stream cases).
18. Demonstration service: invoice total mismatch, streams, slow endpoint.

## Stage 3: offline query-plan comparison

Goal: compare two supplied `EXPLAIN (FORMAT JSON)` plans without running SQL.

1. Plan parsing for estimated and analyzed plans; rejection of other input.
2. Node model with missing metrics kept as unknown.
3. Node matching heuristic with recorded reasons.
4. Scan and join strategy changes.
5. Estimate versus actual row discrepancies (per-loop semantics).
6. Temporary I/O and sort or hash spill indicators.
7. Structure changes, added and removed nodes.
8. Cost, time and buffer observations with stated caveats.
9. Opt-in failure classes for exit code 1.
10. `backendlab plans compare` and reports.

## Stage 4: durable runs and dashboard

Goal: an optional single-operator server that executes approved suites in
the background and shows results.

1. Versioned SQL migrations and a migration runner with checksums.
2. Run and case-result storage with bounded reports.
3. State machine and transition checks.
4. Claim with `FOR UPDATE SKIP LOCKED`, leases, heartbeats and fencing
   tokens.
5. Cancellation requests.
6. Recovery of expired leases: bounded retry for read-only runs, mutating
   runs marked interrupted.
7. Worker with bounded concurrency and graceful shutdown.
8. Password hashing, PostgreSQL-backed sessions, CSRF tokens, login
   throttling.
9. Egress policy for server-executed requests.
10. Suite executor wiring for the worker (approved suites, server-side
    targets and write permission).
11. Authenticated JSON API: submit, list, details, cancel, report download,
    liveness, readiness.
12. Server-rendered dashboard: run list, submission, run detail with case
    results, differences, stream and validation findings, plan comparison
    upload, report download.
13. `serve`, `worker`, `migrate`, `hash-password`, retention cleanup.
14. Docker image, Compose file, CI workflow.
15. Automated demonstration scenario and final documentation.

## Out of scope for the first release

OCR, LLM hosting, generated diagnoses, scripting in suites, a general
workflow engine, message brokers, Kubernetes, SSO, multi-tenant isolation,
running SQL to collect plans, and posting results to external services.

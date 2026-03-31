# Changelog

All notable changes are recorded here. BackendLab has no tagged release
yet; everything below is unreleased work on the main branch.

## Unreleased

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

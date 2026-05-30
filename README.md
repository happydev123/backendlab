# BackendLab

BackendLab is a Go toolkit for reproducing and investigating regressions in
backend and AI services. It replays the same HTTP fixtures against two
environments and compares the responses, inspects server-sent event streams,
validates structured extraction results against schemas and business rules,
and compares PostgreSQL query plans you supply. An optional server runs
approved suites in the background with results stored in PostgreSQL.

Everything except the server works offline from the command line, without a
database.

## One workflow, five modules

A typical investigation: a developer changes an invoice extraction API.

1. **ReplayLab** runs the same suite against the old (`baseline`) and new
   (`candidate`) deployment and compares status codes, selected headers and
   JSON bodies structurally, ignoring volatile fields and allowing explicit
   numeric tolerances.
2. **ExtractCheck** validates each extracted invoice: JSON Schema, required
   fields, decimal money values, subtotal/tax/total consistency, line-item
   sums, currencies, dates and duplicate document numbers.
3. **StreamProbe** checks the streaming endpoint: event framing, required
   events, the terminal marker, payload JSON and gaps between events.
4. **QueryLens** compares `EXPLAIN (FORMAT JSON)` output captured before and
   after the change, if you have it. Plans are always supplied as files;
   BackendLab never runs SQL and does not link HTTP latency to queries.
5. **JobDock** (server mode) runs approved suites in the background with
   leases, cancellation and conservative crash recovery.

All modules report through one finding format (module, code, kind,
severity, case, location, explanation, sanitized expected and observed
values), so reports read the same everywhere.

## Quick start

Requirements: Go 1.26 or newer. PostgreSQL 15+ only for server mode.

```sh
make build                       # bin/backendlab and bin/mockservice
bin/mockservice &                # demo service on 127.0.0.1:8090
cd examples/invoice-api
../../bin/backendlab compare compare-suite.yaml
```

`examples/invoice-api/backendlab.yaml` defines the `baseline` and
`candidate` targets used above. The demonstration service serves both
variants from one process and has deliberate differences.

## CLI use

```
backendlab run SUITE --target NAME           assertions against one target
backendlab compare SUITE                     baseline vs candidate
backendlab stream inspect FILE               saved SSE text or timed recording
backendlab stream probe URL                  live SSE request (optionally --record)
backendlab validate DOCS --rules RULES       extracted JSON documents
backendlab plans compare BEFORE AFTER        EXPLAIN (FORMAT JSON) plans
backendlab version
```

Common flags: `--format text|json|markdown`, `--output FILE`,
`--config FILE` (default `./backendlab.yaml` when present) and
`--no-color`. Reports go to stdout, diagnostics to stderr; JSON output never
contains log lines.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | The command completed and every configured check passed. |
| 1 | The command completed but checks failed: assertions, differences between environments, validation rules, stream expectations, `--fail-on` plan classes, or transport errors on individual cases. |
| 2 | Invalid input or configuration, unsupported input, a suite that could not be executed at all (for example mutating cases without `--allow-writes`, or an unreadable rules file it references), cancellation, or a whole-run timeout. |

A connection failure or timeout on one case is a finding (exit 1); a suite
that cannot start is an error (exit 2).

### Example: comparing environments

```
$ backendlab compare compare-suite.yaml
backendlab compare (backendlab 0.1.0-dev)
Status: FAILED
Input: suite compare-suite.yaml (sha256 8af9cb8d78f3)
Targets: baseline, candidate

Cases: 4 total, 3 passed, 1 failed
  PASS  health             baseline 200 1.1ms | candidate 200 0.0ms
  PASS  list-invoices      baseline 200 0.5ms | candidate 200 0.0ms
  FAIL  invoice-detail     baseline 200 0.5ms | candidate 200 0.0ms
  PASS  invoice-not-found  baseline 404 0.0ms | candidate 404 0.2ms

Findings: 4 (3 failing)
  [info] observation compare.within_tolerance case=invoice-detail
      1 numeric differences were within the configured tolerance
  [error] difference compare.field_missing case=invoice-detail at /customer/email
      member is missing in candidate
      expected: "accounts@nordlicht-logistik.example"
      observed: (absent)
  [error] difference compare.field_added case=invoice-detail at /customer/email_address
      member was added in candidate
  [error] difference compare.value_changed case=invoice-detail at /total
      number changed by 0.05 (tolerance: exact)
      expected: 1300.87
      observed: 1300.92
...
```

(Output trimmed; `details` lines omitted.) The request IDs and timestamps
the service returns are excluded by the suite, the reordered line items are
matched by `sku`, and an exchange-rate change of 4e-7 is inside the
configured tolerance, so none of them is reported as a difference.

### Example: validating extracted invoices

```
$ backendlab validate examples/invoice/invoices-batch.json --rules examples/invoice/invoice-rules.yaml
backendlab validate (backendlab 0.1.0-dev)
Status: FAILED
...
Cases: 4 total, 1 passed, 3 failed
  FAIL  INV-1001
  FAIL  INV-1002
  PASS  INV-1003
  FAIL  INV-1001#3

Findings: 3 (3 failing)
  [error] assertion_failure validate.unique case=INV-1001 at /invoice_number
      rule "unique-invoice-number": the value of /invoice_number also appears in INV-1001#3
  ...
  [error] assertion_failure validate.sum case=INV-1002 at /total
      rule "total": computed 179.96 from /subtotal (149.97) + /tax (29.99); stated /total is 180.00; difference 0.04 exceeds tolerance 0.01
```

All money arithmetic uses decimals. The total rule is written as explicit
terms (`subtotal + tax - discount`), so other invoice formulas are a
configuration change, not a code change.

### Example: inspecting a stream

```
$ backendlab stream inspect truncated.sse --terminal-data '[DONE]' --require-terminal
Status: FAILED
Findings: 5 (1 failing)
  [error] assertion_failure stream.missing_terminal
      stream ended (EOF) before the required terminal marker
  [warning] observation stream.incomplete_event at event:4
      stream ended inside an event (no terminating blank line); per the standard it was not dispatched
...
Limitations:
  - Saved SSE text has no reliable timing: first-event time, gaps and duration are unknown, and none is inferred from position in the file.
```

Plain EOF is not reported as malformed unless a terminal marker is required.
Live probes and timed recordings (`stream probe --record`) do carry timing.

### Example: comparing query plans

```
$ backendlab plans compare examples/plans/before.json examples/plans/after.json --fail-on scan,spill
...
  [error] difference plans.sort_spill_introduced at /Plan
      Sort now spills to disk (it stayed in memory before)
  [error] difference plans.scan_strategy_changed at /Plan/Plans/0/Plans/0
      Scan strategy on orders changed from Index Scan using orders_created_at_idx to Seq Scan
  [info] observation plans.cost_changed at /Plan
      Estimated total cost changed from 2225.40 to 31390.61 (planner cost units, not milliseconds)
```

Without `--fail-on`, plan differences are reported as warnings and the
command exits 0.

## Write safety

- Only `GET`, `HEAD` and `OPTIONS` run by default. Other methods require
  `--allow-writes`; the server additionally requires `allow_writes: true` in
  its configuration, and a submitted run cannot grant itself write access.
- No request is ever retried automatically.
- Cancelling or timing out stops waiting; it does not undo a request the
  upstream already received.
- Background runs with mutating cases are never replayed after an uncertain
  interruption; they are marked `interrupted` for operator review.

## Optional server

```sh
cp .env.example .env              # set POSTGRES_PASSWORD and the password hash
printf '%s\n' 'a long operator passphrase' | bin/backendlab hash-password
docker compose up -d --build      # postgres, migrate, server, worker
open http://127.0.0.1:8080
```

Without Docker:

```sh
export BACKENDLAB_DATABASE_URL=postgres://...
export BACKENDLAB_ADMIN_PASSWORD_HASH='$2a$12$...'
backendlab migrate --config examples/server/backendlab-server.yaml
backendlab serve   --config examples/server/backendlab-server.yaml
backendlab worker  --config examples/server/backendlab-server.yaml
```

The server is for a single operator. It runs only suites from the configured
`suites_dir` against targets defined in server configuration, applies an
egress policy to every outbound connection, and stores sanitized results.
See [docs/server-operations.md](docs/server-operations.md).

## Important limitations

- Timings are single-run observations. Only explicit thresholds
  (`expect.max_duration`, `stream.max_gap`, `stream.max_duration`) fail a
  run; nothing is presented as a statistically established regression.
- Passing extraction validation shows consistency with the configured rules,
  not that a document is authentic or correctly extracted.
- Plan node matching is heuristic, planner cost is not time, and a plan
  difference does not prove the cause of an HTTP regression. QueryLens does
  not recommend indexes.
- Redaction is driven by configuration and cannot guarantee that arbitrary
  payloads contain no sensitive data.
- Server mode has no multi-tenant isolation and no exactly-once execution.
- The server's egress checks complement network controls; they do not
  replace them.

## Documentation

| Topic | Document |
|---|---|
| Architecture and package boundaries | [docs/architecture.md](docs/architecture.md) |
| Suite file reference | [docs/suites.md](docs/suites.md) |
| Comparison semantics | [docs/comparisons.md](docs/comparisons.md) |
| Streams | [docs/streams.md](docs/streams.md) |
| Extraction validation | [docs/validation.md](docs/validation.md) |
| Query plans | [docs/query-plans.md](docs/query-plans.md) |
| Background runs | [docs/jobdock.md](docs/jobdock.md) |
| Server operations | [docs/server-operations.md](docs/server-operations.md) |
| Egress policy | [docs/egress.md](docs/egress.md) |
| Security model | [docs/security.md](docs/security.md) |
| Development history | [docs/development-history.md](docs/development-history.md) |

## Tests

```sh
make check          # gofmt, go vet, go test ./...
make race           # race detector (needs cgo)
make integration    # PostgreSQL tests; set BACKENDLAB_TEST_DATABASE_URL
make fuzz-smoke     # every fuzz target briefly
make demo           # end-to-end scenario: mock service, CLI, server, worker
```

The demonstration scenario in `demo/` asserts on real results: structural
differences, excluded volatile fields, numeric tolerance, an inconsistent
invoice total, a fragmented SSE stream, stream timeout and completion
classification, a plan comparison, a background run through the API,
cancellation, and an interrupted mutating run that is not retried.

## Commit history

Commit timestamps were assigned retrospectively to organize development into
a November 2025–May 2026 timeline; they do not represent the actual dates the
work was performed. See [docs/development-history.md](docs/development-history.md).

## License

MIT; see [LICENSE](LICENSE).

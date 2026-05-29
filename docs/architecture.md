# Architecture

BackendLab is a modular monolith: one Go module, two executables, and a set
of internal packages with one-directional dependencies. Every module reports
through the same finding and report types, so a stream check, an invoice
total check and a JSON difference look the same to the CLI, the database
and the dashboard.

## Executables

| Binary | Purpose |
|---|---|
| `backendlab` | CLI for every offline feature, plus `serve`, `worker`, `migrate` and administrative commands for server mode. |
| `mockservice` | Demonstration service with deliberate faults. Development only. |

## Package map

```
cmd/backendlab ──> internal/cli
                     ├─ runner ──> replay ──> compare
                     │     │          └────> config, redact, report, suite
                     │     ├──────> stream ──> schema
                     │     ├──────> validate ──> schema
                     │     └──────> jobs (JobExecutor) ──> store
                     ├─ plans
                     ├─ server ──> httpapi, dashboard, catalog, auth ──> store
                     └─ store ──> migrations (embedded SQL)

shared core: finding, report, suite, config, limits, redact,
             jsonptr, jsonutil, yamlutil, schema, version
```

Dependencies point downward only. `replay` does not import `stream` or
`validate`; it exposes hooks (`replay.Hooks`), and `runner` plugs the two
modules in. This keeps each module testable on its own and lets the CLI and
the worker share one assembly path.

### Shared core

- `finding`: the `Finding` type, kinds (`transport_error`,
  `assertion_failure`, `difference`, `inconclusive`, `unsupported`,
  `observation`), severities, deterministic sorting and bounded collection.
- `report`: the versioned report document (`backendlab.report/v1`), status
  and exit-code mapping, and the text, JSON and Markdown renderers with
  terminal and Markdown escaping.
- `suite`: the YAML suite model, strict loading, validation and the
  restricted `${name}` substitution.
- `config` and `limits`: CLI and server configuration, targets with
  environment-backed credentials, and resource bounds with hard maximums.
- `redact`: header, JSON path, URL, error and secret-value redaction.
- `jsonptr`, `jsonutil`, `yamlutil`, `schema`: JSON Pointer, bounded JSON
  decoding with `json.Number`, strict YAML decoding, JSON Schema compilation
  with external references disabled.

### Modules

| Module | Package | Input | Output |
|---|---|---|---|
| ReplayLab | `replay`, `compare` | suite + one or two targets | case results, differences, assertion failures |
| StreamProbe | `stream` | live SSE body or saved file | events, timings when known, termination, findings |
| ExtractCheck | `validate` | JSON documents + rules/schema | per-document findings |
| QueryLens | `plans` | two EXPLAIN JSON files | matched nodes, findings, observations |
| JobDock | `jobs`, `store` | queued runs | persisted case results and reports |

### Server mode

- `catalog` lists approved suites from the configured directory and checks
  submissions against server policy (configured targets only, write
  permission from both configuration and request).
- `httpapi` holds the JSON API and middleware (request IDs, access logs,
  security headers, authentication, CSRF, body limits).
- `dashboard` renders HTML pages from templates embedded by the `web`
  package.
- `server` assembles the router and the HTTP server with timeouts and
  graceful shutdown.
- `jobs.Worker` claims runs and calls `runner.JobExecutor`, which executes
  the stored suite snapshot through the egress policy (`egress`).

## Data flow of a comparison

1. `suite.LoadFile` reads and validates the suite (size bound, strict keys,
   reference checks for captured variables).
2. `config.Target.Resolve` reads credentials from the environment. Resolved
   values are registered with the redactor and never stored.
3. `runner.New` compiles every schema, rules file and stream expectation the
   suite references. A broken reference stops the command before any request
   is sent (exit 2).
4. `replay.Executor.Compare` runs each case against baseline then candidate,
   with per-request and whole-run timeouts, bounded response reads and no
   retries. Independent read-only cases may run in parallel; suites with
   captures or mutating cases run sequentially.
5. Assertions, body validation and stream checks produce findings per
   target; `compare` produces differences between the two responses after
   exclusions and tolerances.
6. The report is finalized: findings sorted, summary counted, status
   derived. The CLI renders it to stdout or `--output`; the worker stores it.

## Determinism

- Findings are sorted by case ID, module, location (array indexes and
  `event:N` segments numerically), code, severity and message.
- Cases appear in suite order regardless of parallel execution.
- Offline commands (`validate`, `plans compare`, `stream inspect`) never
  write timestamps, so identical input gives byte-identical output.
- Runtime reports include execution times because they are observations of
  that run.

## Error separation

| Situation | Where it appears | Exit code |
|---|---|---|
| Invalid suite, config, plan or rules file | stderr, no report | 2 |
| Mutating cases without `--allow-writes` | stderr, no report | 2 |
| One case cannot connect or times out | `transport_error` finding, case status `error` | 1 |
| Assertion fails | `assertion_failure` finding | 1 |
| Environments differ | `difference` finding | 1 |
| Check cannot be evaluated | `inconclusive` / `unsupported` finding | 0 unless severity error |
| Run cancelled or whole-run timeout | report status `cancelled` / `incomplete` | 2 |

## Persistence

PostgreSQL holds runs, case results, sessions and login attempts. The schema
is created by numbered SQL files in `migrations/`, applied only by
`backendlab migrate`. `serve` and `worker` refuse to start when migrations
are pending or modified. See [jobdock.md](jobdock.md) for the run state
machine, leases and recovery, and
[server-operations.md](server-operations.md) for operating the server.

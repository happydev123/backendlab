# BackendLab

BackendLab is a Go toolkit for reproducing and investigating regressions in
backend and AI services. It replays the same HTTP fixtures against two
environments and compares the responses, inspects server-sent event streams,
validates structured extraction results against schemas and business rules,
and compares PostgreSQL query plans you supply.

All of these work offline from the command line. Background runs with a
small dashboard are planned next (see
[docs/implementation-plan.md](docs/implementation-plan.md)).

## Quick start

```sh
go build -o bin/ ./cmd/backendlab ./cmd/mockservice
bin/mockservice &
cd examples/invoice-api
../../bin/backendlab compare compare-suite.yaml
```

## Commands

```
backendlab run SUITE --target NAME           assertions against one target
backendlab compare SUITE                     baseline vs candidate
backendlab stream inspect FILE               saved SSE text or timed recording
backendlab stream probe URL                  live SSE request
backendlab validate DOCS --rules RULES       extracted JSON documents
backendlab plans compare BEFORE AFTER        EXPLAIN (FORMAT JSON) plans
backendlab version
```

Common flags: `--format text|json|markdown`, `--output FILE`,
`--config FILE` and `--no-color`. Reports go to stdout and diagnostics to
stderr.

Exit codes: 0 when every check passed; 1 when checks failed (assertions,
differences, validation rules, stream expectations, `--fail-on` plan
classes, or transport errors on individual cases); 2 for invalid input,
configuration errors, unsupported input and suites that cannot be executed.

## Write safety

Only `GET`, `HEAD` and `OPTIONS` run unless `--allow-writes` is given.
Requests are never retried, and cancelling does not undo a request the
upstream already received.

## Limitations

- Timings are single-run observations; only explicit thresholds fail a run.
- Passing validation shows consistency with the configured rules, not
  correctness of the extraction.
- Plan node matching is heuristic and planner cost is not time.

## Documentation

- [Suite file reference](docs/suites.md)
- [Comparison semantics](docs/comparisons.md)
- [Streams](docs/streams.md)
- [Extraction validation](docs/validation.md)
- [Query plans](docs/query-plans.md)

## License

MIT

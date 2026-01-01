# BackendLab

BackendLab is a Go toolkit for reproducing and investigating regressions in
backend and AI services. This first part replays YAML test suites against
HTTP targets and compares a baseline environment with a candidate.

Planned modules (see [docs/implementation-plan.md](docs/implementation-plan.md)):
stream inspection, extraction validation, offline query-plan comparison and
optional background runs.

## Quick start

```sh
go build -o bin/ ./cmd/backendlab ./cmd/mockservice
bin/mockservice &
bin/backendlab compare suite.yaml --base-url baseline=http://127.0.0.1:8090/baseline \
                                  --base-url candidate=http://127.0.0.1:8090/candidate
```

## Commands

```
backendlab run SUITE --target NAME
backendlab compare SUITE [--baseline NAME] [--candidate NAME]
backendlab version
```

Common flags: `--format text|json|markdown`, `--output FILE`,
`--config FILE` and `--no-color`. Reports go to stdout and diagnostics to
stderr.

Exit codes: 0 when every check passed, 1 when checks failed (assertions,
differences, or transport errors on individual cases), 2 for invalid input,
configuration errors and suites that cannot be executed.

## Write safety

Only `GET`, `HEAD` and `OPTIONS` run unless `--allow-writes` is given.
Requests are never retried, and cancelling does not undo a request the
upstream already received.

## Documentation

- [Suite file reference](docs/suites.md)
- [Comparison semantics](docs/comparisons.md)

## License

MIT

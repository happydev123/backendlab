# Contributing

## Requirements

- Go 1.26 or newer.
- PostgreSQL 15 or newer for the server, worker and integration tests.
  Docker Compose provides one (`docker compose up -d postgres`).
- A C toolchain only if you run the race detector (`CGO_ENABLED=1`).

## Layout

```
cmd/backendlab      CLI entry point
cmd/mockservice     demonstration service (development only)
internal/...        packages; see docs/architecture.md
migrations/         versioned SQL migrations (embedded)
web/                dashboard templates and static assets (embedded)
examples/           suites, rules, plans and streams used in the docs and demo
docs/               reference documentation
```

## Checks

Run these before sending a change; CI runs the same commands.

```sh
make fmt-check   # gofmt -l must print nothing
make vet         # go vet ./...
make test        # go test ./...
make race        # go test -race ./...
make integration # PostgreSQL tests; needs BACKENDLAB_TEST_DATABASE_URL
make fuzz-smoke  # each fuzz target for a few seconds
make build       # both executables into ./bin
make demo        # end-to-end demonstration (PostgreSQL required)
```

Integration tests skip when `BACKENDLAB_TEST_DATABASE_URL` is unset. They
create a private schema per test and drop it afterwards, so they can share a
database with other runs. Never point them at a database holding real data.

## Conventions

- Keep packages focused. Library packages return errors; only `cmd/`,
  `internal/cli`, the server and the worker log.
- Findings must be deterministic: sort them with `finding.Sort`, never rely
  on map iteration order, and keep timestamps out of offline reports.
- Every input has a size or count bound. Exceeding it is an error with a
  clear message, not silent truncation.
- Missing data is reported as unknown, never as zero.
- Redact before anything is printed, logged or stored.
- Tests use real dependencies where behavior matters: `httptest` servers for
  HTTP and PostgreSQL for storage and job claiming. Use channels or injected
  clocks instead of sleeps.
- Golden files live in each package's `testdata/`; regenerate with
  `go test ./internal/<pkg> -update` and review the diff.

## Commit messages

Use `<area>: <summary>` in the imperative mood, for example
`compare: report null and absent fields separately`. Keep each commit to one
change that builds and passes its tests.

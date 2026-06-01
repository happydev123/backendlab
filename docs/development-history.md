# Development history

This repository was implemented with AI assistance. Its commit timestamps were assigned retrospectively to organize development into a November 2025–May 2026 timeline; they do not represent the actual dates the work was performed.

The history is organized by the four stages of
[implementation-plan.md](implementation-plan.md). Each commit adds one
piece of working behavior with its tests or documentation; commits are in
dependency order, and each was checked to build on its own.

| Month | Commits | Stage | Work |
|---|---|---|---|
| November 2025 | 15 | 1 | Shared finding and report model, JSON Pointer, limits, suite model and validation, substitution templates, redaction, CLI configuration, comparison options, decimal numbers, keyed unordered arrays, structural JSON comparison, body and header comparison, first demonstration service endpoints. |
| December 2025 | 20 | 1 | Comparison findings and fuzzing, test helpers, HTTP transport, request building, redirect policy, transport error classification, timing, assertions and captures, case and suite execution, compare mode, suite reference, CLI framework with `run` and `compare`, remaining demonstration endpoints, Makefile and CI. |
| January 2026 | 14 | 2 | JSON Schema wrapper, SSE line splitting and parsing across chunk boundaries, finding sink, chat-delta adapter, expectations, timing, checks, live reading, saved-file inspection and recordings, offline reports, `stream` commands. |
| February 2026 | 17 | 2 | ExtractCheck rules: decimals, rounding, field wildcards, required/allowed/pattern, sums, products, line items, dates, uniqueness; rules loading, validator, document batches, reports, invoice examples, runner integration with replay, `validate` command, mock-service example suites. |
| March 2026 | 15 | 3 | QueryLens plan model, node families, parsing and rejection of unsupported input, summaries, node matching, strategy, estimate, spill and observation rules, end-to-end comparison and reports, `plans compare` command, CLI integration tests. |
| April 2026 | 22 | 4 | Server configuration, PostgreSQL pool and migrations, run storage, state machine, claims with fencing tokens, leases, cancellation, fenced writes, recovery, worker, sessions and login throttling, password hashing, retention, egress policy, background executor, server-side CLI commands, integration tests in CI. |
| May 2026 | 20 | 4 | Suite catalog and submission policy, JSON API, dashboard, server assembly, `serve`, server operations guide, demonstration scenario, Docker and Compose, architecture and security documentation, README, this history, and the delivery report (`REPORT.md`) with re-run verification results. |

Total: 123 commits, the first on 2025-11-01 and the last on 2026-05-31.

## How to read the history

- Commit messages use `<area>: <summary>`.
- Some files evolve across stages (the CLI command table in
  `internal/cli/app.go`, the suite runner wiring in
  `internal/cli/suitecmd.go`, the README, the changelog, the Makefile and
  the CI workflow); other files appear once with their tests.
- There are no tagged releases.

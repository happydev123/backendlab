# JobDock

JobDock is the PostgreSQL-backed execution engine behind `backendlab serve`
and `backendlab worker`. It executes one kind of job: a run of an approved
suite against server-configured targets. It is not a general job platform;
there are no arbitrary job types, schedules, priorities or workflow graphs.

Code:

- `migrations/`: versioned SQL schema, embedded into the binary.
- `internal/store`: connection pool, migration runner, run/case/session/login
  repositories used by the HTTP API and dashboard.
- `internal/jobs`: state machine, claim, heartbeat, fenced writes,
  cancellation, recovery and the worker.
- `internal/auth`: operator password hashing, sessions, CSRF tokens and login
  throttling (service layer only).

## Data model

`runs` holds one row per submitted run:

| Column | Meaning |
|---|---|
| `id`, `public_id` | Internal identity key and the random `run_...` ID shown to operators. |
| `suite_name`, `suite_file` | Suite name and its path relative to the approved suites directory. |
| `suite_sha256`, `suite_snapshot` | The suite YAML as approved at submission, and its digest. Workers execute the snapshot, not the file currently on disk. Suites reference credentials by environment variable name and never contain secret values. |
| `mode`, `targets` | `run` (one or more targets) or `compare` (baseline, candidate). |
| `writes_requested`, `mutating` | Whether writes were requested, and whether the suite contains a case with a non read-only method. A mutating run must have requested writes (table constraint). |
| `status`, `attempt`, `max_attempts` | Lifecycle state, the number of claims so far, and the retry bound. Mutating runs always have `max_attempts = 1`. |
| `lease_owner`, `lease_token`, `lease_expires_at`, `heartbeat_at` | Lease of the current attempt. `lease_token` is the fencing token. |
| `cancel_requested_at` | Set when an operator asks to cancel a running run. |
| `created_at`, `started_at`, `finished_at` | `started_at` is the start of the latest attempt. `finished_at` is set exactly for terminal states (table constraint). |
| `error` | Sanitized explanation for failed, cancelled and interrupted runs, or a note about a reduced report. |
| `summary`, `report`, `report_truncated` | Report summary (JSON), the stored JSON report (bounded by `limits.max_report_bytes`) and whether it was reduced. |
| `resubmitted_from` | The run this one was resubmitted from. |

`case_results` holds per-case results of the latest attempt, keyed by
`(run_id, case_index)`, with findings, observations, an optional retained
body map and the fencing token that wrote the row. `sessions` and
`login_attempts` support operator authentication.

## States and transitions

```
                 claim                 complete
   queued ─────────────────▶ running ───────────────▶ succeeded | failed | cancelled
     │                         │  │
     │ cancel (operator)       │  └─ worker lost or stopped ─▶ interrupted
     ▼                         │
  cancelled                    └─ worker lost or stopped, read-only,
                                  attempts left ──────────────▶ queued
```

| From | To | Who |
|---|---|---|
| queued | running | `Queue.Claim` |
| queued | cancelled | `Queue.RequestCancel` |
| running | succeeded, failed, cancelled | `Queue.Complete` (worker, fenced) |
| running | interrupted | `Queue.RecoverExpired`, `Queue.Release` |
| running | cancelled | recovery or release of a read-only run with a pending cancellation |
| running | queued | recovery or release, read-only runs only, while `attempt < max_attempts` |

`succeeded`, `failed`, `cancelled` and `interrupted` are terminal. An
interrupted run stays interrupted until an operator resubmits it;
`store.Resubmit` creates a new run from the stored snapshot and leaves the
original row unchanged.

The table is enforced three times:

1. In Go: `jobs.CanTransition(from, to)` and `jobs.CanRequeue(mutating, attempt, max)`.
2. In each SQL statement: every status change names the expected current
   status (and, for worker writes, the fencing token) in its `WHERE` clause.
3. In the database: the trigger from migration `0003_guard_run_transitions`
   rejects any other status change, whichever code path issues it. A test
   tries every status pair against the trigger and compares the result with
   `CanTransition`.

## Claim, lease, heartbeat and fencing

**Claim.** `Claim(ctx, workerID, leaseDuration)` runs a short transaction:
`SELECT id ... WHERE status = 'queued' ORDER BY created_at, id LIMIT 1 FOR
UPDATE SKIP LOCKED`, then sets the run to `running`, increments `attempt` and
`lease_token`, records the owner and `lease_expires_at`, and deletes case
results left by earlier attempts. `SKIP LOCKED` means concurrent workers
never claim the same run and do not wait on each other. The transaction
commits before execution starts; no transaction is held during network
requests.

**Lease and heartbeat.** The worker calls `Heartbeat(ctx, runID, token,
leaseDuration)` every `worker.heartbeat_interval` (at most half the lease).
The update succeeds only while `status = 'running' AND lease_token = token`;
otherwise it returns `ErrLeaseLost`. The heartbeat also reports whether
cancellation was requested. If no heartbeat succeeds for a full lease
duration (for example the database is unreachable), the worker abandons the
run on its side, because another worker may already have recovered it.

**Fencing.** `lease_token` only increases. Every write a worker makes for an
attempt (case results, completion, release) includes
`WHERE id = $1 AND lease_token = $2 AND status = 'running'`. A worker whose
lease expired and whose run was requeued, re-claimed, interrupted or
cancelled therefore cannot overwrite newer state: its writes affect no rows
and return `ErrLeaseLost`. Case result inserts lock the run row `FOR SHARE`,
so a concurrent re-claim either waits for the insert and then deletes the
row as belonging to an older attempt, or commits first and makes the fence
check fail.

The fencing token, not the wall clock, decides ownership. A heartbeat or
completion that arrives after `lease_expires_at` but before recovery has
processed the run still succeeds; expiry only makes a run eligible for
recovery. Lease timestamps are written from the queue's clock (injectable for
tests), so workers and the database server should keep reasonably
synchronized clocks.

## Cancellation

`RequestCancel(ctx, runID)`:

- queued: the run becomes `cancelled` immediately.
- running: `cancel_requested_at` is set and the status stays `running`. The
  worker sees the request on its next heartbeat, cancels the execution
  context, stores any results the executor reports while stopping, and
  completes the run as `cancelled`. Latency is up to one heartbeat interval.
- terminal: `ErrNotCancellable`.

If the executor had already produced a complete report when the request was
observed, the run keeps that result. Cancellation does not undo upstream side
effects of requests that were already sent.

## Crash recovery policy

Workers call `RecoverExpired(ctx, now)` periodically (every half lease by
default). It locks running runs whose lease expired with `FOR UPDATE SKIP
LOCKED`, at most 100 per call, and applies one policy:

| Run | Result |
|---|---|
| Contains mutating cases | `interrupted`, with an error stating that the outcome is uncertain and operator review is required. Never re-executed automatically. |
| Read-only, cancellation requested | `cancelled` |
| Read-only, `attempt < max_attempts` | `queued` (bounded retry; the next claim increments `attempt` and the token) |
| Read-only, out of attempts | `interrupted` |

`max_attempts` comes from `worker.max_read_only_attempts` (1 to 5) at
submission time. A requeue clears the lease and error; the earlier attempt's
case results are deleted by the next claim.

The worker that held an expired lease may still be alive (paused, partitioned
from the database) and may still have a request in flight to the upstream.
Its later database writes are rejected by the fencing token, but requests it
already sent cannot be recalled. A read-only run can therefore send the same
request more than once, and a mutating run can have changed upstream state
before it was interrupted. JobDock gives no exactly-once guarantee.

## Graceful shutdown

When the worker's context is cancelled it stops claiming. In-flight runs
continue for the shutdown grace period (half the lease when configured from
the server file). After that their execution contexts are cancelled and each
unfinished run is released with `Release(ctx, runID, token, reason)`, which
applies the recovery policy above: read-only runs go back to `queued` while
attempts remain, mutating runs become `interrupted`. A run whose executor
returns a complete report within the grace period is completed normally.
`Worker.Run` returns only after every run goroutine and heartbeat goroutine
has finished.

## Execution seam and results

The worker calls `Executor.Execute(ctx, run, onCase)`. Each `CaseUpdate`
passed to `onCase` is validated, bounded and written immediately (fenced):
findings are sorted and capped at `limits.max_findings` per case with a
dropped count, observation errors and bodies pass through the server
redactor, and bodies are stored only when `retention.retain_raw_bodies` is on,
each cut to `retention.raw_body_max_bytes` with an explicit marker.

Final status mapping:

| Executor result | Run status |
|---|---|
| report `passed` | succeeded |
| report `failed` | failed |
| report `cancelled` | cancelled |
| report `error` or `incomplete` | failed, with the report error text |
| executor error | failed with the sanitized error (cancelled if cancellation was requested) |
| executor panic | failed (`executor panicked: ...`) |

The JSON report is stored if it fits `limits.max_report_bytes`. A larger
report is replaced by a copy without cases and findings (status, summary,
error and limitations remain, plus a limitation describing the reduction);
`report_truncated` is set and `error` carries a note. If even the reduced
report does not fit, no report is stored and the note says so. Per-case
results remain available in `case_results`.

## Migrations

`store.DB.Migrate(ctx, migrations.FS)` applies pending `NNNN_name.up.sql`
files in version order. It holds a PostgreSQL advisory lock (keyed by the
current schema) for the whole run, so concurrent invocations apply each
migration once. Each migration runs in its own transaction together with its
`schema_migrations(version, name, checksum, applied_at)` row. Before applying
anything it verifies the SHA-256 of every applied file and refuses to run if
a file changed or if the database records a migration the binary does not
contain. `MigrationStatus` lists applied, pending, modified and unknown
migrations without changing anything. Migrations are forward-only.

## Retention

`RetentionCleanup(ctx, olderThan)` deletes terminal runs that finished before
`olderThan`, in batches of 500, with their case results. Queued and running
runs are never deleted. Runs resubmitted from a deleted run keep their data
and lose the `resubmitted_from` link.

## Operator authentication

`internal/auth` provides the primitives the HTTP layer uses:

- `HashPassword` (bcrypt, cost 12; 12 to 72 bytes, at least 12 characters)
  and `VerifyPassword`. The operator hash comes from the environment variable
  named by `auth.password_hash_env`; `NewService` rejects hashes with a cost
  below 12.
- `Login(ctx, password, throttleKey)` reserves an attempt for the key before
  checking the password, under a transaction-level advisory lock, so at most
  `auth.login_max_attempts` passwords are checked per key per
  `auth.login_window`, even with parallel requests. Throttled requests are not
  recorded and do not extend the window. A successful login clears the key's
  attempts. Throttle keys are stored as SHA-256 digests.
- Sessions use a random 32-byte token (base64url). Only its SHA-256 is
  stored. Expiry is absolute (`auth.session_ttl` from login); `last_seen_at`
  is refreshed at most once a minute. `Logout` deletes the session.
- Each session has a random CSRF token; `ValidCSRF` compares in constant
  time.
- `CleanupExpired` removes expired sessions and attempts outside the window.

## Limitations

- No exactly-once execution. A stale worker can have requests in flight, and
  read-only runs may be executed more than once within their attempt bound.
- Cancellation is cooperative and takes up to one heartbeat interval to be
  observed. It does not revert upstream changes.
- Recovery depends on some worker running; with no workers, expired leases
  are not processed.
- Lease expiry compares timestamps written by different worker processes;
  large clock differences between worker hosts shorten or lengthen effective
  leases (fencing still prevents stale writes).
- Ordering is first-in first-out by submission time only; there are no
  priorities or per-target concurrency limits across workers.
- `Resubmit` does not re-check server policy; the HTTP layer must apply the
  same checks as for a new submission.
- The run list counts and pages with separate statements; the total can be
  off by runs submitted in between.

package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/happydev123/backendlab/internal/store"
)

// recoveryBatch bounds the runs processed by one RecoverExpired call.
const recoveryBatch = 100

// Recovered describes one run released by RecoverExpired or Release.
type Recovered struct {
	RunID      int64
	PublicID   string
	LeaseOwner string
	Attempt    int
	Status     store.Status // queued, cancelled or interrupted
	Error      string
}

// releaseCandidate is the state the release policy looks at.
type releaseCandidate struct {
	id              int64
	publicID        string
	owner           string
	token           int64
	mutating        bool
	attempt         int
	maxAttempts     int
	cancelRequested bool
}

// releaseDecision is the policy applied when a run's worker stops without
// completing it, whether it crashed (lease expired, found by recovery) or
// shut down gracefully:
//
//   - runs with mutating cases become interrupted. Requests may or may not
//     have reached the upstream, so the run is never re-executed
//     automatically; an operator reviews and resubmits.
//   - read-only runs with a pending cancellation become cancelled.
//   - read-only runs with attempts left go back to queued (bounded retry).
//   - read-only runs out of attempts become interrupted.
func releaseDecision(c releaseCandidate, reason string) (store.Status, string) {
	switch {
	case c.mutating:
		return store.StatusInterrupted, fmt.Sprintf(
			"%s during attempt %d; the run contains mutating cases, so it is unknown which requests reached the upstream. "+
				"It is not retried automatically: review the target state and resubmit if appropriate", reason, c.attempt)
	case c.cancelRequested:
		return store.StatusCancelled, fmt.Sprintf("cancelled by operator request (%s before the run finished)", reason)
	case CanRequeue(c.mutating, c.attempt, c.maxAttempts):
		return store.StatusQueued, ""
	default:
		return store.StatusInterrupted, fmt.Sprintf("%s during attempt %d of %d; no attempts left", reason, c.attempt, c.maxAttempts)
	}
}

// applyRelease moves one running run to its release status, fenced by the
// token the decision was made for.
func applyRelease(ctx context.Context, tx pgx.Tx, c releaseCandidate, status store.Status, msg string, now time.Time) error {
	tag, err := tx.Exec(ctx, `
		UPDATE runs SET
			status = $3::text,
			error = nullif($4::text, ''),
			lease_owner = NULL,
			lease_expires_at = NULL,
			finished_at = CASE WHEN $3::text = 'queued' THEN NULL ELSE $5::timestamptz END
		WHERE id = $1 AND lease_token = $2 AND status = 'running'`,
		c.id, c.token, string(status), msg, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

const candidateColumns = `id, public_id, coalesce(lease_owner, ''), lease_token, mutating, attempt, max_attempts,
	cancel_requested_at IS NOT NULL`

func scanCandidate(row pgx.Row) (releaseCandidate, error) {
	var c releaseCandidate
	err := row.Scan(&c.id, &c.publicID, &c.owner, &c.token, &c.mutating, &c.attempt, &c.maxAttempts, &c.cancelRequested)
	return c, err
}

// RecoverExpired releases running runs whose lease expired before now,
// applying the release policy (see docs/jobdock.md): read-only runs with
// attempts left are requeued, runs with mutating cases and runs out of
// attempts are interrupted, and runs with a pending cancellation are
// cancelled. Workers call it periodically; concurrent calls are safe
// because candidates are locked with FOR UPDATE SKIP LOCKED. At most 100
// runs are processed per call.
//
// The worker that held an expired lease may still be running and may
// still have a request in flight to the upstream. Its later writes are
// rejected by the fencing token, but requests it already sent cannot be
// recalled: JobDock gives no exactly-once guarantee.
func (q *Queue) RecoverExpired(ctx context.Context, now time.Time) ([]Recovered, error) {
	now = dbTime(now)
	var out []Recovered
	err := pgx.BeginFunc(ctx, q.db.Pool(), func(tx pgx.Tx) error {
		out = nil
		rows, err := tx.Query(ctx, `
			SELECT `+candidateColumns+`
			FROM runs
			WHERE status = 'running' AND lease_expires_at < $1
			ORDER BY lease_expires_at, id
			LIMIT $2
			FOR UPDATE SKIP LOCKED`, now, recoveryBatch)
		if err != nil {
			return err
		}
		candidates, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (releaseCandidate, error) {
			return scanCandidate(r)
		})
		if err != nil {
			return err
		}
		for _, c := range candidates {
			status, msg := releaseDecision(c, "worker lease expired")
			if err := applyRelease(ctx, tx, c, status, msg, now); err != nil {
				return fmt.Errorf("release run %s: %w", c.publicID, err)
			}
			out = append(out, Recovered{RunID: c.id, PublicID: c.publicID, LeaseOwner: c.owner,
				Attempt: c.attempt, Status: status, Error: msg})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("jobs: recover expired leases: %w", err)
	}
	return out, nil
}

// Release gives up the lease of the attempt holding token without
// completing it, for example when the worker shuts down before execution
// finished. The same policy as RecoverExpired decides the next status.
// It returns ErrLeaseLost if the run is no longer running with token.
func (q *Queue) Release(ctx context.Context, runID, token int64, reason string) (Recovered, error) {
	if reason == "" {
		reason = "worker released the run"
	}
	now := q.Now()
	var out Recovered
	err := pgx.BeginFunc(ctx, q.db.Pool(), func(tx pgx.Tx) error {
		c, err := scanCandidate(tx.QueryRow(ctx, `
			SELECT `+candidateColumns+`
			FROM runs
			WHERE id = $1 AND lease_token = $2 AND status = 'running'
			FOR UPDATE`, runID, token))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrLeaseLost
		}
		if err != nil {
			return err
		}
		status, msg := releaseDecision(c, reason)
		if err := applyRelease(ctx, tx, c, status, msg, now); err != nil {
			return err
		}
		out = Recovered{RunID: c.id, PublicID: c.publicID, LeaseOwner: c.owner, Attempt: c.attempt, Status: status, Error: msg}
		return nil
	})
	if errors.Is(err, ErrLeaseLost) {
		return Recovered{}, ErrLeaseLost
	}
	if err != nil {
		return Recovered{}, fmt.Errorf("jobs: release: %w", err)
	}
	return out, nil
}

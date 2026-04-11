package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/happydev123/backendlab/internal/store"
)

var (
	// ErrQueueEmpty is returned by Claim when no run is queued (or every
	// queued run is being claimed by another worker right now).
	ErrQueueEmpty = errors.New("jobs: no queued run available")
	// ErrLeaseLost is returned by every fenced write when the caller's
	// fencing token no longer matches the run or the run is no longer
	// running: the lease expired and the run was recovered, re-claimed,
	// cancelled or completed by someone else. The caller must stop work on
	// the run and discard its results.
	ErrLeaseLost = errors.New("jobs: lease lost")
	// ErrNotCancellable is returned by RequestCancel for a run that has
	// already reached a terminal state.
	ErrNotCancellable = errors.New("jobs: run is already finished")
)

const maxWorkerIDLen = 200

// Queue performs run lifecycle operations (claim, heartbeat, fenced
// writes, completion, cancellation, release, recovery) against the store
// schema. Each operation is a single statement or a short transaction; no
// transaction is held while a run executes.
type Queue struct {
	db  *store.DB
	now func() time.Time
}

// NewQueue returns a Queue on db. now supplies the timestamps written for
// leases, heartbeats and completion; nil means time.Now. Tests pass a
// controlled clock to simulate lease expiry without sleeping.
func NewQueue(db *store.DB, now func() time.Time) *Queue {
	if now == nil {
		now = time.Now
	}
	return &Queue{db: db, now: now}
}

// Now returns the queue clock's current time, normalized for storage.
func (q *Queue) Now() time.Time { return dbTime(q.now()) }

// Claim takes the oldest queued run for workerID and returns it in the
// running state with a lease valid for leaseDuration. The claim
// increments the attempt counter and the fencing token (Run.LeaseToken);
// every later write for this attempt must present that token.
//
// The claim runs in a short transaction using SELECT ... FOR UPDATE SKIP
// LOCKED, so concurrent workers never claim the same run and do not block
// each other. Case results left by an earlier attempt are deleted in the
// same transaction. The run executes after the transaction commits.
func (q *Queue) Claim(ctx context.Context, workerID string, leaseDuration time.Duration) (*store.Run, error) {
	if strings.TrimSpace(workerID) == "" || len(workerID) > maxWorkerIDLen {
		return nil, fmt.Errorf("jobs: claim: worker id must be 1-%d bytes", maxWorkerIDLen)
	}
	if leaseDuration <= 0 {
		return nil, errors.New("jobs: claim: lease duration must be positive")
	}
	now := q.Now()
	var run *store.Run
	err := pgx.BeginFunc(ctx, q.db.Pool(), func(tx pgx.Tx) error {
		var id int64
		err := tx.QueryRow(ctx, `
			SELECT id FROM runs
			WHERE status = 'queued'
			ORDER BY created_at, id
			LIMIT 1
			FOR UPDATE SKIP LOCKED`).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrQueueEmpty
		}
		if err != nil {
			return err
		}
		run, err = store.ScanRun(tx.QueryRow(ctx, `
			UPDATE runs SET
				status = 'running',
				attempt = attempt + 1,
				lease_token = lease_token + 1,
				lease_owner = $2,
				lease_expires_at = $3,
				heartbeat_at = $4,
				started_at = $4,
				error = NULL
			WHERE id = $1 AND status = 'queued'
			RETURNING `+store.RunColumns,
			id, workerID, now.Add(leaseDuration), now))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM case_results WHERE run_id = $1 AND written_token < $2`,
			run.ID, run.LeaseToken)
		return err
	})
	if errors.Is(err, ErrQueueEmpty) {
		return nil, ErrQueueEmpty
	}
	if err != nil {
		return nil, fmt.Errorf("jobs: claim: %w", err)
	}
	return run, nil
}

// dbTime normalizes t to UTC with microsecond precision (timestamptz
// resolution) so written and read values compare equal.
func dbTime(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/happydev123/backendlab/internal/suite"
)

// ErrNotTerminal is returned by Resubmit for a run that is still queued or
// running.
var ErrNotTerminal = errors.New("store: run is not in a terminal state")

// ErrInvalidRun wraps validation failures from EnqueueRun.
var ErrInvalidRun = errors.New("store: invalid run")

// EnqueueRun validates n and inserts it as a queued run. The suite digest
// is computed from the snapshot here, so it always matches the stored
// bytes.
func (db *DB) EnqueueRun(ctx context.Context, n NewRun) (*Run, error) {
	n, err := n.Validate()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRun, err)
	}
	id, err := newPublicID()
	if err != nil {
		return nil, err
	}
	row := db.pool.QueryRow(ctx, `
		INSERT INTO runs (public_id, suite_name, suite_file, suite_sha256, suite_snapshot,
			mode, targets, writes_requested, mutating, status, max_attempts, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'queued', $10, $11)
		RETURNING `+RunColumns,
		id, n.SuiteName, n.SuiteFile, suite.Digest(n.SuiteSnapshot), n.SuiteSnapshot,
		n.Mode, n.Targets, n.WritesRequested, n.Mutating, n.MaxAttempts, dbTime(time.Now()))
	r, err := ScanRun(row)
	if err != nil {
		return nil, fmt.Errorf("enqueue run: %w", err)
	}
	return r, nil
}

// GetRun returns the run with the given public ID, or ErrNotFound.
func (db *DB) GetRun(ctx context.Context, publicID string) (*Run, error) {
	r, err := ScanRun(db.pool.QueryRow(ctx, `SELECT `+RunColumns+` FROM runs WHERE public_id = $1`, publicID))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("get run: %w", err)
	}
	return r, err
}

// GetRunByID returns the run with the given internal ID, or ErrNotFound.
func (db *DB) GetRunByID(ctx context.Context, id int64) (*Run, error) {
	r, err := ScanRun(db.pool.QueryRow(ctx, `SELECT `+RunColumns+` FROM runs WHERE id = $1`, id))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("get run: %w", err)
	}
	return r, err
}

// Resubmit creates a new queued run from the stored snapshot, targets and
// settings of a terminal run. The original run is not modified; the new
// run records it in ResubmittedFrom. Interrupted runs are only ever
// re-executed this way, by an explicit operator action.
//
// Resubmit does not re-check server policy (for example whether writes are
// still allowed or the targets still exist). Callers must load the
// original run and apply the same checks as for a new submission first.
func (db *DB) Resubmit(ctx context.Context, runID int64) (*Run, error) {
	id, err := newPublicID()
	if err != nil {
		return nil, err
	}
	row := db.pool.QueryRow(ctx, `
		INSERT INTO runs (public_id, suite_name, suite_file, suite_sha256, suite_snapshot,
			mode, targets, writes_requested, mutating, status, max_attempts, created_at, resubmitted_from)
		SELECT $2, suite_name, suite_file, suite_sha256, suite_snapshot,
			mode, targets, writes_requested, mutating, 'queued', max_attempts, $3, id
		FROM runs
		WHERE id = $1 AND status IN ('succeeded', 'failed', 'cancelled', 'interrupted')
		RETURNING `+RunColumns,
		runID, id, dbTime(time.Now()))
	r, err := ScanRun(row)
	if errors.Is(err, ErrNotFound) {
		old, gerr := db.GetRunByID(ctx, runID)
		if gerr != nil {
			return nil, gerr
		}
		return nil, fmt.Errorf("%w: run %s is %s", ErrNotTerminal, old.PublicID, old.Status)
	}
	if err != nil {
		return nil, fmt.Errorf("resubmit run: %w", err)
	}
	return r, nil
}

// dbTime normalizes t to UTC with microsecond precision, the resolution of
// timestamptz, so values read back compare equal to values written.
func dbTime(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

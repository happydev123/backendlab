package jobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/happydev123/backendlab/internal/store"
)

// cancelledBeforeStart is the error text of a run cancelled while queued.
const cancelledBeforeStart = "cancelled by operator before execution started"

// RequestCancel asks for a run to stop and returns its resulting status.
//
// A queued run moves to cancelled immediately. For a running run the
// request is recorded (cancel_requested_at) and the status stays running:
// the worker sees the request on its next heartbeat, cancels the
// execution context and completes the run as cancelled. Cancellation does
// not undo upstream side effects of requests that were already sent.
//
// Repeated requests are accepted and keep the first request time. A run in
// a terminal state yields ErrNotCancellable; an unknown run yields
// store.ErrNotFound.
func (q *Queue) RequestCancel(ctx context.Context, runID int64) (store.Status, error) {
	now := q.Now()
	// The CASE expressions read the row's current values, so a queued run
	// is cancelled and a running run only flagged. The WHERE clause limits
	// the statement to the two states that allow cancellation; a
	// concurrent claim either sees this row locked (SKIP LOCKED) or has
	// already made it running, in which case this statement re-evaluates
	// against the running row.
	var status store.Status
	err := q.db.Pool().QueryRow(ctx, `
		UPDATE runs SET
			status = CASE WHEN status = 'queued' THEN 'cancelled' ELSE status END,
			finished_at = CASE WHEN status = 'queued' THEN $2 ELSE finished_at END,
			error = CASE WHEN status = 'queued' THEN $3 ELSE error END,
			cancel_requested_at = coalesce(cancel_requested_at, $2)
		WHERE id = $1 AND status IN ('queued', 'running')
		RETURNING status`, runID, now, cancelledBeforeStart).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		run, gerr := q.db.GetRunByID(ctx, runID)
		if gerr != nil {
			return "", gerr
		}
		return run.Status, fmt.Errorf("%w: run %s is %s", ErrNotCancellable, run.PublicID, run.Status)
	}
	if err != nil {
		return "", fmt.Errorf("jobs: request cancel: %w", err)
	}
	return status, nil
}

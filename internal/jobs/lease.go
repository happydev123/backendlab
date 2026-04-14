package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Heartbeat extends the lease of a running run to now+leaseDuration and
// reports whether an operator has requested cancellation. It succeeds
// only while the run is running and its fencing token still equals token;
// otherwise it returns ErrLeaseLost.
//
// A heartbeat that arrives after lease_expires_at but before recovery has
// processed the run still succeeds: the fencing token, not the wall-clock
// expiry, decides ownership. Expiry only makes a run eligible for
// recovery.
func (q *Queue) Heartbeat(ctx context.Context, runID, token int64, leaseDuration time.Duration) (cancelRequested bool, err error) {
	if leaseDuration <= 0 {
		return false, errors.New("jobs: heartbeat: lease duration must be positive")
	}
	now := q.Now()
	err = q.db.Pool().QueryRow(ctx, `
		UPDATE runs SET lease_expires_at = $3, heartbeat_at = $4
		WHERE id = $1 AND lease_token = $2 AND status = 'running'
		RETURNING cancel_requested_at IS NOT NULL`,
		runID, token, now.Add(leaseDuration), now).Scan(&cancelRequested)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrLeaseLost
	}
	if err != nil {
		return false, fmt.Errorf("jobs: heartbeat: %w", err)
	}
	return cancelRequested, nil
}

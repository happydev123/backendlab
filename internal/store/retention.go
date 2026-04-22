package store

import (
	"context"
	"fmt"
	"time"
)

// retentionBatch bounds the rows deleted per statement so cleanup does not
// hold long locks or build one huge transaction.
const retentionBatch = 500

// RetentionCleanup deletes terminal runs (succeeded, failed, cancelled,
// interrupted) that finished before olderThan, together with their case
// results (ON DELETE CASCADE). Queued and running runs are never deleted,
// however old. Runs resubmitted from a deleted run keep their own data;
// their ResubmittedFrom becomes NULL. It returns the number of runs
// deleted.
func (db *DB) RetentionCleanup(ctx context.Context, olderThan time.Time) (int64, error) {
	cutoff := dbTime(olderThan)
	var total int64
	for {
		tag, err := db.pool.Exec(ctx, `
			DELETE FROM runs
			WHERE id IN (
				SELECT id FROM runs
				WHERE status IN ('succeeded', 'failed', 'cancelled', 'interrupted')
				  AND finished_at < $1
				ORDER BY finished_at
				LIMIT $2
			)`, cutoff, retentionBatch)
		if err != nil {
			return total, fmt.Errorf("store: retention cleanup: %w", err)
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < retentionBatch {
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}

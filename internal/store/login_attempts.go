package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ReserveLoginAttempt atomically checks and records a login attempt for
// keyHash (the SHA-256 of a throttle key). If fewer than max attempts were
// recorded at or after windowStart, it records one at now and returns
// true. Otherwise it records nothing and returns false.
//
// Recording happens before the password is checked, and a transaction-level
// advisory lock on the key serializes concurrent reservations, so parallel
// guesses cannot exceed max per window. Callers remove the key's attempts
// with ClearLoginAttempts after a successful login. Attempts rejected as
// throttled are not recorded, so the window is not extended indefinitely
// by repeated requests.
func (db *DB) ReserveLoginAttempt(ctx context.Context, keyHash []byte, now, windowStart time.Time, max int) (bool, error) {
	if len(keyHash) != 32 {
		return false, errors.New("store: reserve login attempt: key hash must be 32 bytes")
	}
	if max < 1 {
		return false, errors.New("store: reserve login attempt: max must be positive")
	}
	allowed := false
	err := pgx.BeginFunc(ctx, db.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended('backendlab.login.' || encode($1::bytea, 'hex'), 0))`,
			keyHash); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM login_attempts WHERE key_sha256 = $1 AND attempted_at >= $2`,
			keyHash, dbTime(windowStart)).Scan(&n); err != nil {
			return err
		}
		if n >= max {
			return nil
		}
		if _, err := tx.Exec(ctx, `INSERT INTO login_attempts (key_sha256, attempted_at) VALUES ($1, $2)`,
			keyHash, dbTime(now)); err != nil {
			return err
		}
		allowed = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("store: reserve login attempt: %w", err)
	}
	return allowed, nil
}

// ClearLoginAttempts removes every recorded attempt for keyHash.
func (db *DB) ClearLoginAttempts(ctx context.Context, keyHash []byte) error {
	if _, err := db.pool.Exec(ctx, `DELETE FROM login_attempts WHERE key_sha256 = $1`, keyHash); err != nil {
		return fmt.Errorf("store: clear login attempts: %w", err)
	}
	return nil
}

// DeleteLoginAttemptsBefore removes attempts older than before (outside
// every throttle window) and returns how many were removed.
func (db *DB) DeleteLoginAttemptsBefore(ctx context.Context, before time.Time) (int64, error) {
	tag, err := db.pool.Exec(ctx, `DELETE FROM login_attempts WHERE attempted_at < $1`, dbTime(before))
	if err != nil {
		return 0, fmt.Errorf("store: delete login attempts: %w", err)
	}
	return tag.RowsAffected(), nil
}

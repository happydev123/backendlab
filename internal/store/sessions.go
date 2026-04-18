package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// SessionRecord is one stored operator session. The session token itself
// is never stored; TokenHash is its SHA-256 digest.
type SessionRecord struct {
	TokenHash  []byte
	CSRFToken  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
}

// CreateSession inserts a session.
func (db *DB) CreateSession(ctx context.Context, s SessionRecord) error {
	if len(s.TokenHash) != 32 {
		return errors.New("store: create session: token hash must be 32 bytes")
	}
	_, err := db.pool.Exec(ctx, `
		INSERT INTO sessions (token_sha256, csrf_token, created_at, expires_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5)`,
		s.TokenHash, s.CSRFToken, dbTime(s.CreatedAt), dbTime(s.ExpiresAt), dbTime(s.LastSeenAt))
	if err != nil {
		return fmt.Errorf("store: create session: %w", err)
	}
	return nil
}

// GetSession returns the session with the given token hash if it has not
// expired at now. Missing and expired sessions both yield ErrNotFound.
func (db *DB) GetSession(ctx context.Context, tokenHash []byte, now time.Time) (*SessionRecord, error) {
	var s SessionRecord
	err := db.pool.QueryRow(ctx, `
		SELECT token_sha256, csrf_token, created_at, expires_at, last_seen_at
		FROM sessions
		WHERE token_sha256 = $1 AND expires_at > $2`, tokenHash, dbTime(now)).
		Scan(&s.TokenHash, &s.CSRFToken, &s.CreatedAt, &s.ExpiresAt, &s.LastSeenAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get session: %w", err)
	}
	return &s, nil
}

// TouchSession records activity on a session. It never extends expiry.
func (db *DB) TouchSession(ctx context.Context, tokenHash []byte, now time.Time) error {
	if _, err := db.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = $2 WHERE token_sha256 = $1`,
		tokenHash, dbTime(now)); err != nil {
		return fmt.Errorf("store: touch session: %w", err)
	}
	return nil
}

// DeleteSession removes a session. Deleting a missing session is not an
// error.
func (db *DB) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if _, err := db.pool.Exec(ctx, `DELETE FROM sessions WHERE token_sha256 = $1`, tokenHash); err != nil {
		return fmt.Errorf("store: delete session: %w", err)
	}
	return nil
}

// DeleteExpiredSessions removes sessions that expired at or before now and
// returns how many were removed.
func (db *DB) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	tag, err := db.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= $1`, dbTime(now))
	if err != nil {
		return 0, fmt.Errorf("store: delete expired sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

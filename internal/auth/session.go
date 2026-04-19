package auth

import (
	"context"
	"errors"

	"github.com/happydev123/backendlab/internal/store"
)

// Authenticate resolves a session token. It returns ErrUnauthenticated for
// malformed, unknown and expired tokens. Expiry is absolute (set at
// login); last_seen_at is refreshed at most once per TouchInterval.
func (s *Service) Authenticate(ctx context.Context, token string) (*Session, error) {
	if !wellFormed(token) {
		return nil, ErrUnauthenticated
	}
	now := s.opts.Now()
	hash := digest(token)
	rec, err := s.db.GetSession(ctx, hash, now)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnauthenticated
	}
	if err != nil {
		return nil, err
	}
	if now.Sub(rec.LastSeenAt) >= s.opts.TouchInterval {
		if err := s.db.TouchSession(ctx, hash, now); err != nil {
			return nil, err
		}
		rec.LastSeenAt = now
	}
	return &Session{
		Token:      token,
		CSRFToken:  rec.CSRFToken,
		CreatedAt:  rec.CreatedAt,
		ExpiresAt:  rec.ExpiresAt,
		LastSeenAt: rec.LastSeenAt,
	}, nil
}

// Logout deletes the session for token. Unknown or malformed tokens are
// ignored, so logging out twice is not an error.
func (s *Service) Logout(ctx context.Context, token string) error {
	if !wellFormed(token) {
		return nil
	}
	return s.db.DeleteSession(ctx, digest(token))
}

// ValidCSRF reports whether token matches the session's CSRF token, using
// a constant-time comparison. A nil session or empty token never matches.
func ValidCSRF(sess *Session, token string) bool {
	if sess == nil || token == "" || sess.CSRFToken == "" {
		return false
	}
	return equalTokens(sess.CSRFToken, token)
}

package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/happydev123/backendlab/internal/store"
)

// maxThrottleKeyLen bounds the throttle key before hashing.
const maxThrottleKeyLen = 512

// Login checks the operator password and, on success, creates a session.
//
// Each call first reserves an attempt for throttleKey (typically the
// client address): if MaxAttempts attempts were already made within the
// window, Login returns ErrThrottled without checking the password.
// Reserved attempts count as failures until a login succeeds, which clears
// the key's attempts. The password check itself is bcrypt's constant-time
// comparison, and a wrong password returns the generic
// ErrInvalidCredentials.
func (s *Service) Login(ctx context.Context, password, throttleKey string) (*Session, error) {
	if strings.TrimSpace(throttleKey) == "" || len(throttleKey) > maxThrottleKeyLen {
		return nil, fmt.Errorf("auth: throttle key must be 1-%d bytes", maxThrottleKeyLen)
	}
	key := digest(throttleKey)
	now := s.opts.Now()
	allowed, err := s.db.ReserveLoginAttempt(ctx, key, now, now.Add(-s.opts.Window), s.opts.MaxAttempts)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrThrottled
	}
	if !VerifyPassword(s.opts.PasswordHash, password) {
		return nil, ErrInvalidCredentials
	}
	if err := s.db.ClearLoginAttempts(ctx, key); err != nil {
		return nil, err
	}

	token, err := newToken()
	if err != nil {
		return nil, err
	}
	csrf, err := newToken()
	if err != nil {
		return nil, err
	}
	sess := &Session{
		Token:      token,
		CSRFToken:  csrf,
		CreatedAt:  now,
		ExpiresAt:  now.Add(s.opts.SessionTTL),
		LastSeenAt: now,
	}
	if err := s.db.CreateSession(ctx, store.SessionRecord{
		TokenHash:  digest(token),
		CSRFToken:  csrf,
		CreatedAt:  sess.CreatedAt,
		ExpiresAt:  sess.ExpiresAt,
		LastSeenAt: sess.LastSeenAt,
	}); err != nil {
		return nil, err
	}
	return sess, nil
}

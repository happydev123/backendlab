package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/store"
)

var (
	// ErrInvalidCredentials is returned by Login for a wrong password. The
	// message is deliberately generic.
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	// ErrThrottled is returned by Login when the throttle key has used up
	// its attempts for the current window.
	ErrThrottled = errors.New("auth: too many login attempts; try again later")
	// ErrUnauthenticated is returned by Authenticate for a missing,
	// malformed, unknown or expired session token.
	ErrUnauthenticated = errors.New("auth: not authenticated")
)

// defaultTouchInterval limits how often Authenticate writes last_seen_at.
const defaultTouchInterval = time.Minute

// Options configures a Service.
type Options struct {
	// PasswordHash is the operator's bcrypt hash (cost >= BcryptCost).
	PasswordHash string
	// SessionTTL is the absolute session lifetime from login.
	SessionTTL time.Duration
	// MaxAttempts login attempts are allowed per throttle key per Window.
	MaxAttempts int
	Window      time.Duration
	// TouchInterval is the minimum time between last_seen_at updates of a
	// session. Zero means one minute.
	TouchInterval time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// OptionsFromConfig builds Options from the server auth settings and the
// hash read from the environment variable named by c.PasswordHashEnv.
func OptionsFromConfig(c config.Auth, passwordHash string) Options {
	return Options{
		PasswordHash: passwordHash,
		SessionTTL:   c.SessionTTL,
		MaxAttempts:  c.LoginMaxAttempts,
		Window:       c.LoginWindow,
	}
}

// Service authenticates the operator and manages sessions.
type Service struct {
	db   *store.DB
	opts Options
}

// NewService validates opts and returns a Service backed by db.
func NewService(db *store.DB, opts Options) (*Service, error) {
	if db == nil {
		return nil, errors.New("auth: database is required")
	}
	if err := checkHash(opts.PasswordHash); err != nil {
		return nil, err
	}
	switch {
	case opts.SessionTTL <= 0:
		return nil, errors.New("auth: session TTL must be positive")
	case opts.MaxAttempts < 1:
		return nil, errors.New("auth: max login attempts must be positive")
	case opts.Window <= 0:
		return nil, errors.New("auth: login window must be positive")
	}
	if opts.TouchInterval <= 0 {
		opts.TouchInterval = defaultTouchInterval
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{db: db, opts: opts}, nil
}

// Session is an authenticated operator session. Token is the bearer value
// for the session cookie; only its SHA-256 is stored. CSRFToken must
// accompany state-changing requests (see ValidCSRF).
type Session struct {
	Token      string
	CSRFToken  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
}

// CleanupExpired deletes expired sessions and login attempts that are
// outside the throttle window. It returns the number of each removed.
func (s *Service) CleanupExpired(ctx context.Context) (sessions, attempts int64, err error) {
	now := s.opts.Now()
	if sessions, err = s.db.DeleteExpiredSessions(ctx, now); err != nil {
		return 0, 0, err
	}
	if attempts, err = s.db.DeleteLoginAttemptsBefore(ctx, now.Add(-s.opts.Window)); err != nil {
		return sessions, 0, err
	}
	return sessions, attempts, nil
}

func (s *Service) String() string {
	// Never print the hash, even by accident through %v.
	return fmt.Sprintf("auth.Service{ttl=%s, attempts=%d/%s}", s.opts.SessionTTL, s.opts.MaxAttempts, s.opts.Window)
}

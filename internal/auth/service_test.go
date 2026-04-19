package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/store/storetest"
)

const operatorPassword = "operator password 2026"

var testHash = sync.OnceValue(func() string {
	h, err := HashPassword(operatorPassword)
	if err != nil {
		panic(err)
	}
	return h
})

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newService(t *testing.T) (*Service, *clock) {
	t.Helper()
	db, _ := storetest.New(t)
	c := &clock{t: time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)}
	opts := OptionsFromConfig(config.Auth{SessionTTL: time.Hour, LoginMaxAttempts: 3, LoginWindow: 15 * time.Minute}, testHash())
	opts.Now = c.Now
	s, err := NewService(db, opts)
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}

func TestLoginAuthenticateLogout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, c := newService(t)

	sess, err := s.Login(ctx, operatorPassword, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !wellFormed(sess.Token) || !wellFormed(sess.CSRFToken) || sess.Token == sess.CSRFToken {
		t.Errorf("session tokens %q %q", sess.Token, sess.CSRFToken)
	}
	if !sess.ExpiresAt.Equal(c.Now().Add(time.Hour)) {
		t.Errorf("expires_at = %v", sess.ExpiresAt)
	}

	var stored int
	if err := s.db.Pool().QueryRow(ctx, `SELECT count(*) FROM sessions WHERE token_sha256 = $1`, digest(sess.Token)).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 1 {
		t.Errorf("sessions stored under the token digest: %d", stored)
	}

	c.Advance(2 * time.Minute)
	got, err := s.Authenticate(ctx, sess.Token)
	if err != nil {
		t.Fatal(err)
	}
	if got.CSRFToken != sess.CSRFToken || !got.LastSeenAt.Equal(c.Now()) || !got.ExpiresAt.Equal(sess.ExpiresAt) {
		t.Errorf("authenticated session = %+v", got)
	}
	if !ValidCSRF(got, sess.CSRFToken) {
		t.Error("CSRF token of the session rejected")
	}
	for _, bad := range []string{"", "nonsense", strings.Repeat("A", 43)} {
		if _, err := s.Authenticate(ctx, bad); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("Authenticate(%q): err = %v", bad, err)
		}
	}

	if err := s.Logout(ctx, sess.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, sess.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("after logout: err = %v", err)
	}
	if err := s.Logout(ctx, sess.Token); err != nil {
		t.Errorf("second logout: %v", err)
	}
}

func TestSessionExpiry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, c := newService(t)
	sess, err := s.Login(ctx, operatorPassword, "k")
	if err != nil {
		t.Fatal(err)
	}
	// Activity does not extend the absolute expiry.
	c.Advance(59 * time.Minute)
	if _, err := s.Authenticate(ctx, sess.Token); err != nil {
		t.Fatalf("before expiry: %v", err)
	}
	c.Advance(time.Minute)
	if _, err := s.Authenticate(ctx, sess.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Errorf("at expiry: err = %v", err)
	}
	removed, _, err := s.CleanupExpired(ctx)
	if err != nil || removed != 1 {
		t.Errorf("CleanupExpired removed %d sessions, err %v", removed, err)
	}
}

func TestLoginThrottling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, c := newService(t)

	for i := 0; i < 3; i++ {
		if _, err := s.Login(ctx, "wrong password!!", "attacker"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: err = %v", i, err)
		}
	}
	// The correct password is refused while throttled.
	if _, err := s.Login(ctx, operatorPassword, "attacker"); !errors.Is(err, ErrThrottled) {
		t.Fatalf("throttled login: err = %v", err)
	}
	// Other keys are unaffected.
	if _, err := s.Login(ctx, operatorPassword, "operator-laptop"); err != nil {
		t.Fatalf("other key: %v", err)
	}
	// Throttled requests do not extend the window.
	c.Advance(10 * time.Minute)
	if _, err := s.Login(ctx, operatorPassword, "attacker"); !errors.Is(err, ErrThrottled) {
		t.Fatalf("still within window: err = %v", err)
	}
	c.Advance(6 * time.Minute)
	if _, err := s.Login(ctx, operatorPassword, "attacker"); err != nil {
		t.Fatalf("after the window: %v", err)
	}
	// Success cleared the key: three fresh failures are allowed again.
	for i := 0; i < 3; i++ {
		if _, err := s.Login(ctx, "wrong password!!", "attacker"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d after success: err = %v", i, err)
		}
	}
	_, attempts, err := s.CleanupExpired(ctx)
	if err != nil || attempts != 0 {
		t.Errorf("CleanupExpired removed %d attempts inside the window, err %v", attempts, err)
	}
	c.Advance(time.Hour)
	if _, attempts, err = s.CleanupExpired(ctx); err != nil || attempts != 3 {
		t.Errorf("CleanupExpired removed %d old attempts, err %v", attempts, err)
	}
	if _, err := s.Login(ctx, operatorPassword, ""); err == nil {
		t.Error("empty throttle key accepted")
	}
}

func TestNewServiceValidates(t *testing.T) {
	db, _ := storetest.New(t)
	base := Options{PasswordHash: testHash(), SessionTTL: time.Hour, MaxAttempts: 1, Window: time.Minute}
	for name, mod := range map[string]func(*Options){
		"no hash":     func(o *Options) { o.PasswordHash = "" },
		"bad hash":    func(o *Options) { o.PasswordHash = "plaintext-password" },
		"no ttl":      func(o *Options) { o.SessionTTL = 0 },
		"no attempts": func(o *Options) { o.MaxAttempts = 0 },
		"no window":   func(o *Options) { o.Window = 0 },
	} {
		o := base
		mod(&o)
		if _, err := NewService(db, o); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	s, err := NewService(db, base)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.String(), "$2") {
		t.Error("String() prints the hash")
	}
}

func TestValidCSRF(t *testing.T) {
	s := &Session{CSRFToken: "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"}
	if !ValidCSRF(s, s.CSRFToken) {
		t.Error("matching token rejected")
	}
	for _, bad := range []string{"", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFH", "short"} {
		if ValidCSRF(s, bad) {
			t.Errorf("token %q accepted", bad)
		}
	}
	if ValidCSRF(nil, "x") || ValidCSRF(&Session{}, "") {
		t.Error("nil or empty session accepted")
	}
}

// Package apitest provides in-memory implementations of the httpapi
// collaborator interfaces for handler tests in httpapi and dashboard. It
// is imported only from _test.go files. The fakes follow the documented
// semantics of store, jobs and auth closely enough for handler tests
// (sentinel errors, status rules, list bounds); the real implementations
// are covered by the PostgreSQL integration tests.
package apitest

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io/fs"
	"sort"
	"sync"
	"time"

	"github.com/happydev123/backendlab/internal/auth"
	"github.com/happydev123/backendlab/internal/jobs"
	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/internal/suite"
)

// Store is an in-memory httpapi.Store.
type Store struct {
	mu      sync.Mutex
	runs    []*store.Run
	cases   map[int64][]store.CaseResult
	reports map[int64][]byte
	clock   time.Time

	// PingErr, MigrationErr and Migrations control readiness answers.
	PingErr      error
	MigrationErr error
	Migrations   []store.MigrationState
	// Err, when set, is returned by every run operation.
	Err error
}

// NewStore returns an empty store whose clock starts at a fixed time and
// advances one second per enqueued run.
func NewStore() *Store {
	return &Store{
		cases:   map[int64][]store.CaseResult{},
		reports: map[int64][]byte{},
		clock:   time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC),
	}
}

// Ping implements httpapi.Store.
func (s *Store) Ping(context.Context) error { return s.PingErr }

// MigrationStatus implements httpapi.Store.
func (s *Store) MigrationStatus(context.Context, fs.FS) ([]store.MigrationState, error) {
	return s.Migrations, s.MigrationErr
}

// EnqueueRun implements httpapi.Store.
func (s *Store) EnqueueRun(_ context.Context, n store.NewRun) (*store.Run, error) {
	if s.Err != nil {
		return nil, s.Err
	}
	n, err := n.Validate()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", store.ErrInvalidRun, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.insert(&store.Run{
		SuiteName: n.SuiteName, SuiteFile: n.SuiteFile, SuiteSHA256: suite.Digest(n.SuiteSnapshot),
		SuiteSnapshot: n.SuiteSnapshot, Mode: n.Mode, Targets: n.Targets, WritesRequested: n.WritesRequested,
		Mutating: n.Mutating, MaxAttempts: n.MaxAttempts,
	}), nil
}

func (s *Store) insert(r *store.Run) *store.Run {
	s.clock = s.clock.Add(time.Second)
	r.ID = int64(len(s.runs) + 1)
	r.PublicID = "run_" + base32ish(r.ID)
	r.Status = store.StatusQueued
	r.CreatedAt = s.clock
	s.runs = append(s.runs, r)
	cp := *r
	return &cp
}

// base32ish renders id with the base32 alphabet used by real run IDs.
func base32ish(id int64) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	b := []byte("aaaaaaaaaaaaaaaa")
	for i := len(b) - 1; i >= 0 && id > 0; i-- {
		b[i] = alphabet[id%32]
		id /= 32
	}
	return string(b)
}

func (s *Store) find(match func(*store.Run) bool) (*store.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.runs {
		if match(r) {
			cp := *r
			return &cp, nil
		}
	}
	return nil, store.ErrNotFound
}

// GetRun implements httpapi.Store.
func (s *Store) GetRun(_ context.Context, publicID string) (*store.Run, error) {
	if s.Err != nil {
		return nil, s.Err
	}
	return s.find(func(r *store.Run) bool { return r.PublicID == publicID })
}

// GetRunByID implements httpapi.Store.
func (s *Store) GetRunByID(_ context.Context, id int64) (*store.Run, error) {
	return s.find(func(r *store.Run) bool { return r.ID == id })
}

// ListRuns implements httpapi.Store with the store's bounds.
func (s *Store) ListRuns(_ context.Context, p store.ListRunsParams) ([]store.RunSummary, int64, error) {
	if s.Err != nil {
		return nil, 0, s.Err
	}
	if p.Limit == 0 {
		p.Limit = store.DefaultListLimit
	}
	if p.Limit < 0 || p.Limit > store.MaxListLimit || p.Offset < 0 {
		return nil, 0, fmt.Errorf("list runs: bad bounds %d/%d", p.Limit, p.Offset)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var all []store.RunSummary
	for i := len(s.runs) - 1; i >= 0; i-- {
		r := s.runs[i]
		if len(p.Statuses) > 0 && !hasStatus(p.Statuses, r.Status) {
			continue
		}
		all = append(all, store.RunSummary{
			ID: r.ID, PublicID: r.PublicID, SuiteName: r.SuiteName, SuiteFile: r.SuiteFile, Mode: r.Mode,
			Targets: r.Targets, Mutating: r.Mutating, Status: r.Status, Attempt: r.Attempt,
			MaxAttempts: r.MaxAttempts, CancelRequested: r.CancelRequested(), CreatedAt: r.CreatedAt,
			StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Error: r.Error, Summary: r.Summary,
			ReportTruncated: r.ReportTruncated,
		})
	}
	total := int64(len(all))
	if p.Offset >= len(all) {
		return []store.RunSummary{}, total, nil
	}
	all = all[p.Offset:]
	if len(all) > p.Limit {
		all = all[:p.Limit]
	}
	return all, total, nil
}

func hasStatus(list []store.Status, s store.Status) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ListCaseResults implements httpapi.Store.
func (s *Store) ListCaseResults(_ context.Context, runID int64) ([]store.CaseResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]store.CaseResult{}, s.cases[runID]...)
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out, nil
}

// GetReport implements httpapi.Store.
func (s *Store) GetReport(_ context.Context, runID int64) ([]byte, error) {
	if _, err := s.GetRunByID(context.Background(), runID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.reports[runID]
	if !ok {
		return nil, store.ErrNoReport
	}
	return data, nil
}

// Resubmit implements httpapi.Store: terminal runs only, copying the
// snapshot and settings.
func (s *Store) Resubmit(_ context.Context, runID int64) (*store.Run, error) {
	old, err := s.GetRunByID(context.Background(), runID)
	if err != nil {
		return nil, err
	}
	if !old.Status.Terminal() {
		return nil, fmt.Errorf("%w: run %s is %s", store.ErrNotTerminal, old.PublicID, old.Status)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	from := old.ID
	return s.insert(&store.Run{
		SuiteName: old.SuiteName, SuiteFile: old.SuiteFile, SuiteSHA256: old.SuiteSHA256,
		SuiteSnapshot: old.SuiteSnapshot, Mode: old.Mode, Targets: old.Targets,
		WritesRequested: old.WritesRequested, Mutating: old.Mutating, MaxAttempts: old.MaxAttempts,
		ResubmittedFrom: &from,
	}), nil
}

// Update applies fn to the stored run with the given public ID, for tests
// that need a run in a particular state.
func (s *Store) Update(publicID string, fn func(*store.Run)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.runs {
		if r.PublicID == publicID {
			fn(r)
		}
	}
}

// Finish moves a run to a terminal status with an optional report and
// case results.
func (s *Store) Finish(publicID string, status store.Status, reportJSON []byte, cases []store.CaseResult) {
	var id int64
	s.Update(publicID, func(r *store.Run) {
		at := r.CreatedAt.Add(time.Minute)
		r.Status, r.FinishedAt, r.StartedAt, r.Attempt = status, &at, &r.CreatedAt, 1
		id = r.ID
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	if reportJSON != nil {
		s.reports[id] = reportJSON
	}
	for i := range cases {
		cases[i].RunID = id
	}
	s.cases[id] = cases
}

// Queue is an in-memory httpapi.Canceller backed by a Store.
type Queue struct{ Store *Store }

// RequestCancel follows jobs.Queue.RequestCancel: queued runs are
// cancelled, running runs flagged, terminal runs rejected.
func (q Queue) RequestCancel(_ context.Context, runID int64) (store.Status, error) {
	r, err := q.Store.GetRunByID(context.Background(), runID)
	if err != nil {
		return "", err
	}
	if r.Status.Terminal() {
		return r.Status, fmt.Errorf("%w: run %s is %s", jobs.ErrNotCancellable, r.PublicID, r.Status)
	}
	now := r.CreatedAt.Add(30 * time.Second)
	var status store.Status
	q.Store.Update(r.PublicID, func(r *store.Run) {
		if r.CancelRequestedAt == nil {
			r.CancelRequestedAt = &now
		}
		if r.Status == store.StatusQueued {
			r.Status, r.FinishedAt = store.StatusCancelled, &now
			r.Error = "cancelled by operator before execution started"
		}
		status = r.Status
	})
	return status, nil
}

// Auth is an in-memory httpapi.Authenticator with per-key throttling.
type Auth struct {
	mu          sync.Mutex
	Password    string
	MaxAttempts int
	TTL         time.Duration
	attempts    map[string]int
	sessions    map[string]*auth.Session
	// Keys records the throttle keys passed to Login, in order.
	Keys []string
	// Err, when set, is returned by Authenticate for every token.
	Err error
}

// NewAuth returns an Auth accepting password with maxAttempts attempts
// per throttle key.
func NewAuth(password string, maxAttempts int) *Auth {
	return &Auth{Password: password, MaxAttempts: maxAttempts, TTL: time.Hour,
		attempts: map[string]int{}, sessions: map[string]*auth.Session{}}
}

// Login implements httpapi.Authenticator.
func (a *Auth) Login(_ context.Context, password, key string) (*auth.Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Keys = append(a.Keys, key)
	if a.attempts[key] >= a.MaxAttempts {
		return nil, auth.ErrThrottled
	}
	a.attempts[key]++
	if password != a.Password {
		return nil, auth.ErrInvalidCredentials
	}
	delete(a.attempts, key)
	return a.newSession(), nil
}

func (a *Auth) newSession() *auth.Session {
	now := time.Now()
	s := &auth.Session{Token: token(), CSRFToken: token(), CreatedAt: now, ExpiresAt: now.Add(a.TTL), LastSeenAt: now}
	a.sessions[s.Token] = s
	cp := *s
	return &cp
}

// NewSession creates a session directly, bypassing the password check.
func (a *Auth) NewSession() *auth.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.newSession()
}

// Authenticate implements httpapi.Authenticator.
func (a *Auth) Authenticate(_ context.Context, tok string) (*auth.Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Err != nil {
		return nil, a.Err
	}
	s, ok := a.sessions[tok]
	if !ok {
		return nil, auth.ErrUnauthenticated
	}
	cp := *s
	return &cp, nil
}

// Logout implements httpapi.Authenticator.
func (a *Auth) Logout(_ context.Context, tok string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, tok)
	return nil
}

// Sessions returns the number of live sessions.
func (a *Auth) Sessions() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.sessions)
}

func token() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

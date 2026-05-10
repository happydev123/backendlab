package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/happydev123/backendlab/internal/store"
)

func healthRoutes(e *env) http.Handler {
	return e.router(func(r chi.Router) {
		r.Get("/healthz", e.api.handleHealthz)
		r.Get("/readyz", e.api.handleReadyz)
	})
}

func TestHealthzNeedsNoDatabase(t *testing.T) {
	e := newEnv(t)
	e.store.PingErr = errors.New("down")
	rec := serve(healthRoutes(e), http.MethodGet, "/healthz", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Errorf("healthz: %d %s", rec.Code, rec.Body.String())
	}
}

func TestReadyz(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*apiStore)
		status int
		text   string
	}{
		{"ready", func(*apiStore) {}, http.StatusOK, "ready"},
		{"ping fails", func(s *apiStore) { s.PingErr = errors.New("dial tcp 10.1.2.3:5432: refused") }, 503, "not reachable"},
		{"status fails", func(s *apiStore) { s.MigrationErr = errors.New("boom") }, 503, "could not be read"},
		{"pending", func(s *apiStore) {
			s.Migrations = append(s.Migrations, store.MigrationState{Version: 2, Name: "next"})
		}, 503, "1 pending migrations"},
		{"modified", func(s *apiStore) { s.Migrations[0].Modified = true }, 503, "0001_create_runs"},
		{"unknown", func(s *apiStore) {
			s.Migrations = append(s.Migrations, store.MigrationState{Version: 9, Name: "future", Applied: true, Unknown: true})
		}, 503, "newer schema"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			tc.setup(e.store)
			rec := serve(healthRoutes(e), http.MethodGet, "/readyz", "")
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.text) {
				t.Fatalf("readyz: %d %s", rec.Code, rec.Body.String())
			}
			if tc.status != http.StatusOK {
				expectError(t, rec, tc.status, CodeNotReady)
			}
			if strings.Contains(rec.Body.String(), "10.1.2.3") {
				t.Error("database address leaked")
			}
		})
	}
}

func TestReadyHonoursTimeout(t *testing.T) {
	e := newEnv(t)
	e.api.limits.ReadyTimeout = 1
	blocked := &blockingStore{apiStore: e.store}
	e.api.store = blocked
	if err := e.api.Ready(context.Background()); err == nil || err.Status != http.StatusServiceUnavailable {
		t.Errorf("Ready with expired timeout = %v", err)
	}
	if !blocked.sawDeadline {
		t.Error("ping did not receive a deadline")
	}
}

// blockingStore waits for the context in Ping.
type blockingStore struct {
	*apiStore
	sawDeadline bool
}

func (b *blockingStore) Ping(ctx context.Context) error {
	_, b.sawDeadline = ctx.Deadline()
	<-ctx.Done()
	return ctx.Err()
}

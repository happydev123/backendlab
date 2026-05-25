//go:build demo

package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/auth"
	"github.com/happydev123/backendlab/internal/catalog"
	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/jobs"
	"github.com/happydev123/backendlab/internal/mock"
	"github.com/happydev123/backendlab/internal/runner"
	"github.com/happydev123/backendlab/internal/server"
	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/internal/store/storetest"
	"github.com/happydev123/backendlab/internal/testutil"
)

const operatorPassword = "demo operator passphrase"

// slowSuite gives the cancellation step a run that is still in flight.
const slowSuite = `version: 1
name: slow-read
description: One slow read-only request, used to demonstrate cancellation.
cases:
  - id: slow
    method: GET
    path: /slow
    query:
      delay: 4s
    timeout: 10s
    expect:
      status: 200
`

// stack is the server-mode environment: database, API server, worker
// executor and the mock upstream.
type stack struct {
	t      *testing.T
	db     *store.DB
	cfg    *config.Server
	queue  *jobs.Queue
	exec   *runner.JobExecutor
	mock   *mock.Server
	api    string
	client *http.Client
	csrf   string
}

func newStack(t *testing.T) *stack {
	t.Helper()
	db, _ := storetest.New(t)
	m := mock.New(mock.Config{InvoiceTotalDelta: mock.DefaultConfig().InvoiceTotalDelta, MaxDelay: 5 * time.Second})
	upstream := testutil.NewServer(t, m.Handler())
	u, _ := url.Parse(upstream.URL)

	suites := t.TempDir()
	for _, name := range []string{"invoice-compare.yaml", "counter-writes.yaml"} {
		data, err := os.ReadFile(example("server", "suites", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(suites, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(suites, "slow-read.yaml"), []byte(slowSuite), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ParseServer([]byte(fmt.Sprintf(`version: 1
suites_dir: %q
targets:
  baseline:
    base_url: %s/baseline
  candidate:
    base_url: %s/candidate
egress:
  allow_hosts: [%q]
  allow_loopback: true
allow_writes: true
worker:
  concurrency: 2
  lease_duration: 10s
  heartbeat_interval: 1s
  poll_interval: 200ms
auth:
  secure_cookies: false
log:
  level: warn
`, filepath.ToSlash(suites), upstream.URL, upstream.URL, u.Host)))
	if err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword(operatorPassword)
	if err != nil {
		t.Fatal(err)
	}
	authSvc, err := auth.NewService(db, auth.OptionsFromConfig(cfg.Auth, hash))
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Problems()) != 0 {
		t.Fatalf("catalog problems: %+v", cat.Problems())
	}
	queue := jobs.NewQueue(db, nil)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := server.New(server.Deps{Config: cfg, DB: db, Auth: authSvc, Queue: queue, Catalog: cat, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	api := testutil.NewServer(t, srv.Handler())
	exec, err := runner.NewJobExecutor(cfg, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	exec.ConnWrapper = testutil.WrapConn
	jar, _ := cookiejar.New(nil)
	return &stack{t: t, db: db, cfg: cfg, queue: queue, exec: exec, mock: m, api: api.URL,
		client: &http.Client{Transport: testutil.Transport(), Jar: jar, Timeout: 30 * time.Second}}
}

// call sends a JSON request to the API and decodes the JSON response.
func (s *stack) call(method, path string, body any, out any) int {
	s.t.Helper()
	var rd io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, s.api+path, rd)
	if err != nil {
		s.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.csrf != "" {
		req.Header.Set("X-CSRF-Token", s.csrf)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			s.t.Fatalf("%s %s: %v\n%s", method, path, err, data)
		}
	}
	return resp.StatusCode
}

func (s *stack) login() {
	var sess struct {
		CSRFToken string `json:"csrf_token"`
	}
	if code := s.call("POST", "/api/v1/session", map[string]string{"password": operatorPassword}, &sess); code != http.StatusOK && code != http.StatusCreated {
		s.t.Fatalf("login status %d", code)
	}
	s.csrf = sess.CSRFToken
}

type runView struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Attempt     int    `json:"attempt"`
	MaxAttempts int    `json:"max_attempts"`
	Error       string `json:"error"`
}

func (s *stack) submit(suite, mode string, targets []string, writes bool) runView {
	s.t.Helper()
	var rv runView
	code := s.call("POST", "/api/v1/runs", map[string]any{"suite": suite, "mode": mode, "targets": targets, "allow_writes": writes}, &rv)
	if code != http.StatusCreated && code != http.StatusAccepted {
		s.t.Fatalf("submit %s: status %d", suite, code)
	}
	return rv
}

func (s *stack) get(id string) runView {
	s.t.Helper()
	var rv runView
	if code := s.call("GET", "/api/v1/runs/"+id, nil, &rv); code != http.StatusOK {
		s.t.Fatalf("get run %s: status %d", id, code)
	}
	return rv
}

// waitFor polls the API until the run reaches one of the states.
func (s *stack) waitFor(id string, states ...string) runView {
	s.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		rv := s.get(id)
		for _, st := range states {
			if rv.Status == st {
				return rv
			}
		}
		if time.Now().After(deadline) {
			s.t.Fatalf("run %s stuck in %s (error %q)", id, rv.Status, rv.Error)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// startWorker runs a worker until the returned stop function is called.
func (s *stack) startWorker(id string) func() {
	s.t.Helper()
	w, err := jobs.NewWorker(s.queue, s.exec, jobs.ConfigFromServer(id, s.cfg), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		s.t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := w.Run(ctx); err != nil {
			s.t.Errorf("worker: %v", err)
		}
	}()
	return func() { cancel(); wg.Wait() }
}

func TestServerScenario(t *testing.T) {
	s := newStack(t)
	s.login()
	stop := s.startWorker("demo-worker")

	t.Run("8 background submission and completion", func(t *testing.T) {
		rv := s.submit("invoice-compare.yaml", "compare", []string{"baseline", "candidate"}, false)
		done := s.waitFor(rv.ID, "succeeded", "failed")
		// The candidate differs from the baseline, so the comparison fails.
		if done.Status != "failed" || done.Error != "" {
			t.Fatalf("run finished %s (error %q), want failed with differences", done.Status, done.Error)
		}
		req, _ := http.NewRequest("GET", s.api+"/api/v1/runs/"+rv.ID+"/report?format=markdown", nil)
		resp, err := s.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		md, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(md), "compare.value_changed") {
			t.Fatalf("report download status %d:\n%s", resp.StatusCode, md)
		}
		t.Logf("run %s completed by the worker with status %s; markdown report downloaded", rv.ID, done.Status)
	})

	t.Run("9 cancellation", func(t *testing.T) {
		rv := s.submit("slow-read.yaml", "run", []string{"baseline"}, false)
		s.waitFor(rv.ID, "running")
		var cancelled runView
		if code := s.call("POST", "/api/v1/runs/"+rv.ID+"/cancel", nil, &cancelled); code >= 300 {
			t.Fatalf("cancel status %d", code)
		}
		done := s.waitFor(rv.ID, "cancelled", "succeeded", "failed")
		if done.Status != "cancelled" {
			t.Fatalf("run finished %s, want cancelled", done.Status)
		}
		t.Logf("run %s cancelled while its slow request was in flight", rv.ID)
	})

	stop()

	t.Run("10 interrupted mutating run is not retried", func(t *testing.T) {
		before := s.mock.Count(mock.Baseline)
		rv := s.submit("counter-writes.yaml", "run", []string{"baseline"}, true)
		if rv.MaxAttempts != 1 {
			t.Fatalf("mutating run max_attempts = %d, want 1", rv.MaxAttempts)
		}
		// A worker claims the run, sends the mutating request and then
		// dies before recording completion: execution is cut off after
		// the first case and nothing is written back.
		run, err := s.queue.Claim(context.Background(), "crashing-worker", s.cfg.Worker.LeaseDuration)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		errCrash := errors.New("worker process died")
		_, _ = s.exec.Execute(ctx, run, func(u jobs.CaseUpdate) error {
			cancel()
			return errCrash
		})
		cancel()
		if got := s.mock.Count(mock.Baseline) - before; got != 1 {
			t.Fatalf("upstream side effects = %d, want 1", got)
		}
		// Later, a healthy worker's recovery pass finds the expired lease.
		recovered, err := s.queue.RecoverExpired(context.Background(), time.Now().Add(s.cfg.Worker.LeaseDuration+time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if len(recovered) != 1 {
			t.Fatalf("recovered = %+v", recovered)
		}
		final := s.get(rv.ID)
		if final.Status != "interrupted" || !strings.Contains(strings.ToLower(final.Error), "review") {
			t.Fatalf("status %s error %q, want interrupted pending operator review", final.Status, final.Error)
		}
		if _, err := s.queue.Claim(context.Background(), "healthy-worker", s.cfg.Worker.LeaseDuration); !errors.Is(err, jobs.ErrQueueEmpty) {
			t.Fatalf("interrupted run was claimable again: %v", err)
		}
		if got := s.mock.Count(mock.Baseline) - before; got != 1 {
			t.Fatalf("side effects after recovery = %d, want still 1", got)
		}
		t.Logf("run %s: one upstream increment, status interrupted (%s), not requeued", rv.ID, final.Error)
	})
}

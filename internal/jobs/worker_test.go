package jobs

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/internal/store/storetest"
)

// testWorker runs a Worker in a goroutine owned by the test.
type testWorker struct {
	queue  *Queue
	cancel context.CancelFunc
	done   chan struct{}
}

func testWorkerConfig() WorkerConfig {
	return WorkerConfig{
		ID:                "test-worker",
		Concurrency:       2,
		LeaseDuration:     5 * time.Second,
		HeartbeatInterval: 20 * time.Millisecond,
		PollInterval:      10 * time.Millisecond,
		ShutdownGrace:     50 * time.Millisecond,
	}
}

func startWorker(t *testing.T, db *store.DB, exec Executor, mod func(*WorkerConfig)) *testWorker {
	t.Helper()
	cfg := testWorkerConfig()
	if mod != nil {
		mod(&cfg)
	}
	q := NewQueue(db, nil)
	w, err := NewWorker(q, exec, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	tw := &testWorker{queue: q, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(tw.done)
		if err := w.Run(ctx); err != nil {
			t.Errorf("worker: %v", err)
		}
	}()
	t.Cleanup(tw.stop)
	return tw
}

// stop cancels the worker and waits (bounded) for Run to return.
func (tw *testWorker) stop() {
	tw.cancel()
	select {
	case <-tw.done:
	case <-time.After(15 * time.Second):
		panic("worker did not stop within 15s")
	}
}

// eventually polls cond until it holds or the timeout expires.
func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		<-tick.C
	}
}

func receive[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

func statusIs(t *testing.T, db *store.DB, id int64, want store.Status) func() bool {
	return func() bool { return getRun(t, db, id).Status == want }
}

func passedReport(cases ...string) *report.Report {
	rep := report.New("run")
	for _, id := range cases {
		rep.Cases = append(rep.Cases, report.Case{ID: id, Status: report.CasePassed})
	}
	rep.Finalize()
	return rep
}

func TestWorkerExecutesRunsAndStoresResults(t *testing.T) {
	t.Parallel()
	db, _ := storetest.New(t)
	exec := ExecutorFunc(func(ctx context.Context, run *store.Run, onCase func(CaseUpdate) error) (*report.Report, error) {
		for i, id := range []string{"list", "get"} {
			if err := onCase(CaseUpdate{CaseID: id, Index: i, Status: "passed",
				Observations: []report.Observation{{Target: run.Targets[0], StatusCode: 200}}}); err != nil {
				return nil, err
			}
		}
		return passedReport("list", "get"), nil
	})
	runs := []*store.Run{enqueue(t, db, false, 2), enqueue(t, db, false, 2), enqueue(t, db, true, 1)}
	startWorker(t, db, exec, nil)

	for _, r := range runs {
		eventually(t, 30*time.Second, "run "+r.PublicID+" to succeed", statusIs(t, db, r.ID, store.StatusSucceeded))
		results, err := db.ListCaseResults(context.Background(), r.ID)
		if err != nil || len(results) != 2 || results[1].CaseID != "get" || results[1].Observations[0].StatusCode != 200 {
			t.Errorf("results of %s = %+v, %v", r.PublicID, results, err)
		}
		got := getRun(t, db, r.ID)
		if got.Summary == nil || got.Summary.Cases != 2 || got.LeaseOwner != "" || got.Attempt != 1 {
			t.Errorf("run = %+v", got)
		}
		if _, err := db.GetReport(context.Background(), r.ID); err != nil {
			t.Errorf("report of %s: %v", r.PublicID, err)
		}
	}
}

func TestWorkerBoundsConcurrency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	var active, maxActive atomic.Int32
	started := make(chan int64, 10)
	release := make(chan struct{})
	exec := ExecutorFunc(func(ctx context.Context, run *store.Run, _ func(CaseUpdate) error) (*report.Report, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			m := maxActive.Load()
			if n <= m || maxActive.CompareAndSwap(m, n) {
				break
			}
		}
		started <- run.ID
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return passedReport(), nil
	})
	var runs []*store.Run
	for i := 0; i < 5; i++ {
		runs = append(runs, enqueue(t, db, false, 1))
	}
	startWorker(t, db, exec, func(c *WorkerConfig) { c.Concurrency = 2 })

	receive(t, started, "first execution")
	receive(t, started, "second execution")
	// Both slots are taken, so the worker cannot claim a third run.
	var running, queued int
	if err := db.Pool().QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'running'), count(*) FILTER (WHERE status = 'queued') FROM runs`,
	).Scan(&running, &queued); err != nil {
		t.Fatal(err)
	}
	if running != 2 || queued != 3 {
		t.Errorf("with both slots busy: running=%d queued=%d", running, queued)
	}
	close(release)
	for _, r := range runs {
		eventually(t, 30*time.Second, "all runs to succeed", statusIs(t, db, r.ID, store.StatusSucceeded))
	}
	if m := maxActive.Load(); m > 2 {
		t.Errorf("%d executions ran at once, limit is 2", m)
	}
}

func TestWorkerCancelsRunningRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	started := make(chan struct{}, 1)
	causes := make(chan error, 1)
	exec := ExecutorFunc(func(ctx context.Context, run *store.Run, onCase func(CaseUpdate) error) (*report.Report, error) {
		started <- struct{}{}
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
			return nil, errors.New("never cancelled")
		}
		causes <- context.Cause(ctx)
		// Results reported while winding down are still stored.
		if err := onCase(CaseUpdate{CaseID: "slow", Index: 0, Status: "cancelled"}); err != nil {
			return nil, err
		}
		rep := report.New("run")
		rep.Cases = []report.Case{{ID: "slow", Status: report.CaseCancelled}}
		rep.Fail(report.StatusCancelled, "cancelled")
		rep.Finalize()
		return rep, nil
	})
	r := enqueue(t, db, false, 1)
	tw := startWorker(t, db, exec, nil)

	receive(t, started, "execution start")
	if st, err := tw.queue.RequestCancel(ctx, r.ID); err != nil || st != store.StatusRunning {
		t.Fatalf("RequestCancel = %s, %v", st, err)
	}
	if cause := receive(t, causes, "execution cancellation"); !errors.Is(cause, errCancelRequested) {
		t.Errorf("cancellation cause = %v", cause)
	}
	eventually(t, 30*time.Second, "run to be cancelled", statusIs(t, db, r.ID, store.StatusCancelled))
	got := getRun(t, db, r.ID)
	if !strings.Contains(got.Error, "operator") || got.FinishedAt == nil {
		t.Errorf("run = %+v", got)
	}
	if results, err := db.ListCaseResults(ctx, r.ID); err != nil || len(results) != 1 || results[0].Status != report.CaseCancelled {
		t.Errorf("results = %+v, %v", results, err)
	}
}

func TestWorkerLeaseLossDiscardsResults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, schema := storetest.New(t)
	started := make(chan struct{}, 1)
	causes := make(chan error, 1)
	lateWrite := make(chan error, 1)
	exec := ExecutorFunc(func(ctx context.Context, run *store.Run, onCase func(CaseUpdate) error) (*report.Report, error) {
		started <- struct{}{}
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
			return nil, errors.New("never cancelled")
		}
		causes <- context.Cause(ctx)
		lateWrite <- onCase(CaseUpdate{CaseID: "c0", Index: 0, Status: "passed"})
		return passedReport("c0"), nil
	})
	r := enqueue(t, db, true, 1)
	tw := startWorker(t, db, exec, nil)
	receive(t, started, "execution start")

	// Another process recovers the run as if the lease had expired.
	other := NewQueue(schema.Open(t, 1), func() time.Time { return time.Now().Add(time.Hour) })
	got, err := other.RecoverExpired(ctx, other.Now())
	if err != nil || len(got) != 1 || got[0].Status != store.StatusInterrupted {
		t.Fatalf("recovery = %+v, %v", got, err)
	}

	if cause := receive(t, causes, "execution cancellation"); !errors.Is(cause, ErrLeaseLost) {
		t.Errorf("cancellation cause = %v", cause)
	}
	if err := receive(t, lateWrite, "late case write"); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("late case write: err = %v", err)
	}
	tw.stop()
	final := getRun(t, db, r.ID)
	if final.Status != store.StatusInterrupted || !strings.Contains(final.Error, "lease expired") || final.Summary != nil {
		t.Errorf("stale worker overwrote the run: %+v", final)
	}
	if results, err := db.ListCaseResults(ctx, r.ID); err != nil || len(results) != 0 {
		t.Errorf("results = %+v, %v", results, err)
	}
}

func TestWorkerShutdownReleasesInFlightRuns(t *testing.T) {
	t.Parallel()
	db, _ := storetest.New(t)
	started := make(chan struct{}, 2)
	causes := make(chan error, 2)
	exec := ExecutorFunc(func(ctx context.Context, run *store.Run, _ func(CaseUpdate) error) (*report.Report, error) {
		started <- struct{}{}
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
			return nil, errors.New("never cancelled")
		}
		causes <- context.Cause(ctx)
		return nil, ctx.Err()
	})
	readOnly := enqueue(t, db, false, 2)
	mutating := enqueue(t, db, true, 1)
	tw := startWorker(t, db, exec, nil)
	receive(t, started, "first execution")
	receive(t, started, "second execution")

	tw.stop()
	for i := 0; i < 2; i++ {
		if c := receive(t, causes, "cancellation"); !errors.Is(c, errShutdown) {
			t.Errorf("cancellation cause = %v", c)
		}
	}
	ro := getRun(t, db, readOnly.ID)
	if ro.Status != store.StatusQueued || ro.Attempt != 1 || ro.LeaseOwner != "" {
		t.Errorf("read-only run after shutdown = %+v", ro)
	}
	mu := getRun(t, db, mutating.ID)
	if mu.Status != store.StatusInterrupted || !strings.Contains(mu.Error, "worker shut down") {
		t.Errorf("mutating run after shutdown = %+v", mu)
	}
}

func TestWorkerShutdownWaitsForRunWithinGrace(t *testing.T) {
	t.Parallel()
	db, _ := storetest.New(t)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	exec := ExecutorFunc(func(ctx context.Context, run *store.Run, _ func(CaseUpdate) error) (*report.Report, error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return passedReport(), nil
	})
	first := enqueue(t, db, true, 1)
	second := enqueue(t, db, false, 1)
	tw := startWorker(t, db, exec, func(c *WorkerConfig) {
		c.Concurrency = 1
		c.ShutdownGrace = 10 * time.Second
	})
	receive(t, started, "execution start")
	tw.cancel()
	close(release)
	receive(t, tw.done, "worker stop")

	if r := getRun(t, db, first.ID); r.Status != store.StatusSucceeded {
		t.Errorf("run finishing within the grace period = %s (%s)", r.Status, r.Error)
	}
	if r := getRun(t, db, second.ID); r.Status != store.StatusQueued || r.Attempt != 0 {
		t.Errorf("run claimed after shutdown started: %+v", r)
	}
}

func TestWorkerExecutorErrors(t *testing.T) {
	t.Parallel()
	db, _ := storetest.New(t)
	failing := enqueue(t, db, false, 1)
	panicking := enqueue(t, db, false, 1)
	exec := ExecutorFunc(func(ctx context.Context, run *store.Run, _ func(CaseUpdate) error) (*report.Report, error) {
		if run.ID == panicking.ID {
			panic("boom")
		}
		return nil, errors.New("dial upstream with key sk-live-123456: refused")
	})
	startWorker(t, db, exec, func(c *WorkerConfig) {
		c.Bounds.Redactor = redact.Default().WithSecrets("sk-live-123456")
	})
	eventually(t, 30*time.Second, "failing run", statusIs(t, db, failing.ID, store.StatusFailed))
	eventually(t, 30*time.Second, "panicking run", statusIs(t, db, panicking.ID, store.StatusFailed))
	if r := getRun(t, db, failing.ID); strings.Contains(r.Error, "sk-live") || !strings.Contains(r.Error, "refused") {
		t.Errorf("error = %q", r.Error)
	}
	if r := getRun(t, db, panicking.ID); !strings.Contains(r.Error, "executor panicked: boom") {
		t.Errorf("error = %q", r.Error)
	}
}

func TestNewWorkerValidatesConfig(t *testing.T) {
	q := NewQueue(nil, nil)
	exec := ExecutorFunc(func(context.Context, *store.Run, func(CaseUpdate) error) (*report.Report, error) { return nil, nil })
	for name, mod := range map[string]func(*WorkerConfig){
		"no id":            func(c *WorkerConfig) { c.ID = "" },
		"zero concurrency": func(c *WorkerConfig) { c.Concurrency = 0 },
		"slow heartbeat":   func(c *WorkerConfig) { c.HeartbeatInterval = c.LeaseDuration },
		"no poll":          func(c *WorkerConfig) { c.PollInterval = 0 },
		"negative grace":   func(c *WorkerConfig) { c.ShutdownGrace = -1 },
	} {
		cfg := testWorkerConfig()
		mod(&cfg)
		if _, err := NewWorker(q, exec, cfg, nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := NewWorker(q, nil, testWorkerConfig(), nil); err == nil {
		t.Error("nil executor accepted")
	}
}

func TestConfigFromServer(t *testing.T) {
	s := &config.Server{}
	s.Worker = config.Worker{Concurrency: 3, LeaseDuration: 30 * time.Second, HeartbeatInterval: 10 * time.Second,
		PollInterval: 2 * time.Second, MaxReadOnlyAttempts: 2}
	s.Limits.MaxReportBytes = 1 << 20
	s.Limits.MaxFindings = 50
	s.Retention.RetainRawBodies = true
	s.Retention.RawBodyMaxBytes = 1024
	cfg := ConfigFromServer("w", s)
	if cfg.Concurrency != 3 || cfg.ShutdownGrace != 15*time.Second || cfg.Bounds.MaxReportBytes != 1<<20 ||
		cfg.Bounds.MaxCaseFindings != 50 || !cfg.Bounds.RetainBodies || cfg.Bounds.MaxBodyBytes != 1024 {
		t.Errorf("config = %+v", cfg)
	}
	if _, err := cfg.validate(); err != nil {
		t.Error(err)
	}
	if id := DefaultWorkerID(); id == "" || id == DefaultWorkerID() {
		t.Errorf("worker ids not unique: %q", id)
	}
}

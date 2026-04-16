package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/store"
)

// WorkerConfig configures a Worker.
type WorkerConfig struct {
	// ID identifies the worker in leases and logs. It must be unique among
	// running workers; DefaultWorkerID provides one.
	ID string
	// Concurrency is the maximum number of runs executed at once.
	Concurrency int
	// LeaseDuration is how long a claim or heartbeat keeps the lease.
	LeaseDuration time.Duration
	// HeartbeatInterval is how often a running run's lease is extended and
	// cancellation is checked. At most half of LeaseDuration.
	HeartbeatInterval time.Duration
	// PollInterval is the wait between claim attempts when the queue is
	// empty.
	PollInterval time.Duration
	// RecoveryInterval is how often the worker runs RecoverExpired. Zero
	// means half of LeaseDuration; negative disables recovery in this
	// worker.
	RecoveryInterval time.Duration
	// ShutdownGrace is how long in-flight runs may continue after shutdown
	// starts before their execution context is cancelled.
	ShutdownGrace time.Duration
	// Bounds sanitizes and bounds stored results.
	Bounds Bounds
}

// ConfigFromServer derives a worker configuration from server settings.
// The shutdown grace period is half the lease duration, so a released run
// is handed back well before its lease would expire.
func ConfigFromServer(id string, s *config.Server) WorkerConfig {
	return WorkerConfig{
		ID:                id,
		Concurrency:       s.Worker.Concurrency,
		LeaseDuration:     s.Worker.LeaseDuration,
		HeartbeatInterval: s.Worker.HeartbeatInterval,
		PollInterval:      s.Worker.PollInterval,
		ShutdownGrace:     s.Worker.LeaseDuration / 2,
		Bounds: Bounds{
			MaxReportBytes:  s.Limits.MaxReportBytes,
			MaxCaseFindings: s.Limits.MaxFindings,
			RetainBodies:    s.Retention.RetainRawBodies,
			MaxBodyBytes:    s.Retention.RawBodyMaxBytes,
			Redactor:        s.Redactor(),
		},
	}
}

// DefaultWorkerID returns "<hostname>-<pid>-<random>".
func DefaultWorkerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "worker"
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), hex.EncodeToString(b[:]))
}

func (c WorkerConfig) validate() (WorkerConfig, error) {
	switch {
	case strings.TrimSpace(c.ID) == "" || len(c.ID) > maxWorkerIDLen:
		return c, fmt.Errorf("worker id must be 1-%d bytes", maxWorkerIDLen)
	case c.Concurrency < 1 || c.Concurrency > 64:
		return c, errors.New("worker concurrency must be between 1 and 64")
	case c.LeaseDuration <= 0:
		return c, errors.New("lease duration must be positive")
	case c.HeartbeatInterval <= 0 || 2*c.HeartbeatInterval > c.LeaseDuration:
		return c, errors.New("heartbeat interval must be positive and at most half of the lease duration")
	case c.PollInterval <= 0:
		return c, errors.New("poll interval must be positive")
	case c.ShutdownGrace < 0:
		return c, errors.New("shutdown grace must not be negative")
	}
	if c.RecoveryInterval == 0 {
		c.RecoveryInterval = c.LeaseDuration / 2
	}
	c.Bounds = c.Bounds.withDefaults()
	return c, nil
}

// Worker claims queued runs and executes them with bounded concurrency.
// Every goroutine it starts is owned by Run and finished before Run
// returns.
type Worker struct {
	queue *Queue
	exec  Executor
	cfg   WorkerConfig
	log   *slog.Logger
}

// NewWorker validates cfg and returns a worker. A nil logger discards logs.
func NewWorker(q *Queue, exec Executor, cfg WorkerConfig, log *slog.Logger) (*Worker, error) {
	if q == nil || exec == nil {
		return nil, errors.New("jobs: worker needs a queue and an executor")
	}
	cfg, err := cfg.validate()
	if err != nil {
		return nil, fmt.Errorf("jobs: worker: %w", err)
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Worker{queue: q, exec: exec, cfg: cfg, log: log.With("worker_id", cfg.ID)}, nil
}

// errShutdown is the cancellation cause for executions stopped because
// the worker is shutting down.
var errShutdown = errors.New("worker shutting down")

// claimTimeout bounds one claim transaction. Claims use a context that
// shutdown does not cancel, so a committed claim is never mistaken for a
// failed one.
const claimTimeout = 30 * time.Second

// Run claims and executes runs until ctx is cancelled, then shuts down:
// it stops claiming, lets in-flight runs continue for ShutdownGrace, then
// cancels their execution and releases them under the recovery policy
// (read-only runs requeued while attempts remain, mutating runs marked
// interrupted). Run returns once every in-flight run has finished or been
// released.
func (w *Worker) Run(ctx context.Context) error {
	execBase, stopExec := context.WithCancelCause(context.WithoutCancel(ctx))
	defer stopExec(nil)
	var wg sync.WaitGroup
	slots := make(chan struct{}, w.cfg.Concurrency)
	nextRecovery := time.Now()
	w.log.Info("worker started", "concurrency", w.cfg.Concurrency, "lease", w.cfg.LeaseDuration.String())

	for ctx.Err() == nil {
		if w.cfg.RecoveryInterval > 0 && !time.Now().Before(nextRecovery) {
			w.recoverExpired(ctx)
			nextRecovery = time.Now().Add(w.cfg.RecoveryInterval)
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			continue
		}
		run, err := w.claim(ctx)
		if err != nil {
			<-slots
			if !errors.Is(err, ErrQueueEmpty) {
				w.log.Error("claim failed", "error", err)
			}
			w.wait(ctx, w.cfg.PollInterval)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			w.process(execBase, run)
		}()
	}

	w.log.Info("worker stopping; no new claims", "in_flight", len(slots))
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	grace := time.NewTimer(w.cfg.ShutdownGrace)
	defer grace.Stop()
	select {
	case <-done:
	case <-grace.C:
		w.log.Warn("shutdown grace period elapsed; cancelling in-flight runs")
		stopExec(errShutdown)
		<-done
	}
	w.log.Info("worker stopped")
	return nil
}

func (w *Worker) claim(ctx context.Context) (*store.Run, error) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), claimTimeout)
	defer cancel()
	return w.queue.Claim(cctx, w.cfg.ID, w.cfg.LeaseDuration)
}

func (w *Worker) recoverExpired(ctx context.Context) {
	got, err := w.queue.RecoverExpired(ctx, w.queue.Now())
	if err != nil {
		if ctx.Err() == nil {
			w.log.Error("lease recovery failed", "error", err)
		}
		return
	}
	for _, r := range got {
		w.log.Warn("recovered run with expired lease",
			"run_id", r.PublicID, "previous_owner", r.LeaseOwner, "attempt", r.Attempt,
			"status", string(r.Status), "reason", r.Error)
	}
}

// wait blocks for d or until ctx is done.
func (w *Worker) wait(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

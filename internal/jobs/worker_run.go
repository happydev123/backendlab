package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/store"
)

// writeTimeout bounds each result write. Writes use a context detached
// from execution cancellation so results reported while a run is being
// cancelled are still stored.
const writeTimeout = 30 * time.Second

// errCancelRequested is the cancellation cause after an operator request.
var errCancelRequested = errors.New("cancellation requested by operator")

// attempt tracks one claimed run while it executes.
type attempt struct {
	run             *store.Run
	log             *slog.Logger
	cancelExec      context.CancelCauseFunc
	leaseLost       atomic.Bool
	cancelRequested atomic.Bool
}

func (a *attempt) loseLease() {
	if !a.leaseLost.Swap(true) {
		a.cancelExec(ErrLeaseLost)
	}
}

// process executes one claimed run: it heartbeats while the executor runs,
// persists case results as they arrive, and then completes, releases or
// abandons the run depending on how execution ended.
func (w *Worker) process(base context.Context, run *store.Run) {
	execCtx, cancelExec := context.WithCancelCause(base)
	defer cancelExec(nil)
	a := &attempt{
		run:        run,
		cancelExec: cancelExec,
		log: w.log.With("run_id", run.PublicID, "attempt", run.Attempt,
			"lease_token", run.LeaseToken),
	}
	a.log.Info("run claimed", "suite", run.SuiteName, "mutating", run.Mutating)

	hbCtx, stopHeartbeat := context.WithCancel(context.WithoutCancel(base))
	var hb sync.WaitGroup
	hb.Add(1)
	go func() {
		defer hb.Done()
		w.heartbeatLoop(hbCtx, a)
	}()
	rep, execErr := w.execute(execCtx, base, a)
	stopHeartbeat()
	hb.Wait()

	writeCtx, cancelWrite := context.WithTimeout(context.WithoutCancel(base), writeTimeout)
	defer cancelWrite()
	complete := execErr == nil && rep != nil && (rep.Status == report.StatusPassed || rep.Status == report.StatusFailed)
	switch {
	case a.leaseLost.Load():
		a.log.Warn("lease lost; results of this attempt are discarded")
		return
	case errors.Is(context.Cause(execCtx), errShutdown) && !complete:
		rel, err := w.queue.Release(writeCtx, run.ID, run.LeaseToken, "worker shut down")
		switch {
		case errors.Is(err, ErrLeaseLost):
			a.log.Warn("lease lost before release; results of this attempt are discarded")
		case err != nil:
			a.log.Error("release on shutdown failed; recovery will handle the run after lease expiry", "error", err)
		default:
			a.log.Warn("run released on shutdown", "status", string(rel.Status), "reason", rel.Error)
		}
		return
	}

	o, err := buildOutcome(w.cfg.Bounds, rep, execErr, a.cancelRequested.Load())
	if err != nil {
		o = Outcome{Status: store.StatusFailed, Error: w.cfg.Bounds.sanitizeError(err.Error())}
	}
	if err := w.queue.Complete(writeCtx, run.ID, run.LeaseToken, o); err != nil {
		if errors.Is(err, ErrLeaseLost) {
			a.log.Warn("lease lost before completion; result discarded")
		} else {
			a.log.Error("storing run result failed; recovery will handle the run after lease expiry", "error", err)
		}
		return
	}
	a.log.Info("run finished", "status", string(o.Status), "report_truncated", o.ReportTruncated)
}

// execute calls the executor with a fenced case callback. A panic in the
// executor's own goroutine is converted into an error so one faulty run
// does not stop the worker.
func (w *Worker) execute(ctx, base context.Context, a *attempt) (rep *report.Report, err error) {
	defer func() {
		if p := recover(); p != nil {
			rep, err = nil, fmt.Errorf("executor panicked: %v", p)
		}
	}()
	onCase := func(u CaseUpdate) error {
		if a.leaseLost.Load() {
			return ErrLeaseLost
		}
		rec, err := w.cfg.Bounds.CaseRecord(u)
		if err != nil {
			return fmt.Errorf("invalid case update: %w", err)
		}
		wctx, cancel := context.WithTimeout(context.WithoutCancel(base), writeTimeout)
		defer cancel()
		if err := w.queue.RecordCase(wctx, a.run.ID, a.run.LeaseToken, rec); err != nil {
			if errors.Is(err, ErrLeaseLost) {
				a.log.Warn("case result rejected: lease lost", "case_id", u.CaseID)
				a.loseLease()
			}
			return err
		}
		return nil
	}
	return w.exec.Execute(ctx, a.run, onCase)
}

// heartbeatLoop extends the lease every HeartbeatInterval until ctx ends.
// It cancels execution when an operator requests cancellation, when the
// fencing token no longer matches, and when no heartbeat has succeeded for
// a full lease duration (another worker may have recovered the run by
// then, so continuing would risk duplicate upstream requests).
func (w *Worker) heartbeatLoop(ctx context.Context, a *attempt) {
	t := time.NewTicker(w.cfg.HeartbeatInterval)
	defer t.Stop()
	lastOK := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		hctx, cancel := context.WithTimeout(ctx, w.cfg.LeaseDuration/2)
		cancelRequested, err := w.queue.Heartbeat(hctx, a.run.ID, a.run.LeaseToken, w.cfg.LeaseDuration)
		cancel()
		switch {
		case errors.Is(err, ErrLeaseLost):
			a.log.Warn("heartbeat rejected: lease lost; cancelling execution")
			a.loseLease()
			return
		case err != nil:
			if ctx.Err() != nil {
				return
			}
			if time.Since(lastOK) >= w.cfg.LeaseDuration {
				a.log.Error("no successful heartbeat for a full lease duration; abandoning the run", "error", err)
				a.loseLease()
				return
			}
			a.log.Warn("heartbeat failed; retrying", "error", err)
		default:
			lastOK = time.Now()
			if cancelRequested && !a.cancelRequested.Swap(true) {
				a.log.Info("cancellation requested; cancelling execution")
				a.cancelExec(errCancelRequested)
			}
		}
	}
}

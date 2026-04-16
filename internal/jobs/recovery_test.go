package jobs

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/internal/store/storetest"
)

func TestReleaseDecision(t *testing.T) {
	cases := []struct {
		name string
		c    releaseCandidate
		want store.Status
		sub  string
	}{
		{"read-only with attempts", releaseCandidate{attempt: 1, maxAttempts: 2}, store.StatusQueued, ""},
		{"read-only out of attempts", releaseCandidate{attempt: 2, maxAttempts: 2}, store.StatusInterrupted, "no attempts left"},
		{"mutating", releaseCandidate{mutating: true, attempt: 1, maxAttempts: 1}, store.StatusInterrupted, "not retried automatically"},
		{"mutating with cancel", releaseCandidate{mutating: true, cancelRequested: true, attempt: 1, maxAttempts: 1}, store.StatusInterrupted, "mutating"},
		{"read-only with cancel", releaseCandidate{cancelRequested: true, attempt: 1, maxAttempts: 3}, store.StatusCancelled, "cancelled"},
	}
	for _, tc := range cases {
		got, msg := releaseDecision(tc.c, "worker lease expired")
		if got != tc.want || !strings.Contains(msg, tc.sub) {
			t.Errorf("%s: %s %q, want %s containing %q", tc.name, got, msg, tc.want, tc.sub)
		}
	}
}

func TestLeaseExpiryAndBoundedRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	clock := newTestClock()
	q := NewQueue(db, clock.Now)
	run := enqueue(t, db, false, 2)
	r := mustClaim(t, q, "w1", 30*time.Second)

	// Before expiry nothing is recovered; a heartbeat moves the expiry.
	if got, err := q.RecoverExpired(ctx, clock.Now().Add(29*time.Second)); err != nil || len(got) != 0 {
		t.Fatalf("early recovery = %v, %v", got, err)
	}
	clock.Advance(20 * time.Second)
	if _, err := q.Heartbeat(ctx, r.ID, r.LeaseToken, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if got, err := q.RecoverExpired(ctx, clock.Now().Add(29*time.Second)); err != nil || len(got) != 0 {
		t.Fatalf("recovery after heartbeat = %v, %v", got, err)
	}

	// The worker stops heartbeating; after expiry the read-only run is
	// requeued because it has an attempt left.
	clock.Advance(31 * time.Second)
	got, err := q.RecoverExpired(ctx, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RunID != run.ID || got[0].Status != store.StatusQueued || got[0].LeaseOwner != "w1" {
		t.Fatalf("recovered = %+v", got)
	}
	requeued := getRun(t, db, run.ID)
	if requeued.Status != store.StatusQueued || requeued.LeaseOwner != "" || requeued.LeaseExpiresAt != nil ||
		requeued.Attempt != 1 || requeued.LeaseToken != 1 || requeued.FinishedAt != nil {
		t.Errorf("requeued run = %+v", requeued)
	}

	// Second attempt also dies: no attempts left, so it is interrupted.
	r2 := mustClaim(t, q, "w2", 30*time.Second)
	if r2.Attempt != 2 || r2.LeaseToken != 2 {
		t.Fatalf("second claim = attempt %d token %d", r2.Attempt, r2.LeaseToken)
	}
	clock.Advance(time.Minute)
	got, err = q.RecoverExpired(ctx, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Status != store.StatusInterrupted {
		t.Fatalf("second recovery = %+v", got)
	}
	final := getRun(t, db, run.ID)
	if final.Status != store.StatusInterrupted || final.FinishedAt == nil || !strings.Contains(final.Error, "no attempts left") {
		t.Errorf("final run = %+v", final)
	}
	if _, err := q.Claim(ctx, "w3", time.Minute); !errors.Is(err, ErrQueueEmpty) {
		t.Errorf("interrupted run was claimable: %v", err)
	}
}

func TestMutatingRunIsInterruptedNeverRequeued(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	clock := newTestClock()
	q := NewQueue(db, clock.Now)
	run := enqueue(t, db, true, 5)
	mustClaim(t, q, "w1", 10*time.Second)

	clock.Advance(11 * time.Second)
	got, err := q.RecoverExpired(ctx, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Status != store.StatusInterrupted {
		t.Fatalf("recovered = %+v", got)
	}
	r := getRun(t, db, run.ID)
	if r.Status != store.StatusInterrupted || r.Attempt != 1 ||
		!strings.Contains(r.Error, "unknown which requests reached the upstream") {
		t.Errorf("run = %+v", r)
	}
	if _, err := q.Claim(ctx, "w2", time.Minute); !errors.Is(err, ErrQueueEmpty) {
		t.Errorf("mutating run was requeued: %v", err)
	}
	// Only an explicit resubmit runs it again, as a new run.
	fresh, err := db.Resubmit(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ID == run.ID || fresh.Status != store.StatusQueued || !fresh.Mutating {
		t.Errorf("resubmitted run = %+v", fresh)
	}
}

func TestRecoveryCancelsRunWithPendingCancellation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	clock := newTestClock()
	q := NewQueue(db, clock.Now)
	run := enqueue(t, db, false, 3)
	mustClaim(t, q, "w1", 10*time.Second)
	if _, err := q.RequestCancel(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	if _, err := q.RecoverExpired(ctx, clock.Now()); err != nil {
		t.Fatal(err)
	}
	if r := getRun(t, db, run.ID); r.Status != store.StatusCancelled || r.FinishedAt == nil {
		t.Errorf("run = %+v", r)
	}
}

// TestStaleWorkerWritesRejected: worker A loses its lease, the run is
// recovered and re-claimed by worker B on a separate pool, and every
// later write by A is rejected while B's results survive.
func TestStaleWorkerWritesRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, schema := storetest.New(t)
	clock := newTestClock()
	qa := NewQueue(schema.Open(t, 2), clock.Now)
	qb := NewQueue(schema.Open(t, 2), clock.Now)
	run := enqueue(t, db, false, 2)

	a := mustClaim(t, qa, "worker-a", 30*time.Second)
	for i, id := range []string{"c0", "c1"} {
		if err := qa.RecordCase(ctx, a.ID, a.LeaseToken, CaseRecord{CaseID: id, Index: i, Status: report.CaseFailed}); err != nil {
			t.Fatal(err)
		}
	}

	clock.Advance(31 * time.Second)
	if _, err := qb.RecoverExpired(ctx, clock.Now()); err != nil {
		t.Fatal(err)
	}
	// While queued, A is already fenced off.
	if err := qa.RecordCase(ctx, a.ID, a.LeaseToken, CaseRecord{CaseID: "c2", Index: 2, Status: report.CasePassed}); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("A write while queued: err = %v", err)
	}

	b := mustClaim(t, qb, "worker-b", 30*time.Second)
	if b.ID != run.ID || b.LeaseToken <= a.LeaseToken {
		t.Fatalf("B claimed run %d with token %d (A had %d)", b.ID, b.LeaseToken, a.LeaseToken)
	}
	if results, err := db.ListCaseResults(ctx, run.ID); err != nil || len(results) != 0 {
		t.Fatalf("results of A's attempt survived the re-claim: %+v, %v", results, err)
	}
	if err := qb.RecordCase(ctx, b.ID, b.LeaseToken, CaseRecord{CaseID: "c0", Index: 0, Status: report.CasePassed}); err != nil {
		t.Fatal(err)
	}

	// A wakes up and tries to continue.
	if err := qa.RecordCase(ctx, a.ID, a.LeaseToken, CaseRecord{CaseID: "c0", Index: 0, Status: report.CaseFailed}); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("A overwrite: err = %v", err)
	}
	if _, err := qa.Heartbeat(ctx, a.ID, a.LeaseToken, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("A heartbeat: err = %v", err)
	}
	if err := qa.Complete(ctx, a.ID, a.LeaseToken, Outcome{Status: store.StatusFailed, Error: "stale"}); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("A completion: err = %v", err)
	}
	if _, err := qa.Release(ctx, a.ID, a.LeaseToken, "shutdown"); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("A release: err = %v", err)
	}

	if err := qb.Complete(ctx, b.ID, b.LeaseToken, Outcome{Status: store.StatusSucceeded}); err != nil {
		t.Fatal(err)
	}
	final := getRun(t, db, run.ID)
	if final.Status != store.StatusSucceeded || final.Error != "" || final.Attempt != 2 {
		t.Errorf("final run = %+v", final)
	}
	results, err := db.ListCaseResults(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != report.CasePassed || results[0].WrittenToken != b.LeaseToken {
		t.Errorf("results = %+v", results)
	}
}

func TestReleaseAppliesPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	q := NewQueue(db, newTestClock().Now)
	readOnly := enqueue(t, db, false, 2)
	ro := mustClaim(t, q, "w1", time.Minute)
	mutating := enqueue(t, db, true, 1)
	mu := mustClaim(t, q, "w1", time.Minute)

	got, err := q.Release(ctx, ro.ID, ro.LeaseToken, "worker shutting down")
	if err != nil || got.Status != store.StatusQueued || got.RunID != readOnly.ID {
		t.Errorf("read-only release = %+v, %v", got, err)
	}
	got, err = q.Release(ctx, mu.ID, mu.LeaseToken, "worker shutting down")
	if err != nil || got.Status != store.StatusInterrupted || got.RunID != mutating.ID ||
		!strings.HasPrefix(got.Error, "worker shutting down") {
		t.Errorf("mutating release = %+v, %v", got, err)
	}
	if _, err := q.Release(ctx, mu.ID, mu.LeaseToken, ""); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("second release: err = %v", err)
	}
}

// TestConcurrentRecovery runs recovery from two pools at once; every
// expired run is released exactly once.
func TestConcurrentRecovery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, schema := storetest.New(t)
	clock := newTestClock()
	q := NewQueue(db, clock.Now)
	const n = 20
	for i := 0; i < n; i++ {
		enqueue(t, db, i%2 == 0, 2)
		mustClaim(t, q, "dead-worker", 10*time.Second)
	}
	clock.Advance(time.Minute)

	queues := []*Queue{NewQueue(schema.Open(t, 2), clock.Now), NewQueue(schema.Open(t, 2), clock.Now)}
	var (
		mu   sync.Mutex
		seen = map[int64]int{}
		wg   sync.WaitGroup
	)
	for _, rq := range queues {
		wg.Add(1)
		go func(rq *Queue) {
			defer wg.Done()
			got, err := rq.RecoverExpired(ctx, clock.Now())
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			for _, r := range got {
				seen[r.RunID]++
			}
			mu.Unlock()
		}(rq)
	}
	wg.Wait()
	// A run skipped by both calls (locked by the other at the time) is
	// picked up by a later call.
	rest, err := q.RecoverExpired(ctx, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rest {
		seen[r.RunID]++
	}
	if len(seen) != n {
		t.Errorf("recovered %d runs, want %d", len(seen), n)
	}
	for id, c := range seen {
		if c != 1 {
			t.Errorf("run %d recovered %d times", id, c)
		}
	}
	var running, queued, interrupted int
	if err := db.Pool().QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'running'),
		       count(*) FILTER (WHERE status = 'queued' AND NOT mutating),
		       count(*) FILTER (WHERE status = 'interrupted' AND mutating)
		FROM runs`).Scan(&running, &queued, &interrupted); err != nil {
		t.Fatal(err)
	}
	if running != 0 || queued != n/2 || interrupted != n/2 {
		t.Errorf("running=%d queued=%d interrupted=%d", running, queued, interrupted)
	}
}

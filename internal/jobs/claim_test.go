package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/internal/store/storetest"
)

func TestClaimSetsLeaseAndFencingToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	clock := newTestClock()
	q := NewQueue(db, clock.Now)

	if _, err := q.Claim(ctx, "w1", time.Minute); !errors.Is(err, ErrQueueEmpty) {
		t.Fatalf("claim on empty queue: err = %v", err)
	}
	first := enqueue(t, db, false, 2)
	second := enqueue(t, db, false, 2)

	r := mustClaim(t, q, "w1", 30*time.Second)
	if r.ID != first.ID {
		t.Fatalf("claimed run %d, want the oldest (%d)", r.ID, first.ID)
	}
	if r.Status != store.StatusRunning || r.Attempt != 1 || r.LeaseToken != 1 || r.LeaseOwner != "w1" {
		t.Errorf("claimed run = %+v", r)
	}
	if r.LeaseExpiresAt == nil || !r.LeaseExpiresAt.Equal(clock.Now().Add(30*time.Second)) {
		t.Errorf("lease_expires_at = %v", r.LeaseExpiresAt)
	}
	if r.StartedAt == nil || !r.StartedAt.Equal(clock.Now()) {
		t.Errorf("started_at = %v", r.StartedAt)
	}
	if next := mustClaim(t, q, "w2", time.Minute); next.ID != second.ID {
		t.Errorf("second claim got %d, want %d", next.ID, second.ID)
	}
	if _, err := q.Claim(ctx, "w3", time.Minute); !errors.Is(err, ErrQueueEmpty) {
		t.Errorf("claim after draining: err = %v", err)
	}
}

func TestClaimValidatesArguments(t *testing.T) {
	q := NewQueue(nil, nil)
	if _, err := q.Claim(context.Background(), "", time.Minute); err == nil {
		t.Error("empty worker id accepted")
	}
	if _, err := q.Claim(context.Background(), "w", 0); err == nil {
		t.Error("zero lease accepted")
	}
}

// TestClaimExclusiveAcrossPools runs claimers on separate connection pools
// (as separate worker processes would) and checks that every run is
// claimed exactly once.
func TestClaimExclusiveAcrossPools(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, schema := storetest.New(t)
	const runs, claimers = 40, 8
	for i := 0; i < runs; i++ {
		enqueue(t, db, false, 1)
	}

	var (
		mu      sync.Mutex
		claimed = map[int64]string{}
		dupes   []string
		errs    []error
	)
	queues := make([]*Queue, claimers)
	for i := range queues {
		queues[i] = NewQueue(schema.Open(t, 2), nil)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, q := range queues {
		wg.Add(1)
		go func(worker string, q *Queue) {
			defer wg.Done()
			<-start
			for {
				r, err := q.Claim(ctx, worker, time.Minute)
				if errors.Is(err, ErrQueueEmpty) {
					return
				}
				mu.Lock()
				if err != nil {
					errs = append(errs, err)
					mu.Unlock()
					return
				}
				if prev, ok := claimed[r.ID]; ok {
					dupes = append(dupes, fmt.Sprintf("run %d by %s and %s", r.ID, prev, worker))
				}
				claimed[r.ID] = worker
				mu.Unlock()
			}
		}(fmt.Sprintf("worker-%d", i), q)
	}
	close(start)
	wg.Wait()

	for _, err := range errs {
		t.Error(err)
	}
	for _, d := range dupes {
		t.Errorf("claimed twice: %s", d)
	}
	if len(claimed) != runs {
		t.Errorf("claimed %d distinct runs, want %d", len(claimed), runs)
	}
	var queued, badTokens int
	if err := db.Pool().QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'queued'),
		       count(*) FILTER (WHERE lease_token <> 1 OR attempt <> 1)
		FROM runs`).Scan(&queued, &badTokens); err != nil {
		t.Fatal(err)
	}
	if queued != 0 || badTokens != 0 {
		t.Errorf("after claiming: %d still queued, %d with unexpected token/attempt", queued, badTokens)
	}
}

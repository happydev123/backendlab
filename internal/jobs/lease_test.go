package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/internal/store/storetest"
)

func TestHeartbeatExtendsLease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	clock := newTestClock()
	q := NewQueue(db, clock.Now)
	enqueue(t, db, false, 1)
	r := mustClaim(t, q, "w1", 30*time.Second)

	clock.Advance(20 * time.Second)
	cancelled, err := q.Heartbeat(ctx, r.ID, r.LeaseToken, 30*time.Second)
	if err != nil || cancelled {
		t.Fatalf("heartbeat = %v, %v", cancelled, err)
	}
	got := getRun(t, db, r.ID)
	if !got.LeaseExpiresAt.Equal(clock.Now().Add(30*time.Second)) || !got.HeartbeatAt.Equal(clock.Now()) {
		t.Errorf("lease_expires_at = %v, heartbeat_at = %v", got.LeaseExpiresAt, got.HeartbeatAt)
	}

	if _, err := q.Heartbeat(ctx, r.ID, r.LeaseToken+1, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("heartbeat with a wrong token: err = %v", err)
	}
	if _, err := q.Heartbeat(ctx, r.ID, r.LeaseToken, 0); err == nil {
		t.Error("zero lease duration accepted")
	}
}

func TestCancelQueuedRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	clock := newTestClock()
	q := NewQueue(db, clock.Now)
	r := enqueue(t, db, false, 1)

	status, err := q.RequestCancel(ctx, r.ID)
	if err != nil || status != store.StatusCancelled {
		t.Fatalf("RequestCancel = %s, %v", status, err)
	}
	got := getRun(t, db, r.ID)
	if got.Status != store.StatusCancelled || got.FinishedAt == nil || !got.FinishedAt.Equal(clock.Now()) ||
		got.Error != cancelledBeforeStart || got.CancelRequestedAt == nil {
		t.Errorf("cancelled run = %+v", got)
	}
	if _, err := q.Claim(ctx, "w1", time.Minute); !errors.Is(err, ErrQueueEmpty) {
		t.Errorf("cancelled run was claimable: err = %v", err)
	}
	if st, err := q.RequestCancel(ctx, r.ID); !errors.Is(err, ErrNotCancellable) || st != store.StatusCancelled {
		t.Errorf("second cancel = %s, %v", st, err)
	}
	if _, err := q.RequestCancel(ctx, 777777); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("cancel of a missing run: err = %v", err)
	}
}

func TestCancelRunningRunIsSeenByHeartbeat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	clock := newTestClock()
	q := NewQueue(db, clock.Now)
	enqueue(t, db, false, 1)
	r := mustClaim(t, q, "w1", time.Minute)

	status, err := q.RequestCancel(ctx, r.ID)
	if err != nil || status != store.StatusRunning {
		t.Fatalf("RequestCancel = %s, %v", status, err)
	}
	first := getRun(t, db, r.ID).CancelRequestedAt
	if first == nil {
		t.Fatal("cancel_requested_at not set")
	}
	clock.Advance(time.Second)
	if _, err := q.RequestCancel(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if again := getRun(t, db, r.ID); !again.CancelRequestedAt.Equal(*first) || again.Status != store.StatusRunning {
		t.Errorf("repeated cancel changed the run: %+v", again)
	}

	cancelled, err := q.Heartbeat(ctx, r.ID, r.LeaseToken, time.Minute)
	if err != nil || !cancelled {
		t.Errorf("heartbeat = %v, %v; want cancellation reported", cancelled, err)
	}
}

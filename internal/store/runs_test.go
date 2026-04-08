package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/internal/store/storetest"
	"github.com/happydev123/backendlab/internal/suite"
)

func newRun(name string) store.NewRun {
	return store.NewRun{
		SuiteName:     name,
		SuiteFile:     name + ".yaml",
		SuiteSnapshot: []byte("version: 1\nname: " + name + "\n"),
		Mode:          store.ModeCompare,
		Targets:       []string{"baseline", "candidate"},
		MaxAttempts:   2,
	}
}

func TestEnqueueAndGetRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)

	n := newRun("orders")
	r, err := db.EnqueueRun(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID == 0 || r.Status != store.StatusQueued || r.Attempt != 0 || r.MaxAttempts != 2 {
		t.Errorf("enqueued run = %+v", r)
	}
	if r.SuiteSHA256 != suite.Digest(n.SuiteSnapshot) {
		t.Errorf("sha256 = %s", r.SuiteSHA256)
	}
	if r.LeaseOwner != "" || r.LeaseExpiresAt != nil || r.FinishedAt != nil || r.Summary != nil {
		t.Errorf("new run has lease or completion fields: %+v", r)
	}

	got, err := db.GetRun(ctx, r.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != r.ID || string(got.SuiteSnapshot) != string(n.SuiteSnapshot) ||
		got.Mode != store.ModeCompare || len(got.Targets) != 2 || got.Targets[1] != "candidate" {
		t.Errorf("GetRun = %+v", got)
	}
	if !got.CreatedAt.Equal(r.CreatedAt) {
		t.Errorf("created_at %v != %v", got.CreatedAt, r.CreatedAt)
	}
	byID, err := db.GetRunByID(ctx, r.ID)
	if err != nil || byID.PublicID != r.PublicID {
		t.Errorf("GetRunByID = %+v, %v", byID, err)
	}

	if _, err := db.GetRun(ctx, "run_missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing run err = %v", err)
	}
	if _, err := db.GetRunByID(ctx, 999999); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing run err = %v", err)
	}
}

func TestEnqueueRejectsInvalidRun(t *testing.T) {
	t.Parallel()
	db, _ := storetest.New(t)
	n := newRun("x")
	n.Mutating = true // writes not requested
	if _, err := db.EnqueueRun(context.Background(), n); !errors.Is(err, store.ErrInvalidRun) {
		t.Fatalf("err = %v, want ErrInvalidRun", err)
	}
}

func TestEnqueueMutatingRunGetsSingleAttempt(t *testing.T) {
	t.Parallel()
	db, _ := storetest.New(t)
	n := newRun("writes")
	n.Mutating, n.WritesRequested, n.MaxAttempts = true, true, 4
	r, err := db.EnqueueRun(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Mutating || !r.WritesRequested || r.MaxAttempts != 1 {
		t.Errorf("run = %+v", r)
	}
}

func TestResubmitCreatesNewRun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	old, err := db.EnqueueRun(ctx, newRun("orders"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Resubmit(ctx, old.ID); !errors.Is(err, store.ErrNotTerminal) {
		t.Fatalf("resubmit of queued run: err = %v", err)
	}
	if _, err := db.Pool().Exec(ctx,
		`UPDATE runs SET status = 'cancelled', finished_at = now(), error = 'stopped' WHERE id = $1`, old.ID); err != nil {
		t.Fatal(err)
	}

	fresh, err := db.Resubmit(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ID == old.ID || fresh.PublicID == old.PublicID {
		t.Fatal("resubmit reused the old run")
	}
	if fresh.Status != store.StatusQueued || fresh.ResubmittedFrom == nil || *fresh.ResubmittedFrom != old.ID {
		t.Errorf("fresh run = %+v", fresh)
	}
	if string(fresh.SuiteSnapshot) != string(old.SuiteSnapshot) || fresh.SuiteSHA256 != old.SuiteSHA256 ||
		fresh.MaxAttempts != old.MaxAttempts || fresh.Error != "" {
		t.Errorf("fresh run does not copy the snapshot: %+v", fresh)
	}

	again, err := db.GetRunByID(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != store.StatusCancelled || again.Error != "stopped" {
		t.Errorf("old run changed: %+v", again)
	}
	if _, err := db.Resubmit(ctx, 424242); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("resubmit of missing run: err = %v", err)
	}
}

func TestPing(t *testing.T) {
	t.Parallel()
	db, _ := storetest.New(t)
	if err := db.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
}

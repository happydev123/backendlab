package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/internal/store/storetest"
)

func TestRetentionCleanupDeletesOnlyOldTerminalRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	cutoff := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	old := cutoff.Add(-48 * time.Hour)
	recent := cutoff.Add(time.Hour)

	mk := func(name string) *store.Run {
		r, err := db.EnqueueRun(ctx, newRun(name))
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	finish := func(r *store.Run, status string, at time.Time) {
		if _, err := db.Pool().Exec(ctx, `
			UPDATE runs SET status = 'running', lease_owner = 'w', lease_expires_at = $2, attempt = 1 WHERE id = $1`,
			r.ID, at); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool().Exec(ctx, `
			UPDATE runs SET status = $2::text, finished_at = $3, lease_owner = NULL, lease_expires_at = NULL WHERE id = $1`,
			r.ID, status, at); err != nil {
			t.Fatal(err)
		}
	}
	oldSucceeded, oldInterrupted, recentFailed := mk("a"), mk("b"), mk("c")
	finish(oldSucceeded, "succeeded", old)
	finish(oldInterrupted, "interrupted", old)
	finish(recentFailed, "failed", recent)
	queued := mk("d")
	running := mk("e")
	if _, err := db.Pool().Exec(ctx, `
		UPDATE runs SET created_at = $2 WHERE id = ANY($1)`, []int64{queued.ID, running.ID}, old.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `
		UPDATE runs SET status = 'running', lease_owner = 'w', lease_expires_at = $2, attempt = 1 WHERE id = $1`,
		running.ID, old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool().Exec(ctx, `
		INSERT INTO case_results (run_id, case_index, case_id, status, written_token, recorded_at)
		VALUES ($1, 0, 'x', 'passed', 1, now()), ($2, 0, 'y', 'passed', 1, now())`,
		oldSucceeded.ID, recentFailed.ID); err != nil {
		t.Fatal(err)
	}
	child, err := db.Resubmit(ctx, oldInterrupted.ID)
	if err != nil {
		t.Fatal(err)
	}

	n, err := db.RetentionCleanup(ctx, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("deleted %d runs, want 2", n)
	}
	for _, gone := range []*store.Run{oldSucceeded, oldInterrupted} {
		if _, err := db.GetRunByID(ctx, gone.ID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("run %s survived: %v", gone.PublicID, err)
		}
	}
	for _, kept := range []*store.Run{recentFailed, queued, running} {
		if _, err := db.GetRunByID(ctx, kept.ID); err != nil {
			t.Errorf("run %s deleted: %v", kept.PublicID, err)
		}
	}
	var orphans int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM case_results WHERE run_id = $1`, oldSucceeded.ID).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Errorf("%d case results of a deleted run remain", orphans)
	}
	if results, err := db.ListCaseResults(ctx, recentFailed.ID); err != nil || len(results) != 1 {
		t.Errorf("results of a kept run = %v, %v", results, err)
	}
	c, err := db.GetRunByID(ctx, child.ID)
	if err != nil || c.ResubmittedFrom != nil || c.Status != store.StatusQueued {
		t.Errorf("resubmitted child = %+v, %v", c, err)
	}

	if n, err := db.RetentionCleanup(ctx, cutoff); err != nil || n != 0 {
		t.Errorf("second cleanup = %d, %v", n, err)
	}
}

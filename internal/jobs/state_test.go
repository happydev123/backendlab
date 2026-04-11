package jobs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/internal/store/storetest"
)

func TestCanTransitionTable(t *testing.T) {
	allowed := map[[2]store.Status]bool{
		{store.StatusQueued, store.StatusRunning}:        true,
		{store.StatusQueued, store.StatusCancelled}:      true,
		{store.StatusRunning, store.StatusSucceeded}:     true,
		{store.StatusRunning, store.StatusFailed}:        true,
		{store.StatusRunning, store.StatusCancelled}:     true,
		{store.StatusRunning, store.StatusInterrupted}:   true,
		{store.StatusRunning, store.StatusQueued}:        true,
		{store.StatusInterrupted, store.StatusQueued}:    false,
		{store.StatusInterrupted, store.StatusRunning}:   false,
		{store.StatusSucceeded, store.StatusRunning}:     false,
		{store.StatusQueued, store.StatusSucceeded}:      false,
		{store.StatusCancelled, store.StatusQueued}:      false,
		{store.StatusFailed, store.StatusInterrupted}:    false,
		{store.StatusQueued, store.StatusInterrupted}:    false,
		{store.StatusRunning, store.StatusRunning}:       false,
		{store.StatusSucceeded, store.StatusSucceeded}:   false,
		{store.StatusInterrupted, store.StatusFailed}:    false,
		{store.StatusCancelled, store.StatusSucceeded}:   false,
		{store.StatusFailed, store.StatusQueued}:         false,
		{store.StatusSucceeded, store.StatusInterrupted}: false,
	}
	for pair, want := range allowed {
		if got := CanTransition(pair[0], pair[1]); got != want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", pair[0], pair[1], got, want)
		}
	}
	for _, s := range store.Statuses {
		if s.Terminal() && len(Next(s)) != 0 {
			t.Errorf("terminal state %s has transitions %v", s, Next(s))
		}
	}
}

func TestCanRequeue(t *testing.T) {
	if !CanRequeue(false, 1, 2) {
		t.Error("read-only run with attempts left not requeueable")
	}
	if CanRequeue(false, 2, 2) {
		t.Error("read-only run out of attempts requeueable")
	}
	if CanRequeue(true, 0, 5) {
		t.Error("mutating run requeueable")
	}
}

// TestDatabaseGuardMatchesCanTransition attempts every status pair
// directly in SQL and checks that the trigger from migration 0003 accepts
// exactly the transitions CanTransition allows.
func TestDatabaseGuardMatchesCanTransition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	for _, from := range store.Statuses {
		for _, to := range store.Statuses {
			if from == to {
				continue
			}
			for _, mutating := range []bool{false, true} {
				name := fmt.Sprintf("%s->%s/mutating=%v", from, to, mutating)
				id := insertRunInState(t, db, from, mutating, 1, 2)
				err := updateStatus(ctx, db, id, to)
				want := CanTransition(from, to)
				if from == store.StatusRunning && to == store.StatusQueued {
					want = CanRequeue(mutating, 1, 2)
				}
				if want && err != nil {
					t.Errorf("%s: rejected by the database: %v", name, err)
				}
				if !want && !isGuardViolation(err) {
					t.Errorf("%s: database accepted a forbidden transition (err %v)", name, err)
				}
			}
		}
	}

	// running -> queued with no attempts left is rejected.
	id := insertRunInState(t, db, store.StatusRunning, false, 2, 2)
	if err := updateStatus(ctx, db, id, store.StatusQueued); !isGuardViolation(err) {
		t.Errorf("requeue without attempts left: err = %v", err)
	}
}

func isGuardViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514" &&
		strings.Contains(pgErr.Message, "invalid run status transition")
}

// insertRunInState inserts a run that satisfies the table constraints for
// the given status.
func insertRunInState(t *testing.T, db *store.DB, status store.Status, mutating bool, attempt, maxAttempts int) int64 {
	t.Helper()
	if mutating {
		maxAttempts = 1
		if attempt > 1 {
			attempt = 1
		}
	}
	var id int64
	err := db.Pool().QueryRow(context.Background(), `
		INSERT INTO runs (public_id, suite_name, suite_file, suite_sha256, suite_snapshot, mode, targets,
			writes_requested, mutating, status, attempt, max_attempts, lease_owner, lease_token,
			lease_expires_at, finished_at)
		VALUES ('run_' || md5(random()::text), 's', 's.yaml', repeat('a', 64), 'x', 'run', '{t}',
			$1, $1, $2::text, $3, $4,
			CASE WHEN $2::text = 'running' THEN 'w' END, 1,
			CASE WHEN $2::text = 'running' THEN now() END,
			CASE WHEN $2::text IN ('succeeded', 'failed', 'cancelled', 'interrupted') THEN now() END)
		RETURNING id`, mutating, string(status), attempt, maxAttempts).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// updateStatus changes only the status plus the columns the table
// constraints tie to it, so any rejection comes from the trigger.
func updateStatus(ctx context.Context, db *store.DB, id int64, to store.Status) error {
	_, err := db.Pool().Exec(ctx, `
		UPDATE runs SET status = $2::text,
			lease_owner = CASE WHEN $2::text = 'running' THEN 'w' END,
			lease_expires_at = CASE WHEN $2::text = 'running' THEN now() END,
			finished_at = CASE WHEN $2::text IN ('succeeded', 'failed', 'cancelled', 'interrupted') THEN now() END
		WHERE id = $1`, id, string(to))
	return err
}

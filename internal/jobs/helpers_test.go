package jobs

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/store"
)

// testClock is a manually advanced clock for lease timestamps.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock {
	return &testClock{t: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

var runSeq struct {
	sync.Mutex
	n int
}

// enqueue adds a run. Mutating runs request writes, as the API requires.
func enqueue(t *testing.T, db *store.DB, mutating bool, maxAttempts int) *store.Run {
	t.Helper()
	runSeq.Lock()
	runSeq.n++
	name := fmt.Sprintf("suite-%d", runSeq.n)
	runSeq.Unlock()
	r, err := db.EnqueueRun(context.Background(), store.NewRun{
		SuiteName:       name,
		SuiteFile:       name + ".yaml",
		SuiteSnapshot:   []byte("version: 1\nname: " + name + "\n"),
		Mode:            store.ModeRun,
		Targets:         []string{"staging"},
		WritesRequested: mutating,
		Mutating:        mutating,
		MaxAttempts:     maxAttempts,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func getRun(t *testing.T, db *store.DB, id int64) *store.Run {
	t.Helper()
	r, err := db.GetRunByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustClaim(t *testing.T, q *Queue, worker string, lease time.Duration) *store.Run {
	t.Helper()
	r, err := q.Claim(context.Background(), worker, lease)
	if err != nil {
		t.Fatalf("claim by %s: %v", worker, err)
	}
	return r
}

package store_test

import (
	"context"
	"crypto/sha256"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/store/storetest"
)

func TestReserveLoginAttemptWindow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	key := sha256.Sum256([]byte("10.0.0.1"))
	other := sha256.Sum256([]byte("10.0.0.2"))
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	window := 15 * time.Minute

	for i := 0; i < 3; i++ {
		ok, err := db.ReserveLoginAttempt(ctx, key[:], now.Add(time.Duration(i)*time.Minute), now.Add(-window), 3)
		if err != nil || !ok {
			t.Fatalf("attempt %d: %v, %v", i, ok, err)
		}
	}
	at := now.Add(5 * time.Minute)
	if ok, err := db.ReserveLoginAttempt(ctx, key[:], at, at.Add(-window), 3); err != nil || ok {
		t.Fatalf("fourth attempt allowed: %v, %v", ok, err)
	}
	if ok, err := db.ReserveLoginAttempt(ctx, other[:], at, at.Add(-window), 3); err != nil || !ok {
		t.Fatalf("other key throttled: %v, %v", ok, err)
	}
	// Once the first attempt leaves the window, one more is allowed.
	later := now.Add(window + 30*time.Second)
	if ok, err := db.ReserveLoginAttempt(ctx, key[:], later, later.Add(-window), 3); err != nil || !ok {
		t.Fatalf("attempt after the window moved: %v, %v", ok, err)
	}
	if err := db.ClearLoginAttempts(ctx, key[:]); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.ReserveLoginAttempt(ctx, key[:], later, later.Add(-window), 1); err != nil || !ok {
		t.Fatalf("attempt after clearing: %v, %v", ok, err)
	}
	n, err := db.DeleteLoginAttemptsBefore(ctx, later)
	if err != nil || n != 1 {
		t.Errorf("DeleteLoginAttemptsBefore = %d, %v (want the other key's attempt)", n, err)
	}
	if _, err := db.ReserveLoginAttempt(ctx, []byte("short"), now, now, 1); err == nil {
		t.Error("short key hash accepted")
	}
}

func TestReserveLoginAttemptConcurrent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, schema := storetest.New(t)
	key := sha256.Sum256([]byte("burst"))
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	other := schema.Open(t, 4)
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pool := db
			if i%2 == 1 {
				pool = other
			}
			ok, err := pool.ReserveLoginAttempt(ctx, key[:], now, now.Add(-time.Hour), 5)
			if err != nil {
				t.Error(err)
			}
			if ok {
				allowed.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if n := allowed.Load(); n != 5 {
		t.Errorf("%d concurrent attempts allowed, want exactly 5", n)
	}
}

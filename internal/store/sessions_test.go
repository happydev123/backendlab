package store_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/internal/store/storetest"
)

func TestSessionLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	hash := sha256.Sum256([]byte("token-a"))
	rec := store.SessionRecord{TokenHash: hash[:], CSRFToken: "csrf-a", CreatedAt: now, ExpiresAt: now.Add(time.Hour), LastSeenAt: now}
	if err := db.CreateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(ctx, store.SessionRecord{TokenHash: []byte("short")}); err == nil {
		t.Error("short token hash accepted")
	}

	got, err := db.GetSession(ctx, hash[:], now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.TokenHash, hash[:]) || got.CSRFToken != "csrf-a" || !got.ExpiresAt.Equal(rec.ExpiresAt) {
		t.Errorf("session = %+v", got)
	}
	if err := db.TouchSession(ctx, hash[:], now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetSession(ctx, hash[:], now.Add(11*time.Minute))
	if !got.LastSeenAt.Equal(now.Add(10*time.Minute)) || !got.ExpiresAt.Equal(rec.ExpiresAt) {
		t.Errorf("after touch: %+v", got)
	}
	if _, err := db.GetSession(ctx, hash[:], now.Add(time.Hour)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expired session: err = %v", err)
	}

	other := sha256.Sum256([]byte("token-b"))
	if err := db.CreateSession(ctx, store.SessionRecord{TokenHash: other[:], CSRFToken: "c", CreatedAt: now,
		ExpiresAt: now.Add(3 * time.Hour), LastSeenAt: now}); err != nil {
		t.Fatal(err)
	}
	n, err := db.DeleteExpiredSessions(ctx, now.Add(2*time.Hour))
	if err != nil || n != 1 {
		t.Errorf("DeleteExpiredSessions = %d, %v", n, err)
	}
	if err := db.DeleteSession(ctx, other[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetSession(ctx, other[:], now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleted session: err = %v", err)
	}
	if err := db.DeleteSession(ctx, other[:]); err != nil {
		t.Errorf("deleting twice: %v", err)
	}
}

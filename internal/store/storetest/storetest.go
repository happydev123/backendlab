// Package storetest gives tests an isolated PostgreSQL schema. It is
// imported only from _test.go files.
//
// Every test gets a fresh schema with a random name in the database named
// by BACKENDLAB_TEST_DATABASE_URL, and pools whose search_path points at
// that schema. Tests in different packages can therefore run in parallel
// against one database. When the variable is unset the test is skipped.
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/migrations"
)

// EnvURL names the variable holding the test database URL.
const EnvURL = "BACKENDLAB_TEST_DATABASE_URL"

// Schema is a test-owned PostgreSQL schema.
type Schema struct {
	Name string
	url  string
}

// NewSchema creates an empty schema and drops it (with everything in it)
// when the test finishes. It skips the test when EnvURL is unset.
func NewSchema(t testing.TB) *Schema {
	t.Helper()
	url := os.Getenv(EnvURL)
	if url == "" {
		t.Skipf("%s is not set; skipping PostgreSQL integration test", EnvURL)
	}
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	s := &Schema{Name: "bltest_" + hex.EncodeToString(b[:]), url: url}
	s.exec(t, "CREATE SCHEMA "+pgx.Identifier{s.Name}.Sanitize())
	t.Cleanup(func() {
		s.exec(t, "DROP SCHEMA IF EXISTS "+pgx.Identifier{s.Name}.Sanitize()+" CASCADE")
	})
	return s
}

func (s *Schema) exec(t testing.TB, sql string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, s.url)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// Open returns a new, independent pool bound to the schema. The pool is
// closed when the test finishes. maxConns <= 0 uses 4.
func (s *Schema) Open(t testing.TB, maxConns int32) *store.DB {
	t.Helper()
	if maxConns <= 0 {
		maxConns = 4
	}
	pc, err := store.ParsePoolConfig(s.url, config.Database{MaxConns: maxConns})
	if err != nil {
		t.Fatal(err)
	}
	pc.ConnConfig.RuntimeParams["search_path"] = s.Name
	// Test schemas are disposable; skipping the commit flush makes the
	// DDL-heavy setup much faster without changing visibility or locking.
	pc.ConnConfig.RuntimeParams["synchronous_commit"] = "off"
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	db, err := store.OpenConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

// Migrate applies the embedded migrations through db.
func Migrate(t testing.TB, db *store.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := db.Migrate(ctx, migrations.FS); err != nil {
		t.Fatal(err)
	}
}

// New returns a pool on a fresh, fully migrated schema.
func New(t testing.TB) (*store.DB, *Schema) {
	t.Helper()
	s := NewSchema(t)
	db := s.Open(t, 8)
	Migrate(t, db)
	return db, s
}

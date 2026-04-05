package store_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/internal/store/storetest"
	"github.com/happydev123/backendlab/migrations"
)

func TestMigrateAppliesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewSchema(t)
	db := s.Open(t, 2)

	files, err := store.LoadMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := db.Migrate(ctx, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != len(files) {
		t.Fatalf("applied %d migrations, want %d", len(applied), len(files))
	}
	var n int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM runs`).Scan(&n); err != nil {
		t.Fatalf("runs table missing: %v", err)
	}

	again, err := db.Migrate(ctx, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Errorf("second migrate applied %d migrations", len(again))
	}
	var rows int
	if err := db.Pool().QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != len(files) {
		t.Errorf("schema_migrations has %d rows, want %d", rows, len(files))
	}
}

func TestMigrateRefusesModifiedFile(t *testing.T) {
	ctx := context.Background()
	db := storetest.NewSchema(t).Open(t, 2)
	v1 := fstest.MapFS{"0001_items.up.sql": {Data: []byte("CREATE TABLE items (id int);")}}
	if _, err := db.Migrate(ctx, v1); err != nil {
		t.Fatal(err)
	}
	changed := fstest.MapFS{
		"0001_items.up.sql": {Data: []byte("CREATE TABLE items (id bigint);")},
		"0002_more.up.sql":  {Data: []byte("CREATE TABLE more (id int);")},
	}
	_, err := db.Migrate(ctx, changed)
	if err == nil || !strings.Contains(err.Error(), "modified after it was applied") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
	var exists bool
	if err := db.Pool().QueryRow(ctx, `SELECT to_regclass('more') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("pending migration ran despite the checksum mismatch")
	}

	st, err := db.MigrationStatus(ctx, changed)
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 2 || !st[0].Applied || !st[0].Modified || !st[1].Pending() {
		t.Errorf("status = %+v", st)
	}
}

func TestMigrateRefusesNewerDatabase(t *testing.T) {
	ctx := context.Background()
	db := storetest.NewSchema(t).Open(t, 2)
	two := fstest.MapFS{
		"0001_a.up.sql": {Data: []byte("CREATE TABLE a (id int);")},
		"0002_b.up.sql": {Data: []byte("CREATE TABLE b (id int);")},
	}
	if _, err := db.Migrate(ctx, two); err != nil {
		t.Fatal(err)
	}
	one := fstest.MapFS{"0001_a.up.sql": two["0001_a.up.sql"]}
	if _, err := db.Migrate(ctx, one); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("err = %v, want newer-schema refusal", err)
	}
	st, err := db.MigrationStatus(ctx, one)
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 2 || !st[1].Unknown || st[1].Pending() {
		t.Errorf("status = %+v", st)
	}
}

func TestMigrateFailureRollsBackThatMigration(t *testing.T) {
	ctx := context.Background()
	db := storetest.NewSchema(t).Open(t, 2)
	fsys := fstest.MapFS{
		"0001_a.up.sql": {Data: []byte("CREATE TABLE a (id int);")},
		"0002_b.up.sql": {Data: []byte("CREATE TABLE b (id int);\nSELECT no_such_column FROM a;")},
	}
	applied, err := db.Migrate(ctx, fsys)
	if err == nil {
		t.Fatal("broken migration succeeded")
	}
	if !strings.Contains(err.Error(), "0002_b") {
		t.Errorf("error does not name the migration: %v", err)
	}
	if len(applied) != 1 || applied[0].Version != 1 {
		t.Errorf("applied = %+v, want only 0001", applied)
	}
	var bExists bool
	if err := db.Pool().QueryRow(ctx, `SELECT to_regclass('b') IS NOT NULL`).Scan(&bExists); err != nil {
		t.Fatal(err)
	}
	if bExists {
		t.Error("table from the failed migration was not rolled back")
	}
	st, err := db.MigrationStatus(ctx, fsys)
	if err != nil {
		t.Fatal(err)
	}
	if !st[0].Applied || !st[1].Pending() {
		t.Errorf("status = %+v", st)
	}
}

func TestMigrationStatusOnEmptyDatabase(t *testing.T) {
	db := storetest.NewSchema(t).Open(t, 1)
	st, err := db.MigrationStatus(context.Background(), migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range st {
		if !m.Pending() || m.AppliedAt != nil {
			t.Errorf("migration %d not pending on an empty schema: %+v", m.Version, m)
		}
	}
}

// TestConcurrentMigrate runs Migrate from several independent pools at
// once; the advisory lock must make every migration apply exactly once.
func TestConcurrentMigrate(t *testing.T) {
	ctx := context.Background()
	s := storetest.NewSchema(t)
	const n = 6
	dbs := make([]*store.DB, n)
	for i := range dbs {
		dbs[i] = s.Open(t, 1)
	}
	files, err := store.LoadMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	counts := make([]int, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range dbs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			applied, err := dbs[i].Migrate(ctx, migrations.FS)
			counts[i], errs[i] = len(applied), err
		}(i)
	}
	close(start)
	wg.Wait()
	total := 0
	for i := range dbs {
		if errs[i] != nil {
			t.Errorf("migrator %d: %v", i, errs[i])
		}
		total += counts[i]
	}
	if total != len(files) {
		t.Errorf("migrations applied %d times in total, want %d", total, len(files))
	}
}

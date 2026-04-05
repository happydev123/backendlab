package store

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"time"
)

// MigrationState describes one migration as seen by MigrationStatus.
type MigrationState struct {
	Version  int
	Name     string
	Checksum string // checksum of the file; empty when Unknown
	Applied  bool
	// AppliedAt is set for applied migrations.
	AppliedAt *time.Time
	// Modified is true when the file checksum differs from the one recorded
	// at apply time. Migrate refuses to run in that case.
	Modified bool
	// Unknown is true for a migration recorded in the database that has no
	// file in fsys.
	Unknown bool
}

// Pending reports whether the migration still has to be applied.
func (s MigrationState) Pending() bool { return !s.Applied && !s.Unknown }

// MigrationStatus lists every migration in fsys and every migration
// recorded in the database, ordered by version, without changing anything.
// A database that has never been migrated reports all files as pending.
func (db *DB) MigrationStatus(ctx context.Context, fsys fs.FS) ([]MigrationState, error) {
	files, err := LoadMigrations(fsys)
	if err != nil {
		return nil, err
	}
	conn, err := db.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("migration status: acquire connection: %w", err)
	}
	defer conn.Release()

	var exists bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		return nil, fmt.Errorf("migration status: %w", err)
	}
	done := map[int]appliedMigration{}
	if exists {
		if done, err = readApplied(ctx, conn.Conn()); err != nil {
			return nil, fmt.Errorf("migration status: %w", err)
		}
	}

	out := make([]MigrationState, 0, len(files)+len(done))
	inFiles := map[int]bool{}
	for _, m := range files {
		inFiles[m.Version] = true
		st := MigrationState{Version: m.Version, Name: m.Name, Checksum: m.Checksum}
		if a, ok := done[m.Version]; ok {
			at := a.AppliedAt
			st.Applied = true
			st.AppliedAt = &at
			st.Modified = a.Checksum != m.Checksum
		}
		out = append(out, st)
	}
	for _, v := range sortedVersions(done) {
		if inFiles[v] {
			continue
		}
		a := done[v]
		at := a.AppliedAt
		out = append(out, MigrationState{Version: v, Name: a.Name, Applied: true, AppliedAt: &at, Unknown: true})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func sortedVersions(m map[int]appliedMigration) []int {
	vs := make([]int, 0, len(m))
	for v := range m {
		vs = append(vs, v)
	}
	sort.Ints(vs)
	return vs
}

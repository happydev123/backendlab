package store

import (
	"context"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const createMigrationsTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    integer     PRIMARY KEY,
    name       text        NOT NULL,
    checksum   text        NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
)`

// The advisory lock key is derived from the current schema so that
// separate schemas in one database (as the tests use) migrate
// independently, while concurrent migrations of the same schema
// serialize.
const (
	lockMigrations   = `SELECT pg_advisory_lock(hashtextextended('backendlab.migrate.' || coalesce(current_schema(), ''), 0))`
	unlockMigrations = `SELECT pg_advisory_unlock(hashtextextended('backendlab.migrate.' || coalesce(current_schema(), ''), 0))`
)

// appliedMigration is one row of schema_migrations.
type appliedMigration struct {
	Version   int
	Name      string
	Checksum  string
	AppliedAt time.Time
}

// Migrate applies the pending migrations from fsys and returns the ones it
// applied, in order. It holds a PostgreSQL advisory lock for the duration,
// so concurrent invocations (for example several servers starting at once)
// apply each migration exactly once. Each migration runs in its own
// transaction together with its schema_migrations row.
//
// Before applying anything, Migrate verifies the checksum of every
// already-applied migration against fsys. It refuses to run if an applied
// file was modified or if the database records a migration that fsys does
// not contain (a schema newer than this binary).
func (db *DB) Migrate(ctx context.Context, fsys fs.FS) ([]Migration, error) {
	files, err := LoadMigrations(fsys)
	if err != nil {
		return nil, err
	}
	var applied []Migration
	err = db.withMigrationLock(ctx, func(conn *pgx.Conn) error {
		if _, err := conn.Exec(ctx, createMigrationsTable); err != nil {
			return fmt.Errorf("create schema_migrations: %w", err)
		}
		done, err := readApplied(ctx, conn)
		if err != nil {
			return err
		}
		if err := verifyApplied(files, done); err != nil {
			return err
		}
		for _, m := range files {
			if _, ok := done[m.Version]; ok {
				continue
			}
			if err := applyMigration(ctx, conn, m); err != nil {
				return err
			}
			applied = append(applied, m)
		}
		return nil
	})
	if err != nil {
		return applied, fmt.Errorf("migrate: %w", err)
	}
	return applied, nil
}

func (db *DB) withMigrationLock(ctx context.Context, fn func(*pgx.Conn) error) error {
	conn, err := db.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, lockMigrations); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer releaseMigrationLock(ctx, conn)
	return fn(conn.Conn())
}

// releaseMigrationLock unlocks with a fresh bounded context, because ctx
// may already be cancelled. If unlocking fails the connection is closed,
// which releases the session-level lock and makes the pool discard it.
func releaseMigrationLock(ctx context.Context, conn *pgxpool.Conn) {
	uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := conn.Exec(uctx, unlockMigrations); err != nil {
		_ = conn.Conn().Close(uctx)
	}
}

func readApplied(ctx context.Context, conn *pgx.Conn) (map[int]appliedMigration, error) {
	rows, err := conn.Query(ctx, `SELECT version, name, checksum, applied_at FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (appliedMigration, error) {
		var a appliedMigration
		err := r.Scan(&a.Version, &a.Name, &a.Checksum, &a.AppliedAt)
		return a, err
	})
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	out := make(map[int]appliedMigration, len(list))
	for _, a := range list {
		out[a.Version] = a
	}
	return out, nil
}

func verifyApplied(files []Migration, done map[int]appliedMigration) error {
	byVersion := make(map[int]Migration, len(files))
	for _, m := range files {
		byVersion[m.Version] = m
	}
	for _, v := range sortedVersions(done) {
		a := done[v]
		m, ok := byVersion[v]
		if !ok {
			return fmt.Errorf("database has migration %04d_%s, which this binary does not contain (schema is newer)", v, a.Name)
		}
		if m.Checksum != a.Checksum {
			return fmt.Errorf("migration %04d_%s was modified after it was applied (file checksum %s, recorded %s); restore the original file and add a new migration instead",
				v, m.Name, m.Checksum, a.Checksum)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, conn *pgx.Conn, m Migration) error {
	err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		// Exec without arguments uses the simple protocol, which allows
		// several statements per file.
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
			m.Version, m.Name, m.Checksum)
		return err
	})
	if err != nil {
		return fmt.Errorf("apply %04d_%s: %w", m.Version, m.Name, err)
	}
	return nil
}

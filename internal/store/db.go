// Package store is the PostgreSQL persistence layer for JobDock runs,
// case results, operator sessions and login throttling. It uses pgx v5
// with a connection pool and explicit SQL; there is no ORM and no query
// builder. Schema changes are applied by Migrate from the embedded files in
// the migrations package.
//
// Run lifecycle transitions performed by workers (claim, heartbeat,
// completion, recovery) live in package jobs; this package provides the
// pool, the run model and the read/write paths used by the HTTP API and
// the dashboard.
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/happydev123/backendlab/internal/config"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("store: not found")

// DB wraps a pgx connection pool. It is safe for concurrent use.
type DB struct {
	pool *pgxpool.Pool
}

// ParsePoolConfig parses url and applies the pool settings from c. Zero
// values in c keep the pgx defaults. Parse errors from pgx mask the
// password in the connection string.
func ParsePoolConfig(url string, c config.Database) (*pgxpool.Config, error) {
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("store: database URL is empty")
	}
	pc, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("store: parse database URL: %w", err)
	}
	if c.MaxConns > 0 {
		pc.MaxConns = c.MaxConns
	}
	if c.MinConns > 0 {
		pc.MinConns = c.MinConns
	}
	if pc.MinConns > pc.MaxConns {
		return nil, fmt.Errorf("store: min_conns (%d) exceeds max_conns (%d)", pc.MinConns, pc.MaxConns)
	}
	if c.MaxConnLifetime > 0 {
		pc.MaxConnLifetime = c.MaxConnLifetime
	}
	if c.MaxConnIdleTime > 0 {
		pc.MaxConnIdleTime = c.MaxConnIdleTime
	}
	return pc, nil
}

// Open creates a pool from url and c and verifies connectivity.
func Open(ctx context.Context, url string, c config.Database) (*DB, error) {
	pc, err := ParsePoolConfig(url, c)
	if err != nil {
		return nil, err
	}
	return OpenConfig(ctx, pc)
}

// OpenConfig creates a pool from an already parsed configuration and
// verifies connectivity with a ping. The ping is bounded by ctx's deadline,
// or by 10 seconds when ctx has none.
func OpenConfig(ctx context.Context, pc *pgxpool.Config) (*DB, error) {
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("store: create pool: %w", err)
	}
	db := &DB{pool: pool}
	pingCtx, cancel := ctx, context.CancelFunc(func() {})
	if _, ok := ctx.Deadline(); !ok {
		pingCtx, cancel = context.WithTimeout(ctx, 10*time.Second)
	}
	defer cancel()
	if err := db.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}
	return db, nil
}

// New wraps an existing pool. The caller keeps ownership of the pool.
func New(pool *pgxpool.Pool) *DB { return &DB{pool: pool} }

// Pool returns the underlying pool for packages that issue their own SQL
// against the store schema (package jobs).
func (db *DB) Pool() *pgxpool.Pool { return db.pool }

// Ping checks that a connection can be acquired and used. The readiness
// endpoint calls it.
func (db *DB) Ping(ctx context.Context) error {
	if err := db.pool.Ping(ctx); err != nil {
		return fmt.Errorf("store: ping: %w", err)
	}
	return nil
}

// Close closes the pool, waiting for acquired connections to be released.
func (db *DB) Close() { db.pool.Close() }

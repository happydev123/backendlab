package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/happydev123/backendlab/internal/report"
)

// List bounds.
const (
	DefaultListLimit = 50
	MaxListLimit     = 200
	maxListOffset    = 100000
)

// ListRunsParams selects a page of runs, newest first.
type ListRunsParams struct {
	// Limit is the page size; 0 means DefaultListLimit. Values above
	// MaxListLimit are rejected.
	Limit  int
	Offset int
	// Statuses filters by status; empty means all.
	Statuses []Status
}

// RunSummary is a run as shown in lists: no snapshot, lease details or
// report bytes.
type RunSummary struct {
	ID              int64
	PublicID        string
	SuiteName       string
	SuiteFile       string
	Mode            Mode
	Targets         []string
	Mutating        bool
	Status          Status
	Attempt         int
	MaxAttempts     int
	CancelRequested bool
	CreatedAt       time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
	Error           string
	Summary         *report.Summary
	ReportTruncated bool
}

func (p ListRunsParams) normalize() (ListRunsParams, error) {
	switch {
	case p.Limit == 0:
		p.Limit = DefaultListLimit
	case p.Limit < 0 || p.Limit > MaxListLimit:
		return p, fmt.Errorf("limit must be between 1 and %d", MaxListLimit)
	}
	if p.Offset < 0 || p.Offset > maxListOffset {
		return p, fmt.Errorf("offset must be between 0 and %d", maxListOffset)
	}
	for _, s := range p.Statuses {
		if !s.Valid() {
			return p, fmt.Errorf("unknown run status %q", s)
		}
	}
	return p, nil
}

// ListRuns returns one page of runs ordered by creation time (newest
// first) and the total number of runs matching the filter. The page and
// the total are read by separate statements, so the total can be off by
// runs enqueued in between.
func (db *DB) ListRuns(ctx context.Context, p ListRunsParams) ([]RunSummary, int64, error) {
	p, err := p.normalize()
	if err != nil {
		return nil, 0, fmt.Errorf("list runs: %w", err)
	}
	statuses := make([]string, len(p.Statuses))
	for i, s := range p.Statuses {
		statuses[i] = string(s)
	}
	// An empty array means "no filter".
	const where = `(cardinality($1::text[]) = 0 OR status = ANY($1::text[]))`

	var total int64
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE `+where, statuses).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("list runs: count: %w", err)
	}
	rows, err := db.pool.Query(ctx, `
		SELECT id, public_id, suite_name, suite_file, mode, targets, mutating, status,
			attempt, max_attempts, cancel_requested_at IS NOT NULL, created_at, started_at,
			finished_at, coalesce(error, ''), summary, report_truncated
		FROM runs
		WHERE `+where+`
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3`, statuses, p.Limit, p.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list runs: %w", err)
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (RunSummary, error) {
		var s RunSummary
		var summary []byte
		if err := r.Scan(&s.ID, &s.PublicID, &s.SuiteName, &s.SuiteFile, &s.Mode, &s.Targets, &s.Mutating,
			&s.Status, &s.Attempt, &s.MaxAttempts, &s.CancelRequested, &s.CreatedAt, &s.StartedAt,
			&s.FinishedAt, &s.Error, &summary, &s.ReportTruncated); err != nil {
			return s, err
		}
		var err error
		s.Summary, err = decodeSummary(summary)
		return s, err
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list runs: %w", err)
	}
	return list, total, nil
}

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/report"
)

// ErrNoReport is returned by GetReport when the run exists but has no
// stored report (it has not finished, or finished without one).
var ErrNoReport = errors.New("store: run has no stored report")

// CaseResult is one persisted per-case result. Results are written by the
// worker holding the run's lease (see package jobs) and reflect the
// latest attempt only.
type CaseResult struct {
	RunID           int64
	Index           int
	CaseID          string
	Status          report.CaseStatus
	Findings        []finding.Finding
	FindingsDropped int
	Observations    []report.Observation
	// Bodies maps target name to a bounded, redacted response body. It is
	// nil unless raw body retention is enabled.
	Bodies       map[string]string
	WrittenToken int64
	RecordedAt   time.Time
}

// ListCaseResults returns the stored case results of a run in suite order.
// It returns an empty list (not ErrNotFound) for a run without results.
func (db *DB) ListCaseResults(ctx context.Context, runID int64) ([]CaseResult, error) {
	rows, err := db.pool.Query(ctx, `
		SELECT run_id, case_index, case_id, status, findings, findings_dropped,
			observations, bodies, written_token, recorded_at
		FROM case_results
		WHERE run_id = $1
		ORDER BY case_index`, runID)
	if err != nil {
		return nil, fmt.Errorf("list case results: %w", err)
	}
	list, err := pgx.CollectRows(rows, scanCaseResult)
	if err != nil {
		return nil, fmt.Errorf("list case results: %w", err)
	}
	return list, nil
}

func scanCaseResult(r pgx.CollectableRow) (CaseResult, error) {
	var (
		c                           CaseResult
		findings, observations, bod []byte
	)
	if err := r.Scan(&c.RunID, &c.Index, &c.CaseID, &c.Status, &findings, &c.FindingsDropped,
		&observations, &bod, &c.WrittenToken, &c.RecordedAt); err != nil {
		return c, err
	}
	if err := json.Unmarshal(findings, &c.Findings); err != nil {
		return c, fmt.Errorf("case %s findings: %w", c.CaseID, err)
	}
	if err := json.Unmarshal(observations, &c.Observations); err != nil {
		return c, fmt.Errorf("case %s observations: %w", c.CaseID, err)
	}
	if bod != nil {
		if err := json.Unmarshal(bod, &c.Bodies); err != nil {
			return c, fmt.Errorf("case %s bodies: %w", c.CaseID, err)
		}
	}
	return c, nil
}

// GetReport returns the stored JSON report of a run. It returns
// ErrNotFound for an unknown run and ErrNoReport when none is stored. The
// report is bounded by limits.max_report_bytes at write time; check
// Run.ReportTruncated to learn whether it was reduced to a summary.
func (db *DB) GetReport(ctx context.Context, runID int64) ([]byte, error) {
	var data []byte
	err := db.pool.QueryRow(ctx, `SELECT report FROM runs WHERE id = $1`, runID).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get report: %w", err)
	}
	if data == nil {
		return nil, ErrNoReport
	}
	return data, nil
}

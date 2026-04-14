package jobs

import (
	"context"
	"encoding/json"
	"fmt"
)

// RecordCase stores one case result for the attempt holding token. The
// write is fenced: it happens only while the run is running with that
// fencing token, otherwise ErrLeaseLost is returned and nothing is
// written. Recording the same case index again in the same attempt
// replaces the earlier row.
//
// The statement locks the run row FOR SHARE while it inserts. A concurrent
// claim or recovery (which update the row) therefore either waits for this
// insert to commit, and a re-claim then deletes the row as belonging to an
// older attempt, or commits first, in which case the fence check fails.
func (q *Queue) RecordCase(ctx context.Context, runID, token int64, rec CaseRecord) error {
	findings, err := json.Marshal(nonNil(rec.Findings))
	if err != nil {
		return fmt.Errorf("jobs: record case %s: encode findings: %w", rec.CaseID, err)
	}
	observations, err := json.Marshal(nonNil(rec.Observations))
	if err != nil {
		return fmt.Errorf("jobs: record case %s: encode observations: %w", rec.CaseID, err)
	}
	var bodies []byte
	if rec.Bodies != nil {
		if bodies, err = json.Marshal(rec.Bodies); err != nil {
			return fmt.Errorf("jobs: record case %s: encode bodies: %w", rec.CaseID, err)
		}
	}
	tag, err := q.db.Pool().Exec(ctx, `
		INSERT INTO case_results (run_id, case_index, case_id, status, findings, findings_dropped,
			observations, bodies, written_token, recorded_at)
		SELECT r.id, $3::int, $4::text, $5::text, $6::jsonb, $7::int, $8::jsonb, $9::jsonb,
			r.lease_token, $10::timestamptz
		FROM runs r
		WHERE r.id = $1 AND r.lease_token = $2 AND r.status = 'running'
		FOR SHARE
		ON CONFLICT (run_id, case_index) DO UPDATE SET
			case_id = EXCLUDED.case_id,
			status = EXCLUDED.status,
			findings = EXCLUDED.findings,
			findings_dropped = EXCLUDED.findings_dropped,
			observations = EXCLUDED.observations,
			bodies = EXCLUDED.bodies,
			written_token = EXCLUDED.written_token,
			recorded_at = EXCLUDED.recorded_at
		WHERE case_results.written_token <= EXCLUDED.written_token`,
		runID, token, rec.Index, rec.CaseID, string(rec.Status), findings, rec.FindingsDropped,
		observations, bodies, q.Now())
	if err != nil {
		return fmt.Errorf("jobs: record case %s: %w", rec.CaseID, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// nonNil returns an empty slice for nil so JSON encodes [] rather than null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

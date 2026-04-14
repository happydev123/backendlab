package jobs

import (
	"context"
	"fmt"

	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/store"
)

// Outcome is the final result of an attempt, written by Complete.
type Outcome struct {
	// Status is succeeded, failed or cancelled.
	Status store.Status
	// Error is sanitized text explaining a failure or cancellation, or a
	// note about a reduced report.
	Error string
	// Summary is the JSON-encoded report.Summary, or nil.
	Summary []byte
	// Report is the JSON report, bounded by limits.max_report_bytes, or nil.
	Report          []byte
	ReportTruncated bool
}

// Complete records the final outcome of the attempt holding token and
// releases the lease. Like every worker write it is fenced: it returns
// ErrLeaseLost, and changes nothing, unless the run is still running with
// that token.
func (q *Queue) Complete(ctx context.Context, runID, token int64, o Outcome) error {
	switch o.Status {
	case store.StatusSucceeded, store.StatusFailed, store.StatusCancelled:
	default:
		return fmt.Errorf("jobs: complete: %s is not a completion status", o.Status)
	}
	if !CanTransition(store.StatusRunning, o.Status) {
		return fmt.Errorf("jobs: complete: running -> %s is not allowed", o.Status)
	}
	tag, err := q.db.Pool().Exec(ctx, `
		UPDATE runs SET
			status = $3::text,
			finished_at = $4,
			error = nullif($5::text, ''),
			summary = $6::jsonb,
			report = $7,
			report_truncated = $8,
			lease_owner = NULL,
			lease_expires_at = NULL
		WHERE id = $1 AND lease_token = $2 AND status = 'running'`,
		runID, token, string(o.Status), q.Now(), o.Error, o.Summary, o.Report, o.ReportTruncated)
	if err != nil {
		return fmt.Errorf("jobs: complete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// buildOutcome maps an executor result to the stored outcome:
//
//	report passed                    -> succeeded
//	report failed                    -> failed
//	report cancelled                 -> cancelled
//	report error or incomplete       -> failed, with the report error text
//	executor error                   -> failed with the sanitized error, or
//	                                    cancelled if cancellation was requested
//
// A run whose executor finished with a complete report keeps that result
// even if cancellation was requested in the meantime.
func buildOutcome(b Bounds, rep *report.Report, execErr error, cancelRequested bool) (Outcome, error) {
	if execErr != nil {
		if cancelRequested {
			return Outcome{Status: store.StatusCancelled, Error: "cancelled by operator request"}, nil
		}
		return Outcome{Status: store.StatusFailed, Error: b.sanitizeError("execution failed: " + execErr.Error())}, nil
	}
	if rep == nil {
		return Outcome{Status: store.StatusFailed, Error: "execution returned no report"}, nil
	}
	var o Outcome
	switch rep.Status {
	case report.StatusPassed:
		o.Status = store.StatusSucceeded
	case report.StatusFailed:
		o.Status = store.StatusFailed
	case report.StatusCancelled:
		o.Status = store.StatusCancelled
		o.Error = "cancelled"
		if cancelRequested {
			o.Error = "cancelled by operator request"
		}
	case report.StatusError, report.StatusIncomplete:
		o.Status = store.StatusFailed
		o.Error = fmt.Sprintf("run ended with status %s", rep.Status)
		if rep.Error != "" {
			o.Error += ": " + rep.Error
		}
	default:
		o.Status = store.StatusFailed
		o.Error = fmt.Sprintf("report has unknown status %q", rep.Status)
	}
	enc, err := b.encodeReport(rep)
	if err != nil {
		return Outcome{}, err
	}
	o.Summary, o.Report, o.ReportTruncated = enc.Summary, enc.Data, enc.Truncated
	o.Error = joinNotes(o.Error, enc.Note)
	o.Error = b.sanitizeError(o.Error)
	return o, nil
}

func joinNotes(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "; " + b
}

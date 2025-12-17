package replay

import (
	"fmt"
	"time"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/report"
)

// Limitation texts recorded in runtime reports.
const (
	limitationTiming = "Timings are single-run observations, not statistically established regressions; " +
		"only expect.max_duration turns a timing into a failure."
	limitationSideEffects = "Cancellation and timeouts stop waiting for responses but do not undo side effects " +
		"a target may already have applied."
	limitationStreamCompare = "Stream response bodies are not compared between targets; status and selected " +
		"headers are compared and stream expectations are checked per target."
)

// buildReport assembles the report for a finished run. interrupted is
// true when some cases never started.
func (rs *runState) buildReport(command string, runs []*targetRun, results []CaseResult, interrupted bool, start, end time.Time) *report.Report {
	l := rs.l
	rep := report.New(command)
	rep.Inputs = []report.Input{{Kind: "suite", Name: l.Source.Path, SHA256: l.Source.SHA256}}
	for _, tr := range runs {
		rep.Targets = append(rep.Targets, tr.target.Name)
	}
	rep.Timing = &report.Timing{
		StartedAt:  start.UTC(),
		FinishedAt: end.UTC(),
		DurationMS: *report.Millis(nonNegative(end.Sub(start))),
	}

	col := finding.NewCollector(rs.e.lim.MaxFindings)
	stoppedInFlight := false
	hasStream, hasMutating := false, false
	for _, cr := range results {
		c := &l.Suite.Cases[cr.Index]
		hasStream = hasStream || c.Stream != nil
		hasMutating = hasMutating || cr.Mutating
		rep.Cases = append(rep.Cases, report.Case{
			ID:           cr.CaseID,
			Description:  c.Description,
			Status:       cr.Status,
			Observations: cr.Observations,
		})
		for _, f := range cr.Findings {
			if f.Code == CodeCancelled || f.Code == CodeRunTimeout {
				stoppedInFlight = true
			}
			col.Add(f)
		}
	}
	rep.Findings = append(rep.Findings, col.Items()...)
	rep.Dropped = col.Dropped()

	rep.AddLimitation(limitationTiming)
	if hasMutating {
		rep.AddLimitation(limitationSideEffects)
	}
	if hasStream && len(runs) == 2 {
		rep.AddLimitation(limitationStreamCompare)
	}
	if interrupted || stoppedInFlight {
		if rs.cancelledByCaller() {
			rep.Fail(report.StatusCancelled, "the run was cancelled before all cases completed")
		} else {
			rep.Fail(report.StatusIncomplete, fmt.Sprintf("the run deadline expired before all cases completed (run timeout %s)", rs.e.lim.RunTimeout))
		}
	}
	rep.Finalize()
	return rep
}

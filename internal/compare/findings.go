package compare

import (
	"fmt"
	"strconv"

	"github.com/happydev123/backendlab/internal/finding"
)

// Context identifies the comparison for findings.
type Context struct {
	CaseID          string
	BaselineTarget  string
	CandidateTarget string
}

func (c Context) details() map[string]string {
	d := map[string]string{}
	if c.BaselineTarget != "" {
		d["baseline_target"] = c.BaselineTarget
	}
	if c.CandidateTarget != "" {
		d["candidate_target"] = c.CandidateTarget
	}
	return d
}

// Findings converts r into findings of module compare. Differences become
// kind difference with severity error; entries that could not be decided
// become kind inconclusive with severity warning. Expected holds the
// baseline preview and Observed the candidate preview. Dropped differences
// are reported as one additional difference carrying the count, and
// numeric differences accepted by a tolerance as one info observation.
func (r Result) Findings(ctx Context) []finding.Finding {
	out := make([]finding.Finding, 0, len(r.Differences)+2)
	for _, d := range r.Differences {
		f := finding.Finding{
			Module:   finding.ModuleCompare,
			Code:     d.Code,
			Kind:     finding.KindDifference,
			Severity: finding.SeverityError,
			CaseID:   ctx.CaseID,
			Location: d.Location,
			Message:  d.Message,
			Expected: d.Baseline,
			Observed: d.Candidate,
			Details:  ctx.details(),
		}
		if d.Inconclusive {
			f.Kind = finding.KindInconclusive
			f.Severity = finding.SeverityWarning
		}
		out = append(out, f)
	}
	if r.Dropped > 0 {
		f := finding.Finding{
			Module:   finding.ModuleCompare,
			Code:     CodeDifferencesDropped,
			Kind:     finding.KindDifference,
			Severity: finding.SeverityError,
			CaseID:   ctx.CaseID,
			Message:  fmt.Sprintf("%d further differences were not listed (max_differences reached)", r.Dropped),
			Details:  ctx.details(),
		}
		out = append(out, f.WithDetail("dropped", strconv.Itoa(r.Dropped)))
	}
	if r.WithinTolerance > 0 {
		f := finding.Finding{
			Module:   finding.ModuleCompare,
			Code:     CodeWithinTolerance,
			Kind:     finding.KindObservation,
			Severity: finding.SeverityInfo,
			CaseID:   ctx.CaseID,
			Message:  fmt.Sprintf("%d numeric differences were within the configured tolerance", r.WithinTolerance),
			Details:  ctx.details(),
		}
		out = append(out, f.WithDetail("count", strconv.Itoa(r.WithinTolerance)))
	}
	return out
}

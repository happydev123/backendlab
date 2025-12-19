package replay

import (
	"context"

	"github.com/happydev123/backendlab/internal/compare"
	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/suite"
)

// Compare executes every case against baseline and then candidate and
// compares the two responses of each case with the case's effective
// comparison settings. Each target keeps its own captured variables, so a
// value captured from the baseline is only sent to the baseline. Cases in
// which either target produced no complete response are not compared;
// their transport errors are reported instead.
func (e *Executor) Compare(ctx context.Context, l *suite.Loaded, baseline, candidate *config.ResolvedTarget) (*Result, error) {
	return e.execute(ctx, "compare", l, []*config.ResolvedTarget{baseline, candidate}, compareResponses)
}

// compareResponses is the postCase step of compare mode.
func compareResponses(rs *runState, c *suite.Case, runs []*targetRun, outs []outcome) []finding.Finding {
	opts, err := compare.OptionsFromSpec(rs.l.Suite.EffectiveCompare(c), rs.e.lim, rs.redactor())
	if err != nil {
		return []finding.Finding{inconclusive(CodeCompareUnavailable, c.ID, "",
			"responses were not compared: "+rs.redactor().Error(err))}
	}
	res := compare.Responses(*outs[0].resp, *outs[1].resp, opts)
	return res.Findings(compare.Context{
		CaseID:          c.ID,
		BaselineTarget:  runs[0].target.Name,
		CandidateTarget: runs[1].target.Name,
	})
}

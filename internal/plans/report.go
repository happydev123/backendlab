package plans

import (
	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/report"
)

// Command is the report command name for plan comparisons.
const Command = "plans.compare"

// Report converts the comparison into a finalized report for the
// "plans.compare" command. inputs identify the two plan files (usually the
// values returned by ParseFile, before first). The report has no timing
// block and no timestamps, so the same inputs always render the same bytes.
func (c *Comparison) Report(inputs []report.Input) *report.Report {
	r := report.New(Command)
	if len(inputs) > 0 {
		r.Inputs = append(r.Inputs, inputs...)
	}
	r.AddFindings(c.Findings...)
	if r.Findings == nil {
		r.Findings = []finding.Finding{}
	}
	r.Dropped = c.Dropped
	for _, l := range c.Limitations {
		r.AddLimitation(l)
	}
	r.Finalize()
	return r
}

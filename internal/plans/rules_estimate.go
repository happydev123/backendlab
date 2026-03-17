package plans

import (
	"fmt"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/shopspring/decimal"
)

// estimate holds the row figures of one executed node. PostgreSQL reports
// "Plan Rows" as the estimate for one execution of the node and "Actual
// Rows" as the average over "Actual Loops" executions, so the per-loop
// values are directly comparable and totals are per-loop values times
// loops.
type estimate struct {
	planRows, actualRows, loops decimal.Decimal
	estTotal, actTotal          decimal.Decimal
	ratio                       decimal.Decimal
	under                       bool
}

// estimateOf returns the row figures of n. It fails for estimated plans,
// for nodes without row counts and for nodes that never ran (loops 0),
// whose per-loop values carry no information.
func estimateOf(n *Node) (estimate, bool) {
	pr, ok1 := value(n.PlanRows)
	ar, ok2 := value(n.ActualRows)
	lo, ok3 := value(n.ActualLoops)
	if !ok1 || !ok2 || !ok3 || lo.IsZero() {
		return estimate{}, false
	}
	return estimate{
		planRows: pr, actualRows: ar, loops: lo,
		estTotal: pr.Mul(lo), actTotal: ar.Mul(lo),
		ratio: rowRatio(pr, ar),
		under: ar.GreaterThan(pr),
	}, true
}

// mismatch reports whether e crosses both thresholds: the ratio is at
// least RowFactor and the larger of the two totals is at least MinRows.
// The floor keeps small nodes (estimated 1 row, got 40) out of the report.
func (c *comparer) mismatch(e estimate) bool {
	floor := decimal.NewFromInt(c.opts.MinRows)
	if decimal.Max(e.estTotal, e.actTotal).LessThan(floor) {
		return false
	}
	return e.ratio.GreaterThanOrEqual(decimal.NewFromFloat(c.opts.RowFactor))
}

// checkEstimate reports a large estimated-vs-actual discrepancy on a node of
// the after plan. m is the node's match, or nil when it has no
// counterpart; the before node's figures are added for context.
func (c *comparer) checkEstimate(a *Node, m *Match) {
	e, ok := estimateOf(a)
	if !ok || !c.mismatch(e) {
		return
	}
	direction := "overestimate"
	if e.under {
		direction = "underestimate"
	}
	msg := fmt.Sprintf("Row estimate for %s is off by %sx (%s): estimated %s rows per loop, actual %s rows per loop over %s",
		label(a), fmtDec(e.ratio), direction, fmtDec(e.planRows), fmtDec(e.actualRows), loopsText(e.loops))
	var f finding.Finding
	if m != nil {
		f = matchFinding(CodeEstimateMismatch, finding.KindDifference, finding.SeverityWarning, *m, msg)
	} else {
		f = afterFinding(CodeEstimateMismatch, finding.KindDifference, finding.SeverityWarning, a, msg)
	}
	f.Expected = fmt.Sprintf("%s rows estimated in total (%s per loop)", fmtDec(e.estTotal), fmtDec(e.planRows))
	f.Observed = fmt.Sprintf("%s rows in total (%s per loop x %s)", fmtDec(e.actTotal), fmtDec(e.actualRows), loopsText(e.loops))
	f.Details = merge(f.Details, map[string]string{
		"direction":  direction,
		"ratio":      fmtDec(e.ratio),
		"row_factor": fmtDec(decimal.NewFromFloat(c.opts.RowFactor)),
		"min_rows":   fmt.Sprint(c.opts.MinRows),
		"loops":      fmtDec(e.loops),
	})
	if m != nil {
		if be, ok := estimateOf(m.Before); ok {
			f.Details["before_ratio"] = fmtDec(be.ratio)
			f.Details["before_mismatch"] = fmt.Sprint(c.mismatch(be))
		} else {
			f.Details["before_ratio"] = "unknown"
		}
	}
	c.add(f)
}

// loopsText renders a loop count with the right noun form.
func loopsText(d decimal.Decimal) string {
	if d.Equal(decimal.NewFromInt(1)) {
		return "1 loop"
	}
	return fmtDec(d) + " loops"
}

// merge copies extra into base (allocating when base is nil).
func merge(base, extra map[string]string) map[string]string {
	if base == nil {
		base = make(map[string]string, len(extra))
	}
	for k, v := range extra {
		base[k] = v
	}
	return base
}

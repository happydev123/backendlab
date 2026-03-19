package plans

import (
	"fmt"
	"strings"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/shopspring/decimal"
)

// Observation thresholds. Observations never fail a comparison; the
// thresholds only keep small run-to-run noise out of the report.
const (
	// changeFactor is the ratio at which row estimates, node times and
	// loop counts are reported as changed.
	changeFactor = 2
	// minTimeChangeMS is the smallest absolute node time change reported.
	minTimeChangeMS = 1
)

const timingCaveat = "EXPLAIN ANALYZE timing includes instrumentation overhead and single runs vary"

// observePlan records the statement-level observations and the
// inconclusive findings for metrics only one side reports.
func (c *comparer) observePlan() {
	b, a := c.before, c.after
	if bc, ac := b.Root.TotalCost, a.Root.TotalCost; bc != nil && ac != nil && !bc.Decimal().Equal(ac.Decimal()) {
		f := newFinding(CodeCostChanged, finding.KindObservation, finding.SeverityInfo, "/Plan",
			fmt.Sprintf("Estimated total cost changed from %s to %s (planner cost units, not milliseconds)", bc, ac))
		f.Expected, f.Observed = bc.String(), ac.String()
		c.add(f)
	}
	switch {
	case b.Analyzed != a.Analyzed:
		side := "before"
		if a.Analyzed {
			side = "after"
		}
		c.add(newFinding(CodeRuntimeInconclusive, finding.KindInconclusive, finding.SeverityInfo, "/Plan",
			fmt.Sprintf("Only the %s plan comes from EXPLAIN ANALYZE; runtime comparisons (times, actual rows, loops, buffers, spills) are inconclusive", side)))
		c.limit("One plan is estimated and the other analyzed: only estimates and plan shape were compared; runtime metrics were not.")
		return
	case !b.Analyzed:
		c.limit("Both plans are estimated (EXPLAIN without ANALYZE): only plan shape, costs and row estimates were compared.")
		return
	}
	c.observeExecution()
	c.observeBuffers()
}

// observeExecution compares statement execution time.
func (c *comparer) observeExecution() {
	b, a := c.before, c.after
	if b.Timing != a.Timing {
		c.add(newFinding(CodeTimingInconclusive, finding.KindInconclusive, finding.SeverityInfo, "/Plan",
			"Only one plan reports node timings (the other used ANALYZE with TIMING OFF); node time comparisons are inconclusive"))
	}
	bt, at := b.ExecutionTime, a.ExecutionTime
	switch {
	case bt != nil && at != nil:
		if bt.Decimal().Equal(at.Decimal()) {
			return
		}
		f := newFinding(CodeExecutionTimeChanged, finding.KindObservation, finding.SeverityInfo, "/Execution Time",
			fmt.Sprintf("Execution time changed from %s ms to %s ms (%s)", bt, at, timingCaveat))
		f.Expected, f.Observed = bt.String()+" ms", at.String()+" ms"
		if b.PlanningTime != nil || a.PlanningTime != nil {
			f = f.WithDetail("planning_time_ms", fmtNum(b.PlanningTime)+" -> "+fmtNum(a.PlanningTime))
		}
		c.add(f)
	case bt != nil || at != nil:
		c.add(newFinding(CodeTimingInconclusive, finding.KindInconclusive, finding.SeverityInfo, "/Execution Time",
			"Only one plan reports Execution Time; the statement time comparison is inconclusive"))
	}
}

// observeBuffers compares the root node's cumulative block counters.
func (c *comparer) observeBuffers() {
	b, a := c.before, c.after
	if b.Buffers != a.Buffers {
		c.add(newFinding(CodeBuffersInconclusive, finding.KindInconclusive, finding.SeverityInfo, "/Plan",
			"Only one plan includes BUFFERS counters; buffer and temp I/O comparisons are inconclusive"))
		c.limit("Buffer counters are missing on one side: temp I/O findings could not be evaluated.")
		return
	}
	if !b.Buffers {
		return
	}
	rb, ra := b.Root.Buffers, a.Root.Buffers
	names := []string{"shared hit", "shared read", "shared dirtied", "shared written", "local hit", "local read", "local dirtied", "local written", "temp read", "temp written"}
	bs, as := rb.all(), ra.all()
	var parts []string
	f := newFinding(CodeBuffersChanged, finding.KindObservation, finding.SeverityInfo, "/Plan", "")
	for i, name := range names {
		bv, okB := value(bs[i])
		av, okA := value(as[i])
		if !okB || !okA || bv.Equal(av) {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s -> %s", name, bs[i], as[i]))
		f = f.WithDetail(strings.ReplaceAll(name, " ", "_")+"_blocks", bs[i].String()+" -> "+as[i].String())
	}
	if len(parts) == 0 {
		return
	}
	f.Message = "Root buffer counters changed (blocks, cumulative over the whole plan): " + strings.Join(parts, ", ")
	c.add(f)
}

// observeNode records per-node observations for a matched pair.
func (c *comparer) observeNode(m Match) {
	b, a := m.Before, m.After
	floor := decimal.NewFromInt(c.opts.MinRows)
	factor := decimal.NewFromInt(changeFactor)
	if br, ok1 := value(b.PlanRows); ok1 {
		if ar, ok2 := value(a.PlanRows); ok2 && !decimal.Max(br, ar).LessThan(floor) && rowRatio(br, ar).GreaterThanOrEqual(factor) {
			f := matchFinding(CodeRowsEstimateChanged, finding.KindObservation, finding.SeverityInfo, m,
				fmt.Sprintf("Estimated rows per loop for %s changed from %s to %s", label(a), b.PlanRows, a.PlanRows))
			f.Expected, f.Observed = b.PlanRows.String(), a.PlanRows.String()
			c.add(f)
		}
	}
	if !b.Executed() || !a.Executed() {
		return
	}
	bl, al := b.ActualLoops.Decimal(), a.ActualLoops.Decimal()
	if rowRatio(bl, al).GreaterThanOrEqual(factor) {
		f := matchFinding(CodeLoopsChanged, finding.KindObservation, finding.SeverityInfo, m,
			fmt.Sprintf("%s ran %s times instead of %s; actual rows and times are per-loop averages, so totals are multiplied by loops", label(a), a.ActualLoops, b.ActualLoops))
		f.Expected, f.Observed = b.ActualLoops.String()+" loops", a.ActualLoops.String()+" loops"
		c.add(f)
	}
	c.observeNodeTime(m)
}

// observeNodeTime compares total time (per-loop time times loops) for the
// root and for leaf nodes. Node times include their children, so reporting
// every inner node would repeat one slow leaf on all of its ancestors.
func (c *comparer) observeNodeTime(m Match) {
	b, a := m.Before, m.After
	if a.Depth != 0 && len(a.Plans) > 0 && a.NodeType != "Bitmap Heap Scan" {
		return
	}
	bt, ok1 := value(b.ActualTotalTime)
	at, ok2 := value(a.ActualTotalTime)
	if !ok1 || !ok2 {
		return
	}
	bTotal, aTotal := bt.Mul(b.ActualLoops.Decimal()), at.Mul(a.ActualLoops.Decimal())
	lo, hi := decimal.Min(bTotal, aTotal), decimal.Max(bTotal, aTotal)
	if hi.Sub(lo).LessThan(decimal.NewFromInt(minTimeChangeMS)) {
		return
	}
	if lo.IsPositive() && hi.LessThan(lo.Mul(decimal.NewFromInt(changeFactor))) {
		return
	}
	f := matchFinding(CodeActualTimeChanged, finding.KindObservation, finding.SeverityInfo, m,
		fmt.Sprintf("Total time of %s changed from %s ms to %s ms (per-loop time x loops; %s)", label(a), fmtDec(bTotal), fmtDec(aTotal), timingCaveat))
	f.Expected, f.Observed = fmtDec(bTotal)+" ms", fmtDec(aTotal)+" ms"
	c.add(f)
}

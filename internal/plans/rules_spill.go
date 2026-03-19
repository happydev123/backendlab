package plans

import (
	"fmt"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/shopspring/decimal"
)

// tempIncreaseFactor is how many times the before value a node's own temp
// blocks must reach to count as an increase. Small changes in temp usage
// between runs are normal and not reported.
const tempIncreaseFactor = 2

// checkSpill reports a matched node that stayed within work_mem before and
// spills to disk after: a disk or external sort, a hash with more than one
// batch, or a hash aggregate with batches or disk usage.
func (c *comparer) checkSpill(m Match) {
	as, adesc := m.After.spill()
	if as != spillDisk {
		return
	}
	bs, bdesc := m.Before.spill()
	switch bs {
	case spillDisk:
		return
	case spillUnknown:
		f := matchFinding(CodeSpillInconclusive, finding.KindInconclusive, finding.SeverityInfo, m,
			fmt.Sprintf("%s spills to disk in the after plan; the before plan does not report whether it spilled", label(m.After)))
		f.Observed = adesc
		c.add(f)
		return
	}
	msg := fmt.Sprintf("%s now spills to disk (it stayed in memory before)", label(m.After))
	f := matchFinding(CodeSortSpillIntroduced, finding.KindDifference, finding.SeverityWarning, m, msg)
	f.Expected, f.Observed = bdesc, adesc
	if bs == spillNotApplicable {
		f.Expected = "no spill-capable operation (" + m.Before.Operation() + ")"
	}
	c.add(f)
}

// checkSpillAdded reports an after node without counterpart that spills.
// It is only a regression signal when the before plan was analyzed;
// otherwise there is nothing to compare with.
func (c *comparer) checkSpillAdded(a *Node) {
	st, desc := a.spill()
	if st != spillDisk {
		return
	}
	if !c.before.Analyzed {
		return
	}
	f := afterFinding(CodeSortSpillIntroduced, finding.KindDifference, finding.SeverityWarning, a,
		fmt.Sprintf("%s spills to disk and has no counterpart in the before plan", label(a)))
	f.Observed = desc
	c.add(f)
}

// checkTemp reports temporary-file I/O that appears or grows on a matched
// node. It compares each node's own temp blocks (see Node.SelfTemp) so
// one spilling node is not reported again on every ancestor.
func (c *comparer) checkTemp(m Match) {
	at, bt := m.After.SelfTemp(), m.Before.SelfTemp()
	if at == nil || at.IsZero() || bt == nil {
		return
	}
	var msg string
	switch {
	case bt.IsZero():
		msg = fmt.Sprintf("%s now reads or writes %s temp blocks (none before)", label(m.After), at)
	case at.Decimal().GreaterThanOrEqual(bt.Decimal().Mul(decimal.NewFromInt(tempIncreaseFactor))):
		msg = fmt.Sprintf("%s temp blocks grew from %s to %s", label(m.After), bt, at)
	default:
		return
	}
	f := matchFinding(CodeTempIOIntroduced, finding.KindDifference, finding.SeverityWarning, m, msg)
	f.Expected = bt.String() + " temp blocks"
	f.Observed = at.String() + " temp blocks"
	f = f.WithDetail("temp_read", fmtNum(m.After.Buffers.TempRead)).WithDetail("temp_written", fmtNum(m.After.Buffers.TempWritten))
	c.add(f)
}

// checkTempAdded reports temp I/O on an after node without counterpart,
// when the before plan has buffer counters to compare with.
func (c *comparer) checkTempAdded(a *Node) {
	at := a.SelfTemp()
	if at == nil || at.IsZero() || !c.before.Buffers {
		return
	}
	f := afterFinding(CodeTempIOIntroduced, finding.KindDifference, finding.SeverityWarning, a,
		fmt.Sprintf("%s reads or writes %s temp blocks and has no counterpart in the before plan", label(a), at))
	f.Observed = at.String() + " temp blocks"
	c.add(f)
}

package plans

import (
	"fmt"
	"strings"

	"github.com/happydev123/backendlab/internal/finding"
)

// checkScan reports a changed access path for the same relation: for
// example Index Scan to Seq Scan, a different index, a bitmap scan, an
// index-only scan, or a switch to a parallel scan. Scans of different
// relations (possible after a low-confidence positional match) are not
// compared.
func (c *comparer) checkScan(m Match) {
	b, a := m.Before, m.After
	if !relationScan(b.NodeType) || !relationScan(a.NodeType) {
		return
	}
	if b.Relation() != a.Relation() {
		return
	}
	bp, ap := b.accessPath(), a.accessPath()
	if bp == ap {
		return
	}
	rel := a.Relation()
	msg := fmt.Sprintf("Scan strategy on %s changed from %s to %s", rel, bp, ap)
	f := matchFinding(CodeScanStrategyChanged, finding.KindDifference, finding.SeverityWarning, m, msg)
	f.Expected, f.Observed = bp, ap
	f = f.WithDetail("relation", rel)
	if b.Executed() && a.Executed() {
		f = f.WithDetail("rows_removed_by_filter", fmtNum(b.RowsRemovedByFilter)+" -> "+fmtNum(a.RowsRemovedByFilter))
	}
	c.add(f)
}

// checkJoin reports a changed join algorithm (Nested Loop, Hash Join,
// Merge Join) and a changed join type (Inner, Left, Semi, Anti, ...).
func (c *comparer) checkJoin(m Match) {
	b, a := m.Before, m.After
	if b.Family() != FamilyJoin || a.Family() != FamilyJoin {
		return
	}
	typeChanged := b.NodeType != a.NodeType
	joinTypeChanged := b.JoinType != a.JoinType
	if !typeChanged && !joinTypeChanged {
		return
	}
	var parts []string
	if typeChanged {
		parts = append(parts, fmt.Sprintf("join strategy changed from %s to %s", b.NodeType, a.NodeType))
	}
	if joinTypeChanged {
		parts = append(parts, fmt.Sprintf("join type changed from %s to %s", orUnknown(b.JoinType), orUnknown(a.JoinType)))
	}
	msg := strings.ToUpper(parts[0][:1]) + strings.Join(parts, "; ")[1:]
	f := matchFinding(CodeJoinStrategyChanged, finding.KindDifference, finding.SeverityWarning, m, msg)
	f.Expected, f.Observed = b.Operation(), a.Operation()
	if rels := subtreeRelations(a); len(rels) > 0 {
		f = f.WithDetail("relations", strings.Join(rels, ", "))
	}
	if inner := childByRelationship(a, "Inner"); a.NodeType == "Nested Loop" && a.Executed() && inner != nil {
		// The inner side of a nested loop runs once per outer row; its
		// loop count shows how often.
		f = f.WithDetail("inner_loops", fmtNum(inner.ActualLoops))
	}
	c.add(f)
}

// childByRelationship returns the first child with the given "Parent
// Relationship". Init plans are listed before the outer and inner children,
// so positions cannot be used.
func childByRelationship(n *Node, rel string) *Node {
	for _, c := range n.Plans {
		if c.ParentRelationship == rel {
			return c
		}
	}
	return nil
}

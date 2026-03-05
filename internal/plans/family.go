package plans

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

// Family groups node types that play the same role in a plan. Matching
// only pairs nodes of the same family, and strategy rules compare node
// types within a family.
type Family string

const (
	FamilyScan        Family = "scan"
	FamilyJoin        Family = "join"
	FamilyAggregate   Family = "aggregate"
	FamilySort        Family = "sort"
	FamilyHash        Family = "hash"
	FamilyMaterialize Family = "materialize"
	FamilyGather      Family = "gather"
	FamilyAppend      Family = "append"
	FamilySetOp       Family = "setop"
	FamilyLimit       Family = "limit"
	FamilyModify      Family = "modify"
	FamilyResult      Family = "result"
	FamilyOther       Family = "other"
)

// FamilyOf classifies a "Node Type" value. Unrecognized types (for example
// from extensions or future PostgreSQL versions) are FamilyOther.
func FamilyOf(nodeType string) Family {
	switch nodeType {
	case "Seq Scan", "Index Scan", "Index Only Scan", "Bitmap Heap Scan", "Bitmap Index Scan",
		"Tid Scan", "Tid Range Scan", "Sample Scan", "Function Scan", "Table Function Scan",
		"Values Scan", "CTE Scan", "Named Tuplestore Scan", "WorkTable Scan", "Foreign Scan",
		"Custom Scan", "Subquery Scan":
		return FamilyScan
	case "Nested Loop", "Hash Join", "Merge Join":
		return FamilyJoin
	case "Aggregate", "Group", "WindowAgg":
		return FamilyAggregate
	case "Sort", "Incremental Sort":
		return FamilySort
	case "Hash":
		return FamilyHash
	case "Materialize", "Memoize":
		return FamilyMaterialize
	case "Gather", "Gather Merge":
		return FamilyGather
	case "Append", "Merge Append", "Recursive Union", "BitmapAnd", "BitmapOr":
		return FamilyAppend
	case "SetOp", "Unique":
		return FamilySetOp
	case "Limit":
		return FamilyLimit
	case "ModifyTable", "LockRows":
		return FamilyModify
	case "Result", "ProjectSet":
		return FamilyResult
	}
	return FamilyOther
}

// Family returns the node's family.
func (n *Node) Family() Family { return FamilyOf(n.NodeType) }

// relationScan reports whether t reads a table directly. Only these
// types take part in scan strategy comparison; function, CTE and
// subquery scans have no alternative access path to compare.
func relationScan(t string) bool {
	switch t {
	case "Seq Scan", "Index Scan", "Index Only Scan", "Bitmap Heap Scan", "Tid Scan",
		"Tid Range Scan", "Sample Scan":
		return true
	}
	return false
}

// parallel reports whether the node is marked "Parallel Aware".
func (n *Node) parallel() bool { return n.ParallelAware != nil && *n.ParallelAware }

// Relation returns the relation name qualified with the schema when the
// plan includes one (EXPLAIN VERBOSE).
func (n *Node) Relation() string {
	if n.RelationName == "" {
		return ""
	}
	if n.Schema != "" {
		return n.Schema + "." + n.RelationName
	}
	return n.RelationName
}

// Operation returns the operation name as EXPLAIN's text format prints it,
// for example "Parallel Seq Scan", "Hash Left Join", "Partial
// HashAggregate" or "Nested Loop Anti Join".
func (n *Node) Operation() string {
	op := n.NodeType
	switch n.Family() {
	case FamilyJoin:
		if n.JoinType != "" && n.JoinType != "Inner" {
			if op == "Nested Loop" {
				op += " " + n.JoinType + " Join"
			} else {
				op = strings.TrimSuffix(op, " Join") + " " + n.JoinType + " Join"
			}
		}
	case FamilyAggregate:
		if op == "Aggregate" {
			switch n.Strategy {
			case "Hashed":
				op = "HashAggregate"
			case "Sorted":
				op = "GroupAggregate"
			case "Mixed":
				op = "MixedAggregate"
			}
		}
		switch n.PartialMode {
		case "Partial":
			op = "Partial " + op
		case "Finalize":
			op = "Finalize " + op
		}
	}
	if n.parallel() {
		op = "Parallel " + op
	}
	return op
}

// Label returns a short readable description of the node, for example
// "Index Scan using invoices_pkey on invoices i" or "CTE Scan on recent".
// A subplan name is appended in brackets.
func (n *Node) Label() string {
	var b strings.Builder
	b.WriteString(n.Operation())
	if n.IndexName != "" {
		b.WriteString(" using " + n.IndexName)
	}
	target := n.Relation()
	switch {
	case target != "":
	case n.CTEName != "":
		target = n.CTEName
	case n.FunctionName != "":
		target = n.FunctionName
	case n.Alias != "" && n.Family() == FamilyScan:
		target = n.Alias
	}
	if target != "" {
		b.WriteString(" on " + target)
		if n.Alias != "" && n.Alias != target && n.Alias != n.RelationName {
			b.WriteString(" " + n.Alias)
		}
	}
	if n.SubplanName != "" {
		b.WriteString(" [" + n.SubplanName + "]")
	}
	return b.String()
}

// accessPath describes how a relation scan reads its table: the operation
// plus the index when one is used. Two scans of the same relation with
// different access paths are a scan strategy change.
// A Bitmap Heap Scan takes its indexes from the Bitmap Index Scan nodes
// below it.
func (n *Node) accessPath() string {
	if n.IndexName != "" {
		return n.Operation() + " using " + n.IndexName
	}
	if n.NodeType == "Bitmap Heap Scan" {
		var idx []string
		for _, c := range n.Plans {
			walk(c, func(d *Node) bool {
				if d.NodeType == "Bitmap Index Scan" && d.IndexName != "" {
					idx = append(idx, d.IndexName)
				}
				return true
			})
		}
		if len(idx) > 0 {
			return n.Operation() + " using " + strings.Join(idx, ", ")
		}
	}
	return n.Operation()
}

// spillState says whether a node that can use work_mem ran out of it.
type spillState int

const (
	// spillNotApplicable: the node type never spills to disk by itself.
	spillNotApplicable spillState = iota
	// spillUnknown: the node can spill but the plan does not say (for
	// example an estimated plan).
	spillUnknown
	// spillMemory: the node reports that it stayed in memory.
	spillMemory
	// spillDisk: the node reports a disk sort, extra hash batches or
	// aggregate disk usage.
	spillDisk
)

// spill classifies the node and describes the evidence.
func (n *Node) spill() (spillState, string) {
	switch {
	case n.Family() == FamilySort:
		if n.SortSpaceType == "Disk" || strings.HasPrefix(n.SortMethod, "external") {
			return spillDisk, fmt.Sprintf("Sort Method %s, Sort Space %s kB on %s", orUnknown(n.SortMethod), fmtNum(n.SortSpaceUsed), orUnknown(n.SortSpaceType))
		}
		if n.SortSpaceType != "" || n.SortMethod != "" {
			return spillMemory, fmt.Sprintf("Sort Method %s, Sort Space %s kB on %s", orUnknown(n.SortMethod), fmtNum(n.SortSpaceUsed), orUnknown(n.SortSpaceType))
		}
		return spillUnknown, "no sort method reported"
	case n.NodeType == "Hash":
		b, ok := value(n.HashBatches)
		if !ok {
			return spillUnknown, "no hash batches reported"
		}
		desc := fmt.Sprintf("Hash Batches %s (originally %s), Peak Memory %s kB", n.HashBatches, fmtNum(n.OriginalHashBatches), fmtNum(n.PeakMemoryUsage))
		if b.GreaterThan(decimal.NewFromInt(1)) {
			return spillDisk, desc
		}
		return spillMemory, desc
	case n.NodeType == "Aggregate" && (n.Strategy == "Hashed" || n.Strategy == "Mixed"):
		batches, okB := value(n.HashAggBatches)
		disk, okD := value(n.DiskUsage)
		if !okB && !okD {
			return spillUnknown, "no HashAgg batches reported"
		}
		desc := fmt.Sprintf("HashAgg Batches %s, Disk Usage %s kB", fmtNum(n.HashAggBatches), fmtNum(n.DiskUsage))
		if (okB && batches.GreaterThan(decimal.NewFromInt(1))) || (okD && disk.IsPositive()) {
			return spillDisk, desc
		}
		return spillMemory, desc
	}
	return spillNotApplicable, ""
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

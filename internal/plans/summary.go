package plans

import "sort"

// Summary describes one plan at a glance for listings and the dashboard.
// Nil numbers are unknown (for example timings of an estimated plan).
type Summary struct {
	Name      string `json:"name"`
	NodeCount int    `json:"node_count"`
	MaxDepth  int    `json:"max_depth"`
	Analyzed  bool   `json:"analyzed"`
	Buffers   bool   `json:"buffers"`
	Timing    bool   `json:"timing"`
	// Relations lists the distinct relations read by scans, sorted.
	Relations []string `json:"relations"`
	// ScanStrategies and JoinStrategies count nodes by operation, for
	// example {"Index Scan": 2, "Seq Scan": 1} and {"Hash Join": 1}.
	ScanStrategies map[string]int `json:"scan_strategies"`
	JoinStrategies map[string]int `json:"join_strategies"`
	// TotalCost and PlanRows come from the root node. Cost is in planner
	// units, not milliseconds.
	TotalCost  *Number `json:"total_cost,omitempty"`
	PlanRows   *Number `json:"plan_rows,omitempty"`
	ActualRows *Number `json:"actual_rows,omitempty"`
	// PlanningTime and ExecutionTime are milliseconds from EXPLAIN ANALYZE.
	PlanningTime  *Number `json:"planning_time_ms,omitempty"`
	ExecutionTime *Number `json:"execution_time_ms,omitempty"`
	// SharedBlocks (hit plus read) and TempBlocks (read plus written) are
	// the root node's cumulative counters.
	SharedBlocks *Number `json:"shared_blocks,omitempty"`
	TempBlocks   *Number `json:"temp_blocks,omitempty"`
	// Spills counts nodes that report a disk sort, extra hash batches or
	// aggregate disk usage.
	Spills int `json:"spills"`
	// NeverExecuted counts nodes with Actual Loops 0.
	NeverExecuted int `json:"never_executed"`
	UnknownKeys   int `json:"unknown_keys"`
}

// Summarize computes the Summary of p. It returns a zero Summary with empty
// lists for a nil plan.
func Summarize(p *Plan) Summary {
	s := Summary{Relations: []string{}, ScanStrategies: map[string]int{}, JoinStrategies: map[string]int{}}
	if p == nil {
		return s
	}
	s.Name = p.Name
	s.NodeCount = p.NodeCount
	s.MaxDepth = p.MaxDepth
	s.Analyzed, s.Buffers, s.Timing = p.Analyzed, p.Buffers, p.Timing
	s.PlanningTime, s.ExecutionTime = p.PlanningTime, p.ExecutionTime
	s.UnknownKeys = p.UnknownKeys
	if r := p.Root; r != nil {
		s.TotalCost, s.PlanRows, s.ActualRows = r.TotalCost, r.PlanRows, r.ActualRows
		s.SharedBlocks = sumKnown(r.Buffers.SharedHit, r.Buffers.SharedRead)
		s.TempBlocks = r.Buffers.Temp()
	}
	relations := map[string]bool{}
	p.Walk(func(n *Node) bool {
		switch n.Family() {
		case FamilyScan:
			s.ScanStrategies[n.Operation()]++
			if rel := n.Relation(); rel != "" {
				relations[rel] = true
			}
		case FamilyJoin:
			s.JoinStrategies[n.Operation()]++
		}
		if st, _ := n.spill(); st == spillDisk {
			s.Spills++
		}
		if n.ActualLoops != nil && n.ActualLoops.IsZero() {
			s.NeverExecuted++
		}
		return true
	})
	for r := range relations {
		s.Relations = append(s.Relations, r)
	}
	sort.Strings(s.Relations)
	return s
}

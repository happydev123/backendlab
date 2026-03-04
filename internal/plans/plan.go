// Package plans implements QueryLens: offline parsing and comparison of
// PostgreSQL EXPLAIN (FORMAT JSON) output. It reads plans that were saved to
// files, aligns the nodes of two plans with a documented heuristic, and
// reports strategy changes, estimate mismatches, spills and temporary I/O as
// findings. It never connects to a database and never runs SQL.
package plans

import "github.com/shopspring/decimal"

// Plan is one parsed EXPLAIN statement.
type Plan struct {
	// Name identifies the input (usually the file path) in messages.
	Name string `json:"name"`
	Root *Node  `json:"plan"`

	// Statement-level values. Nil means the plan did not contain them.
	PlanningTime  *Number   `json:"planning_time_ms,omitempty"`
	ExecutionTime *Number   `json:"execution_time_ms,omitempty"`
	Triggers      []Trigger `json:"triggers,omitempty"`
	JIT           *JIT      `json:"jit,omitempty"`
	// Settings lists planner settings changed from their defaults
	// (EXPLAIN SETTINGS). encoding/json writes the keys in sorted order.
	Settings map[string]string `json:"settings,omitempty"`

	// Analyzed is true when any node carries "Actual Loops", that is the
	// plan came from EXPLAIN ANALYZE.
	Analyzed bool `json:"analyzed"`
	// Buffers is true when any node carries a block counter
	// (EXPLAIN (ANALYZE, BUFFERS)).
	Buffers bool `json:"buffers"`
	// Timing is true when any node carries "Actual Total Time". ANALYZE
	// with TIMING OFF reports rows and loops but no times.
	Timing bool `json:"timing"`

	NodeCount int `json:"node_count"`
	MaxDepth  int `json:"max_depth"`
	// UnknownKeys counts keys this package does not recognize, across the
	// statement and every node. They are ignored. UnknownKeyNames holds
	// up to maxUnknownNames distinct names in sorted order.
	UnknownKeys     int      `json:"unknown_keys,omitempty"`
	UnknownKeyNames []string `json:"unknown_key_names,omitempty"`
}

// Trigger is one entry of the "Triggers" list of an analyzed statement.
type Trigger struct {
	Name     string  `json:"name"`
	Relation string  `json:"relation,omitempty"`
	Time     *Number `json:"time_ms,omitempty"`
	Calls    *Number `json:"calls,omitempty"`
}

// JIT holds the summary of the "JIT" block when present.
type JIT struct {
	Functions *Number `json:"functions,omitempty"`
	TotalTime *Number `json:"total_time_ms,omitempty"`
}

// Node is one plan node. String fields are empty when absent; numeric
// fields are nil when absent, so an unknown metric is never read as zero.
type Node struct {
	// Path is the JSON Pointer of the node inside the statement object,
	// for example "/Plan/Plans/0". Depth is 0 for the root.
	Path  string `json:"path"`
	Depth int    `json:"depth"`

	NodeType           string `json:"node_type"`
	ParentRelationship string `json:"parent_relationship,omitempty"`
	SubplanName        string `json:"subplan_name,omitempty"`
	RelationName       string `json:"relation_name,omitempty"`
	Schema             string `json:"schema,omitempty"`
	Alias              string `json:"alias,omitempty"`
	IndexName          string `json:"index_name,omitempty"`
	CTEName            string `json:"cte_name,omitempty"`
	FunctionName       string `json:"function_name,omitempty"`
	JoinType           string `json:"join_type,omitempty"`
	Strategy           string `json:"strategy,omitempty"`
	PartialMode        string `json:"partial_mode,omitempty"`
	ParallelAware      *bool  `json:"parallel_aware,omitempty"`

	StartupCost *Number `json:"startup_cost,omitempty"`
	TotalCost   *Number `json:"total_cost,omitempty"`
	PlanRows    *Number `json:"plan_rows,omitempty"`
	PlanWidth   *Number `json:"plan_width,omitempty"`

	// Actual values are averages per loop, as PostgreSQL reports them.
	ActualStartupTime       *Number `json:"actual_startup_time_ms,omitempty"`
	ActualTotalTime         *Number `json:"actual_total_time_ms,omitempty"`
	ActualRows              *Number `json:"actual_rows,omitempty"`
	ActualLoops             *Number `json:"actual_loops,omitempty"`
	RowsRemovedByFilter     *Number `json:"rows_removed_by_filter,omitempty"`
	RowsRemovedByJoinFilter *Number `json:"rows_removed_by_join_filter,omitempty"`

	Buffers  Buffers  `json:"buffers"`
	IOTiming IOTiming `json:"io_timing"`

	SortKey             []string `json:"sort_key,omitempty"`
	SortMethod          string   `json:"sort_method,omitempty"`
	SortSpaceUsed       *Number  `json:"sort_space_used_kb,omitempty"`
	SortSpaceType       string   `json:"sort_space_type,omitempty"`
	HashBuckets         *Number  `json:"hash_buckets,omitempty"`
	HashBatches         *Number  `json:"hash_batches,omitempty"`
	OriginalHashBatches *Number  `json:"original_hash_batches,omitempty"`
	PeakMemoryUsage     *Number  `json:"peak_memory_usage_kb,omitempty"`
	// HashAggBatches and DiskUsage come from analyzed HashAggregate nodes
	// (PostgreSQL 13 and later) and show aggregate spills.
	HashAggBatches  *Number `json:"hashagg_batches,omitempty"`
	DiskUsage       *Number `json:"disk_usage_kb,omitempty"`
	WorkersPlanned  *Number `json:"workers_planned,omitempty"`
	WorkersLaunched *Number `json:"workers_launched,omitempty"`

	Filter      string `json:"filter,omitempty"`
	IndexCond   string `json:"index_cond,omitempty"`
	RecheckCond string `json:"recheck_cond,omitempty"`
	HashCond    string `json:"hash_cond,omitempty"`
	MergeCond   string `json:"merge_cond,omitempty"`
	JoinFilter  string `json:"join_filter,omitempty"`

	Plans []*Node `json:"plans,omitempty"`
}

// Buffers holds the block counters of EXPLAIN (BUFFERS). PostgreSQL reports
// temporary blocks as read and written only. Counters are cumulative: a
// node's values include those of its children.
type Buffers struct {
	SharedHit     *Number `json:"shared_hit,omitempty"`
	SharedRead    *Number `json:"shared_read,omitempty"`
	SharedDirtied *Number `json:"shared_dirtied,omitempty"`
	SharedWritten *Number `json:"shared_written,omitempty"`
	LocalHit      *Number `json:"local_hit,omitempty"`
	LocalRead     *Number `json:"local_read,omitempty"`
	LocalDirtied  *Number `json:"local_dirtied,omitempty"`
	LocalWritten  *Number `json:"local_written,omitempty"`
	TempRead      *Number `json:"temp_read,omitempty"`
	TempWritten   *Number `json:"temp_written,omitempty"`
}

// Known reports whether any counter is present.
func (b Buffers) Known() bool {
	for _, p := range b.all() {
		if p != nil {
			return true
		}
	}
	return false
}

// Temp returns temp read plus temp written blocks, or nil when neither is
// reported.
func (b Buffers) Temp() *Number { return sumKnown(b.TempRead, b.TempWritten) }

func (b Buffers) all() []*Number {
	return []*Number{
		b.SharedHit, b.SharedRead, b.SharedDirtied, b.SharedWritten,
		b.LocalHit, b.LocalRead, b.LocalDirtied, b.LocalWritten,
		b.TempRead, b.TempWritten,
	}
}

// IOTiming holds I/O timings (track_io_timing). PostgreSQL 16 and earlier
// report "I/O Read Time"/"I/O Write Time"; PostgreSQL 17 splits shared and
// local timings. Temp timings exist from PostgreSQL 15.
type IOTiming struct {
	ReadTime       *Number `json:"read_time_ms,omitempty"`
	WriteTime      *Number `json:"write_time_ms,omitempty"`
	SharedReadTime *Number `json:"shared_read_time_ms,omitempty"`
	SharedWrite    *Number `json:"shared_write_time_ms,omitempty"`
	LocalReadTime  *Number `json:"local_read_time_ms,omitempty"`
	LocalWrite     *Number `json:"local_write_time_ms,omitempty"`
	TempReadTime   *Number `json:"temp_read_time_ms,omitempty"`
	TempWrite      *Number `json:"temp_write_time_ms,omitempty"`
}

// Walk visits every node in pre-order (parent before children, children in
// plan order). Visiting stops early when fn returns false.
func (p *Plan) Walk(fn func(*Node) bool) {
	if p == nil || p.Root == nil {
		return
	}
	walk(p.Root, fn)
}

func walk(n *Node, fn func(*Node) bool) bool {
	if !fn(n) {
		return false
	}
	for _, c := range n.Plans {
		if !walk(c, fn) {
			return false
		}
	}
	return true
}

// Nodes returns every node in pre-order.
func (p *Plan) Nodes() []*Node {
	var out []*Node
	p.Walk(func(n *Node) bool { out = append(out, n); return true })
	return out
}

// Executed reports whether the node ran at least once in an analyzed
// plan. It is false for estimated plans and for nodes PostgreSQL marks as
// never executed (Actual Loops 0).
func (n *Node) Executed() bool {
	return n.ActualLoops != nil && !n.ActualLoops.IsZero()
}

// SelfTemp returns the node's own temporary blocks: its temp counters minus
// those of its direct children, clamped at zero. Buffer counters in EXPLAIN
// are cumulative, so without this a single spilling sort would show temp
// I/O on every ancestor. Nil means the node reports no temp counters.
func (n *Node) SelfTemp() *Number {
	own := n.Buffers.Temp()
	if own == nil {
		return nil
	}
	d := own.Decimal()
	for _, c := range n.Plans {
		if ct, ok := value(c.Buffers.Temp()); ok {
			d = d.Sub(ct)
		}
	}
	if d.IsNegative() {
		d = decimal.Zero
	}
	v := numberFromDecimal(d)
	return &v
}

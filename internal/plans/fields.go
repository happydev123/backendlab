package plans

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/happydev123/backendlab/internal/jsonutil"
)

// setField assigns one recognized node key. It returns known=false for keys
// the model does not hold, and an error when a recognized key has the
// wrong JSON type. "Plans" is handled by the tree decoder.
func (n *Node) setField(key string, v any) (known bool, err error) {
	switch key {
	case "Node Type":
		return true, setString(&n.NodeType, v)
	case "Parent Relationship":
		return true, setString(&n.ParentRelationship, v)
	case "Subplan Name":
		return true, setString(&n.SubplanName, v)
	case "Relation Name":
		return true, setString(&n.RelationName, v)
	case "Schema":
		return true, setString(&n.Schema, v)
	case "Alias":
		return true, setString(&n.Alias, v)
	case "Index Name":
		return true, setString(&n.IndexName, v)
	case "CTE Name":
		return true, setString(&n.CTEName, v)
	case "Function Name":
		return true, setString(&n.FunctionName, v)
	case "Join Type":
		return true, setString(&n.JoinType, v)
	case "Strategy":
		return true, setString(&n.Strategy, v)
	case "Partial Mode":
		return true, setString(&n.PartialMode, v)
	case "Parallel Aware":
		b, ok := v.(bool)
		if !ok {
			return true, typeError("boolean", v)
		}
		n.ParallelAware = &b
		return true, nil
	case "Sort Method":
		return true, setString(&n.SortMethod, v)
	case "Sort Space Type":
		return true, setString(&n.SortSpaceType, v)
	case "Filter":
		return true, setString(&n.Filter, v)
	case "Index Cond":
		return true, setString(&n.IndexCond, v)
	case "Recheck Cond":
		return true, setString(&n.RecheckCond, v)
	case "Hash Cond":
		return true, setString(&n.HashCond, v)
	case "Merge Cond":
		return true, setString(&n.MergeCond, v)
	case "Join Filter":
		return true, setString(&n.JoinFilter, v)
	case "Sort Key":
		return true, setStrings(&n.SortKey, v)
	}
	if p := n.numberField(key); p != nil {
		return true, setNumber(p, v)
	}
	return false, nil
}

// numberField returns the destination of a numeric key, or nil.
func (n *Node) numberField(key string) **Number {
	switch key {
	case "Startup Cost":
		return &n.StartupCost
	case "Total Cost":
		return &n.TotalCost
	case "Plan Rows":
		return &n.PlanRows
	case "Plan Width":
		return &n.PlanWidth
	case "Actual Startup Time":
		return &n.ActualStartupTime
	case "Actual Total Time":
		return &n.ActualTotalTime
	case "Actual Rows":
		return &n.ActualRows
	case "Actual Loops":
		return &n.ActualLoops
	case "Rows Removed by Filter":
		return &n.RowsRemovedByFilter
	case "Rows Removed by Join Filter":
		return &n.RowsRemovedByJoinFilter
	case "Sort Space Used":
		return &n.SortSpaceUsed
	case "Hash Buckets":
		return &n.HashBuckets
	case "Hash Batches":
		return &n.HashBatches
	case "Original Hash Batches":
		return &n.OriginalHashBatches
	case "Peak Memory Usage":
		return &n.PeakMemoryUsage
	case "HashAgg Batches":
		return &n.HashAggBatches
	case "Disk Usage":
		return &n.DiskUsage
	case "Workers Planned":
		return &n.WorkersPlanned
	case "Workers Launched":
		return &n.WorkersLaunched
	}
	if p := n.Buffers.field(key); p != nil {
		return p
	}
	return n.IOTiming.field(key)
}

// field returns the destination of a block counter key, or nil.
func (b *Buffers) field(key string) **Number {
	switch key {
	case "Shared Hit Blocks":
		return &b.SharedHit
	case "Shared Read Blocks":
		return &b.SharedRead
	case "Shared Dirtied Blocks":
		return &b.SharedDirtied
	case "Shared Written Blocks":
		return &b.SharedWritten
	case "Local Hit Blocks":
		return &b.LocalHit
	case "Local Read Blocks":
		return &b.LocalRead
	case "Local Dirtied Blocks":
		return &b.LocalDirtied
	case "Local Written Blocks":
		return &b.LocalWritten
	case "Temp Read Blocks":
		return &b.TempRead
	case "Temp Written Blocks":
		return &b.TempWritten
	}
	return nil
}

// field returns the destination of an I/O timing key, or nil.
func (t *IOTiming) field(key string) **Number {
	switch key {
	case "I/O Read Time":
		return &t.ReadTime
	case "I/O Write Time":
		return &t.WriteTime
	case "Shared I/O Read Time":
		return &t.SharedReadTime
	case "Shared I/O Write Time":
		return &t.SharedWrite
	case "Local I/O Read Time":
		return &t.LocalReadTime
	case "Local I/O Write Time":
		return &t.LocalWrite
	case "Temp I/O Read Time":
		return &t.TempReadTime
	case "Temp I/O Write Time":
		return &t.TempWrite
	}
	return nil
}

// ignoredNodeKey lists keys PostgreSQL emits that the model deliberately
// does not hold. They are neither stored nor counted as unknown.
func ignoredNodeKey(key string) bool {
	switch key {
	case "Async Capable", "Scan Direction", "Output", "Inner Unique", "Single Copy",
		"Group Key", "Grouping Sets", "Presorted Key", "Full-sort Groups", "Pre-sorted Groups",
		"Rows Removed by Index Recheck", "Heap Fetches", "Exact Heap Blocks", "Lossy Heap Blocks",
		"Original Hash Buckets", "Planned Partitions", "Memory Usage", "Cache Key", "Cache Mode",
		"Cache Hits", "Cache Misses", "Cache Evictions", "Cache Overflows", "Workers",
		"One-Time Filter", "Subplans Removed", "Order By", "Operation", "Command",
		"Sampling Method", "Sampling Parameters", "Repeatable", "Table Function Name",
		"Conflict Resolution", "Conflict Arbiter Indexes", "Conflict Filter",
		"Rows Removed by Conflict Filter", "Tuples Inserted", "Conflicting Tuples",
		"Target Tables", "TID Cond", "Run Condition", "Window", "Disabled",
		"Storage", "Maximum Storage", "Index Searches", "Remote SQL", "Custom Plan Provider":
		return true
	}
	return false
}

func typeError(want string, v any) error {
	article := "a"
	if strings.ContainsRune("aeiou", rune(want[0])) {
		article = "an"
	}
	return fmt.Errorf("must be %s %s, got %s", article, want, jsonutil.TypeName(v))
}

func setString(dst *string, v any) error {
	s, ok := v.(string)
	if !ok {
		return typeError("string", v)
	}
	*dst = s
	return nil
}

func setStrings(dst *[]string, v any) error {
	arr, ok := v.([]any)
	if !ok {
		return typeError("array of strings", v)
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, ok := e.(string)
		if !ok {
			return typeError("array of strings", v)
		}
		out = append(out, s)
	}
	*dst = out
	return nil
}

func setNumber(dst **Number, v any) error {
	jn, ok := v.(json.Number)
	if !ok {
		return typeError("number", v)
	}
	n, err := parseNumber(jn)
	if err != nil {
		return err
	}
	*dst = &n
	return nil
}

package plans

import "testing"

func TestFamilyOf(t *testing.T) {
	cases := map[string]Family{
		"Seq Scan": FamilyScan, "Index Only Scan": FamilyScan, "CTE Scan": FamilyScan,
		"Bitmap Heap Scan": FamilyScan, "Subquery Scan": FamilyScan,
		"Nested Loop": FamilyJoin, "Hash Join": FamilyJoin, "Merge Join": FamilyJoin,
		"Aggregate": FamilyAggregate, "WindowAgg": FamilyAggregate,
		"Sort": FamilySort, "Incremental Sort": FamilySort,
		"Hash": FamilyHash, "Memoize": FamilyMaterialize, "Gather Merge": FamilyGather,
		"Append": FamilyAppend, "Merge Append": FamilyAppend, "Limit": FamilyLimit,
		"ModifyTable": FamilyModify, "Result": FamilyResult, "Unique": FamilySetOp,
		"Some Extension Node": FamilyOther,
	}
	for typ, want := range cases {
		if got := FamilyOf(typ); got != want {
			t.Errorf("FamilyOf(%q) = %s, want %s", typ, got, want)
		}
	}
}

func TestLabel(t *testing.T) {
	yes := true
	cases := []struct {
		node Node
		want string
	}{
		{Node{NodeType: "Seq Scan", RelationName: "invoices", Alias: "i"}, "Seq Scan on invoices i"},
		{Node{NodeType: "Seq Scan", RelationName: "invoices", Alias: "invoices"}, "Seq Scan on invoices"},
		{Node{NodeType: "Seq Scan", RelationName: "events", ParallelAware: &yes}, "Parallel Seq Scan on events"},
		{Node{NodeType: "Index Scan", IndexName: "invoices_pkey", RelationName: "invoices", Schema: "billing", Alias: "i"}, "Index Scan using invoices_pkey on billing.invoices i"},
		{Node{NodeType: "Hash Join", JoinType: "Inner"}, "Hash Join"},
		{Node{NodeType: "Hash Join", JoinType: "Left"}, "Hash Left Join"},
		{Node{NodeType: "Merge Join", JoinType: "Anti"}, "Merge Anti Join"},
		{Node{NodeType: "Nested Loop", JoinType: "Semi"}, "Nested Loop Semi Join"},
		{Node{NodeType: "Aggregate", Strategy: "Hashed", PartialMode: "Simple"}, "HashAggregate"},
		{Node{NodeType: "Aggregate", Strategy: "Plain", PartialMode: "Finalize"}, "Finalize Aggregate"},
		{Node{NodeType: "Aggregate", Strategy: "Sorted", PartialMode: "Partial"}, "Partial GroupAggregate"},
		{Node{NodeType: "CTE Scan", CTEName: "recent", Alias: "r"}, "CTE Scan on recent r"},
		{Node{NodeType: "Function Scan", FunctionName: "generate_series", Alias: "g"}, "Function Scan on generate_series g"},
		{Node{NodeType: "Subquery Scan", Alias: "sub"}, "Subquery Scan on sub"},
		{Node{NodeType: "Aggregate", Strategy: "Plain", SubplanName: "InitPlan 2"}, "Aggregate [InitPlan 2]"},
	}
	for _, c := range cases {
		if got := c.node.Label(); got != c.want {
			t.Errorf("Label() = %q, want %q", got, c.want)
		}
	}
}

func TestAccessPath(t *testing.T) {
	bitmap := &Node{NodeType: "Bitmap Heap Scan", RelationName: "invoices", Plans: []*Node{
		{NodeType: "BitmapOr", Plans: []*Node{
			{NodeType: "Bitmap Index Scan", IndexName: "invoices_status_idx"},
			{NodeType: "Bitmap Index Scan", IndexName: "invoices_due_idx"},
		}},
	}}
	if got := bitmap.accessPath(); got != "Bitmap Heap Scan using invoices_status_idx, invoices_due_idx" {
		t.Errorf("bitmap access path = %q", got)
	}
	idx := &Node{NodeType: "Index Only Scan", IndexName: "invoices_pkey"}
	if got := idx.accessPath(); got != "Index Only Scan using invoices_pkey" {
		t.Errorf("index access path = %q", got)
	}
	if !relationScan("Index Only Scan") || relationScan("CTE Scan") || relationScan("Function Scan") {
		t.Error("relationScan classification wrong")
	}
}

func TestSpillState(t *testing.T) {
	n := func(s string) *Number { return num(t, s) }
	cases := []struct {
		name string
		node Node
		want spillState
	}{
		{"memory sort", Node{NodeType: "Sort", SortMethod: "quicksort", SortSpaceType: "Memory", SortSpaceUsed: n("330")}, spillMemory},
		{"top-N", Node{NodeType: "Sort", SortMethod: "top-N heapsort", SortSpaceType: "Memory"}, spillMemory},
		{"external merge", Node{NodeType: "Sort", SortMethod: "external merge", SortSpaceType: "Disk", SortSpaceUsed: n("1888")}, spillDisk},
		{"external sort without type", Node{NodeType: "Sort", SortMethod: "external sort"}, spillDisk},
		{"estimated sort", Node{NodeType: "Sort"}, spillUnknown},
		{"one batch", Node{NodeType: "Hash", HashBatches: n("1")}, spillMemory},
		{"four batches", Node{NodeType: "Hash", HashBatches: n("4"), OriginalHashBatches: n("2")}, spillDisk},
		{"estimated hash", Node{NodeType: "Hash"}, spillUnknown},
		{"hashagg memory", Node{NodeType: "Aggregate", Strategy: "Hashed", HashAggBatches: n("1"), DiskUsage: n("0")}, spillMemory},
		{"hashagg disk", Node{NodeType: "Aggregate", Strategy: "Hashed", HashAggBatches: n("5"), DiskUsage: n("8216")}, spillDisk},
		{"hashagg estimated", Node{NodeType: "Aggregate", Strategy: "Hashed"}, spillUnknown},
		{"plain aggregate", Node{NodeType: "Aggregate", Strategy: "Plain"}, spillNotApplicable},
		{"scan", Node{NodeType: "Seq Scan"}, spillNotApplicable},
	}
	for _, c := range cases {
		got, desc := c.node.spill()
		if got != c.want {
			t.Errorf("%s: spill = %d (%s), want %d", c.name, got, desc, c.want)
		}
	}
	_, desc := (&Node{NodeType: "Hash", HashBatches: n("4"), OriginalHashBatches: n("2"), PeakMemoryUsage: n("3073")}).spill()
	if desc != "Hash Batches 4 (originally 2), Peak Memory 3073 kB" {
		t.Errorf("hash description = %q", desc)
	}
}

package plans

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParseCTEAndSubplans(t *testing.T) {
	p := mustParseFile(t, "cte_subplan.json")
	root := p.Root
	if root.Label() != "CTE Scan on recent r" || root.CTEName != "recent" {
		t.Fatalf("root = %q", root.Label())
	}
	if len(root.Plans) != 3 {
		t.Fatalf("children = %d", len(root.Plans))
	}
	cte, initPlan, subPlan := root.Plans[0], root.Plans[1], root.Plans[2]
	if cte.ParentRelationship != "InitPlan" || cte.SubplanName != "CTE recent" || cte.Label() != "Seq Scan on invoices [CTE recent]" {
		t.Errorf("cte = %q (%s)", cte.Label(), cte.ParentRelationship)
	}
	if initPlan.SubplanName != "InitPlan 2" || initPlan.Label() != "Aggregate [InitPlan 2]" {
		t.Errorf("init plan = %q", initPlan.Label())
	}
	if subPlan.ParentRelationship != "SubPlan" || subPlan.ActualLoops.String() != "1688" {
		t.Errorf("subplan = %q loops %s", subPlan.Label(), subPlan.ActualLoops)
	}
	if p.Buffers || !p.Analyzed {
		t.Error("ANALYZE without BUFFERS must report analyzed and no buffers")
	}
}

func TestParseParallelGather(t *testing.T) {
	p := mustParseFile(t, "parallel_gather.json")
	gather := p.Root.Plans[0]
	if gather.NodeType != "Gather" || gather.WorkersPlanned.String() != "2" || gather.WorkersLaunched.String() != "2" {
		t.Fatalf("gather = %+v", gather)
	}
	scan := gather.Plans[0].Plans[0]
	if scan.Label() != "Parallel Seq Scan on events" || scan.ActualLoops.String() != "3" {
		t.Fatalf("scan = %q loops %s", scan.Label(), scan.ActualLoops)
	}
	if p.Root.Operation() != "Finalize Aggregate" || gather.Plans[0].Operation() != "Partial Aggregate" {
		t.Fatalf("aggregates = %q / %q", p.Root.Operation(), gather.Plans[0].Operation())
	}
}

func TestParseNestedAppendJITAndNeverExecuted(t *testing.T) {
	p := mustParseFile(t, "partitions_append.json")
	if p.Root.NodeType != "Append" || p.Root.Plans[1].NodeType != "Append" {
		t.Fatal("nested Append not parsed")
	}
	if p.MaxDepth != 2 || p.NodeCount != 6 {
		t.Fatalf("depth/count = %d/%d", p.MaxDepth, p.NodeCount)
	}
	pruned := p.Root.Plans[2]
	if pruned.Executed() || pruned.ActualLoops == nil {
		t.Fatal("never-executed member must have known loops 0")
	}
	if p.JIT == nil || p.JIT.Functions.String() != "6" || p.JIT.TotalTime.String() != "44.174" {
		t.Fatalf("jit = %+v", p.JIT)
	}
	if p.Settings["jit_above_cost"] != "100000" {
		t.Fatal("settings")
	}
}

func TestParseTimingOff(t *testing.T) {
	p := mustParseFile(t, "timing_off.json")
	if !p.Analyzed || p.Timing {
		t.Fatalf("analyzed=%v timing=%v, want true/false", p.Analyzed, p.Timing)
	}
	if p.Root.ActualTotalTime != nil || p.Root.ActualStartupTime != nil {
		t.Fatal("TIMING OFF times must be unknown, not zero")
	}
	if p.Root.ActualRows.String() != "243" || p.Root.HashAggBatches.String() != "1" || !p.Root.DiskUsage.IsZero() {
		t.Fatal("rows and hashagg values lost")
	}
	if st, _ := p.Root.spill(); st != spillMemory {
		t.Fatalf("hashagg spill state = %d", st)
	}
}

func TestSummarize(t *testing.T) {
	cases := []struct {
		file string
		want Summary
	}{
		{"analyzed_buffers.json", Summary{
			NodeCount: 5, MaxDepth: 3, Analyzed: true, Buffers: true, Timing: true,
			Relations:      []string{"customers", "invoices"},
			ScanStrategies: map[string]int{"Seq Scan": 2},
			JoinStrategies: map[string]int{"Hash Join": 1},
		}},
		{"cte_subplan.json", Summary{
			NodeCount: 6, MaxDepth: 2, Analyzed: true, Timing: true,
			Relations:      []string{"invoices", "payments"},
			ScanStrategies: map[string]int{"CTE Scan": 1, "Index Scan": 1, "Seq Scan": 2},
			JoinStrategies: map[string]int{},
		}},
		{"parallel_gather.json", Summary{
			NodeCount: 4, MaxDepth: 3, Analyzed: true, Buffers: true, Timing: true,
			Relations:      []string{"events"},
			ScanStrategies: map[string]int{"Parallel Seq Scan": 1},
			JoinStrategies: map[string]int{},
		}},
		{"partitions_append.json", Summary{
			NodeCount: 6, MaxDepth: 2, Analyzed: true, Timing: true, NeverExecuted: 1,
			Relations:      []string{"measurements_2026_08", "measurements_2026_09_p0", "measurements_2026_09_p1", "measurements_2026_10"},
			ScanStrategies: map[string]int{"Index Scan": 1, "Seq Scan": 3},
			JoinStrategies: map[string]int{},
		}},
		{"regress_after.json", Summary{
			NodeCount: 2, MaxDepth: 1, Analyzed: true, Buffers: true, Timing: true, Spills: 1,
			Relations:      []string{"invoices"},
			ScanStrategies: map[string]int{"Seq Scan": 1},
			JoinStrategies: map[string]int{},
		}},
	}
	for _, c := range cases {
		s := Summarize(mustParseFile(t, c.file))
		got := s
		got.Name, got.TotalCost, got.PlanRows, got.ActualRows = "", nil, nil, nil
		got.PlanningTime, got.ExecutionTime, got.SharedBlocks, got.TempBlocks = nil, nil, nil, nil
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.file, got, c.want)
		}
		if !strings.HasSuffix(s.Name, c.file) || s.TotalCost == nil {
			t.Errorf("%s: name %q cost %v", c.file, s.Name, s.TotalCost)
		}
	}
}

func TestSummarizeValues(t *testing.T) {
	s := Summarize(mustParseFile(t, "regress_after.json"))
	if s.TotalCost.String() != "9128.43" || s.ExecutionTime.String() != "108.316" || s.SharedBlocks.String() != "5520" || s.TempBlocks.String() != "473" {
		t.Fatalf("values = %s %s %s %s", s.TotalCost, s.ExecutionTime, s.SharedBlocks, s.TempBlocks)
	}
	est := Summarize(mustParseFile(t, "estimated_simple.json"))
	if est.ExecutionTime != nil || est.SharedBlocks != nil || est.TempBlocks != nil || est.ActualRows != nil {
		t.Fatal("estimated plan summary reports runtime values")
	}
	data, err := json.Marshal(est)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "execution_time_ms") || !strings.Contains(string(data), `"total_cost":1880.67`) {
		t.Fatalf("summary JSON = %s", data)
	}
}

func TestSummarizeNil(t *testing.T) {
	s := Summarize(nil)
	if s.Relations == nil || s.ScanStrategies == nil || s.NodeCount != 0 {
		t.Fatalf("nil summary = %+v", s)
	}
}

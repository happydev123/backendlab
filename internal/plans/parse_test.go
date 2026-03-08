package plans

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/limits"
)

// mustParse parses inline plan JSON.
func mustParse(t *testing.T, name, s string) *Plan {
	t.Helper()
	p, err := Parse([]byte(s), name, limits.Default())
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return p
}

// mustParseFile parses a fixture from testdata.
func mustParseFile(t *testing.T, name string) *Plan {
	t.Helper()
	p, _, err := ParseFile(filepath.Join("testdata", name), limits.Default())
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return p
}

func TestParseEstimatedPlan(t *testing.T) {
	p := mustParseFile(t, "estimated_simple.json")
	if p.Analyzed || p.Buffers || p.Timing {
		t.Fatalf("estimated plan flagged analyzed=%v buffers=%v timing=%v", p.Analyzed, p.Buffers, p.Timing)
	}
	if p.NodeCount != 5 || p.MaxDepth != 3 {
		t.Fatalf("count/depth = %d/%d", p.NodeCount, p.MaxDepth)
	}
	if p.ExecutionTime != nil || p.PlanningTime != nil {
		t.Fatal("estimated plan must not have times")
	}
	scan := p.Root.Plans[0].Plans[0]
	if scan.Label() != "Seq Scan on invoices i" || scan.Path != "/Plan/Plans/0/Plans/0" {
		t.Fatalf("scan = %q at %s", scan.Label(), scan.Path)
	}
	// Unknown is not zero: estimated nodes have no actual values at all.
	if scan.ActualRows != nil || scan.ActualLoops != nil || scan.Buffers.Known() || scan.RowsRemovedByFilter != nil {
		t.Fatal("estimated node has runtime metrics")
	}
	if !scan.StartupCost.IsZero() || scan.StartupCost.String() != "0.00" {
		t.Fatalf("startup cost = %v", scan.StartupCost)
	}
	if p.UnknownKeys != 0 {
		t.Fatalf("unknown keys: %v", p.UnknownKeyNames)
	}
}

func TestParseAnalyzedPlanWithBuffers(t *testing.T) {
	p := mustParseFile(t, "analyzed_buffers.json")
	if !p.Analyzed || !p.Buffers || !p.Timing {
		t.Fatalf("flags analyzed=%v buffers=%v timing=%v", p.Analyzed, p.Buffers, p.Timing)
	}
	if p.ExecutionTime.String() != "15.402" || p.PlanningTime.String() != "0.284" {
		t.Fatalf("times = %s / %s", p.ExecutionTime, p.PlanningTime)
	}
	if len(p.Triggers) != 0 || p.Settings["work_mem"] != "8MB" || p.Settings["random_page_cost"] != "1.1" {
		t.Fatalf("triggers/settings = %v / %v", p.Triggers, p.Settings)
	}
	hash := p.Root.Plans[0].Plans[1]
	if hash.HashBatches.String() != "1" || hash.PeakMemoryUsage.String() != "63" || hash.HashBuckets.String() != "1024" {
		t.Fatalf("hash metrics = %s %s %s", hash.HashBatches, hash.PeakMemoryUsage, hash.HashBuckets)
	}
	// A reported zero stays a known zero.
	if hash.Buffers.TempRead == nil || !hash.Buffers.TempRead.IsZero() {
		t.Fatal("reported zero temp blocks lost")
	}
	if p.Root.SortSpaceType != "Memory" || p.Root.SortMethod != "quicksort" {
		t.Fatal("sort fields")
	}
	if p.UnknownKeys != 0 {
		t.Fatalf("unknown keys: %v", p.UnknownKeyNames)
	}
}

func TestParseBareObjectAndStatementFields(t *testing.T) {
	p := mustParse(t, "bare", `{
		"Plan": {"Node Type": "Result", "Plan Rows": 1, "Actual Rows": 1, "Actual Loops": 1},
		"Triggers": [{"Trigger Name": "RI_ConstraintTrigger_a_16410", "Constraint Name": "fk", "Relation": "invoices", "Time": 0.412, "Calls": 3}],
		"JIT": {"Functions": 4, "Timing": {"Generation": 0.5, "Total": 9.25}},
		"Query Identifier": 123, "Brand New Key": true,
		"Execution Time": 1.0
	}`)
	if p.Root.NodeType != "Result" || !p.Analyzed {
		t.Fatal("bare object not parsed")
	}
	tr := p.Triggers[0]
	if tr.Name != "RI_ConstraintTrigger_a_16410" || tr.Relation != "invoices" || tr.Time.String() != "0.412" || tr.Calls.String() != "3" {
		t.Fatalf("trigger = %+v", tr)
	}
	if p.JIT.Functions.String() != "4" || p.JIT.TotalTime.String() != "9.25" {
		t.Fatalf("jit = %+v", p.JIT)
	}
	if p.UnknownKeys != 1 || p.UnknownKeyNames[0] != "Brand New Key" {
		t.Fatalf("unknown = %d %v", p.UnknownKeys, p.UnknownKeyNames)
	}
}

func TestParseRejects(t *testing.T) {
	big := limits.Default()
	cases := []struct {
		name, input, want string
	}{
		{"empty input", "  \n", "input is empty"},
		{"empty array", "[]", "JSON array is empty"},
		{"two statements", `[{"Plan": {"Node Type": "Result"}}, {"Plan": {"Node Type": "Result"}}]`, "input holds 2 statements"},
		{"two documents", `[{"Plan": {"Node Type": "Result"}}] [{"Plan": {"Node Type": "Result"}}]`, "more than one JSON document"},
		{"trailing garbage", `{"Plan": {"Node Type": "Result"}} xyz`, "unexpected data after the plan"},
		{"no plan key", `{"query": "select 1", "rows": []}`, `no "Plan" key (found keys "query", "rows")`},
		{"array of non-object", `[42]`, "expected a statement object, got number"},
		{"scalar", `"Seq Scan"`, "input is not JSON"},
		{"number", `12`, "input is not JSON"},
		{"plan not object", `{"Plan": []}`, "plan node object"},
		{"xml", `<explain xmlns="http://www.postgresql.org/2009/explain"><Query><Plan>`, "FORMAT XML"},
		{"yaml", "- Plan: \n    Node Type: \"Seq Scan\"\n", "FORMAT YAML"},
		{"text", "Seq Scan on invoices  (cost=0.00..1688.00 rows=80000 width=30)", "EXPLAIN text output"},
		{"psql table", "                QUERY PLAN\n---------------\n [ +\n", "psql table output"},
		{"syntax error", `[{"Plan": {"Node Type": "Result"}`, "parse JSON"},
		{"bad execution time", `{"Plan": {"Node Type": "Result"}, "Execution Time": "fast"}`, `must be a number`},
		{"bad trigger", `{"Plan": {"Node Type": "Result"}, "Triggers": [{"Time": "x"}]}`, `"Time" must be a number`},
		{"bad settings", `{"Plan": {"Node Type": "Result"}, "Settings": {"work_mem": 4}}`, `setting "work_mem" must be a string`},
		{"bad jit", `{"Plan": {"Node Type": "Result"}, "JIT": {"Timing": 3}}`, `"Timing" must be an object`},
		{"json nesting", strings.Repeat("[", maxJSONDepth+1) + strings.Repeat("]", maxJSONDepth+1), "JSON nesting is deeper"},
		{"node nesting", `{"Plan": ` + nestedNodes(MaxNodeDepth+1) + `}`, "deeper than"},
	}
	for _, c := range cases {
		_, err := Parse([]byte(c.input), "in.json", big)
		if err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		var pe *ParseError
		if !errors.As(err, &pe) || pe.Name != "in.json" {
			t.Errorf("%s: err %T %v lacks ParseError with name", c.name, err, err)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not contain %q", c.name, err, c.want)
		}
	}
}

func TestParseSizeLimit(t *testing.T) {
	lim := limits.Default()
	lim.MaxPlanBytes = 64
	data := []byte(`{"Plan": {"Node Type": "Result", "Plan Rows": 1, "Plan Width": 0, "Total Cost": 0.01}}`)
	_, err := Parse(data, "big.json", lim)
	if !errors.Is(err, jsonutil.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	path := filepath.Join(t.TempDir(), "big.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ParseFile(path, lim); !errors.Is(err, jsonutil.ErrTooLarge) {
		t.Fatalf("ParseFile err = %v, want ErrTooLarge", err)
	}
}

func TestParseNodeCountLimitAcrossInput(t *testing.T) {
	children := make([]string, MaxNodes)
	for i := range children {
		children[i] = `{"Node Type":"Result"}`
	}
	in := `[{"Plan":{"Node Type":"Append","Plans":[` + strings.Join(children, ",") + `]}}]`
	_, err := Parse([]byte(in), "wide.json", limits.Default())
	if !errors.Is(err, ErrTooManyNodes) {
		t.Fatalf("err = %v, want ErrTooManyNodes", err)
	}
}

func TestParseFileInput(t *testing.T) {
	path := filepath.Join("testdata", "estimated_simple.json")
	p, in, err := ParseFile(path, limits.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if in.Kind != InputKind || in.Name != path || in.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("input = %+v", in)
	}
	if p.Name != path {
		t.Fatalf("plan name = %q", p.Name)
	}
	if _, _, err := ParseFile(filepath.Join("testdata", "missing.json"), limits.Default()); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestParseAcceptsByteOrderMark(t *testing.T) {
	p, err := Parse([]byte("\xef\xbb\xbf[{\"Plan\": {\"Node Type\": \"Result\"}}]"), "bom.json", limits.Default())
	if err != nil || p.Root.NodeType != "Result" {
		t.Fatalf("BOM input: %v", err)
	}
}

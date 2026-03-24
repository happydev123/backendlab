package plans

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/report"
)

// TestExamplePlans keeps examples/plans in sync with what the
// documentation says they demonstrate: an index scan that became a
// sequential scan plus a sort that now spills to disk.
func TestExamplePlans(t *testing.T) {
	dir := filepath.Join("..", "..", "examples", "plans")
	before, bin, err := ParseFile(filepath.Join(dir, "before.json"), limits.Default())
	if err != nil {
		t.Fatal(err)
	}
	after, ain, err := ParseFile(filepath.Join(dir, "after.json"), limits.Default())
	if err != nil {
		t.Fatal(err)
	}
	c := Compare(before, after, Options{})
	validate(t, c.Findings)
	differences := map[string]string{}
	for _, f := range c.Findings {
		if f.Kind == finding.KindDifference {
			differences[f.Code] = f.Location
		}
	}
	want := map[string]string{
		CodeScanStrategyChanged: "/Plan/Plans/0/Plans/0",
		CodeSortSpillIntroduced: "/Plan",
		CodeTempIOIntroduced:    "/Plan",
	}
	if !reflect.DeepEqual(differences, want) {
		t.Fatalf("differences = %v, want %v", differences, want)
	}
	r := c.Report([]report.Input{bin, ain})
	if r.ExitCode() != 0 {
		t.Fatalf("default exit code = %d", r.ExitCode())
	}
	failOn, _ := ParseFailOn("scan,spill")
	r = Compare(before, after, Options{FailOn: failOn}).Report([]report.Input{bin, ain})
	if r.ExitCode() != 1 || r.Summary.Findings.Failing != 2 {
		t.Fatalf("fail-on exit = %d failing = %d", r.ExitCode(), r.Summary.Findings.Failing)
	}
	s := Summarize(after)
	if s.Spills != 1 || s.ScanStrategies["Seq Scan"] != 2 || s.JoinStrategies["Hash Join"] != 1 {
		t.Fatalf("summary = %+v", s)
	}
}

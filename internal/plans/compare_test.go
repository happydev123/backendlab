package plans

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
)

func codeCounts(fs []finding.Finding) map[string]int {
	m := map[string]int{}
	for _, f := range fs {
		m[f.Code]++
	}
	return m
}

func TestCompareRegressionDefaultsNeverFail(t *testing.T) {
	c := Compare(mustParseFile(t, "regress_before.json"), mustParseFile(t, "regress_after.json"), Options{})
	validate(t, c.Findings)
	want := map[string]int{
		CodeScanStrategyChanged: 1, CodeSortSpillIntroduced: 1, CodeTempIOIntroduced: 1, CodeEstimateMismatch: 2,
		CodeCostChanged: 1, CodeExecutionTimeChanged: 1, CodeBuffersChanged: 1, CodeActualTimeChanged: 2,
	}
	if got := codeCounts(c.Findings); !reflect.DeepEqual(got, want) {
		t.Fatalf("codes = %v, want %v", got, want)
	}
	if finding.AnyFailing(c.Findings) {
		t.Fatal("default options produced failing findings")
	}
	if len(c.Matches) != 2 || c.Dropped != 0 {
		t.Fatalf("matches = %d dropped = %d", len(c.Matches), c.Dropped)
	}
	joined := strings.Join(c.Limitations, "\n")
	for _, s := range []string{"Node matching is heuristic", "does not recommend indexes", "HTTP", "instrumentation overhead"} {
		if !strings.Contains(joined, s) {
			t.Errorf("limitations lack %q:\n%s", s, joined)
		}
	}
}

func TestCompareFailOnPromotion(t *testing.T) {
	before, after := mustParseFile(t, "regress_before.json"), mustParseFile(t, "regress_after.json")
	cases := []struct {
		failOn  string
		failing map[string]int
	}{
		{"scan", map[string]int{CodeScanStrategyChanged: 1}},
		{"spill,temp_io", map[string]int{CodeSortSpillIntroduced: 1, CodeTempIOIntroduced: 1}},
		{"estimate", map[string]int{CodeEstimateMismatch: 2}},
		{"join,structure", map[string]int{}},
		{"all", map[string]int{CodeScanStrategyChanged: 1, CodeSortSpillIntroduced: 1, CodeTempIOIntroduced: 1, CodeEstimateMismatch: 2}},
	}
	for _, tc := range cases {
		failOn, err := ParseFailOn(tc.failOn)
		if err != nil {
			t.Fatal(err)
		}
		c := Compare(before, after, Options{FailOn: failOn})
		got := map[string]int{}
		for _, f := range c.Findings {
			if f.Failing() {
				got[f.Code]++
				if f.Details["fail_on"] != failClass(f.Code) {
					t.Errorf("%s: fail_on detail = %q", tc.failOn, f.Details["fail_on"])
				}
			}
			if f.Kind == finding.KindObservation && f.Severity != finding.SeverityInfo {
				t.Errorf("%s: observation %s promoted", tc.failOn, f.Code)
			}
		}
		if !reflect.DeepEqual(got, tc.failing) {
			t.Errorf("fail-on %q: failing = %v, want %v", tc.failOn, got, tc.failing)
		}
	}
}

func TestCompareStructureFindings(t *testing.T) {
	before := planOf(t, "b", jn("Aggregate", `"Strategy": "Plain"`, scan("events", "Outer")))
	after := planOf(t, "a", jn("Aggregate", `"Strategy": "Plain", "Partial Mode": "Finalize"`,
		jn("Gather", `"Parent Relationship": "Outer", "Workers Planned": 2`,
			jn("Aggregate", `"Strategy": "Plain", "Partial Mode": "Partial", "Parent Relationship": "Outer"`,
				jn("Seq Scan", `"Relation Name": "events", "Parallel Aware": true, "Parent Relationship": "Outer"`)))))
	failOn, _ := ParseFailOn("structure")
	c := Compare(before, after, Options{FailOn: failOn})
	validate(t, c.Findings)
	byCode := map[string]finding.Finding{}
	for _, f := range c.Findings {
		byCode[f.Code] = f
	}
	sc := byCode[CodeStructureChanged]
	if sc.Location != "/Plan" || sc.Expected != "Seq Scan on events" || sc.Observed != "Gather" || !sc.Failing() {
		t.Fatalf("structure = %+v", sc)
	}
	added, removed := byCode[CodeNodeAdded], byCode[CodeNodeRemoved]
	if added.Message != "Gather (with 2 nodes below it) has no counterpart in the before plan" || added.Location != "/Plan/Plans/0" {
		t.Fatalf("added = %+v", added)
	}
	if removed.Location != "before:/Plan/Plans/0" || removed.Message != "Seq Scan on events has no counterpart in the after plan" {
		t.Fatalf("removed = %+v", removed)
	}
	if !strings.Contains(strings.Join(c.Limitations, "\n"), "Plan shapes diverge at 1 place(s)") {
		t.Fatalf("limitations = %v", c.Limitations)
	}
}

func TestCompareRootDivergence(t *testing.T) {
	before := planOf(t, "b", jn("Seq Scan", `"Relation Name": "t"`))
	after := planOf(t, "a", jn("Result", ""))
	c := Compare(before, after, Options{})
	if len(c.Matches) != 0 {
		t.Fatalf("matches = %+v", c.Matches)
	}
	sc := withCode(c.Findings, CodeStructureChanged)
	if len(sc) != 1 || !strings.Contains(sc[0].Message, "Root nodes differ") {
		t.Fatalf("findings = %+v", c.Findings)
	}
}

func TestCompareAddedWrapperSpill(t *testing.T) {
	before := planOf(t, "b", jn("Seq Scan", `"Relation Name": "t", "Plan Rows": 5000, "Actual Rows": 5000, "Actual Loops": 1, "Temp Read Blocks": 0, "Temp Written Blocks": 0`))
	after := planOf(t, "a", jn("Sort", `"Plan Rows": 5000, "Actual Rows": 5000, "Actual Loops": 1, "Sort Method": "external merge", "Sort Space Type": "Disk", "Sort Space Used": 1200, "Temp Read Blocks": 150, "Temp Written Blocks": 150`,
		jn("Seq Scan", `"Relation Name": "t", "Parent Relationship": "Outer", "Plan Rows": 5000, "Actual Rows": 5000, "Actual Loops": 1, "Temp Read Blocks": 0, "Temp Written Blocks": 0`)))
	c := Compare(before, after, Options{})
	got := codeCounts(c.Findings)
	if got[CodeNodeAdded] != 1 || got[CodeSortSpillIntroduced] != 1 || got[CodeTempIOIntroduced] != 1 {
		t.Fatalf("codes = %v", got)
	}
	added := withCode(c.Findings, CodeNodeAdded)[0]
	if added.Message != "Sort was added above a matched node" {
		t.Fatalf("added = %q", added.Message)
	}
}

func TestCompareMaxFindings(t *testing.T) {
	c := Compare(mustParseFile(t, "regress_before.json"), mustParseFile(t, "regress_after.json"), Options{MaxFindings: 3})
	if len(c.Findings) != 3 || c.Dropped != 7 {
		t.Fatalf("kept %d dropped %d", len(c.Findings), c.Dropped)
	}
	if !strings.Contains(c.Limitations[len(c.Limitations)-1], "7 findings beyond the limit of 3 were omitted") {
		t.Fatalf("limitations = %v", c.Limitations)
	}
	full := Compare(mustParseFile(t, "regress_before.json"), mustParseFile(t, "regress_after.json"), Options{})
	if !reflect.DeepEqual(full.Findings[:3], c.Findings) {
		t.Fatal("truncation must keep the first findings in sorted order")
	}
}

func TestCompareDeterministic(t *testing.T) {
	var first []finding.Finding
	for i := 0; i < 20; i++ {
		c := Compare(mustParseFile(t, "hash_batches_before.json"), mustParseFile(t, "hash_batches_after.json"), Options{})
		if i == 0 {
			first = c.Findings
			continue
		}
		if !reflect.DeepEqual(first, c.Findings) {
			t.Fatal("findings differ between runs")
		}
	}
}

func TestCompareUnknownKeysAndNil(t *testing.T) {
	before := mustParse(t, "b", `{"Plan": {"Node Type": "Result", "Shiny New Field": 1}}`)
	c := Compare(before, before, Options{})
	if !strings.Contains(strings.Join(c.Limitations, "\n"), "The before plan contains 1 unrecognized keys, which were ignored: Shiny New Field.") {
		t.Fatalf("limitations = %v", c.Limitations)
	}
	n := Compare(nil, before, Options{})
	if len(n.Findings) != 0 || len(n.Limitations) != 1 || n.Matches == nil {
		t.Fatalf("nil comparison = %+v", n)
	}
}

func TestCompareIdenticalPlansHasNoDifferences(t *testing.T) {
	p := mustParseFile(t, "cte_subplan.json")
	c := Compare(p, p, Options{})
	for _, f := range c.Findings {
		t.Errorf("identical plans produced %s at %s: %s", f.Code, f.Location, f.Message)
	}
	if len(c.Matches) != p.NodeCount {
		t.Fatalf("matches = %d, nodes = %d", len(c.Matches), p.NodeCount)
	}
}

// TestCompareLargePlans runs the comparison on plans at the node bounds: a
// wide Append whose members are listed in reverse order, and a chain at
// the maximum depth.
func TestCompareLargePlans(t *testing.T) {
	n := MaxNodes - 1
	fwd, rev := make([]string, n), make([]string, n)
	for i := 0; i < n; i++ {
		fwd[i] = scan(fmt.Sprintf("part_%d", i), "Member")
		rev[n-1-i] = fwd[i]
	}
	before := planOf(t, "b", jn("Append", "", fwd...))
	after := planOf(t, "a", jn("Append", "", rev...))
	c := Compare(before, after, Options{})
	if len(c.Matches) != MaxNodes {
		t.Fatalf("matches = %d", len(c.Matches))
	}
	for _, f := range c.Findings {
		t.Fatalf("reordered members produced %s: %s", f.Code, f.Message)
	}
	deep := planOf(t, "d", nestedNodes(MaxNodeDepth))
	if c := Compare(deep, deep, Options{}); len(c.Matches) != MaxNodeDepth+1 {
		t.Fatalf("deep matches = %d", len(c.Matches))
	}
}

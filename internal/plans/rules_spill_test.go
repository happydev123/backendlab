package plans

import (
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
)

func spillRules(c *comparer, m Match) {
	c.checkSpill(m)
	c.checkTemp(m)
}

func TestSortSpillExternalMerge(t *testing.T) {
	c := runRule(t, mustParseFile(t, "regress_before.json"), mustParseFile(t, "regress_after.json"), Options{}, spillRules)
	validate(t, c.findings)
	spills := withCode(c.findings, CodeSortSpillIntroduced)
	if len(spills) != 1 {
		t.Fatalf("findings = %+v", c.findings)
	}
	s := spills[0]
	if s.Location != "/Plan" || s.Message != "Sort now spills to disk (it stayed in memory before)" {
		t.Fatalf("spill = %+v", s)
	}
	if s.Expected != "Sort Method quicksort, Sort Space 330 kB on Memory" || s.Observed != "Sort Method external merge, Sort Space 1888 kB on Disk" {
		t.Fatalf("expected/observed = %q / %q", s.Expected, s.Observed)
	}
	temps := withCode(c.findings, CodeTempIOIntroduced)
	if len(temps) != 1 || temps[0].Location != "/Plan" || temps[0].Observed != "473 temp blocks" || temps[0].Expected != "0 temp blocks" {
		t.Fatalf("temp findings = %+v", temps)
	}
	if temps[0].Details["temp_read"] != "236" || temps[0].Details["temp_written"] != "237" {
		t.Fatalf("temp details = %v", temps[0].Details)
	}
}

func TestHashBatchesSpill(t *testing.T) {
	c := runRule(t, mustParseFile(t, "hash_batches_before.json"), mustParseFile(t, "hash_batches_after.json"), Options{}, spillRules)
	validate(t, c.findings)
	spills := withCode(c.findings, CodeSortSpillIntroduced)
	if len(spills) != 1 || spills[0].Location != "/Plan/Plans/1" {
		t.Fatalf("spills = %+v", spills)
	}
	if spills[0].Observed != "Hash Batches 4 (originally 2), Peak Memory 3073 kB" || spills[0].Expected != "Hash Batches 1 (originally 1), Peak Memory 4462 kB" {
		t.Fatalf("hash spill = %+v", spills[0])
	}
	// Temp I/O is attributed to the node that does it: the Hash writes
	// 1100 blocks and the Hash Join's own share is 4400 - 1100 = 3300.
	temps := withCode(c.findings, CodeTempIOIntroduced)
	if len(temps) != 2 {
		t.Fatalf("temp findings = %+v", temps)
	}
	got := map[string]string{temps[0].Location: temps[0].Observed, temps[1].Location: temps[1].Observed}
	if got["/Plan"] != "3300 temp blocks" || got["/Plan/Plans/1"] != "1100 temp blocks" {
		t.Fatalf("temp attribution = %v", got)
	}
}

func TestTempIncreaseThreshold(t *testing.T) {
	sortNode := func(tempRead string) string {
		return jn("Sort", `"Sort Method": "external merge", "Sort Space Type": "Disk", "Actual Loops": 1, "Temp Read Blocks": `+tempRead+`, "Temp Written Blocks": 0`)
	}
	cases := []struct {
		before, after string
		want          int
	}{
		{"100", "150", 0},
		{"100", "200", 1},
		{"100", "0", 0},
		{"0", "0", 0},
	}
	for _, c := range cases {
		got := runRule(t, planOf(t, "b", sortNode(c.before)), planOf(t, "a", sortNode(c.after)), Options{}, (*comparer).checkTemp)
		if len(got.findings) != c.want {
			t.Errorf("%s -> %s: findings = %+v", c.before, c.after, got.findings)
		}
	}
	// Unknown before counters never count as zero.
	noBuffers := planOf(t, "b", jn("Sort", `"Sort Method": "external merge", "Actual Loops": 1`))
	if got := runRule(t, noBuffers, planOf(t, "a", sortNode("50")), Options{}, (*comparer).checkTemp); len(got.findings) != 0 {
		t.Fatalf("unknown before treated as zero: %+v", got.findings)
	}
}

func TestSpillInconclusiveWhenBeforeEstimated(t *testing.T) {
	before := planOf(t, "b", jn("Sort", `"Sort Key": ["total DESC"], "Plan Rows": 10`))
	after := planOf(t, "a", jn("Sort", `"Sort Key": ["total DESC"], "Plan Rows": 10, "Actual Loops": 1, "Sort Method": "external merge", "Sort Space Type": "Disk", "Sort Space Used": 900`))
	c := runRule(t, before, after, Options{}, spillRules)
	if len(c.findings) != 1 || c.findings[0].Code != CodeSpillInconclusive || c.findings[0].Kind != finding.KindInconclusive {
		t.Fatalf("findings = %+v", c.findings)
	}
	// Already spilling before: nothing new to report.
	if got := runRule(t, after, after, Options{}, spillRules); len(got.findings) != 0 {
		t.Fatalf("existing spill reported: %+v", got.findings)
	}
}

func TestSpillAndTempOnAddedNodes(t *testing.T) {
	before := planOf(t, "b", jn("Seq Scan", `"Relation Name": "t", "Actual Loops": 1, "Temp Read Blocks": 0, "Temp Written Blocks": 0`))
	after := planOf(t, "a", jn("Sort", `"Actual Loops": 1, "Sort Method": "external merge", "Sort Space Type": "Disk", "Sort Space Used": 1200, "Temp Read Blocks": 150, "Temp Written Blocks": 150`,
		jn("Seq Scan", `"Relation Name": "t", "Parent Relationship": "Outer", "Actual Loops": 1, "Temp Read Blocks": 0, "Temp Written Blocks": 0`)))
	c := newComparer(before, after, Options{})
	c.checkSpillAdded(after.Root)
	c.checkTempAdded(after.Root)
	validate(t, c.findings)
	if len(c.findings) != 2 || c.findings[0].Code != CodeSortSpillIntroduced || c.findings[1].Code != CodeTempIOIntroduced {
		t.Fatalf("findings = %+v", c.findings)
	}
	if c.findings[1].Message != "Sort reads or writes 300 temp blocks and has no counterpart in the before plan" {
		t.Fatalf("message = %q", c.findings[1].Message)
	}
	// Without runtime data in the before plan there is nothing to compare.
	estimated := planOf(t, "e", jn("Seq Scan", `"Relation Name": "t"`))
	c = newComparer(estimated, after, Options{})
	c.checkSpillAdded(after.Root)
	c.checkTempAdded(after.Root)
	if len(c.findings) != 0 {
		t.Fatalf("estimated before: findings = %+v", c.findings)
	}
}

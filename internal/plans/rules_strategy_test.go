package plans

import (
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
)

// runRule applies one match rule to every match of the two plans.
func runRule(t *testing.T, before, after *Plan, opts Options, rule func(c *comparer, m Match)) *comparer {
	t.Helper()
	c := newComparer(before, after, opts)
	for _, m := range align(before, after).matches {
		rule(c, m)
	}
	return c
}

// withCode returns the findings with the given code.
func withCode(fs []finding.Finding, code string) []finding.Finding {
	var out []finding.Finding
	for _, f := range fs {
		if f.Code == code {
			out = append(out, f)
		}
	}
	return out
}

func validate(t *testing.T, fs []finding.Finding) {
	t.Helper()
	for _, f := range fs {
		if err := f.Validate(); err != nil {
			t.Errorf("invalid finding: %v", err)
		}
	}
}

func TestScanStrategyIndexToSeq(t *testing.T) {
	c := runRule(t, mustParseFile(t, "regress_before.json"), mustParseFile(t, "regress_after.json"), Options{}, (*comparer).checkScan)
	validate(t, c.findings)
	fs := withCode(c.findings, CodeScanStrategyChanged)
	if len(fs) != 1 {
		t.Fatalf("findings = %+v", c.findings)
	}
	f := fs[0]
	if f.Location != "/Plan/Plans/0" || f.Kind != finding.KindDifference || f.Severity != finding.SeverityWarning {
		t.Fatalf("finding = %+v", f)
	}
	if f.Expected != "Index Scan using invoices_created_at_idx" || f.Observed != "Seq Scan" {
		t.Fatalf("expected/observed = %q / %q", f.Expected, f.Observed)
	}
	if f.Message != "Scan strategy on invoices changed from Index Scan using invoices_created_at_idx to Seq Scan" {
		t.Fatalf("message = %q", f.Message)
	}
	if f.Details["relation"] != "invoices" || f.Details["after_node"] != "Seq Scan on invoices" ||
		f.Details["before_path"] != "/Plan/Plans/0" || f.Details["match_confidence"] != ConfidenceHigh ||
		f.Details["rows_removed_by_filter"] != "unknown -> 251790" {
		t.Fatalf("details = %v", f.Details)
	}
}

func TestScanStrategyVariants(t *testing.T) {
	yes := `"Parallel Aware": true, `
	cases := []struct {
		name          string
		before, after string
		want          int
	}{
		{"same index", `"Index Name": "a_idx", `, `"Index Name": "a_idx", `, 0},
		{"different index", `"Index Name": "a_idx", `, `"Index Name": "b_idx", `, 1},
		{"parallel seq scan", ``, yes, 1},
	}
	for _, c := range cases {
		typ := "Seq Scan"
		if c.before != "" && c.before != yes {
			typ = "Index Scan"
		}
		before := planOf(t, "b", jn(typ, c.before+`"Relation Name": "invoices"`))
		after := planOf(t, "a", jn(typ, c.after+`"Relation Name": "invoices"`))
		got := runRule(t, before, after, Options{}, (*comparer).checkScan)
		if len(got.findings) != c.want {
			t.Errorf("%s: findings = %+v", c.name, got.findings)
		}
	}
	// Index Only Scan to Bitmap Heap Scan names the bitmap indexes.
	before := planOf(t, "b", jn("Index Only Scan", `"Index Name": "invoices_status_idx", "Relation Name": "invoices"`))
	after := planOf(t, "a", jn("Bitmap Heap Scan", `"Relation Name": "invoices"`,
		jn("Bitmap Index Scan", `"Index Name": "invoices_status_idx", "Parent Relationship": "Outer"`)))
	got := runRule(t, before, after, Options{}, (*comparer).checkScan)
	if len(got.findings) != 1 || got.findings[0].Observed != "Bitmap Heap Scan using invoices_status_idx" {
		t.Fatalf("bitmap findings = %+v", got.findings)
	}
	// Scans of different relations (positional match) are not compared.
	x := planOf(t, "x", jn("Append", "", jn("Seq Scan", `"Relation Name": "a", "Parent Relationship": "Member"`)))
	y := planOf(t, "y", jn("Append", "", jn("Index Scan", `"Relation Name": "b", "Index Name": "b_idx", "Parent Relationship": "Member"`)))
	if got := runRule(t, x, y, Options{}, (*comparer).checkScan); len(got.findings) != 0 {
		t.Fatalf("different relations compared: %+v", got.findings)
	}
}

func TestJoinStrategyHashToNestedLoop(t *testing.T) {
	before, after := mustParseFile(t, "join_hash_before.json"), mustParseFile(t, "join_nestloop_after.json")
	c := runRule(t, before, after, Options{}, func(c *comparer, m Match) { c.checkJoin(m); c.checkScan(m) })
	validate(t, c.findings)
	joins := withCode(c.findings, CodeJoinStrategyChanged)
	if len(joins) != 1 {
		t.Fatalf("findings = %+v", c.findings)
	}
	j := joins[0]
	if j.Location != "/Plan" || j.Message != "Join strategy changed from Hash Join to Nested Loop" || j.Expected != "Hash Join" || j.Observed != "Nested Loop" {
		t.Fatalf("join finding = %+v", j)
	}
	if j.Details["relations"] != "customers, invoices" || j.Details["inner_loops"] != "2380" {
		t.Fatalf("details = %v", j.Details)
	}
	scans := withCode(c.findings, CodeScanStrategyChanged)
	if len(scans) != 1 || scans[0].Location != "/Plan/Plans/1" || scans[0].Details["before_path"] != "/Plan/Plans/1/Plans/0" {
		t.Fatalf("scan findings = %+v", scans)
	}
}

func TestJoinTypeChange(t *testing.T) {
	before := planOf(t, "b", jn("Hash Join", `"Join Type": "Inner"`, scan("a", "Outer"), jn("Hash", `"Parent Relationship": "Inner"`, scan("b", "Outer"))))
	after := planOf(t, "a", jn("Merge Join", `"Join Type": "Left"`, scan("a", "Outer"), scan("b", "Inner")))
	c := runRule(t, before, after, Options{}, (*comparer).checkJoin)
	if len(c.findings) != 1 {
		t.Fatalf("findings = %+v", c.findings)
	}
	f := c.findings[0]
	if f.Message != "Join strategy changed from Hash Join to Merge Join; join type changed from Inner to Left" || f.Observed != "Merge Left Join" {
		t.Fatalf("finding = %+v", f)
	}
	same := runRule(t, before, before, Options{}, (*comparer).checkJoin)
	if len(same.findings) != 0 {
		t.Fatalf("unchanged join reported: %+v", same.findings)
	}
}

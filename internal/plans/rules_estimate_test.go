package plans

import (
	"testing"
)

func runEstimates(t *testing.T, before, after *Plan, opts Options) *comparer {
	t.Helper()
	c := newComparer(before, after, opts)
	al := align(before, after)
	byAfter := map[*Node]*Match{}
	for i := range al.matches {
		byAfter[al.matches[i].After] = &al.matches[i]
	}
	after.Walk(func(n *Node) bool { c.checkEstimate(n, byAfter[n]); return true })
	return c
}

func analyzedScan(rel, planRows, actualRows, loops string) string {
	return jn("Seq Scan", `"Relation Name": "`+rel+`", "Parent Relationship": "Outer", "Plan Rows": `+planRows+`, "Actual Rows": `+actualRows+`, "Actual Loops": `+loops)
}

func TestEstimateOfUsesPerLoopValues(t *testing.T) {
	n := &Node{PlanRows: num(t, "1"), ActualRows: num(t, "40"), ActualLoops: num(t, "2380")}
	e, ok := estimateOf(n)
	if !ok {
		t.Fatal("estimate not computed")
	}
	if e.estTotal.String() != "2380" || e.actTotal.String() != "95200" || e.ratio.String() != "40" || !e.under {
		t.Fatalf("estimate = est %s act %s ratio %s under %v", e.estTotal, e.actTotal, e.ratio, e.under)
	}
	for _, bad := range []*Node{
		{PlanRows: num(t, "1")},
		{PlanRows: num(t, "1"), ActualRows: num(t, "0"), ActualLoops: num(t, "0")},
		{ActualRows: num(t, "5"), ActualLoops: num(t, "1")},
	} {
		if _, ok := estimateOf(bad); ok {
			t.Errorf("estimate computed for %+v", bad)
		}
	}
}

func TestEstimateMismatchFixture(t *testing.T) {
	c := runEstimates(t, mustParseFile(t, "regress_before.json"), mustParseFile(t, "regress_after.json"), Options{})
	validate(t, c.findings)
	fs := withCode(c.findings, CodeEstimateMismatch)
	if len(fs) != 2 {
		t.Fatalf("findings = %+v", c.findings)
	}
	scanF := fs[1]
	if scanF.Location != "/Plan/Plans/0" {
		t.Fatalf("order/location = %s", scanF.Location)
	}
	wantMsg := "Row estimate for Seq Scan on invoices is off by 15.07x (underestimate): estimated 3200 rows per loop, actual 48210 rows per loop over 1 loop"
	if scanF.Message != wantMsg {
		t.Fatalf("message = %q", scanF.Message)
	}
	if scanF.Expected != "3200 rows estimated in total (3200 per loop)" || scanF.Observed != "48210 rows in total (48210 per loop x 1 loop)" {
		t.Fatalf("expected/observed = %q / %q", scanF.Expected, scanF.Observed)
	}
	d := scanF.Details
	if d["direction"] != "underestimate" || d["ratio"] != "15.07" || d["row_factor"] != "10" || d["min_rows"] != "100" ||
		d["before_ratio"] != "1.05" || d["before_mismatch"] != "false" {
		t.Fatalf("details = %v", d)
	}
}

func TestEstimateMismatchLoopsAndThresholds(t *testing.T) {
	cases := []struct {
		name                    string
		planRows, actual, loops string
		opts                    Options
		want                    bool
	}{
		// 1 estimated, 40 actual per loop over 2380 loops: 95200 rows total.
		{"inner side of nested loop", "1", "40", "2380", Options{}, true},
		// Same per-loop ratio but only 40 rows in total: below the floor.
		{"below floor", "1", "40", "1", Options{}, false},
		{"floor lowered", "1", "40", "1", Options{MinRows: 10}, true},
		{"ratio below factor", "1000", "9000", "1", Options{}, false},
		{"factor lowered", "1000", "9000", "1", Options{RowFactor: 5}, true},
		{"exactly at factor", "100", "1000", "1", Options{}, true},
		{"overestimate", "50000", "12", "1", Options{}, true},
		{"zero actual rows", "500", "0", "1", Options{}, true},
		{"never executed", "500", "0", "0", Options{}, false},
	}
	for _, c := range cases {
		before := planOf(t, "b", jn("Limit", "", analyzedScan("t", c.planRows, c.actual, c.loops)))
		after := planOf(t, "a", jn("Limit", "", analyzedScan("t", c.planRows, c.actual, c.loops)))
		got := runEstimates(t, before, after, c.opts)
		if (len(got.findings) == 1) != c.want || len(got.findings) > 1 {
			t.Errorf("%s: findings = %+v", c.name, got.findings)
		}
	}
	over := runEstimates(t, planOf(t, "b", analyzedScan("t", "50000", "12", "1")), planOf(t, "a", analyzedScan("t", "50000", "12", "1")), Options{})
	if over.findings[0].Details["direction"] != "overestimate" || over.findings[0].Details["before_mismatch"] != "true" {
		t.Fatalf("overestimate details = %v", over.findings[0].Details)
	}
}

func TestEstimateMismatchUnmatchedAndEstimatedBefore(t *testing.T) {
	before := planOf(t, "b", jn("Result", `"Plan Rows": 1`))
	after := planOf(t, "a", analyzedScan("t", "10", "5000", "1"))
	c := runEstimates(t, before, after, Options{})
	if len(c.findings) != 1 {
		t.Fatalf("findings = %+v", c.findings)
	}
	f := c.findings[0]
	if _, ok := f.Details["before_ratio"]; ok || f.Details["after_node"] != "Seq Scan on t" {
		t.Fatalf("unmatched node details = %v", f.Details)
	}
	estimated := planOf(t, "e", jn("Seq Scan", `"Relation Name": "t", "Plan Rows": 10`))
	c = runEstimates(t, estimated, after, Options{})
	if c.findings[0].Details["before_ratio"] != "unknown" {
		t.Fatalf("estimated before details = %v", c.findings[0].Details)
	}
}

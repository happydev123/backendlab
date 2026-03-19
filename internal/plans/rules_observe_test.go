package plans

import (
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
)

func observeAll(t *testing.T, before, after *Plan) *comparer {
	t.Helper()
	c := newComparer(before, after, Options{})
	c.observePlan()
	for _, m := range align(before, after).matches {
		c.observeNode(m)
	}
	validate(t, c.findings)
	for _, f := range c.findings {
		if f.Severity != finding.SeverityInfo {
			t.Errorf("observation %s has severity %s", f.Code, f.Severity)
		}
	}
	return c
}

func TestObservationsRegression(t *testing.T) {
	c := observeAll(t, mustParseFile(t, "regress_before.json"), mustParseFile(t, "regress_after.json"))
	cost := withCode(c.findings, CodeCostChanged)
	if len(cost) != 1 || cost[0].Location != "/Plan" ||
		cost[0].Message != "Estimated total cost changed from 420.36 to 9128.43 (planner cost units, not milliseconds)" {
		t.Fatalf("cost = %+v", cost)
	}
	exec := withCode(c.findings, CodeExecutionTimeChanged)
	if len(exec) != 1 || exec[0].Location != "/Execution Time" || exec[0].Expected != "6.903 ms" || exec[0].Observed != "108.316 ms" ||
		!strings.Contains(exec[0].Message, "instrumentation overhead") || exec[0].Details["planning_time_ms"] != "0.152 -> 0.187" {
		t.Fatalf("execution = %+v", exec)
	}
	buf := withCode(c.findings, CodeBuffersChanged)
	if len(buf) != 1 || buf[0].Message != "Root buffer counters changed (blocks, cumulative over the whole plan): shared hit 2980 -> 1210, shared read 41 -> 4310, temp read 0 -> 236, temp written 0 -> 237" {
		t.Fatalf("buffers = %+v", buf)
	}
	if buf[0].Details["temp_written_blocks"] != "0 -> 237" {
		t.Fatalf("buffer details = %v", buf[0].Details)
	}
	times := withCode(c.findings, CodeActualTimeChanged)
	if len(times) != 2 {
		t.Fatalf("time observations = %+v", times)
	}
	if times[1].Location != "/Plan/Plans/0" || times[1].Expected != "4.88 ms" || times[1].Observed != "71.4 ms" {
		t.Fatalf("leaf time = %+v", times[1])
	}
	if len(withCode(c.findings, CodeRowsEstimateChanged)) != 0 || len(withCode(c.findings, CodeLoopsChanged)) != 0 {
		t.Fatalf("unexpected row/loop observations: %+v", c.findings)
	}
	if len(c.limitations) != 0 {
		t.Fatalf("limitations = %v", c.limitations)
	}
}

func TestObservationsLoopsAndRows(t *testing.T) {
	c := observeAll(t, mustParseFile(t, "join_hash_before.json"), mustParseFile(t, "join_nestloop_after.json"))
	loops := withCode(c.findings, CodeLoopsChanged)
	if len(loops) != 1 || loops[0].Location != "/Plan/Plans/1" || loops[0].Observed != "2380 loops" ||
		!strings.Contains(loops[0].Message, "per-loop averages") {
		t.Fatalf("loops = %+v", loops)
	}
	rows := withCode(c.findings, CodeRowsEstimateChanged)
	// Root and outer scan went from 2400 to 12 estimated rows; the inner
	// scan from 50000 to 1.
	if len(rows) != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	if len(withCode(c.findings, CodeBuffersChanged)) != 0 || len(withCode(c.findings, CodeBuffersInconclusive)) != 0 {
		t.Fatal("plans without BUFFERS on both sides must not produce buffer findings")
	}
}

func TestObservationsMixedAnalyzed(t *testing.T) {
	c := observeAll(t, mustParseFile(t, "estimated_simple.json"), mustParseFile(t, "analyzed_buffers.json"))
	inc := withCode(c.findings, CodeRuntimeInconclusive)
	if len(inc) != 1 || inc[0].Kind != finding.KindInconclusive || !strings.Contains(inc[0].Message, "Only the after plan") {
		t.Fatalf("inconclusive = %+v", c.findings)
	}
	if len(withCode(c.findings, CodeExecutionTimeChanged)) != 0 || len(withCode(c.findings, CodeActualTimeChanged)) != 0 {
		t.Fatal("runtime compared across estimated and analyzed plans")
	}
	if len(c.limitations) != 1 || !strings.Contains(c.limitations[0], "One plan is estimated") {
		t.Fatalf("limitations = %v", c.limitations)
	}
	both := observeAll(t, mustParseFile(t, "estimated_simple.json"), mustParseFile(t, "estimated_simple.json"))
	if len(both.findings) != 0 || !strings.Contains(both.limitations[0], "Both plans are estimated") {
		t.Fatalf("estimated pair: %+v %v", both.findings, both.limitations)
	}
}

func TestObservationsTimingAndBuffersMissingOnOneSide(t *testing.T) {
	timed := planOf(t, "b", jn("Seq Scan", `"Relation Name": "t", "Plan Rows": 10, "Actual Rows": 10, "Actual Loops": 1, "Actual Total Time": 3.5, "Shared Hit Blocks": 4`))
	untimed := mustParse(t, "a", `{"Plan": `+jn("Seq Scan", `"Relation Name": "t", "Plan Rows": 10, "Actual Rows": 10, "Actual Loops": 1`)+`, "Execution Time": 2.0}`)
	c := observeAll(t, timed, untimed)
	if len(withCode(c.findings, CodeTimingInconclusive)) != 2 {
		t.Fatalf("timing inconclusive = %+v", c.findings)
	}
	if len(withCode(c.findings, CodeBuffersInconclusive)) != 1 || len(c.limitations) != 1 {
		t.Fatalf("buffers inconclusive = %+v %v", c.findings, c.limitations)
	}
	if len(withCode(c.findings, CodeActualTimeChanged)) != 0 {
		t.Fatal("node time compared although one side has no timing")
	}
}

func TestNodeTimeThresholds(t *testing.T) {
	leaf := func(ms, loops string) *Plan {
		return planOf(t, "p", jn("Seq Scan", `"Relation Name": "t", "Actual Total Time": `+ms+`, "Actual Loops": `+loops))
	}
	cases := []struct {
		b, bl, a, al string
		want         int
	}{
		{"10", "1", "15", "1", 0},         // below 2x
		{"10", "1", "20", "1", 1},         // 2x
		{"0.2", "1", "0.9", "1", 0},       // under 1 ms absolute change
		{"0.010", "1", "0.010", "500", 1}, // 0.01 ms -> 5 ms via loops
		{"0", "1", "3", "1", 1},           // from zero
	}
	for _, c := range cases {
		got := observeAll(t, leaf(c.b, c.bl), leaf(c.a, c.al))
		if n := len(withCode(got.findings, CodeActualTimeChanged)); n != c.want {
			t.Errorf("%s x%s -> %s x%s: %d time findings, want %d", c.b, c.bl, c.a, c.al, n, c.want)
		}
	}
	// Inner nodes are skipped because their times include children.
	inner := func(ms string) *Plan {
		return planOf(t, "p", jn("Limit", "", jn("Sort", `"Parent Relationship": "Outer", "Actual Total Time": `+ms+`, "Actual Loops": 1`,
			jn("Seq Scan", `"Relation Name": "t", "Parent Relationship": "Outer", "Actual Total Time": 1, "Actual Loops": 1`))))
	}
	if got := observeAll(t, inner("5"), inner("50")); len(withCode(got.findings, CodeActualTimeChanged)) != 0 {
		t.Fatalf("inner node time reported: %+v", got.findings)
	}
}

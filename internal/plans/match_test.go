package plans

import (
	"strings"
	"testing"
)

// jn builds a plan node as JSON. fields is a comma-separated list of extra
// "Key": value pairs.
func jn(nodeType, fields string, children ...string) string {
	s := `{"Node Type": "` + nodeType + `"`
	if fields != "" {
		s += ", " + fields
	}
	if len(children) > 0 {
		s += `, "Plans": [` + strings.Join(children, ", ") + `]`
	}
	return s + "}"
}

// planOf wraps a root node in the EXPLAIN JSON array.
func planOf(t *testing.T, name, root string) *Plan {
	t.Helper()
	return mustParse(t, name, `[{"Plan": `+root+`}]`)
}

func scan(rel, parent string) string {
	return jn("Seq Scan", `"Relation Name": "`+rel+`", "Alias": "`+rel+`", "Parent Relationship": "`+parent+`"`)
}

// matchMap renders matches as "beforePath=afterPath" for compact asserts.
func matchMap(al alignment) map[string]string {
	m := map[string]string{}
	for _, x := range al.matches {
		m[x.BeforePath] = x.AfterPath
	}
	return m
}

func paths(us []unmatched) string {
	var out []string
	for _, u := range us {
		p := u.node.Path
		if u.wrapper {
			p += "(wrapper)"
		}
		out = append(out, p)
	}
	return strings.Join(out, ",")
}

func TestAlignIdenticalPlans(t *testing.T) {
	p := mustParseFile(t, "analyzed_buffers.json")
	q := mustParseFile(t, "estimated_simple.json")
	al := align(p, q)
	if len(al.matches) != 5 || len(al.added) != 0 || len(al.removed) != 0 || len(al.divergent) != 0 {
		t.Fatalf("matches=%d added=%d removed=%d divergent=%d", len(al.matches), len(al.added), len(al.removed), len(al.divergent))
	}
	for _, m := range al.matches {
		if m.BeforePath != m.AfterPath || m.Confidence != ConfidenceHigh || m.Reason == "" {
			t.Errorf("match %+v", m)
		}
	}
}

func TestAlignReorderedChildren(t *testing.T) {
	before := planOf(t, "b", jn("Append", "", scan("m_2026_08", "Member"), scan("m_2026_09", "Member"), scan("m_2026_10", "Member")))
	after := planOf(t, "a", jn("Append", "", scan("m_2026_10", "Member"), scan("m_2026_08", "Member"), scan("m_2026_09", "Member")))
	al := align(before, after)
	got := matchMap(al)
	want := map[string]string{
		"/Plan": "/Plan", "/Plan/Plans/0": "/Plan/Plans/1", "/Plan/Plans/1": "/Plan/Plans/2", "/Plan/Plans/2": "/Plan/Plans/0",
	}
	for b, a := range want {
		if got[b] != a {
			t.Errorf("%s matched %q, want %q", b, got[b], a)
		}
	}
	if len(al.added)+len(al.removed)+len(al.divergent) != 0 {
		t.Fatal("reordering produced added/removed nodes")
	}
}

func TestAlignSwappedJoinSides(t *testing.T) {
	before := planOf(t, "b", jn("Hash Join", `"Join Type": "Inner"`,
		scan("invoices", "Outer"),
		jn("Hash", `"Parent Relationship": "Inner"`, scan("customers", "Outer"))))
	after := planOf(t, "a", jn("Hash Join", `"Join Type": "Inner"`,
		scan("customers", "Outer"),
		jn("Hash", `"Parent Relationship": "Inner"`, scan("invoices", "Outer"))))
	al := align(before, after)
	got := matchMap(al)
	if got["/Plan/Plans/0"] != "/Plan/Plans/1/Plans/0" || got["/Plan/Plans/1/Plans/0"] != "/Plan/Plans/0" {
		t.Fatalf("scans not matched by relation: %v", got)
	}
	if paths(al.added) != "/Plan/Plans/1(wrapper)" || paths(al.removed) != "/Plan/Plans/1(wrapper)" {
		t.Fatalf("added %s removed %s", paths(al.added), paths(al.removed))
	}
}

func TestAlignDivergentSubtreesStopsMatching(t *testing.T) {
	before := planOf(t, "b", jn("Aggregate", `"Strategy": "Plain"`, scan("events", "Outer")))
	after := planOf(t, "a", jn("Aggregate", `"Strategy": "Plain", "Partial Mode": "Finalize"`,
		jn("Gather", `"Parent Relationship": "Outer", "Workers Planned": 2`,
			jn("Aggregate", `"Strategy": "Plain", "Partial Mode": "Partial", "Parent Relationship": "Outer"`,
				jn("Seq Scan", `"Relation Name": "events", "Parallel Aware": true, "Parent Relationship": "Outer"`)))))
	al := align(before, after)
	if len(al.matches) != 1 || al.matches[0].AfterPath != "/Plan" {
		t.Fatalf("matches = %+v", al.matches)
	}
	if len(al.divergent) != 1 || al.divergent[0].after.Path != "/Plan" {
		t.Fatalf("divergent = %+v", al.divergent)
	}
	if paths(al.removed) != "/Plan/Plans/0" || paths(al.added) != "/Plan/Plans/0" || al.added[0].size != 3 {
		t.Fatalf("removed %s added %s", paths(al.removed), paths(al.added))
	}
}

func TestAlignPositionalFallback(t *testing.T) {
	before := planOf(t, "b", jn("Append", "", scan("events_old", "Member")))
	after := planOf(t, "a", jn("Append", "", scan("events_new", "Member")))
	al := align(before, after)
	if len(al.matches) != 2 {
		t.Fatalf("matches = %+v", al.matches)
	}
	m := al.matches[1]
	if m.Confidence != ConfidenceLow || !strings.Contains(m.Reason, "positional fallback") || !strings.Contains(m.Reason, "events_old") {
		t.Fatalf("fallback match = %+v", m)
	}
	// Different families are never paired by position.
	after2 := planOf(t, "a2", jn("Append", "", jn("Result", `"Parent Relationship": "Member"`)))
	al = align(before, after2)
	if len(al.matches) != 1 || len(al.divergent) != 1 {
		t.Fatalf("cross-family pairing: matches=%d divergent=%d", len(al.matches), len(al.divergent))
	}
}

func TestAlignRootWrapperAndRootDivergence(t *testing.T) {
	before := planOf(t, "b", jn("Seq Scan", `"Relation Name": "invoices"`))
	after := planOf(t, "a", jn("Sort", `"Sort Key": ["total"]`, jn("Seq Scan", `"Relation Name": "invoices", "Parent Relationship": "Outer"`)))
	al := align(before, after)
	if got := matchMap(al); got["/Plan"] != "/Plan/Plans/0" {
		t.Fatalf("root not matched below added Sort: %v", got)
	}
	if paths(al.added) != "/Plan(wrapper)" || !strings.Contains(al.matches[0].Reason, "Sort added at the root") {
		t.Fatalf("added %s reason %q", paths(al.added), al.matches[0].Reason)
	}
	al = align(after, before)
	if paths(al.removed) != "/Plan(wrapper)" || matchMap(al)["/Plan/Plans/0"] != "/Plan" {
		t.Fatalf("removed root wrapper: %s %v", paths(al.removed), matchMap(al))
	}
	other := planOf(t, "o", jn("Result", ""))
	al = align(before, other)
	if len(al.matches) != 0 || len(al.divergent) != 1 || al.divergent[0].before != nil {
		t.Fatalf("root divergence: %+v", al)
	}
}

func TestAlignWrapperWithInitPlan(t *testing.T) {
	before := planOf(t, "b", jn("Limit", "", scan("invoices", "Outer")))
	after := planOf(t, "a", jn("Limit", "",
		jn("Sort", `"Parent Relationship": "Outer"`,
			jn("Result", `"Parent Relationship": "InitPlan", "Subplan Name": "InitPlan 1"`),
			scan("invoices", "Outer"))))
	al := align(before, after)
	if matchMap(al)["/Plan/Plans/0"] != "/Plan/Plans/0/Plans/1" {
		t.Fatalf("scan not matched below Sort: %v", matchMap(al))
	}
	if paths(al.added) != "/Plan/Plans/0(wrapper),/Plan/Plans/0/Plans/0" {
		t.Fatalf("added = %s", paths(al.added))
	}
}

func TestAlignHashJoinToNestedLoop(t *testing.T) {
	al := align(mustParseFile(t, "join_hash_before.json"), mustParseFile(t, "join_nestloop_after.json"))
	got := matchMap(al)
	if got["/Plan"] != "/Plan" || got["/Plan/Plans/0"] != "/Plan/Plans/0" || got["/Plan/Plans/1/Plans/0"] != "/Plan/Plans/1" {
		t.Fatalf("matches = %v", got)
	}
	if paths(al.removed) != "/Plan/Plans/1(wrapper)" || len(al.added) != 0 {
		t.Fatalf("removed %s added %s", paths(al.removed), paths(al.added))
	}
	for _, m := range al.matches {
		if m.BeforePath == "/Plan/Plans/1/Plans/0" && (m.Confidence != ConfidenceMedium || !strings.Contains(m.Reason, "removed Hash")) {
			t.Fatalf("match through removed Hash = %+v", m)
		}
	}
}

func TestSignature(t *testing.T) {
	a := &aligner{sig: map[*Node]string{}}
	p := mustParseFile(t, "cte_subplan.json")
	if got := a.signature(p.Root); got != "scan:CTE recent" {
		t.Errorf("CTE signature = %q", got)
	}
	if got := a.signature(p.Root.Plans[1]); got != "aggregate:{invoices}" {
		t.Errorf("aggregate signature = %q", got)
	}
	fn := &Node{NodeType: "Function Scan", FunctionName: "generate_series", Alias: "g"}
	if got := a.signature(fn); got != "scan:function generate_series" {
		t.Errorf("function signature = %q", got)
	}
	sub := &Node{NodeType: "Subquery Scan", Alias: "totals"}
	if got := a.signature(sub); got != "scan:alias totals" {
		t.Errorf("subquery signature = %q", got)
	}
}

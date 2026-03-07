package plans

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// decodeJSON decodes test input the way the parser does (json.Number).
func decodeJSON(t *testing.T, s string) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode test JSON: %v", err)
	}
	return v
}

func buildNode(t *testing.T, s string) (*Plan, *Node, error) {
	t.Helper()
	p := &Plan{}
	b := newTreeBuilder(p)
	n, err := b.node(decodeJSON(t, s), "/Plan", 0)
	b.finish()
	return p, n, err
}

func TestNodeFieldsDecoded(t *testing.T) {
	p, n, err := buildNode(t, `{
		"Node Type": "Hash Join", "Parent Relationship": "Outer", "Join Type": "Left",
		"Startup Cost": 38.50, "Total Cost": 1733.31, "Plan Rows": 2500, "Plan Width": 45,
		"Actual Startup Time": 0.611, "Actual Total Time": 13.904, "Actual Rows": 2431, "Actual Loops": 1,
		"Rows Removed by Join Filter": 7, "Hash Cond": "(i.customer_id = c.id)", "Join Filter": "(c.active)",
		"Shared Hit Blocks": 446, "Temp Read Blocks": 0, "Shared I/O Read Time": 1.250, "I/O Read Time": 0.5,
		"Inner Unique": true, "Async Capable": false,
		"Plans": [{"Node Type": "Sort", "Sort Key": ["a", "b DESC"], "Sort Method": "quicksort",
			"Sort Space Used": 25, "Sort Space Type": "Memory", "Workers Planned": 2, "Workers Launched": 1,
			"Index Cond": "(x = 1)", "Recheck Cond": "(y = 2)", "Merge Cond": "(a = b)", "Filter": "(z)"}]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if n.NodeType != "Hash Join" || n.JoinType != "Left" || n.ParentRelationship != "Outer" {
		t.Errorf("strings not decoded: %+v", n)
	}
	if n.TotalCost.String() != "1733.31" || n.PlanRows.String() != "2500" || n.ActualTotalTime.String() != "13.904" {
		t.Errorf("numbers not decoded: %s %s %s", n.TotalCost, n.PlanRows, n.ActualTotalTime)
	}
	if n.RowsRemovedByJoinFilter.String() != "7" || n.RowsRemovedByFilter != nil {
		t.Error("rows removed fields wrong: missing values must stay nil")
	}
	if n.Buffers.SharedHit.String() != "446" || n.Buffers.TempRead == nil || !n.Buffers.TempRead.IsZero() || n.Buffers.SharedRead != nil {
		t.Error("buffers: present zero must be zero, absent must be nil")
	}
	if n.IOTiming.SharedReadTime.String() != "1.250" || n.IOTiming.ReadTime.String() != "0.5" {
		t.Error("I/O timings not decoded")
	}
	c := n.Plans[0]
	if c.Path != "/Plan/Plans/0" || c.Depth != 1 {
		t.Errorf("child path/depth = %s/%d", c.Path, c.Depth)
	}
	if strings.Join(c.SortKey, "|") != "a|b DESC" || c.SortSpaceType != "Memory" || c.WorkersLaunched.String() != "1" {
		t.Errorf("sort/worker fields wrong: %+v", c)
	}
	if c.IndexCond == "" || c.RecheckCond == "" || c.MergeCond == "" || c.Filter == "" {
		t.Error("condition text not decoded")
	}
	if p.NodeCount != 2 || p.MaxDepth != 1 || !p.Analyzed || !p.Buffers || !p.Timing {
		t.Errorf("plan flags = count %d depth %d analyzed %v buffers %v timing %v", p.NodeCount, p.MaxDepth, p.Analyzed, p.Buffers, p.Timing)
	}
	if p.UnknownKeys != 0 {
		t.Errorf("recognized keys counted as unknown: %v", p.UnknownKeyNames)
	}
}

func TestUnknownKeysCounted(t *testing.T) {
	p, _, err := buildNode(t, `{"Node Type": "Seq Scan", "Future Metric": 1, "Plans": [
		{"Node Type": "Result", "Future Metric": 2, "Another": "x"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if p.UnknownKeys != 3 || strings.Join(p.UnknownKeyNames, ",") != "Another,Future Metric" {
		t.Fatalf("unknown = %d %v", p.UnknownKeys, p.UnknownKeyNames)
	}
}

func TestNodeDecodeErrors(t *testing.T) {
	cases := []struct {
		name, input, loc, msg string
	}{
		{"no node type", `{"Plan Rows": 1}`, "/Plan", `no "Node Type"`},
		{"empty node type", `{"Node Type": ""}`, "/Plan", "empty"},
		{"node not object", `[1]`, "/Plan", "plan node object"},
		{"number as string", `{"Node Type": "Seq Scan", "Plan Rows": "12"}`, "/Plan", `"Plan Rows" must be a number, got string`},
		{"string as number", `{"Node Type": "Seq Scan", "Relation Name": 5}`, "/Plan", `"Relation Name" must be a string, got number`},
		{"bool wrong", `{"Node Type": "Seq Scan", "Parallel Aware": "yes"}`, "/Plan", "boolean"},
		{"sort key wrong", `{"Node Type": "Sort", "Sort Key": [1]}`, "/Plan", "array of strings"},
		{"negative rows", `{"Node Type": "Seq Scan", "Plan Rows": -5}`, "/Plan", "negative"},
		{"plans not array", `{"Node Type": "Sort", "Plans": {}}`, "/Plan/Plans", "array"},
		{"bad child", `{"Node Type": "Sort", "Plans": [{"Node Type": "Seq Scan"}, {"Node Type": 3}]}`, "/Plan/Plans/1", "must be a string"},
	}
	for _, c := range cases {
		_, _, err := buildNode(t, c.input)
		var pe *ParseError
		if !errors.As(err, &pe) {
			t.Errorf("%s: err = %v, want *ParseError", c.name, err)
			continue
		}
		if pe.Location != c.loc || !strings.Contains(pe.Error(), c.msg) {
			t.Errorf("%s: got %q at %q, want %q at %q", c.name, pe.Error(), pe.Location, c.msg, c.loc)
		}
	}
}

func nestedNodes(depth int) string {
	var b bytes.Buffer
	for i := 0; i < depth; i++ {
		b.WriteString(`{"Node Type": "Limit", "Plans": [`)
	}
	b.WriteString(`{"Node Type": "Result"}`)
	for i := 0; i < depth; i++ {
		b.WriteString(`]}`)
	}
	return b.String()
}

func TestTreeBounds(t *testing.T) {
	if _, _, err := buildNode(t, nestedNodes(MaxNodeDepth)); err != nil {
		t.Fatalf("depth %d rejected: %v", MaxNodeDepth, err)
	}
	if _, _, err := buildNode(t, nestedNodes(MaxNodeDepth+1)); !errors.Is(err, ErrTooDeep) {
		t.Fatalf("depth %d: err = %v, want ErrTooDeep", MaxNodeDepth+1, err)
	}
	wide := func(n int) string {
		children := make([]string, n)
		for i := range children {
			children[i] = `{"Node Type": "Result"}`
		}
		return `{"Node Type": "Append", "Plans": [` + strings.Join(children, ",") + `]}`
	}
	if _, _, err := buildNode(t, wide(MaxNodes-1)); err != nil {
		t.Fatalf("%d nodes rejected: %v", MaxNodes, err)
	}
	if _, _, err := buildNode(t, wide(MaxNodes)); !errors.Is(err, ErrTooManyNodes) {
		t.Fatalf("%d nodes: err = %v, want ErrTooManyNodes", MaxNodes+1, err)
	}
}

func TestParseErrorFormat(t *testing.T) {
	e := &ParseError{Name: "before.json", Location: "/Plan", Err: errors.New("boom")}
	if e.Error() != "plan before.json: at /Plan: boom" {
		t.Errorf("Error() = %q", e.Error())
	}
	e = &ParseError{Err: errors.New("boom")}
	if e.Error() != "plan: boom" {
		t.Errorf("Error() = %q", e.Error())
	}
}

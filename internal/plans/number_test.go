package plans

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func num(t *testing.T, s string) *Number {
	t.Helper()
	n, err := parseNumber(json.Number(s))
	if err != nil {
		t.Fatalf("parseNumber(%q): %v", s, err)
	}
	return &n
}

func TestParseNumberKeepsLiteralAndExactValue(t *testing.T) {
	for _, s := range []string{"0", "0.00", "1688.00", "12.630", "10000000000.00", "1e3", "300000"} {
		n := num(t, s)
		if n.String() != s {
			t.Errorf("String() = %q, want %q", n.String(), s)
		}
		data, err := json.Marshal(n)
		if err != nil || string(data) != s {
			t.Errorf("MarshalJSON = %s, %v; want %s", data, err, s)
		}
	}
	// 0.1 + 0.2 is exactly 0.3 in decimal, unlike float64.
	sum := sumKnown(num(t, "0.1"), num(t, "0.2"))
	if !sum.Decimal().Equal(decimal.RequireFromString("0.3")) {
		t.Fatalf("0.1+0.2 = %s", sum)
	}
}

func TestParseNumberRejects(t *testing.T) {
	cases := map[string]string{
		"negative":       "-1",
		"huge exponent":  "1e999999999",
		"tiny exponent":  "1e-2000000000",
		"too long":       "1" + strings.Repeat("0", 60),
		"empty":          "",
		"not numeric":    "abc",
		"exponent >40":   "1e41",
		"exponent < -40": "1e-41",
	}
	for name, s := range cases {
		if _, err := parseNumber(json.Number(s)); err == nil {
			t.Errorf("%s: parseNumber(%q) succeeded", name, s)
		}
	}
}

func TestSumKnownKeepsUnknown(t *testing.T) {
	if got := sumKnown(nil, nil); got != nil {
		t.Fatalf("sum of unknowns = %v, want nil", got)
	}
	got := sumKnown(nil, num(t, "0"))
	if got == nil || !got.IsZero() {
		t.Fatalf("sum with a known zero = %v, want 0", got)
	}
	if got := sumKnown(num(t, "236"), num(t, "237")); got.String() != "473" {
		t.Fatalf("sum = %s", got)
	}
}

func TestRowRatio(t *testing.T) {
	d := decimal.RequireFromString
	cases := []struct{ a, b, want string }{
		{"3200", "48210", "15.0656"},
		{"48210", "3200", "15.0656"},
		{"0", "500", "500"},
		{"1", "0", "1"},
		{"0", "0", "1"},
		{"0.5", "40", "40"},
	}
	for _, c := range cases {
		if got := rowRatio(d(c.a), d(c.b)); !got.Equal(d(c.want)) {
			t.Errorf("rowRatio(%s, %s) = %s, want %s", c.a, c.b, got, c.want)
		}
	}
}

func TestFormatting(t *testing.T) {
	if got := fmtNum(nil); got != "unknown" {
		t.Errorf("fmtNum(nil) = %q", got)
	}
	if got := fmtDec(decimal.RequireFromString("15.0656")); got != "15.07" {
		t.Errorf("fmtDec = %q", got)
	}
}

func TestBuffersAndSelfTemp(t *testing.T) {
	var empty Buffers
	if empty.Known() || empty.Temp() != nil {
		t.Fatal("empty buffers must be unknown, not zero")
	}
	child := &Node{Buffers: Buffers{TempWritten: num(t, "1100"), TempRead: num(t, "0")}}
	parent := &Node{
		Buffers: Buffers{TempRead: num(t, "2190"), TempWritten: num(t, "2210")},
		Plans:   []*Node{child, {}},
	}
	if got := parent.SelfTemp(); got.String() != "3300" {
		t.Fatalf("parent SelfTemp = %s, want 3300", got)
	}
	if got := child.SelfTemp(); got.String() != "1100" {
		t.Fatalf("child SelfTemp = %s", got)
	}
	// Parallel workers can make children report more than the parent;
	// the result is clamped instead of going negative.
	odd := &Node{Buffers: Buffers{TempRead: num(t, "5")}, Plans: []*Node{child}}
	if got := odd.SelfTemp(); !got.IsZero() {
		t.Fatalf("clamped SelfTemp = %s", got)
	}
	if (&Node{}).SelfTemp() != nil {
		t.Fatal("SelfTemp without counters must be nil")
	}
}

func TestWalkPreOrderAndStop(t *testing.T) {
	p := &Plan{Root: &Node{Path: "a", Plans: []*Node{
		{Path: "b", Plans: []*Node{{Path: "c"}}},
		{Path: "d"},
	}}}
	var got []string
	for _, n := range p.Nodes() {
		got = append(got, n.Path)
	}
	if strings.Join(got, ",") != "a,b,c,d" {
		t.Fatalf("pre-order = %v", got)
	}
	count := 0
	p.Walk(func(n *Node) bool { count++; return n.Path != "b" })
	if count != 2 {
		t.Fatalf("walk visited %d nodes after stop, want 2", count)
	}
	var nilPlan *Plan
	nilPlan.Walk(func(*Node) bool { t.Fatal("visited node of nil plan"); return true })
}

func TestExecuted(t *testing.T) {
	if (&Node{}).Executed() {
		t.Error("estimated node reported as executed")
	}
	if (&Node{ActualLoops: num(t, "0")}).Executed() {
		t.Error("never-executed node reported as executed")
	}
	if !(&Node{ActualLoops: num(t, "3")}).Executed() {
		t.Error("executed node not reported")
	}
}

package finding

import (
	"testing"
)

func TestSortIsDeterministic(t *testing.T) {
	fs := []Finding{
		{Module: ModuleCompare, Code: "compare.value_changed", Kind: KindDifference, Severity: SeverityError, CaseID: "b", Location: "/items/10/id", Message: "m"},
		{Module: ModuleCompare, Code: "compare.value_changed", Kind: KindDifference, Severity: SeverityError, CaseID: "b", Location: "/items/2/id", Message: "m"},
		{Module: ModuleReplay, Code: "replay.status", Kind: KindAssertionFailure, Severity: SeverityError, CaseID: "a", Message: "m"},
		{Module: ModuleCompare, Code: "compare.field_missing", Kind: KindDifference, Severity: SeverityError, CaseID: "b", Location: "/items/2/id", Message: "m"},
		{Module: ModuleStream, Code: "stream.eof", Kind: KindObservation, Severity: SeverityInfo, Message: "m"},
	}
	Sort(fs)
	want := []string{
		"|stream.eof",
		"a|replay.status",
		"b|compare.field_missing",
		"b|compare.value_changed",
		"b|compare.value_changed",
	}
	for i, f := range fs {
		if got := f.CaseID + "|" + f.Code; got != want[i] {
			t.Fatalf("position %d = %s, want %s", i, got, want[i])
		}
	}
	// Numeric index ordering: /items/2 before /items/10.
	if fs[3].Location != "/items/2/id" || fs[4].Location != "/items/10/id" {
		t.Fatalf("locations not in numeric order: %q, %q", fs[3].Location, fs[4].Location)
	}
}

func TestCompareLocation(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"/a/2", "/a/10", -1},
		{"/a/10", "/a/2", 1},
		{"/a", "/a/0", -1},
		{"/a/0", "/a/b", -1},
		{"/b", "/a", 1},
		{"status", "status", 0},
		{"event:2", "event:10", -1},
		{"event:10", "event:9", 1},
		{"event:2", "gap:1", -1},
	}
	for _, tt := range tests {
		if got := compareLocation(tt.a, tt.b); got != tt.want {
			t.Errorf("compareLocation(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestSummarizeAndFailing(t *testing.T) {
	fs := []Finding{
		{Kind: KindDifference, Severity: SeverityError},
		{Kind: KindInconclusive, Severity: SeverityWarning},
		{Kind: KindObservation, Severity: SeverityInfo},
	}
	s := Summarize(fs)
	if s.Total != 3 || s.Failing != 1 {
		t.Fatalf("summary = %+v", s)
	}
	if s.ByKind[KindTransportError] != 0 {
		t.Fatal("expected zero-valued keys to be present")
	}
	if _, ok := s.ByKind[KindUnsupported]; !ok {
		t.Fatal("expected every kind key in summary")
	}
	if !AnyFailing(fs) || AnyFailing(fs[1:]) {
		t.Fatal("AnyFailing mismatch")
	}
}

func TestValidate(t *testing.T) {
	ok := Finding{Module: ModuleReplay, Code: "replay.x", Kind: KindTransportError, Severity: SeverityError, Message: "m"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := ok
	bad.Kind = "nope"
	if bad.Validate() == nil {
		t.Fatal("expected unknown kind error")
	}
	bad = ok
	bad.Message = ""
	if bad.Validate() == nil {
		t.Fatal("expected missing message error")
	}
}

func TestCollectorBounds(t *testing.T) {
	c := NewCollector(2)
	for i := 0; i < 5; i++ {
		c.Add(Finding{Code: "x"})
	}
	if len(c.Items()) != 2 || c.Dropped() != 3 {
		t.Fatalf("items=%d dropped=%d", len(c.Items()), c.Dropped())
	}
}

func TestWithDetailCopies(t *testing.T) {
	a := Finding{Details: map[string]string{"k": "v"}}
	b := a.WithDetail("x", "y")
	if _, ok := a.Details["x"]; ok {
		t.Fatal("WithDetail modified the original map")
	}
	if b.Details["k"] != "v" || b.Details["x"] != "y" {
		t.Fatalf("details = %v", b.Details)
	}
}

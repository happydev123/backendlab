package compare

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/happydev123/backendlab/internal/jsonptr"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/suite"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestOptionsFromSpec(t *testing.T) {
	lim := limits.Default()
	lim.MaxDifferences = 7
	lim.MaxValuePreview = 33
	spec := suite.CompareSpec{
		Ignore:         []string{"/meta", "/items/*/updated_at"},
		Headers:        []string{"content-type", "X-Request-Id", "Content-Type"},
		Tolerance:      &suite.Tolerance{Absolute: "0.01"},
		PathTolerances: []suite.PathTolerance{{Pointer: "/totals", Tolerance: suite.Tolerance{Relative: "0.05"}}},
		Unordered:      []suite.UnorderedArray{{Pointer: "/items", Key: "id"}},
		SkipStatus:     true,
	}
	o, err := OptionsFromSpec(spec, lim, nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.MaxDifferences != 7 || o.MaxValuePreview != 33 || !o.SkipStatus {
		t.Fatalf("bounds not copied: %+v", o)
	}
	if got := strings.Join(o.Headers, ","); got != "Content-Type,X-Request-Id" {
		t.Fatalf("headers = %s", got)
	}
	if !o.ignored(jsonptr.MustParse("/meta/request_id")) || !o.ignored(jsonptr.MustParse("/items/3/updated_at")) {
		t.Fatal("ignore patterns not applied by prefix")
	}
	if o.ignored(jsonptr.MustParse("/metadata")) {
		t.Fatal("ignore matched a sibling key")
	}
	if tol := o.toleranceAt(jsonptr.MustParse("/totals/net")); !tol.Relative.Equal(dec("0.05")) || !tol.Absolute.IsZero() {
		t.Fatalf("path tolerance not applied: %s", tol)
	}
	if tol := o.toleranceAt(jsonptr.MustParse("/price")); !tol.Absolute.Equal(dec("0.01")) {
		t.Fatalf("global tolerance not applied: %s", tol)
	}
	if key, ok := o.unorderedKey(jsonptr.MustParse("/items")); !ok || key != "id" {
		t.Fatal("unordered rule not found")
	}
	if _, ok := o.unorderedKey(jsonptr.MustParse("/items/0")); ok {
		t.Fatal("unordered rule must match the array pointer exactly")
	}
}

func TestOptionsFromSpecErrors(t *testing.T) {
	lim := limits.Default()
	cases := map[string]suite.CompareSpec{
		"bad ignore":        {Ignore: []string{"meta"}},
		"bad tolerance":     {Tolerance: &suite.Tolerance{Absolute: "abc"}},
		"negative relative": {PathTolerances: []suite.PathTolerance{{Pointer: "/a", Tolerance: suite.Tolerance{Relative: "-1"}}}},
		"bad path pattern":  {PathTolerances: []suite.PathTolerance{{Pointer: "a", Tolerance: suite.Tolerance{Relative: "1"}}}},
		"missing key":       {Unordered: []suite.UnorderedArray{{Pointer: "/items"}}},
	}
	for name, spec := range cases {
		if _, err := OptionsFromSpec(spec, lim, nil); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestToleranceWithin(t *testing.T) {
	cases := []struct {
		name      string
		abs, rel  string
		base, cnd string
		want      bool
	}{
		{"exact equal", "", "", "1.0", "1.00", true},
		{"exact differs", "", "", "1.0", "1.01", false},
		{"abs inside", "0.01", "", "10.00", "10.01", true},
		{"abs boundary", "0.01", "", "10.00", "9.99", true},
		{"abs outside", "0.01", "", "10.00", "10.02", false},
		{"rel inside", "", "0.1", "100", "109", true},
		{"rel outside", "", "0.1", "100", "111", false},
		{"rel uses baseline", "", "0.1", "-100", "-110", true},
		{"rel zero baseline", "", "0.5", "0", "0.1", false},
		{"either applies", "0.5", "0.001", "1000", "1000.9", true},
	}
	for _, c := range cases {
		tol, err := ParseTolerance(c.abs, c.rel)
		if err != nil {
			t.Fatal(err)
		}
		if got := tol.Within(dec(c.base), dec(c.cnd)); got != c.want {
			t.Errorf("%s: Within(%s, %s) = %v", c.name, c.base, c.cnd, got)
		}
	}
}

func TestToleranceString(t *testing.T) {
	for _, c := range []struct{ abs, rel, want string }{
		{"", "", "exact"},
		{"0.5", "", "absolute 0.5"},
		{"", "0.1", "relative 0.1"},
		{"1", "0.1", "absolute 1 or relative 0.1"},
	} {
		tol, _ := ParseTolerance(c.abs, c.rel)
		if got := tol.String(); got != c.want {
			t.Errorf("String() = %q, want %q", got, c.want)
		}
	}
}

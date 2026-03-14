package plans

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestParseFailOn(t *testing.T) {
	cases := []struct {
		in   string
		want map[string]bool
	}{
		{"", map[string]bool{}},
		{"scan", map[string]bool{"scan": true}},
		{" Scan , spill,,temp_io ", map[string]bool{"scan": true, "spill": true, "temp_io": true}},
		{"join,join", map[string]bool{"join": true}},
		{"all", map[string]bool{"scan": true, "join": true, "estimate": true, "spill": true, "temp_io": true, "structure": true}},
	}
	for _, c := range cases {
		got, err := ParseFailOn(c.in)
		if err != nil {
			t.Errorf("ParseFailOn(%q): %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseFailOn(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	_, err := ParseFailOn("scan,latency")
	if err == nil || !strings.Contains(err.Error(), `"latency"`) || !strings.Contains(err.Error(), "estimate, join, scan, spill, structure, temp_io") {
		t.Fatalf("unknown class error = %v", err)
	}
}

func TestOptionsValidate(t *testing.T) {
	good := []Options{
		{},
		{RowFactor: 4, MinRows: 1, MaxFindings: 10, FailOn: map[string]bool{"scan": true}},
	}
	for _, o := range good {
		if err := o.Validate(); err != nil {
			t.Errorf("Validate(%+v): %v", o, err)
		}
	}
	bad := []Options{
		{RowFactor: 1},
		{RowFactor: 0.5},
		{RowFactor: math.NaN()},
		{RowFactor: math.Inf(1)},
		{MinRows: -1},
		{MaxFindings: -3},
		{FailOn: map[string]bool{"latency": true}},
		{FailOn: map[string]bool{"scan,join": true}},
	}
	for _, o := range bad {
		if err := o.Validate(); err == nil {
			t.Errorf("Validate(%+v) accepted", o)
		}
	}
}

func TestOptionsNormalized(t *testing.T) {
	n := Options{}.normalized()
	if n.RowFactor != DefaultRowFactor || n.MinRows != DefaultMinRows || n.MaxFindings != DefaultMaxFindings {
		t.Fatalf("defaults = %+v", n)
	}
	n = Options{RowFactor: math.NaN(), MinRows: -4}.normalized()
	if n.RowFactor != DefaultRowFactor || n.MinRows != DefaultMinRows {
		t.Fatalf("invalid values not replaced: %+v", n)
	}
	n = Options{RowFactor: 3, MinRows: 5, MaxFindings: 7}.normalized()
	if n.RowFactor != 3 || n.MinRows != 5 || n.MaxFindings != 7 {
		t.Fatalf("explicit values changed: %+v", n)
	}
}

func TestFailClass(t *testing.T) {
	cases := map[string]string{
		CodeScanStrategyChanged: FailScan,
		CodeJoinStrategyChanged: FailJoin,
		CodeEstimateMismatch:    FailEstimate,
		CodeSortSpillIntroduced: FailSpill,
		CodeTempIOIntroduced:    FailTempIO,
		CodeStructureChanged:    FailStructure,
		CodeNodeAdded:           FailStructure,
		CodeNodeRemoved:         FailStructure,
		CodeCostChanged:         "",
		CodeRuntimeInconclusive: "",
	}
	for code, want := range cases {
		if got := failClass(code); got != want {
			t.Errorf("failClass(%s) = %q, want %q", code, got, want)
		}
	}
}

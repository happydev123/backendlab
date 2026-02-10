package validate

import (
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/redact"
)

func invoiceSum(t testing.TB, tol string, round *roundSpec) *sumRule {
	t.Helper()
	r, err := compileSum(newBase("totals", typeSum), ruleSpec{
		Terms: []termSpec{
			{Field: "/subtotal"},
			{Field: "/tax"},
			{Field: "/discount", Sign: intp(-1), Optional: true},
		},
		Equals:    "/total",
		Tolerance: tol,
		Round:     round,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSumRule(t *testing.T) {
	tests := []struct {
		name  string
		tol   string
		round *roundSpec
		doc   string
		want  string
		kind  finding.Kind
	}{
		{name: "exact match", doc: `{"subtotal":"100.00","tax":"8.25","discount":"5.00","total":"103.25"}`},
		{name: "float trap 0.1+0.2", doc: `{"subtotal":0.1,"tax":0.2,"total":0.3}`},
		{name: "float trap strings", doc: `{"subtotal":"0.1","tax":"0.2","total":"0.30"}`},
		{name: "mixed numbers and strings", doc: `{"subtotal":100,"tax":"8.25","total":108.25}`},
		{name: "optional discount absent", doc: `{"subtotal":"100.00","tax":"8.25","total":"108.25"}`},
		{name: "optional discount null", doc: `{"subtotal":"100.00","tax":"8.25","discount":null,"total":"108.25"}`},
		{name: "mismatch exact", doc: `{"subtotal":"100.00","tax":"8.25","total":"108.26"}`,
			want: "validate.sum@/total", kind: finding.KindAssertionFailure},
		{name: "within tolerance", tol: "0.01", doc: `{"subtotal":"100.00","tax":"8.25","total":"108.26"}`},
		{name: "beyond tolerance", tol: "0.01", doc: `{"subtotal":"100.00","tax":"8.25","total":"108.27"}`,
			want: "validate.sum@/total", kind: finding.KindAssertionFailure},
		{name: "rounded half_up", round: &roundSpec{Scale: intp(2), Mode: "half_up"},
			doc: `{"subtotal":"100.004","tax":"0.001","total":"100.01"}`},
		{name: "rounded down differs", round: &roundSpec{Scale: intp(2), Mode: "down"},
			doc: `{"subtotal":"100.004","tax":"0.001","total":"100.01"}`, want: "validate.sum@/total", kind: finding.KindAssertionFailure},
		{name: "missing required term", doc: `{"subtotal":"100.00","total":"100.00"}`,
			want: "validate.input_missing@/tax", kind: finding.KindInconclusive},
		{name: "missing equals", doc: `{"subtotal":"100.00","tax":"1"}`,
			want: "validate.input_missing@/total", kind: finding.KindInconclusive},
		{name: "null required term", doc: `{"subtotal":null,"tax":"1","total":"1"}`,
			want: "validate.input_missing@/subtotal", kind: finding.KindInconclusive},
		{name: "non-decimal term", doc: `{"subtotal":"100,00","tax":"1","total":"101"}`,
			want: "validate.sum@/subtotal", kind: finding.KindAssertionFailure},
		{name: "invalid optional term still fails", doc: `{"subtotal":"1","tax":"1","discount":true,"total":"2"}`,
			want: "validate.sum@/discount", kind: finding.KindAssertionFailure},
		{name: "invalid wins over missing", doc: `{"subtotal":{},"total":"1"}`,
			want: "validate.sum@/subtotal", kind: finding.KindAssertionFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := invoiceSum(t, tt.tol, tt.round)
			fs := runRule(t, r, mustDecode(t, tt.doc))
			if got := codes(fs); got != tt.want {
				t.Fatalf("got %q, want %q (%+v)", got, tt.want, fs)
			}
			if len(fs) > 0 && fs[0].Kind != tt.kind {
				t.Fatalf("kind = %s, want %s", fs[0].Kind, tt.kind)
			}
		})
	}
}

func TestSumRuleMessages(t *testing.T) {
	r := invoiceSum(t, "0.01", nil)
	if r.expected != "/subtotal + /tax - /discount (optional) = /total (tolerance 0.01)" {
		t.Fatalf("expected = %q", r.expected)
	}
	fs := runRule(t, r, mustDecode(t, `{"subtotal":"100.00","tax":"8.25","discount":"5.00","total":"103.30"}`))
	if len(fs) != 1 {
		t.Fatalf("findings = %+v", fs)
	}
	want := `rule "totals": computed 103.25 from /subtotal (100.00) + /tax (8.25) - /discount (5.00); stated /total is 103.30; difference 0.05 exceeds tolerance 0.01`
	if fs[0].Message != want || fs[0].Observed != "103.30" || fs[0].Details["rule_id"] != "totals" {
		t.Fatalf("finding = %+v", fs[0])
	}

	rr := invoiceSum(t, "", &roundSpec{Scale: intp(2), Mode: "half_even"})
	fs = runRule(t, rr, mustDecode(t, `{"subtotal":"1.005","tax":"0","total":"1.01"}`))
	if len(fs) != 1 || !strings.Contains(fs[0].Message, "computed 1.005, 1.00 after rounding half_even to 2 places") ||
		!strings.Contains(fs[0].Message, "difference 0.01") || strings.Contains(fs[0].Message, "exceeds") {
		t.Fatalf("rounded message: %+v", fs)
	}
	if rr.expected != "/subtotal + /tax - /discount (optional) = /total (rounded half_even to 2 places, exact)" {
		t.Fatalf("expected = %q", rr.expected)
	}

	fs = runRule(t, r, mustDecode(t, `{"subtotal":"1"}`))
	if len(fs) != 1 || !strings.Contains(fs[0].Message, "/tax, /total absent or null") || fs[0].Failing() {
		t.Fatalf("missing message: %+v", fs)
	}
}

func TestSumRuleRedaction(t *testing.T) {
	red, _ := redact.New(redact.Policy{JSONPaths: []string{"/tax"}})
	r := invoiceSum(t, "", nil)
	fs := runRuleWith(t, r, mustDecode(t, `{"subtotal":"100.00","tax":"7.77","total":"108.00"}`), newSanitizer(red, 0))
	if len(fs) != 1 || strings.Contains(fs[0].Message, "7.77") || strings.Contains(fs[0].Message, "107.77") ||
		!strings.Contains(fs[0].Message, "computed [REDACTED]") {
		t.Fatalf("redaction: %+v", fs)
	}
}

func TestSumRuleCompileErrors(t *testing.T) {
	tests := []struct {
		spec ruleSpec
		err  string
	}{
		{ruleSpec{Equals: "/t"}, "at least one term"},
		{ruleSpec{Terms: []termSpec{{Field: "/a", Sign: intp(2)}}, Equals: "/t"}, "sign must be 1 or -1"},
		{ruleSpec{Terms: []termSpec{{Field: "/a", Sign: intp(0)}}, Equals: "/t"}, "sign must be 1 or -1"},
		{ruleSpec{Terms: []termSpec{{Field: "/a"}, {Field: "/a"}}, Equals: "/t"}, "twice"},
		{ruleSpec{Terms: []termSpec{{Field: "/a"}}, Equals: "/a"}, "also a term"},
		{ruleSpec{Terms: []termSpec{{Field: "/a"}}}, "equals is required"},
		{ruleSpec{Terms: []termSpec{{Field: "/items/*/a"}}, Equals: "/t"}, "wildcards are not allowed"},
		{ruleSpec{Terms: []termSpec{{Field: "a"}}, Equals: "/t"}, "must start with '/'"},
		{ruleSpec{Terms: []termSpec{{Field: "/a"}}, Equals: "/t", Tolerance: "-0.01"}, "must not be negative"},
		{ruleSpec{Terms: []termSpec{{Field: "/a"}}, Equals: "/t", Round: &roundSpec{Scale: intp(2), Mode: "up"}}, "unknown round.mode"},
	}
	for _, tt := range tests {
		_, err := compileSum(newBase("s", typeSum), tt.spec)
		if err == nil || !strings.Contains(err.Error(), tt.err) {
			t.Errorf("%+v: err = %v, want %q", tt.spec, err, tt.err)
		}
	}
	neg, err := compileSum(newBase("s", typeSum), ruleSpec{Terms: []termSpec{{Field: "/a", Sign: intp(-1)}, {Field: "/b"}}, Equals: "/t"})
	if err != nil || neg.formula() != "-/a + /b" {
		t.Fatalf("formula = %q %v", neg.formula(), err)
	}
	if fs := runRule(t, neg, mustDecode(t, `{"a":"2.50","b":"10","t":"7.5"}`)); len(fs) != 0 {
		t.Fatalf("negative first term: %+v", fs)
	}
}

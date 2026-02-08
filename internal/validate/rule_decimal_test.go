package validate

import (
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/redact"
)

func strp(s string) *string { return &s }

func TestDecimalRule(t *testing.T) {
	money := ruleSpec{
		Fields:   []string{"/total", "/line_items/*/amount"},
		MaxScale: intp(2),
		Min:      strp("0"),
		Max:      strp("1000000"),
	}
	tests := []struct {
		name   string
		accept []string
		doc    string
		want   string
	}{
		{"numbers and strings", nil, `{"total":10.50,"line_items":[{"amount":"5.25"},{"amount":5.25}]}`, ""},
		{"too many places", nil, `{"total":"10.505"}`, "validate.decimal@/total"},
		{"scale counts trailing zeros", nil, `{"total":"10.500"}`, "validate.decimal@/total"},
		{"negative below min", nil, `{"total":"-1.00"}`, "validate.decimal@/total"},
		{"above max", nil, `{"total":1000000.01}`, "validate.decimal@/total"},
		{"string only rejects number", []string{"string"}, `{"total":10.5}`, "validate.decimal@/total"},
		{"number only rejects string", []string{"number"}, `{"total":"10.5"}`, "validate.decimal@/total"},
		{"thousands separator", nil, `{"total":"1,000.00"}`, "validate.decimal@/total"},
		{"exponent string", nil, `{"total":"1e3"}`, "validate.decimal@/total"},
		{"exponent number within scale", []string{"number"}, `{"total":1.5e2}`, ""},
		{"boolean", nil, `{"total":true}`, "validate.decimal@/total"},
		{"null skipped", nil, `{"total":null}`, ""},
		{"absent skipped", nil, `{}`, ""},
		{"wildcard item", nil, `{"line_items":[{"amount":"1.00"},{"amount":"x"}]}`, "validate.decimal@/line_items/1/amount"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := money
			spec.Accept = tt.accept
			r, err := compileDecimal(newBase("amounts", typeDecimal), spec)
			if err != nil {
				t.Fatal(err)
			}
			if got := codes(runRule(t, r, mustDecode(t, tt.doc))); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
	r, _ := compileDecimal(newBase("amounts", typeDecimal), money)
	if r.expected != "decimal number or decimal string, at most 2 decimal places, >= 0, <= 1000000" {
		t.Fatalf("expected = %q", r.expected)
	}
	fs := runRule(t, r, mustDecode(t, `{"total":"-1.005"}`))
	if len(fs) != 1 || !strings.Contains(fs[0].Message, "has 3 decimal places, at most 2 allowed and is below the minimum") {
		t.Fatalf("combined problems: %+v", fs)
	}
}

func TestDecimalRuleCompileErrors(t *testing.T) {
	tests := []struct {
		spec ruleSpec
		err  string
	}{
		{ruleSpec{}, "at least one field"},
		{ruleSpec{Fields: []string{"/a"}, Accept: []string{"float"}}, "unknown accept value"},
		{ruleSpec{Fields: []string{"/a"}, Accept: []string{"number", "number"}}, "twice"},
		{ruleSpec{Fields: []string{"/a"}, MaxScale: intp(-1)}, "max_scale"},
		{ruleSpec{Fields: []string{"/a"}, MaxScale: intp(41)}, "max_scale"},
		{ruleSpec{Fields: []string{"/a"}, Min: strp("abc")}, "min must be a decimal string"},
		{ruleSpec{Fields: []string{"/a"}, Max: strp("1e3")}, "max must be a decimal string"},
		{ruleSpec{Fields: []string{"/a"}, Min: strp("10"), Max: strp("1")}, "greater than max"},
	}
	for _, tt := range tests {
		_, err := compileDecimal(newBase("d", typeDecimal), tt.spec)
		if err == nil || !strings.Contains(err.Error(), tt.err) {
			t.Errorf("%+v: err = %v, want %q", tt.spec, err, tt.err)
		}
	}
}

func TestDecimalRuleRedactsParseError(t *testing.T) {
	red, _ := redact.New(redact.Policy{JSONPaths: []string{"/secret_amount"}})
	r, _ := compileDecimal(newBase("d", typeDecimal), ruleSpec{Fields: []string{"/secret_amount"}})
	fs := runRuleWith(t, r, mustDecode(t, `{"secret_amount":"12,34 hidden"}`), newSanitizer(red, 0))
	if len(fs) != 1 || strings.Contains(fs[0].Message, "hidden") || fs[0].Observed != redact.Marker {
		t.Fatalf("leaked: %+v", fs)
	}
}

package validate

import (
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/redact"
)

func TestRequiredRule(t *testing.T) {
	tests := []struct {
		name      string
		fields    []string
		allowNull bool
		doc       string
		want      string
	}{
		{"all present", []string{"/a", "/b/c"}, false, `{"a":1,"b":{"c":"x"}}`, ""},
		{"absent", []string{"/a", "/b"}, false, `{"a":1}`, "validate.required@/b"},
		{"null rejected by default", []string{"/a"}, false, `{"a":null}`, "validate.required@/a"},
		{"null allowed", []string{"/a"}, true, `{"a":null}`, ""},
		{"nested parent absent", []string{"/customer/name"}, false, `{}`, "validate.required@/customer/name"},
		{"wildcard per item", []string{"/items/*/amount"}, false,
			`{"items":[{"amount":"1"},{},{"amount":null},"x"]}`,
			"validate.required@/items/1/amount validate.required@/items/2/amount validate.required@/items/3/amount"},
		{"wildcard with no array", []string{"/items/*/amount"}, false, `{}`, ""},
		{"empty string is present", []string{"/a"}, false, `{"a":""}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := compileRequired(newBase("req", typeRequired), ruleSpec{Fields: tt.fields, AllowNull: tt.allowNull})
			if err != nil {
				t.Fatal(err)
			}
			fs := runRule(t, r, mustDecode(t, tt.doc))
			if got := codes(fs); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
	if _, err := compileRequired(newBase("req", typeRequired), ruleSpec{Fields: []string{"/items/*"}}); err == nil {
		t.Fatal("trailing wildcard accepted")
	}
	r, _ := compileRequired(newBase("req", typeRequired), ruleSpec{Fields: []string{"/a"}})
	fs := runRule(t, r, mustDecode(t, `{"a":null}`))
	if fs[0].Observed != "null" || fs[0].Expected != "field present and not null" || !strings.Contains(fs[0].Message, "allow_null") {
		t.Fatalf("finding = %+v", fs[0])
	}
}

func TestAllowedValuesRule(t *testing.T) {
	r, err := compileAllowedValues(newBase("cur", typeAllowedValues), ruleSpec{Field: "/currency", Values: []string{"EUR", "USD"}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		doc, want, observed string
	}{
		{`{"currency":"EUR"}`, "", ""},
		{`{"currency":"eur"}`, "validate.allowed_values@/currency", `"eur"`},
		{`{"currency":978}`, "validate.allowed_values@/currency", "978"},
		{`{"currency":null}`, "", ""},
		{`{}`, "", ""},
	}
	for _, tt := range tests {
		fs := runRule(t, r, mustDecode(t, tt.doc))
		if got := codes(fs); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.doc, got, tt.want)
			continue
		}
		if len(fs) > 0 && (fs[0].Observed != tt.observed || fs[0].Expected != "one of EUR, USD") {
			t.Errorf("%s: finding = %+v", tt.doc, fs[0])
		}
	}
	w, _ := compileAllowedValues(newBase("cur", typeAllowedValues), ruleSpec{Field: "/items/*/currency", Values: []string{"EUR"}})
	if got := codes(runRule(t, w, mustDecode(t, `{"items":[{"currency":"EUR"},{"currency":"GBP"}]}`))); got != "validate.allowed_values@/items/1/currency" {
		t.Fatalf("wildcard: %s", got)
	}
	for _, spec := range []ruleSpec{
		{Field: "/c"},
		{Field: "/c", Values: []string{"A", "A"}},
		{Field: "", Values: []string{"A"}},
	} {
		if _, err := compileAllowedValues(newBase("x", typeAllowedValues), spec); err == nil {
			t.Errorf("%+v accepted", spec)
		}
	}
}

func TestPatternRule(t *testing.T) {
	r, err := compilePattern(newBase("num", typePattern), ruleSpec{Field: "/invoice_number", Pattern: `^INV-[0-9]{4}$`})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ doc, want string }{
		{`{"invoice_number":"INV-1001"}`, ""},
		{`{"invoice_number":"INV-10011"}`, "validate.pattern@/invoice_number"},
		{`{"invoice_number":1001}`, "validate.pattern@/invoice_number"},
		{`{"invoice_number":null}`, ""},
	}
	for _, tt := range tests {
		if got := codes(runRule(t, r, mustDecode(t, tt.doc))); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.doc, got, tt.want)
		}
	}
	if _, err := compilePattern(newBase("x", typePattern), ruleSpec{Field: "/a", Pattern: `(a`}); err == nil {
		t.Fatal("invalid regexp accepted")
	}
	if _, err := compilePattern(newBase("x", typePattern), ruleSpec{Field: "/a", Pattern: `(?=a)`}); err == nil {
		t.Fatal("lookahead (not RE2) accepted")
	}
	if _, err := compilePattern(newBase("x", typePattern), ruleSpec{Field: "/a"}); err == nil {
		t.Fatal("empty pattern accepted")
	}
	if _, err := compilePattern(newBase("x", typePattern), ruleSpec{Field: "/a", Pattern: strings.Repeat("a", MaxPatternLength+1)}); err == nil {
		t.Fatal("long pattern accepted")
	}
}

func TestValueRulesRedactObserved(t *testing.T) {
	red, _ := redact.New(redact.Policy{JSONPaths: []string{"/customer/tax_id"}})
	r, _ := compilePattern(newBase("tax", typePattern), ruleSpec{Field: "/customer/tax_id", Pattern: `^DE[0-9]{9}$`})
	r.severity = finding.SeverityWarning
	fs := runRuleWith(t, r, mustDecode(t, `{"customer":{"tax_id":"DE12"}}`), newSanitizer(red, 0))
	if len(fs) != 1 || fs[0].Observed != redact.Marker || fs[0].Severity != finding.SeverityWarning {
		t.Fatalf("findings = %+v", fs)
	}
}

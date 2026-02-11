package validate

import (
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
)

func TestLineItemsSumRule(t *testing.T) {
	r, err := compileLineItemsSum(newBase("lines", typeLineItemsSum), ruleSpec{Items: "/line_items", Amount: "amount", Equals: "/subtotal"})
	if err != nil {
		t.Fatal(err)
	}
	if r.expected != "sum of /line_items/*/amount = /subtotal (exact)" {
		t.Fatalf("expected = %q", r.expected)
	}
	tests := []struct {
		name, doc, want string
		kind            finding.Kind
	}{
		{name: "matches", doc: `{"line_items":[{"amount":"0.10"},{"amount":0.20}],"subtotal":"0.30"}`},
		{name: "empty array sums to zero", doc: `{"line_items":[],"subtotal":"0.00"}`},
		{name: "mismatch", doc: `{"line_items":[{"amount":"1.00"},{"amount":"2.00"}],"subtotal":"3.01"}`,
			want: "validate.line_items_sum@/subtotal", kind: finding.KindAssertionFailure},
		{name: "items absent", doc: `{"subtotal":"1"}`, want: "validate.input_missing@/line_items", kind: finding.KindInconclusive},
		{name: "items null", doc: `{"line_items":null,"subtotal":"1"}`, want: "validate.input_missing@/line_items", kind: finding.KindInconclusive},
		{name: "items not array", doc: `{"line_items":{"amount":"1"},"subtotal":"1"}`,
			want: "validate.line_items_sum@/line_items", kind: finding.KindAssertionFailure},
		{name: "item without amount", doc: `{"line_items":[{"amount":"1"},{"qty":2}],"subtotal":"1"}`,
			want: "validate.input_missing@/line_items/1/amount", kind: finding.KindInconclusive},
		{name: "item amount not decimal", doc: `{"line_items":[{"amount":"one"}],"subtotal":"1"}`,
			want: "validate.line_items_sum@/line_items/0/amount", kind: finding.KindAssertionFailure},
		{name: "item is scalar", doc: `{"line_items":[5],"subtotal":"5"}`,
			want: "validate.input_missing@/line_items/0/amount", kind: finding.KindInconclusive},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := runRule(t, r, mustDecode(t, tt.doc))
			if got := codes(fs); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			if len(fs) > 0 && fs[0].Kind != tt.kind {
				t.Fatalf("kind = %s", fs[0].Kind)
			}
		})
	}
	fs := runRule(t, r, mustDecode(t, `{"line_items":[{"amount":"1.00"},{"amount":"2.00"}],"subtotal":"3.01"}`))
	if !strings.Contains(fs[0].Message, "computed 3.00 from the amount of 2 elements of /line_items; stated /subtotal is 3.01; difference 0.01") {
		t.Fatalf("message = %q", fs[0].Message)
	}
	tolerant, _ := compileLineItemsSum(newBase("lines", typeLineItemsSum), ruleSpec{Items: "/line_items", Amount: "amount", Equals: "/subtotal", Tolerance: "0.01"})
	if fs := runRule(t, tolerant, mustDecode(t, `{"line_items":[{"amount":"1.00"},{"amount":"2.00"}],"subtotal":"3.01"}`)); len(fs) != 0 {
		t.Fatalf("tolerance ignored: %+v", fs)
	}
	for _, spec := range []ruleSpec{
		{Amount: "amount", Equals: "/s"},
		{Items: "/l", Equals: "/s"},
		{Items: "/l", Amount: "amount"},
		{Items: "/l/*", Amount: "amount", Equals: "/s"},
		{Items: "/l", Amount: "amount", Equals: "/s", Tolerance: "-1"},
	} {
		if _, err := compileLineItemsSum(newBase("x", typeLineItemsSum), spec); err == nil {
			t.Errorf("%+v accepted", spec)
		}
	}
}

func TestLineItemProductRule(t *testing.T) {
	spec := ruleSpec{Items: "/line_items", Quantity: "quantity", UnitPrice: "unit_price", Amount: "amount",
		Round: &roundSpec{Scale: intp(2), Mode: "half_up"}}
	r, err := compileLineItemProduct(newBase("lp", typeLineItemProduct), spec)
	if err != nil {
		t.Fatal(err)
	}
	doc := mustDecode(t, `{"line_items":[
		{"quantity":3,"unit_price":"0.335","amount":"1.01"},
		{"quantity":"2","unit_price":"4.10","amount":"8.21"},
		{"quantity":1,"amount":"1.00"},
		{"quantity":"1.5","unit_price":"2.00","amount":"3.00"}
	]}`)
	fs := runRule(t, r, doc)
	want := "validate.line_item_product@/line_items/1/amount validate.input_missing@/line_items/2/unit_price"
	if got := codes(fs); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if fs[0].Expected != "/line_items/1/quantity * /line_items/1/unit_price = /line_items/1/amount (rounded half_up to 2 places, exact)" {
		t.Fatalf("expected = %q", fs[0].Expected)
	}
	if !strings.Contains(fs[0].Message, "computed 8.20 from /line_items/1/quantity (2) * /line_items/1/unit_price (4.10)") {
		t.Fatalf("message = %q", fs[0].Message)
	}

	// Without rounding 3 * 0.335 = 1.005 does not equal 1.01.
	spec.Round = nil
	exact, _ := compileLineItemProduct(newBase("lp", typeLineItemProduct), spec)
	fs = runRule(t, exact, mustDecode(t, `{"line_items":[{"quantity":3,"unit_price":"0.335","amount":"1.01"}]}`))
	if codes(fs) != "validate.line_item_product@/line_items/0/amount" {
		t.Fatalf("exact: %s", codes(fs))
	}
	// half_even rounds 1.005 to 1.00.
	spec.Round = &roundSpec{Scale: intp(2), Mode: "half_even"}
	even, _ := compileLineItemProduct(newBase("lp", typeLineItemProduct), spec)
	fs = runRule(t, even, mustDecode(t, `{"line_items":[{"quantity":3,"unit_price":"0.335","amount":"1.00"}]}`))
	if len(fs) != 0 {
		t.Fatalf("half_even: %+v", fs)
	}
	if fs := runRule(t, even, mustDecode(t, `{}`)); codes(fs) != "validate.input_missing@/line_items" {
		t.Fatalf("absent items: %s", codes(fs))
	}
	if _, err := compileLineItemProduct(newBase("lp", typeLineItemProduct), ruleSpec{Items: "/l", Quantity: "a", UnitPrice: "a", Amount: "b"}); err == nil {
		t.Fatal("duplicate member names accepted")
	}
}

func TestProductRule(t *testing.T) {
	r, err := compileProduct(newBase("tax", typeProduct), ruleSpec{
		Factors: []string{"/subtotal", "/tax_rate"}, Equals: "/tax",
		Round: &roundSpec{Scale: intp(2), Mode: "half_even"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.expected != "/subtotal * /tax_rate = /tax (rounded half_even to 2 places, exact)" {
		t.Fatalf("expected = %q", r.expected)
	}
	tests := []struct{ name, doc, want string }{
		{"matches after rounding", `{"subtotal":"100.10","tax_rate":"0.0825","tax":"8.26"}`, ""},
		{"half_even tie", `{"subtotal":"10.50","tax_rate":"0.1","tax":"1.05"}`, ""},
		{"half_even tie to even", `{"subtotal":"0.25","tax_rate":"0.1","tax":"0.02"}`, ""},
		{"mismatch", `{"subtotal":"100.00","tax_rate":"0.0825","tax":"8.20"}`, "validate.product@/tax"},
		{"missing factor", `{"subtotal":"100.00","tax":"8.25"}`, "validate.input_missing@/tax_rate"},
		{"invalid factor", `{"subtotal":"100.00","tax_rate":"8.25%","tax":"8.25"}`, "validate.product@/tax_rate"},
	}
	for _, tt := range tests {
		if got := codes(runRule(t, r, mustDecode(t, tt.doc))); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
	fs := runRule(t, r, mustDecode(t, `{"subtotal":"100.00","tax_rate":"0.0825","tax":"8.20"}`))
	if !strings.Contains(fs[0].Message, "computed 8.250000 from /subtotal (100.00) * /tax_rate (0.0825)") {
		t.Fatalf("message = %q", fs[0].Message)
	}
	fs = runRule(t, r, mustDecode(t, `{"subtotal":"100.00","tax_rate":"0.08255","tax":"8.20"}`))
	if !strings.Contains(fs[0].Message, "computed 8.2550000, 8.26 after rounding half_even to 2 places from") {
		t.Fatalf("message = %q", fs[0].Message)
	}
	for _, spec := range []ruleSpec{
		{Factors: []string{"/a"}, Equals: "/t"},
		{Factors: []string{"/a", "/t"}, Equals: "/t"},
		{Factors: []string{"/a", "/b", "/c", "/d", "/e", "/f", "/g", "/h", "/i"}, Equals: "/t"},
		{Factors: []string{"/a", "/b"}},
	} {
		if _, err := compileProduct(newBase("x", typeProduct), spec); err == nil {
			t.Errorf("%+v accepted", spec)
		}
	}
}

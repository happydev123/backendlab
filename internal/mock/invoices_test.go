package mock

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func getInvoice(t *testing.T, s *Server, v Variant, id string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/"+string(v)+"/invoices/"+id, nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	s.handleInvoice(v)(rec, req)
	if rec.Code != http.StatusOK {
		return rec, nil
	}
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("decode %s invoice: %v", v, err)
	}
	return rec, doc
}

func dec(t *testing.T, v any) decimal.Decimal {
	t.Helper()
	n, ok := v.(json.Number)
	if !ok {
		t.Fatalf("value %#v is not a JSON number", v)
	}
	d, err := decimal.NewFromString(n.String())
	if err != nil {
		t.Fatalf("parse %s: %v", n, err)
	}
	return d
}

func skus(t *testing.T, doc map[string]any) []string {
	t.Helper()
	var out []string
	for _, it := range doc["line_items"].([]any) {
		out = append(out, it.(map[string]any)["sku"].(string))
	}
	return out
}

func TestBaselineInvoicesAreConsistent(t *testing.T) {
	s := testServer()
	moneyText := regexp.MustCompile(`"(subtotal|tax|total|unit_price|amount)":(-?[0-9]+\.[0-9]{2})[,}]`)
	for _, r := range catalog() {
		rec, doc := getInvoice(t, s, Baseline, r.ID)
		if doc == nil {
			t.Fatalf("%s: status %d", r.ID, rec.Code)
		}
		sum := decimal.Zero
		for _, it := range doc["line_items"].([]any) {
			item := it.(map[string]any)
			amount := dec(t, item["amount"])
			if want := dec(t, item["unit_price"]).Mul(dec(t, item["quantity"])); !amount.Equal(want) {
				t.Errorf("%s %s: amount %s, want %s", r.ID, item["sku"], amount, want)
			}
			sum = sum.Add(amount)
		}
		if !sum.Equal(dec(t, doc["subtotal"])) {
			t.Errorf("%s: subtotal %s, items sum %s", r.ID, doc["subtotal"], sum)
		}
		if got := dec(t, doc["subtotal"]).Add(dec(t, doc["tax"])); !got.Equal(dec(t, doc["total"])) {
			t.Errorf("%s: subtotal + tax = %s, total %s", r.ID, got, doc["total"])
		}
		// Every money field is written with exactly two decimals.
		body := rec.Body.String()
		wantFields := 3 + 2*len(r.Items)
		if got := len(moneyText.FindAllString(body, -1)); got != wantFields {
			t.Errorf("%s: %d two-decimal money fields, want %d in %s", r.ID, got, wantFields, body)
		}
		if !strings.Contains(body, `"exchange_rate":1.0800,`) {
			t.Errorf("%s: exchange_rate not written as 1.0800: %s", r.ID, body)
		}
		cust := doc["customer"].(map[string]any)
		if cust["email"] == nil || cust["email_address"] != nil {
			t.Errorf("%s: baseline customer %v", r.ID, cust)
		}
	}
}

func TestCandidateDrift(t *testing.T) {
	s := testServer()
	_, base := getInvoice(t, s, Baseline, "inv-1001")
	_, cand := getInvoice(t, s, Candidate, "inv-1001")

	// Reversed order, same set of SKUs.
	bs, cs := skus(t, base), skus(t, cand)
	if len(bs) != len(cs) {
		t.Fatalf("item counts differ: %v vs %v", bs, cs)
	}
	for i := range bs {
		if bs[i] != cs[len(cs)-1-i] {
			t.Fatalf("candidate order %v is not the reverse of %v", cs, bs)
		}
	}

	// Exchange rate inside a 0.00001 tolerance but not equal.
	diff := dec(t, cand["exchange_rate"]).Sub(dec(t, base["exchange_rate"])).Abs()
	if diff.IsZero() || diff.GreaterThan(decimal.RequireFromString("0.00001")) {
		t.Errorf("exchange_rate difference %s", diff)
	}
	if cand["exchange_rate"].(json.Number).String() != "1.0800004" {
		t.Errorf("candidate exchange_rate %s", cand["exchange_rate"])
	}

	// Total off by the configured delta; subtotal and tax unchanged.
	for _, f := range []string{"subtotal", "tax"} {
		if base[f] != cand[f] {
			t.Errorf("%s differs: %s vs %s", f, base[f], cand[f])
		}
	}
	gotDelta := dec(t, cand["total"]).Sub(dec(t, base["total"]))
	if !gotDelta.Equal(decimal.RequireFromString("0.05")) {
		t.Errorf("total delta %s, want 0.05", gotDelta)
	}
	if sum := dec(t, cand["subtotal"]).Add(dec(t, cand["tax"])); sum.Equal(dec(t, cand["total"])) {
		t.Error("candidate total should not equal subtotal + tax")
	}

	// Renamed required field.
	cust := cand["customer"].(map[string]any)
	if _, ok := cust["email"]; ok {
		t.Errorf("candidate still has customer.email: %v", cust)
	}
	if cust["email_address"] != base["customer"].(map[string]any)["email"] {
		t.Errorf("candidate email_address %v", cust["email_address"])
	}
}

func TestCandidateDeltaConfigurable(t *testing.T) {
	for _, delta := range []string{"0", "-10.00", "123.4"} {
		cfg := DefaultConfig()
		cfg.InvoiceTotalDelta = decimal.RequireFromString(delta)
		s := New(cfg)
		_, base := getInvoice(t, s, Baseline, "inv-1002")
		_, cand := getInvoice(t, s, Candidate, "inv-1002")
		got := dec(t, cand["total"]).Sub(dec(t, base["total"]))
		if !got.Equal(decimal.RequireFromString(delta)) {
			t.Errorf("delta %s: total moved by %s", delta, got)
		}
		if !regexp.MustCompile(`^-?[0-9]+\.[0-9]{2}$`).MatchString(cand["total"].(json.Number).String()) {
			t.Errorf("delta %s: total %s is not a two-decimal amount", delta, cand["total"])
		}
	}
}

func TestInvoiceNotFound(t *testing.T) {
	rec, _ := getInvoice(t, testServer(), Baseline, "inv-9999")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invoice not found") {
		t.Fatalf("body %s", rec.Body.String())
	}
}

func TestInvoiceList(t *testing.T) {
	s := testServer()
	var bodies []string
	for _, v := range Variants() {
		rec := httptest.NewRecorder()
		s.handleInvoiceList(v)(rec, httptest.NewRequest(http.MethodGet, "/"+string(v)+"/invoices", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", v, rec.Code)
		}
		var list invoiceListJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		if list.Count != 3 || len(list.Invoices) != 3 || list.Invoices[0].ID != "inv-1001" {
			t.Fatalf("%s: list %+v", v, list)
		}
		for _, inv := range list.Invoices {
			if _, ok := findInvoice(inv.ID); !ok {
				t.Errorf("listed invoice %s cannot be fetched", inv.ID)
			}
		}
		bodies = append(bodies, rec.Body.String())
	}
	if bodies[0] != bodies[1] {
		t.Error("list differs between variants")
	}
}

func TestCatalogReturnsFreshSlices(t *testing.T) {
	a := catalog()
	a[0].Items[0].SKU = "changed"
	if catalog()[0].Items[0].SKU == "changed" {
		t.Fatal("catalog shares state between calls")
	}
}

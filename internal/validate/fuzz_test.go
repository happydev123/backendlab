package validate

import (
	"encoding/json"
	"testing"
)

// FuzzDecimalParsing checks that decimal parsing never panics and that
// accepted values respect the documented bounds.
func FuzzDecimalParsing(f *testing.F) {
	for _, s := range []string{"0", "-0.50", "1250.00", "1e40", "1e-40", "1e41", "007", "1,5", "", "-", "9999999999999999999999999999999999999999",
		"0.1", "1.5E+2", "−" + "1", "1e99999999999", "NaN", "Infinity", "0x10"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, parse := range []func() (int, error){
			func() (int, error) { d, err := parseDecimalString(s); return d.NumDigits(), err },
			func() (int, error) { d, err := parseDecimalNumber(json.Number(s)); return d.NumDigits(), err },
		} {
			digits, err := parse()
			if err == nil && digits > MaxDecimalDigits {
				t.Fatalf("%q: %d digits accepted", s, digits)
			}
		}
	})
}

// FuzzSumRule runs the sum rule over arbitrary string and number inputs.
// It must never panic, and when all three inputs are valid decimals the
// rule passes exactly when a + b equals the stated total.
func FuzzSumRule(f *testing.F) {
	f.Add("0.1", "0.2", "0.3", false)
	f.Add("100.00", "8.25", "108.26", true)
	f.Add("1e40", "-1e-40", "1e40", true)
	f.Add("", "x", "1", false)
	f.Add("9999999999999999999999999999999999999999", "1", "0", true)
	r, err := compileSum(newBase("fuzz", typeSum), ruleSpec{
		Terms:  []termSpec{{Field: "/a"}, {Field: "/b", Optional: true}},
		Equals: "/total",
		Round:  &roundSpec{Scale: intp(2), Mode: "half_even"},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, a, b, total string, numbers bool) {
		val := func(s string) any {
			if numbers {
				return json.Number(s)
			}
			return s
		}
		doc := map[string]any{"a": val(a), "b": val(b), "total": val(total)}
		fs := runRule(t, r, doc)
		x, _, ex := decimalValue(doc["a"], acceptBoth)
		y, _, ey := decimalValue(doc["b"], acceptBoth)
		z, _, ez := decimalValue(doc["total"], acceptBoth)
		if ex != nil || ey != nil || ez != nil {
			return
		}
		want := x.Add(y).RoundBank(2).Equal(z)
		if want != (len(fs) == 0) {
			t.Fatalf("a=%q b=%q total=%q: findings %+v, want pass=%v", a, b, total, fs, want)
		}
	})
}

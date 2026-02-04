package validate

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func TestParseDecimalString(t *testing.T) {
	tests := []struct {
		in    string
		want  string
		scale int
		err   string
	}{
		{in: "0", want: "0", scale: 0},
		{in: "12", want: "12", scale: 0},
		{in: "-0.50", want: "-0.50", scale: 2},
		{in: "1250.00", want: "1250.00", scale: 2},
		{in: "0.1", want: "0.1", scale: 1},
		{in: "", err: "is empty"},
		{in: "-", err: "no integer digits"},
		{in: ".5", err: "no integer digits"},
		{in: "5.", err: "no fraction digits"},
		{in: "007", err: "leading zero"},
		{in: "+5", err: "no integer digits"},
		{in: "1e3", err: "exponent"},
		{in: "1,250.00", err: "unexpected characters"},
		{in: " 12", err: "no integer digits"},
		{in: "12 ", err: "unexpected characters"},
		{in: "€12", err: "no integer digits"},
		{in: "−12", err: "no integer digits"},
		{in: "1." + strings.Repeat("0", 41), err: "decimal places"},
		{in: strings.Repeat("9", 41), err: "more than 40 digits"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			d, err := parseDecimalString(tt.in)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) || !errors.Is(err, errNotDecimal) {
					t.Fatalf("err = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := formatDecimal(d); got != tt.want {
				t.Fatalf("formatted = %q, want %q", got, tt.want)
			}
			if got := scaleOf(d); got != tt.scale {
				t.Fatalf("scale = %d, want %d", got, tt.scale)
			}
		})
	}
}

func TestParseDecimalNumber(t *testing.T) {
	tests := []struct {
		in   string
		want string
		err  bool
	}{
		{in: "10.25", want: "10.25"},
		{in: "1.5e1", want: "15"},
		{in: "-2E-2", want: "-0.02"},
		{in: "1e+3", want: "1000"},
		{in: "1e41", err: true},
		{in: "1e-41", err: true},
		{in: "1e99999", err: true},
		{in: "01", err: true},
		{in: "1e", err: true},
		{in: "NaN", err: true},
	}
	for _, tt := range tests {
		d, err := parseDecimalNumber(json.Number(tt.in))
		if tt.err {
			if err == nil {
				t.Errorf("%s: expected error, got %s", tt.in, d)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tt.in, err)
			continue
		}
		if got := formatDecimal(d); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDecimalValueAccept(t *testing.T) {
	numOnly := acceptSet{number: true}
	strOnly := acceptSet{str: true}
	tests := []struct {
		name   string
		v      any
		accept acceptSet
		ok     bool
	}{
		{"number accepted", json.Number("1.10"), numOnly, true},
		{"string accepted", "1.10", strOnly, true},
		{"number rejected", json.Number("1.10"), strOnly, false},
		{"string rejected", "1.10", numOnly, false},
		{"both", "3", acceptBoth, true},
		{"float64 never", 1.1, acceptBoth, false},
		{"null", nil, acceptBoth, false},
		{"bool", true, acceptBoth, false},
		{"object", map[string]any{}, acceptBoth, false},
		{"bad string", "abc", acceptBoth, false},
	}
	for _, tt := range tests {
		_, _, err := decimalValue(tt.v, tt.accept)
		if (err == nil) != tt.ok {
			t.Errorf("%s: err = %v, want ok=%v", tt.name, err, tt.ok)
		}
	}
}

// TestDecimalArithmeticIsExact documents why float64 is never used:
// 0.1 + 0.2 is 0.30000000000000004 in binary floating point.
func TestDecimalArithmeticIsExact(t *testing.T) {
	a, _ := parseDecimalNumber("0.1")
	b, _ := parseDecimalString("0.2")
	c, _ := parseDecimalNumber("0.3")
	if !a.Add(b).Equal(c) {
		t.Fatalf("0.1 + 0.2 = %s", a.Add(b))
	}
	fa, fb := 0.1, 0.2
	if fa+fb == 0.3 {
		t.Fatal("expected float64 addition to be inexact")
	}
	// Scale does not affect equality: 10.5 equals 10.50.
	x, _ := parseDecimalString("10.5")
	y, _ := parseDecimalString("10.50")
	if !x.Equal(y) {
		t.Fatal("10.5 != 10.50")
	}
	if got := formatDecimal(decimal.RequireFromString("100.00").Add(decimal.RequireFromString("3.20"))); got != "103.20" {
		t.Fatalf("format keeps scale: %q", got)
	}
}

func TestParseRuleDecimal(t *testing.T) {
	if _, err := parseRuleDecimal("tolerance", "0.01"); err != nil {
		t.Fatal(err)
	}
	_, err := parseRuleDecimal("tolerance", "1e-2")
	if err == nil || !strings.Contains(err.Error(), "tolerance must be a decimal string") {
		t.Fatalf("err = %v", err)
	}
}

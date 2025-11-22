package compare

import (
	"encoding/json"
	"testing"
)

func TestCompareNumbers(t *testing.T) {
	abs, _ := ParseTolerance("0.01", "")
	rel, _ := ParseTolerance("", "0.1")
	cases := []struct {
		name string
		b, c any
		tol  Tolerance
		want numberOutcome
	}{
		{"same text", json.Number("10"), json.Number("10"), Tolerance{}, numbersEqual},
		{"trailing zeros", json.Number("1.0"), json.Number("1.00"), Tolerance{}, numbersEqual},
		{"exponent form", json.Number("1e2"), json.Number("100"), Tolerance{}, numbersEqual},
		{"negative zero", json.Number("-0"), json.Number("0"), Tolerance{}, numbersEqual},
		{"exact differs", json.Number("0.1"), json.Number("0.10000000000000001"), Tolerance{}, numbersDiffer},
		{"abs within", json.Number("10.00"), json.Number("10.005"), abs, numbersWithinTolerance},
		{"abs outside", json.Number("10.00"), json.Number("10.02"), abs, numbersDiffer},
		{"rel within", json.Number("200"), json.Number("219"), rel, numbersWithinTolerance},
		{"rel outside", json.Number("200"), json.Number("221"), rel, numbersDiffer},
		{"go float", 1.5, json.Number("1.50"), Tolerance{}, numbersEqual},
		{"go int", 3, json.Number("3.0"), Tolerance{}, numbersEqual},
		{"huge exponent equal text", json.Number("1e999999"), json.Number("1e999999"), Tolerance{}, numbersEqual},
		{"huge exponent differs", json.Number("1e999999"), json.Number("2"), Tolerance{}, numbersUnparsable},
	}
	for _, c := range cases {
		if got := compareNumbers(c.b, c.c, c.tol); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestNumberPrecisionNotFloat(t *testing.T) {
	// These differ as decimals but are the same float64.
	a, b := json.Number("9007199254740993"), json.Number("9007199254740992")
	if numberEqual(a, b) {
		t.Fatal("numbers compared through float64")
	}
}

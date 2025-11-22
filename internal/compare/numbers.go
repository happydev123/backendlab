package compare

import (
	"encoding/json"
	"strconv"

	"github.com/shopspring/decimal"
)

// Numbers are compared as decimals parsed from their JSON text, never as
// float64. Two numbers with the same decimal value are equal whatever
// their spelling: 1, 1.0, 1.00 and 1e0 compare equal. Producers that
// encode meaning in trailing zeros (for example a currency amount
// rendered with a fixed scale) are therefore not distinguished.

// Bounds keep decimal arithmetic cheap. A number written with an extreme
// exponent ("1e999999999") would otherwise force big-integer rescaling
// to millions of digits when compared against a small value.
const (
	maxNumberText = 400
	maxExponent   = 1000
)

// numberOutcome is the result of comparing two numbers.
type numberOutcome int

const (
	numbersEqual numberOutcome = iota
	numbersWithinTolerance
	numbersDiffer
	numbersUnparsable
)

// toDecimal converts a decoded JSON number. It accepts json.Number (the
// form produced by jsonutil.Decode) and Go numeric types for callers that
// built values by hand.
func toDecimal(v any) (decimal.Decimal, bool) {
	var s string
	switch n := v.(type) {
	case json.Number:
		s = string(n)
	case float64:
		return decimal.NewFromFloat(n), true
	case int:
		return decimal.NewFromInt(int64(n)), true
	case int64:
		return decimal.NewFromInt(n), true
	default:
		return decimal.Decimal{}, false
	}
	if len(s) == 0 || len(s) > maxNumberText {
		return decimal.Decimal{}, false
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Decimal{}, false
	}
	if e := d.Exponent(); e > maxExponent || e < -maxExponent {
		return decimal.Decimal{}, false
	}
	return d, true
}

// compareNumbers compares two numbers under tol. When either side cannot
// be parsed within the bounds, the literal texts are compared and an
// unequal pair is reported as unparsable rather than guessed.
func compareNumbers(b, c any, tol Tolerance) numberOutcome {
	bd, bok := toDecimal(b)
	cd, cok := toDecimal(c)
	if !bok || !cok {
		if numberText(b) == numberText(c) {
			return numbersEqual
		}
		return numbersUnparsable
	}
	if bd.Equal(cd) {
		return numbersEqual
	}
	if tol.Within(bd, cd) {
		return numbersWithinTolerance
	}
	return numbersDiffer
}

// numberText returns the literal text of a number for fallback equality.
func numberText(v any) string {
	switch n := v.(type) {
	case json.Number:
		return string(n)
	case float64:
		return strconv.FormatFloat(n, 'g', -1, 64)
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	}
	return ""
}

// numberEqual reports whether two JSON numbers have the same decimal
// value.
func numberEqual(a, b any) bool { return compareNumbers(a, b, Tolerance{}) == numbersEqual }

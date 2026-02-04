package validate

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/happydev123/backendlab/internal/jsonutil"
)

// Decimal bounds. They keep arithmetic cheap: without them a value such as
// 1e999999999 would force a billion-digit big.Int when added to 1.
const (
	// MaxDecimalDigits bounds the significant digits of a parsed value.
	MaxDecimalDigits = 40
	// MaxDecimalExponent bounds the decimal exponent in either direction,
	// so values have at most this many fractional digits and magnitude
	// below 10^(MaxDecimalDigits+MaxDecimalExponent).
	MaxDecimalExponent = 40
)

// errNotDecimal is wrapped by every parse failure.
var errNotDecimal = errors.New("not a decimal")

// acceptSet records which JSON representations a rule accepts as decimals.
type acceptSet struct {
	number bool
	str    bool
}

// acceptBoth accepts JSON numbers and decimal strings.
var acceptBoth = acceptSet{number: true, str: true}

func (a acceptSet) describe() string {
	switch {
	case a.number && a.str:
		return "decimal number or decimal string"
	case a.str:
		return "decimal string"
	default:
		return "decimal number"
	}
}

// parseDecimalString parses a decimal written as a string. The grammar is a
// JSON number without exponent: an optional '-', an integer part without
// leading zeros, and an optional fraction ("12", "-0.50", "1250.00").
// Exponents, '+', whitespace, thousands separators and currency symbols are
// rejected, because a validator must not guess what "1,250" means.
func parseDecimalString(s string) (decimal.Decimal, error) {
	if err := scanNumber(s, false); err != nil {
		return decimal.Decimal{}, err
	}
	return bounded(s)
}

// parseDecimalNumber parses a JSON number token (as kept by json.Number).
// Exponents are allowed because they are valid JSON, within the bounds.
func parseDecimalNumber(n json.Number) (decimal.Decimal, error) {
	s := string(n)
	if err := scanNumber(s, true); err != nil {
		return decimal.Decimal{}, err
	}
	return bounded(s)
}

// decimalValue converts a decoded JSON value to a decimal if its
// representation is accepted. raw is the textual form as written in the
// document, used in messages.
func decimalValue(v any, accept acceptSet) (d decimal.Decimal, raw string, err error) {
	switch t := v.(type) {
	case json.Number:
		if !accept.number {
			return decimal.Decimal{}, string(t), fmt.Errorf("%w: JSON number where a decimal string is required", errNotDecimal)
		}
		d, err := parseDecimalNumber(t)
		return d, string(t), err
	case string:
		if !accept.str {
			return decimal.Decimal{}, t, fmt.Errorf("%w: string where a JSON number is required", errNotDecimal)
		}
		d, err := parseDecimalString(t)
		return d, t, err
	case float64:
		// Values decoded without UseNumber lost precision already.
		return decimal.Decimal{}, "", fmt.Errorf("%w: float64 value (decode with json.Number)", errNotDecimal)
	case nil:
		return decimal.Decimal{}, "null", fmt.Errorf("%w: null", errNotDecimal)
	default:
		return decimal.Decimal{}, "", fmt.Errorf("%w: %s", errNotDecimal, jsonutil.TypeName(v))
	}
}

// scanNumber checks the JSON number grammar, optionally without exponent.
func scanNumber(s string, allowExp bool) error {
	bad := func(why string) error { return fmt.Errorf("%w: %q %s", errNotDecimal, clip(s), why) }
	if s == "" {
		return bad("is empty")
	}
	i := 0
	if s[i] == '-' {
		i++
	}
	start := i
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	switch {
	case i == start:
		return bad("has no integer digits")
	case s[start] == '0' && i-start > 1:
		return bad("has a leading zero")
	}
	if i < len(s) && s[i] == '.' {
		i++
		fs := i
		for i < len(s) && isDigit(s[i]) {
			i++
		}
		if i == fs {
			return bad("has no fraction digits")
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		if !allowExp {
			return bad("uses an exponent")
		}
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		es := i
		for i < len(s) && isDigit(s[i]) {
			i++
		}
		if i == es {
			return bad("has no exponent digits")
		}
		if i-es > 4 {
			return bad("has an exponent out of range")
		}
	}
	if i != len(s) {
		return bad("contains unexpected characters")
	}
	return nil
}

// bounded parses a grammar-checked number and enforces the digit and
// exponent bounds.
func bounded(s string) (decimal.Decimal, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("%w: %q", errNotDecimal, clip(s))
	}
	if exp := d.Exponent(); exp > MaxDecimalExponent || exp < -MaxDecimalExponent {
		return decimal.Decimal{}, fmt.Errorf("%w: %q exceeds %d decimal places or exponent", errNotDecimal, clip(s), MaxDecimalExponent)
	}
	if d.NumDigits() > MaxDecimalDigits {
		return decimal.Decimal{}, fmt.Errorf("%w: %q has more than %d digits", errNotDecimal, clip(s), MaxDecimalDigits)
	}
	return d, nil
}

// scaleOf returns the number of fractional digits as written ("1.50" has
// scale 2, "15" and "1.5e1" have scale 0).
func scaleOf(d decimal.Decimal) int {
	if e := d.Exponent(); e < 0 {
		return int(-e)
	}
	return 0
}

// formatDecimal renders d in plain notation keeping its scale, so computed
// sums keep trailing zeros ("103.20", not "103.2").
func formatDecimal(d decimal.Decimal) string {
	return d.StringFixed(int32(scaleOf(d)))
}

// parseRuleDecimal parses a decimal written in a rules file (tolerance,
// min, max). Rules use the string grammar so YAML never turns them into
// floating point.
func parseRuleDecimal(name, s string) (decimal.Decimal, error) {
	d, err := parseDecimalString(s)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("%s must be a decimal string such as \"0.01\": %v", name, err)
	}
	return d, nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// clip bounds a value quoted inside an error message.
func clip(s string) string {
	const max = 48
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "..."
}

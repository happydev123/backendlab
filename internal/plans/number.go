package plans

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// Number bounds. PostgreSQL prints costs with two decimals, times with three
// and counters as integers, so real plans stay far inside these limits.
// They exist so that hostile input cannot request huge decimal expansions.
const (
	maxNumberLen      = 40
	maxNumberExponent = 40
)

// Number is a non-negative metric decoded from EXPLAIN JSON. It keeps the
// literal PostgreSQL printed and an exact decimal value, so comparisons and
// ratios never round through float64. Metrics that a plan does not contain
// are represented by a nil *Number, never by zero.
type Number struct {
	raw string
	d   decimal.Decimal
}

var errNotNumber = errors.New("not a number")

// parseNumber converts a json.Number literal into a Number. Negative values
// and values outside the supported magnitude are rejected because
// PostgreSQL never emits them for plan metrics.
func parseNumber(n json.Number) (Number, error) {
	s := string(n)
	if len(s) == 0 || len(s) > maxNumberLen {
		return Number{}, fmt.Errorf("%w: literal length %d outside 1..%d", errNotNumber, len(s), maxNumberLen)
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return Number{}, fmt.Errorf("%w: %q", errNotNumber, s)
	}
	if e := d.Exponent(); e > maxNumberExponent || e < -maxNumberExponent {
		return Number{}, fmt.Errorf("number %q is outside the supported range", s)
	}
	if d.IsNegative() {
		return Number{}, fmt.Errorf("number %q is negative", s)
	}
	return Number{raw: s, d: d}, nil
}

// numberFromDecimal builds a Number for a derived value (for example a sum of
// buffer counters). The literal is the canonical decimal string.
func numberFromDecimal(d decimal.Decimal) Number {
	return Number{raw: d.String(), d: d}
}

// String returns the literal as it appeared in the plan.
func (n Number) String() string { return n.raw }

// Decimal returns the exact value.
func (n Number) Decimal() decimal.Decimal { return n.d }

// Float64 returns the value as a float64, for display or charts only.
func (n Number) Float64() float64 { return n.d.InexactFloat64() }

// IsZero reports whether the value is exactly zero.
func (n Number) IsZero() bool { return n.d.IsZero() }

// MarshalJSON writes the original literal, which is always a valid JSON
// number because it came from the JSON decoder or from decimal formatting.
func (n Number) MarshalJSON() ([]byte, error) {
	if n.raw == "" {
		return []byte("0"), nil
	}
	return []byte(n.raw), nil
}

// value returns the decimal behind p and whether it is known.
func value(p *Number) (decimal.Decimal, bool) {
	if p == nil {
		return decimal.Zero, false
	}
	return p.d, true
}

// sumKnown adds the known values among ps. The result is nil when none is
// known, so "no data" never turns into zero.
func sumKnown(ps ...*Number) *Number {
	var total decimal.Decimal
	known := false
	for _, p := range ps {
		if p != nil {
			total = total.Add(p.d)
			known = true
		}
	}
	if !known {
		return nil
	}
	n := numberFromDecimal(total)
	return &n
}

// rowRatio returns how many times larger the bigger of a and b is than the
// smaller one, treating values below one row as one row so that a zero on
// either side does not divide by zero. The result is >= 1.
func rowRatio(a, b decimal.Decimal) decimal.Decimal {
	hi, lo := decimal.Max(a, b), decimal.Min(a, b)
	one := decimal.NewFromInt(1)
	if lo.LessThan(one) {
		lo = one
	}
	if hi.LessThan(one) {
		hi = one
	}
	return hi.DivRound(lo, 4)
}

// fmtDec renders a derived decimal with at most two fractional digits and
// no trailing zeros.
func fmtDec(d decimal.Decimal) string {
	return d.Round(2).String()
}

// fmtNum renders an optional metric, using "unknown" for nil.
func fmtNum(p *Number) string {
	if p == nil {
		return "unknown"
	}
	return p.raw
}

package compare

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// Tolerance allows two numbers to differ by an absolute amount or by a
// fraction of the baseline value. A zero field allows no difference, so the
// zero Tolerance requires decimal equality.
type Tolerance struct {
	Absolute decimal.Decimal
	Relative decimal.Decimal
}

// ParseTolerance parses decimal strings; an empty string means zero.
// Negative values are rejected.
func ParseTolerance(absolute, relative string) (Tolerance, error) {
	var t Tolerance
	for _, f := range []struct {
		name string
		in   string
		out  *decimal.Decimal
	}{{"absolute", absolute, &t.Absolute}, {"relative", relative, &t.Relative}} {
		if f.in == "" {
			continue
		}
		d, err := decimal.NewFromString(f.in)
		if err != nil {
			return Tolerance{}, fmt.Errorf("%s %q is not a decimal number", f.name, f.in)
		}
		if d.IsNegative() {
			return Tolerance{}, fmt.Errorf("%s %q must not be negative", f.name, f.in)
		}
		*f.out = d
	}
	return t, nil
}

// IsZero reports whether t allows no difference.
func (t Tolerance) IsZero() bool { return t.Absolute.IsZero() && t.Relative.IsZero() }

// Within reports whether candidate is equal to baseline or within the
// tolerance: |baseline-candidate| <= Absolute, or
// |baseline-candidate| <= Relative*|baseline|.
func (t Tolerance) Within(baseline, candidate decimal.Decimal) bool {
	diff := baseline.Sub(candidate).Abs()
	if diff.IsZero() {
		return true
	}
	if !t.Absolute.IsZero() && diff.LessThanOrEqual(t.Absolute) {
		return true
	}
	if !t.Relative.IsZero() && diff.LessThanOrEqual(t.Relative.Mul(baseline.Abs())) {
		return true
	}
	return false
}

// String describes t for messages.
func (t Tolerance) String() string {
	switch {
	case t.IsZero():
		return "exact"
	case t.Relative.IsZero():
		return "absolute " + t.Absolute.String()
	case t.Absolute.IsZero():
		return "relative " + t.Relative.String()
	}
	return fmt.Sprintf("absolute %s or relative %s", t.Absolute.String(), t.Relative.String())
}

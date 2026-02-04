package validate

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// MaxRoundScale bounds the scale a computed value may be rounded to.
const MaxRoundScale = 18

// roundMode names a rounding rule for computed values.
type roundMode string

const (
	// roundHalfUp rounds ties away from zero: 2.345 -> 2.35 and
	// -2.345 -> -2.35 at scale 2. This is the usual commercial rule.
	roundHalfUp roundMode = "half_up"
	// roundHalfEven rounds ties to the even neighbour (banker's rounding):
	// 2.345 -> 2.34, 2.355 -> 2.36.
	roundHalfEven roundMode = "half_even"
	// roundDown truncates toward zero: 2.349 -> 2.34, -2.349 -> -2.34.
	roundDown roundMode = "down"
)

// rounding is an optional rounding step applied to a computed value before
// it is compared with the stated value. The zero value means no rounding.
type rounding struct {
	set   bool
	scale int32
	mode  roundMode
}

// compileRounding validates a round block. Both scale and mode are
// required so that the rules file states the rounding explicitly.
func compileRounding(r *roundSpec) (rounding, error) {
	if r == nil {
		return rounding{}, nil
	}
	if r.Scale == nil {
		return rounding{}, errors.New("round.scale is required")
	}
	if *r.Scale < 0 || *r.Scale > MaxRoundScale {
		return rounding{}, fmt.Errorf("round.scale must be between 0 and %d", MaxRoundScale)
	}
	switch m := roundMode(r.Mode); m {
	case roundHalfUp, roundHalfEven, roundDown:
		return rounding{set: true, scale: int32(*r.Scale), mode: m}, nil
	case "":
		return rounding{}, errors.New("round.mode is required (half_up, half_even or down)")
	default:
		return rounding{}, fmt.Errorf("unknown round.mode %q (want half_up, half_even or down)", r.Mode)
	}
}

// apply rounds d, or returns it unchanged when no rounding is configured.
func (r rounding) apply(d decimal.Decimal) decimal.Decimal {
	if !r.set {
		return d
	}
	switch r.mode {
	case roundHalfEven:
		return d.RoundBank(r.scale)
	case roundDown:
		return d.RoundDown(r.scale)
	default:
		return d.Round(r.scale)
	}
}

// describe renders the rounding step for expected-condition text.
func (r rounding) describe() string {
	if !r.set {
		return ""
	}
	return fmt.Sprintf("rounded %s to %d places", r.mode, r.scale)
}

// tolerance is the largest accepted absolute difference between computed
// and stated values. The zero value requires exact decimal equality.
type tolerance struct {
	value decimal.Decimal
	text  string
}

// compileTolerance parses a tolerance string. Empty means zero; negative
// values are rejected.
func compileTolerance(s string) (tolerance, error) {
	if s == "" {
		return tolerance{value: decimal.Zero}, nil
	}
	d, err := parseRuleDecimal("tolerance", s)
	if err != nil {
		return tolerance{}, err
	}
	if d.IsNegative() {
		return tolerance{}, fmt.Errorf("tolerance must not be negative (got %s)", s)
	}
	return tolerance{value: d, text: s}, nil
}

// exact reports whether the tolerance requires exact equality.
func (t tolerance) exact() bool { return t.value.IsZero() }

// within compares computed and stated and returns the absolute difference.
func (t tolerance) within(computed, stated decimal.Decimal) (diff decimal.Decimal, ok bool) {
	diff = computed.Sub(stated).Abs()
	return diff, diff.LessThanOrEqual(t.value)
}

// describe renders the tolerance for expected-condition text.
func (t tolerance) describe() string {
	if t.exact() {
		return "exact"
	}
	return "tolerance " + t.text
}

// comparison combines rounding and tolerance into the suffix used in
// Expected strings: "(rounded half_up to 2 places, tolerance 0.01)".
func comparison(r rounding, t tolerance) string {
	if r.set {
		return "(" + r.describe() + ", " + t.describe() + ")"
	}
	return "(" + t.describe() + ")"
}

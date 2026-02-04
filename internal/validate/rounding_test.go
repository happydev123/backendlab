package validate

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func intp(i int) *int { return &i }

func TestRoundingModes(t *testing.T) {
	tests := []struct {
		in    string
		mode  roundMode
		scale int
		want  string
	}{
		{"2.345", roundHalfUp, 2, "2.35"},
		{"-2.345", roundHalfUp, 2, "-2.35"},
		{"2.345", roundHalfEven, 2, "2.34"},
		{"2.355", roundHalfEven, 2, "2.36"},
		{"-2.345", roundHalfEven, 2, "-2.34"},
		{"2.349", roundDown, 2, "2.34"},
		{"-2.349", roundDown, 2, "-2.34"},
		{"8.2500", roundHalfUp, 2, "8.25"},
		{"0.125", roundHalfUp, 0, "0"},
		{"0.5", roundHalfEven, 0, "0"},
		{"1.5", roundHalfEven, 0, "2"},
	}
	for _, tt := range tests {
		r, err := compileRounding(&roundSpec{Scale: intp(tt.scale), Mode: string(tt.mode)})
		if err != nil {
			t.Fatal(err)
		}
		got := formatDecimal(r.apply(decimal.RequireFromString(tt.in)))
		if got != tt.want {
			t.Errorf("%s %s %d: got %s, want %s", tt.in, tt.mode, tt.scale, got, tt.want)
		}
	}
}

func TestCompileRoundingErrors(t *testing.T) {
	tests := []struct {
		spec roundSpec
		err  string
	}{
		{roundSpec{Mode: "half_up"}, "round.scale is required"},
		{roundSpec{Scale: intp(-1), Mode: "half_up"}, "between 0 and 18"},
		{roundSpec{Scale: intp(19), Mode: "half_up"}, "between 0 and 18"},
		{roundSpec{Scale: intp(2)}, "round.mode is required"},
		{roundSpec{Scale: intp(2), Mode: "ceiling"}, "unknown round.mode"},
	}
	for _, tt := range tests {
		spec := tt.spec
		_, err := compileRounding(&spec)
		if err == nil || !strings.Contains(err.Error(), tt.err) {
			t.Errorf("%+v: err = %v, want %q", tt.spec, err, tt.err)
		}
	}
	r, err := compileRounding(nil)
	if err != nil || r.set {
		t.Fatalf("nil round: %+v %v", r, err)
	}
	if got := r.apply(decimal.RequireFromString("1.005")); got.String() != "1.005" {
		t.Fatalf("unset rounding changed value: %s", got)
	}
}

func TestTolerance(t *testing.T) {
	zero, err := compileTolerance("")
	if err != nil || !zero.exact() {
		t.Fatalf("empty tolerance: %+v %v", zero, err)
	}
	d := decimal.RequireFromString
	if _, ok := zero.within(d("10.5"), d("10.50")); !ok {
		t.Fatal("exact tolerance must accept equal values of different scale")
	}
	if diff, ok := zero.within(d("10.51"), d("10.50")); ok || diff.String() != "0.01" {
		t.Fatalf("exact: diff=%s ok=%v", diff, ok)
	}
	cent, err := compileTolerance("0.01")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cent.within(d("10.51"), d("10.50")); !ok {
		t.Fatal("difference equal to tolerance must pass")
	}
	if _, ok := cent.within(d("10.48"), d("10.50")); ok {
		t.Fatal("difference above tolerance must fail")
	}
	for _, bad := range []string{"-0.01", "0.01.0", "1e-2", "abc"} {
		if _, err := compileTolerance(bad); err == nil {
			t.Errorf("tolerance %q accepted", bad)
		}
	}
	r, _ := compileRounding(&roundSpec{Scale: intp(2), Mode: "half_even"})
	if got := comparison(r, cent); got != "(rounded half_even to 2 places, tolerance 0.01)" {
		t.Fatalf("comparison = %q", got)
	}
	if got := comparison(rounding{}, zero); got != "(exact)" {
		t.Fatalf("comparison = %q", got)
	}
}

// Package compare performs structural comparison of a baseline response
// and a candidate response: status code, explicitly selected headers and
// the body. JSON bodies are compared as decoded values (object key order
// ignored, array order significant unless an array is configured as keyed
// and unordered, numbers compared as decimals). Other bodies are compared
// by size and SHA-256 digest. The package does no I/O.
package compare

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/happydev123/backendlab/internal/jsonptr"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/suite"
)

// Options controls one comparison. Build it with OptionsFromSpec; the zero
// value compares exactly with default bounds and the default redactor.
type Options struct {
	// Ignore excludes values at matching pointers, and everything below
	// them, before comparison and reporting.
	Ignore jsonptr.PatternSet
	// Tolerance applies to every number unless a path tolerance matches.
	Tolerance Tolerance
	// PathTolerances override Tolerance; the first matching entry wins.
	PathTolerances []PathTolerance
	// Unordered lists arrays compared by a key member instead of position.
	Unordered []UnorderedRule
	// Headers lists canonical response header names compared by value.
	Headers []string
	// SkipStatus disables status code comparison.
	SkipStatus bool
	// MaxDifferences bounds the reported differences; further differences
	// are counted in Result.Dropped.
	MaxDifferences int
	// MaxValuePreview bounds each Baseline/Candidate preview in bytes.
	MaxValuePreview int
	// Redactor sanitizes previews. Nil means redact.Default().
	Redactor *redact.Redactor
}

// PathTolerance is a tolerance scoped to a pointer pattern. It applies to
// numbers at the pointer and below it.
type PathTolerance struct {
	Pattern   jsonptr.Pattern
	Tolerance Tolerance
}

// UnorderedRule matches elements of arrays at Pattern by the value of
// their Key member.
type UnorderedRule struct {
	Pattern jsonptr.Pattern
	Key     string
}

// OptionsFromSpec converts a suite comparison spec into Options, using lim
// for the difference and preview bounds and r for preview redaction.
func OptionsFromSpec(spec suite.CompareSpec, lim limits.Limits, r *redact.Redactor) (Options, error) {
	o := Options{
		SkipStatus:      spec.SkipStatus,
		MaxDifferences:  lim.MaxDifferences,
		MaxValuePreview: lim.MaxValuePreview,
		Redactor:        r,
	}
	ignore, err := jsonptr.ParsePatterns(spec.Ignore)
	if err != nil {
		return Options{}, fmt.Errorf("compare ignore: %w", err)
	}
	o.Ignore = ignore
	if spec.Tolerance != nil {
		t, err := ParseTolerance(spec.Tolerance.Absolute, spec.Tolerance.Relative)
		if err != nil {
			return Options{}, fmt.Errorf("compare tolerance: %w", err)
		}
		o.Tolerance = t
	}
	for i, pt := range spec.PathTolerances {
		pat, err := jsonptr.ParsePattern(pt.Pointer)
		if err != nil {
			return Options{}, fmt.Errorf("compare path_tolerances[%d]: %w", i, err)
		}
		t, err := ParseTolerance(pt.Absolute, pt.Relative)
		if err != nil {
			return Options{}, fmt.Errorf("compare path_tolerances[%d]: %w", i, err)
		}
		o.PathTolerances = append(o.PathTolerances, PathTolerance{Pattern: pat, Tolerance: t})
	}
	for i, u := range spec.Unordered {
		pat, err := jsonptr.ParsePattern(u.Pointer)
		if err != nil {
			return Options{}, fmt.Errorf("compare unordered[%d]: %w", i, err)
		}
		if u.Key == "" {
			return Options{}, fmt.Errorf("compare unordered[%d]: key is required", i)
		}
		o.Unordered = append(o.Unordered, UnorderedRule{Pattern: pat, Key: u.Key})
	}
	o.Headers = canonicalHeaders(spec.Headers)
	return o, nil
}

// canonicalHeaders returns the canonical, de-duplicated, sorted names.
func canonicalHeaders(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		c := http.CanonicalHeaderKey(n)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// withDefaults fills unset bounds and the redactor.
func (o Options) withDefaults() Options {
	d := limits.Default()
	if o.MaxDifferences <= 0 {
		o.MaxDifferences = d.MaxDifferences
	}
	if o.MaxValuePreview <= 0 {
		o.MaxValuePreview = d.MaxValuePreview
	}
	if o.Redactor == nil {
		o.Redactor = redact.Default()
	}
	return o
}

// ignored reports whether p, or an ancestor of p, is excluded.
func (o *Options) ignored(p jsonptr.Pointer) bool {
	return o.Ignore.MatchPrefix(p)
}

// toleranceAt returns the tolerance for a number at p.
func (o *Options) toleranceAt(p jsonptr.Pointer) Tolerance {
	for _, pt := range o.PathTolerances {
		if pt.Pattern.MatchPrefix(p) {
			return pt.Tolerance
		}
	}
	return o.Tolerance
}

// unorderedKey returns the key member for the array at p, if configured.
func (o *Options) unorderedKey(p jsonptr.Pointer) (string, bool) {
	for _, u := range o.Unordered {
		if u.Pattern.Match(p) {
			return u.Key, true
		}
	}
	return "", false
}

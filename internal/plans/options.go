package plans

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Finding codes produced by Compare.
const (
	CodeScanStrategyChanged = "plans.scan_strategy_changed"
	CodeJoinStrategyChanged = "plans.join_strategy_changed"
	CodeEstimateMismatch    = "plans.estimate_mismatch"
	CodeTempIOIntroduced    = "plans.temp_io_introduced"
	CodeSortSpillIntroduced = "plans.sort_spill_introduced"
	CodeStructureChanged    = "plans.structure_changed"
	CodeNodeAdded           = "plans.node_added"
	CodeNodeRemoved         = "plans.node_removed"

	CodeCostChanged          = "plans.cost_changed"
	CodeRowsEstimateChanged  = "plans.rows_estimate_changed"
	CodeExecutionTimeChanged = "plans.execution_time_changed"
	CodeActualTimeChanged    = "plans.actual_time_changed"
	CodeBuffersChanged       = "plans.buffers_changed"
	CodeLoopsChanged         = "plans.loops_changed"

	CodeRuntimeInconclusive = "plans.runtime_inconclusive"
	CodeTimingInconclusive  = "plans.timing_inconclusive"
	CodeBuffersInconclusive = "plans.buffers_inconclusive"
	CodeSpillInconclusive   = "plans.spill_inconclusive"
)

// Fail-on classes accepted by ParseFailOn. Each class promotes the
// findings of the listed codes to severity error.
const (
	FailScan      = "scan"
	FailJoin      = "join"
	FailEstimate  = "estimate"
	FailSpill     = "spill"
	FailTempIO    = "temp_io"
	FailStructure = "structure"
)

// Option defaults.
const (
	DefaultRowFactor   = 10.0
	DefaultMinRows     = 100
	DefaultMaxFindings = 1000
)

// Options adjusts Compare. The zero value uses the defaults and fails on
// nothing.
type Options struct {
	// RowFactor is the estimated-vs-actual ratio at or above which a node
	// is reported as an estimate mismatch (default 10).
	RowFactor float64
	// MinRows is the floor on the larger of the total estimated and total
	// actual rows below which mismatches are ignored as noise (default 100).
	MinRows int64
	// FailOn holds fail-on classes; matching findings become errors.
	FailOn map[string]bool
	// MaxFindings bounds the findings kept (default 1000). Extra findings
	// are counted in Comparison.Dropped.
	MaxFindings int
}

// FailOnClasses returns the accepted fail-on classes in sorted order.
func FailOnClasses() []string {
	c := []string{FailScan, FailJoin, FailEstimate, FailSpill, FailTempIO, FailStructure}
	sort.Strings(c)
	return c
}

// ParseFailOn parses a comma-separated list of fail-on classes such as
// "scan,spill". "all" selects every class. An empty string selects none.
func ParseFailOn(s string) (map[string]bool, error) {
	out := map[string]bool{}
	valid := map[string]bool{}
	for _, c := range FailOnClasses() {
		valid[c] = true
	}
	for _, part := range strings.Split(s, ",") {
		c := strings.ToLower(strings.TrimSpace(part))
		switch {
		case c == "":
		case c == "all":
			for k := range valid {
				out[k] = true
			}
		case valid[c]:
			out[c] = true
		default:
			return nil, fmt.Errorf("unknown fail-on class %q (want %s or all)", part, strings.Join(FailOnClasses(), ", "))
		}
	}
	return out, nil
}

// Validate checks option values supplied by a caller.
func (o Options) Validate() error {
	if o.RowFactor != 0 && (math.IsNaN(o.RowFactor) || math.IsInf(o.RowFactor, 0) || o.RowFactor <= 1) {
		return errors.New("row factor must be a finite number greater than 1")
	}
	if o.MinRows < 0 {
		return errors.New("minimum rows must not be negative")
	}
	if o.MaxFindings < 0 {
		return errors.New("max findings must not be negative")
	}
	for c := range o.FailOn {
		if _, err := ParseFailOn(c); err != nil || strings.Contains(c, ",") {
			return fmt.Errorf("unknown fail-on class %q", c)
		}
	}
	return nil
}

// normalized fills defaults and replaces invalid values with defaults, so
// Compare never works with a nonsensical threshold.
func (o Options) normalized() Options {
	if o.RowFactor == 0 || math.IsNaN(o.RowFactor) || math.IsInf(o.RowFactor, 0) || o.RowFactor <= 1 {
		o.RowFactor = DefaultRowFactor
	}
	if o.MinRows <= 0 {
		o.MinRows = DefaultMinRows
	}
	if o.MaxFindings <= 0 {
		o.MaxFindings = DefaultMaxFindings
	}
	return o
}

// failClass maps a finding code to its fail-on class, or "".
func failClass(code string) string {
	switch code {
	case CodeScanStrategyChanged:
		return FailScan
	case CodeJoinStrategyChanged:
		return FailJoin
	case CodeEstimateMismatch:
		return FailEstimate
	case CodeSortSpillIntroduced:
		return FailSpill
	case CodeTempIOIntroduced:
		return FailTempIO
	case CodeStructureChanged, CodeNodeAdded, CodeNodeRemoved:
		return FailStructure
	}
	return ""
}

package compare

// Result is the outcome of one comparison.
type Result struct {
	// Differences are in traversal order: status, headers (sorted by
	// name), then body locations in sorted key order.
	Differences []Difference
	// Dropped counts differences beyond Options.MaxDifferences.
	Dropped int
	// WithinTolerance counts numeric differences that were accepted by a
	// tolerance and therefore not reported.
	WithinTolerance int
}

// Equal reports whether no difference was found. Inconclusive entries
// count: a comparison that could not be completed is not equal.
func (r Result) Equal() bool { return len(r.Differences) == 0 && r.Dropped == 0 }

// Inconclusive reports whether any part of the comparison could not be
// decided.
func (r Result) Inconclusive() bool {
	for _, d := range r.Differences {
		if d.Inconclusive {
			return true
		}
	}
	return false
}

// Codes returns the code of every reported difference, in order.
func (r Result) Codes() []string {
	out := make([]string, len(r.Differences))
	for i, d := range r.Differences {
		out[i] = d.Code
	}
	return out
}

func resultOf(l *diffList, within int) Result {
	return Result{Differences: l.items, Dropped: l.dropped, WithinTolerance: within}
}

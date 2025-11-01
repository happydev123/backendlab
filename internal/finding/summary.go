package finding

// Summary counts findings by kind and severity.
type Summary struct {
	Total      int              `json:"total"`
	Failing    int              `json:"failing"`
	ByKind     map[Kind]int     `json:"by_kind"`
	BySeverity map[Severity]int `json:"by_severity"`
}

// Summarize counts fs. The maps always contain every known key so JSON
// output has a stable shape.
func Summarize(fs []Finding) Summary {
	s := Summary{
		ByKind: map[Kind]int{
			KindTransportError: 0, KindAssertionFailure: 0, KindDifference: 0,
			KindInconclusive: 0, KindUnsupported: 0, KindObservation: 0,
		},
		BySeverity: map[Severity]int{SeverityError: 0, SeverityWarning: 0, SeverityInfo: 0},
	}
	for _, f := range fs {
		s.Total++
		s.ByKind[f.Kind]++
		s.BySeverity[f.Severity]++
		if f.Failing() {
			s.Failing++
		}
	}
	return s
}

// AnyFailing reports whether any finding in fs is failing.
func AnyFailing(fs []Finding) bool {
	for _, f := range fs {
		if f.Failing() {
			return true
		}
	}
	return false
}

// Collector accumulates findings up to a bound. Once the bound is reached
// further findings are counted but dropped, so callers can report that the
// list was truncated instead of growing without limit.
type Collector struct {
	max     int
	items   []Finding
	dropped int
}

// NewCollector returns a collector that keeps at most max findings
// (max <= 0 means 1000).
func NewCollector(max int) *Collector {
	if max <= 0 {
		max = 1000
	}
	return &Collector{max: max}
}

// Add records f, or counts it as dropped when the collector is full.
func (c *Collector) Add(f Finding) {
	if len(c.items) >= c.max {
		c.dropped++
		return
	}
	c.items = append(c.items, f)
}

// Items returns the retained findings.
func (c *Collector) Items() []Finding { return c.items }

// Dropped returns how many findings were discarded after the bound.
func (c *Collector) Dropped() int { return c.dropped }

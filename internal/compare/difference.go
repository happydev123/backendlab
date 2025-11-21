package compare

// Difference codes. Each names one way the candidate can differ from the
// baseline; findings carry the code unchanged.
const (
	CodeStatusChanged        = "compare.status_changed"
	CodeHeaderMissing        = "compare.header_missing"
	CodeHeaderAdded          = "compare.header_added"
	CodeHeaderChanged        = "compare.header_changed"
	CodeBodyFormatChanged    = "compare.body_format_changed"
	CodeBodyChanged          = "compare.body_changed"
	CodeFieldMissing         = "compare.field_missing"
	CodeFieldAdded           = "compare.field_added"
	CodeNullVsAbsent         = "compare.null_vs_absent"
	CodeTypeChanged          = "compare.type_changed"
	CodeValueChanged         = "compare.value_changed"
	CodeArrayLengthChanged   = "compare.array_length_changed"
	CodeElementMissing       = "compare.element_missing"
	CodeElementAdded         = "compare.element_added"
	CodeUnorderedUnmatchable = "compare.unordered_unmatchable"
	CodeTooDeep              = "compare.too_deep"
	CodeNumberUnparsable     = "compare.number_unparsable"
	CodeDifferencesDropped   = "compare.differences_dropped"
	CodeWithinTolerance      = "compare.within_tolerance"
)

// Location labels used outside JSON bodies.
const (
	LocationStatus = "status"
	LocationBody   = "body"
	headerPrefix   = "header:"
)

// absent is the preview shown for a value that does not exist on one side.
const absent = "(absent)"

// Difference is one reported way the candidate differs from the baseline,
// or one part of the comparison that could not be decided. Previews are
// already redacted and bounded.
type Difference struct {
	Code string
	// Location is a JSON Pointer into the body, "body" for the body as a
	// whole, "status", or "header:<Name>".
	Location string
	Message  string
	// Baseline and Candidate are value previews; "(absent)" marks a value
	// that does not exist on that side.
	Baseline  string
	Candidate string
	// Inconclusive marks a part of the documents that was not compared
	// because the configured rule could not be applied safely.
	Inconclusive bool
}

// diffList collects differences up to a bound and counts the rest.
type diffList struct {
	max     int
	items   []Difference
	dropped int
}

func newDiffList(max int) *diffList { return &diffList{max: max} }

func (l *diffList) add(d Difference) {
	if len(l.items) >= l.max {
		l.dropped++
		return
	}
	l.items = append(l.items, d)
}

// empty reports whether nothing was added, including dropped entries.
func (l *diffList) empty() bool { return len(l.items) == 0 && l.dropped == 0 }

// merge appends every entry of other, applying this list's bound.
func (l *diffList) merge(other *diffList) {
	for _, d := range other.items {
		l.add(d)
	}
	l.dropped += other.dropped
}

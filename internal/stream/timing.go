package stream

import (
	"sort"
	"time"
)

// GapSummary describes the time between consecutive events. Events that
// arrive in the same chunk share one receive time, so their gap is zero:
// the resolution is one read, not one event.
type GapSummary struct {
	// Count is the number of gaps (events minus one).
	Count int
	// Max is the largest gap; MaxAfter is the index of the event that
	// ended it.
	Max      time.Duration
	MaxAfter int
	// P50 is the median gap using the nearest-rank method.
	P50 time.Duration
	// Exceeded counts gaps longer than the expectation's MaxGap (0 when
	// no MaxGap is set).
	Exceeded int
}

// timing tracks event receive times for timed input. It keeps every gap
// (one duration per event, bounded by MaxStreamEvents) so the median is
// exact.
type timing struct {
	maxGap time.Duration
	first  *time.Duration
	last   time.Duration
	gaps   []time.Duration
	sum    GapSummary
}

func newTiming(maxGap time.Duration) *timing {
	return &timing{maxGap: maxGap}
}

// event records that event index was received at.
func (t *timing) event(index int, at time.Duration) {
	if t.first == nil {
		v := at
		t.first = &v
		t.last = at
		return
	}
	gap := at - t.last
	if gap < 0 {
		gap = 0 // a non-monotonic clock must not produce negative gaps
	}
	t.last = at
	t.gaps = append(t.gaps, gap)
	if len(t.gaps) == 1 || gap > t.sum.Max {
		t.sum.Max, t.sum.MaxAfter = gap, index
	}
	if t.maxGap > 0 && gap > t.maxGap {
		t.sum.Exceeded++
	}
}

// firstEvent returns the time of the first event, or nil.
func (t *timing) firstEvent() *time.Duration {
	if t.first == nil {
		return nil
	}
	v := *t.first
	return &v
}

// summary returns the gap summary, or nil when fewer than two events
// were received.
func (t *timing) summary() *GapSummary {
	if len(t.gaps) == 0 {
		return nil
	}
	s := t.sum
	s.Count = len(t.gaps)
	sorted := append([]time.Duration(nil), t.gaps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	// Nearest rank: the ceil(0.5*n)-th smallest value.
	s.P50 = sorted[(len(sorted)+1)/2-1]
	return &s
}

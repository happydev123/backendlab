package stream

import (
	"testing"
	"time"
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func TestTimingSummary(t *testing.T) {
	tm := newTiming(ms(100))
	for i, at := range []time.Duration{ms(40), ms(90), ms(90), ms(300), ms(350)} {
		tm.event(i, at)
	}
	if f := tm.firstEvent(); f == nil || *f != ms(40) {
		t.Fatalf("first = %v", f)
	}
	s := tm.summary()
	if s == nil {
		t.Fatal("no summary")
	}
	// Gaps: 50, 0, 210, 50 -> sorted 0, 50, 50, 210; nearest-rank median
	// is the 2nd value.
	if s.Count != 4 || s.Max != ms(210) || s.MaxAfter != 3 || s.P50 != ms(50) || s.Exceeded != 1 {
		t.Fatalf("summary = %+v", s)
	}
}

func TestTimingSingleEventHasNoGaps(t *testing.T) {
	tm := newTiming(0)
	if tm.firstEvent() != nil || tm.summary() != nil {
		t.Fatal("empty timing reported values")
	}
	tm.event(0, ms(5))
	if tm.summary() != nil {
		t.Fatal("one event cannot have a gap")
	}
}

func TestTimingClampsBackwardsClock(t *testing.T) {
	tm := newTiming(ms(1))
	tm.event(0, ms(10))
	tm.event(1, ms(5))
	s := tm.summary()
	if s.Max != 0 || s.Exceeded != 0 {
		t.Fatalf("summary = %+v", s)
	}
}

func TestTimingOddMedian(t *testing.T) {
	tm := newTiming(0)
	for i, at := range []time.Duration{0, ms(10), ms(40), ms(100)} {
		tm.event(i, at)
	}
	// Gaps 10, 30, 60: median 30; no MaxGap configured, nothing exceeded.
	if s := tm.summary(); s.P50 != ms(30) || s.Exceeded != 0 {
		t.Fatalf("summary = %+v", s)
	}
}

package stream

import (
	"time"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/redact"
)

// DefaultKeepEvents is the number of events retained in a Result for
// display when Options.KeepEvents is zero.
const DefaultKeepEvents = 50

// maxKeepEvents bounds Options.KeepEvents.
const maxKeepEvents = 10000

// Input formats reported in Result.Format.
const (
	FormatLive      = "live"
	FormatSSE       = "sse"
	FormatRecording = "recording"
)

// Options configure reading one stream.
type Options struct {
	// Limits bound parsing and findings. Zero fields take defaults.
	Limits limits.Limits
	// CaseID is copied into every finding.
	CaseID string
	// Now returns the current time. It defaults to time.Now and must be
	// safe for concurrent use: ReadLive calls it from its reader goroutine.
	Now func() time.Time
	// Start is the reference point for live timings (for example when the
	// request was sent). It defaults to Now() when ReadLive starts.
	Start time.Time
	// Redactor scrubs stream data placed into findings. Nil means the
	// built-in rules only.
	Redactor *redact.Redactor
	// KeepEvents is the maximum number of events kept in Result.Events
	// (default 50, at most 10000). All events are still checked.
	KeepEvents int
}

func (o Options) withDefaults() Options {
	o.Limits = o.Limits.Merge()
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Redactor == nil {
		o.Redactor = redact.Default()
	}
	switch {
	case o.KeepEvents <= 0:
		o.KeepEvents = DefaultKeepEvents
	case o.KeepEvents > maxKeepEvents:
		o.KeepEvents = maxKeepEvents
	}
	return o
}

// Result is the outcome of reading one stream. Timing fields are nil when
// the input carries no timing (saved SSE text); unknown is never zero.
type Result struct {
	// Events holds the first KeepEvents events. Event data is raw stream
	// content: redact it before display.
	Events []Event
	// EventCount is the number of dispatched events, including those not
	// retained in Events.
	EventCount int
	// Comments counts comment lines; UnknownFields counts ignored fields.
	Comments      int
	UnknownFields int
	// Bytes is the number of stream bytes parsed.
	Bytes int64
	// Timed reports whether the input carried timing.
	Timed bool
	// FirstEvent is the time from start to the first event.
	FirstEvent *time.Duration
	// Duration is the time from start to the end of reading.
	Duration *time.Duration
	// MaxGap is the largest gap between consecutive events (nil with
	// fewer than two events or no timing).
	MaxGap *time.Duration
	// Gaps summarizes inter-event gaps, or nil.
	Gaps *GapSummary
	// Termination classifies how reading ended.
	Termination Termination
	// TerminalSeen reports whether the terminal marker was received;
	// TerminalIndex is its event index (-1 when not seen).
	TerminalSeen  bool
	TerminalIndex int
	// LastEventID is the stream's last event ID at the end.
	LastEventID string
	// Findings are sorted and bounded by Limits.MaxFindings;
	// FindingsDropped counts findings beyond the bound.
	Findings        []finding.Finding
	FindingsDropped int
	// Limitations lists what this result cannot tell, in plain sentences.
	Limitations []string
	// Adapter is set when an adapter was configured.
	Adapter *AdapterResult
	// Format names the input format ("live", "sse", "recording").
	Format string
	// CaseID is Options.CaseID.
	CaseID string
}

// Failing reports whether any finding fails the run.
func (r *Result) Failing() bool { return finding.AnyFailing(r.Findings) }

func (r *Result) addLimitation(text string) {
	for _, l := range r.Limitations {
		if l == text {
			return
		}
	}
	r.Limitations = append(r.Limitations, text)
}

func durationPtr(d time.Duration) *time.Duration { return &d }

// Package stream implements StreamProbe: it parses server-sent event
// streams following the "event stream" interpretation rules of the WHATWG
// HTML standard, checks them against suite expectations, and records
// timing for live responses and timed recordings.
//
// Parsing is provider-neutral. Payload interpretation for chat-style
// incremental text (the "chat-delta" adapter) is optional and never
// affects how events are framed.
package stream

import "time"

// DefaultEventType is the type of an event that has no "event" field.
const DefaultEventType = "message"

// Event is one dispatched server-sent event.
type Event struct {
	// Index is the zero-based position of the event in the stream.
	Index int
	// Type is the value of the last "event" field, or "message".
	Type string
	// Data is the joined "data" field values (lines separated by "\n").
	Data string
	// ID is the stream's last event ID at dispatch time. Per the standard
	// the ID persists across events until another "id" field changes it.
	ID string
	// Retry is the reconnection time (milliseconds) set by a valid "retry"
	// field inside this event's block, or nil when the block had none.
	Retry *int
	// At is the time since the start of reading when the chunk that
	// completed the event was received, or nil when timing is unknown
	// (saved SSE text).
	At *time.Duration
}

// Termination classifies how reading a stream ended.
type Termination string

const (
	// TermTerminalEvent: the configured application terminal marker (an
	// event type or a data payload such as "[DONE]") was received.
	TermTerminalEvent Termination = "terminal_event"
	// TermEOF: the body ended without a terminal marker. EOF is the
	// normal end of an SSE response and is not malformed on its own.
	TermEOF Termination = "eof"
	// TermTimeout: the context deadline or the expectation's MaxDuration
	// expired before the stream ended.
	TermTimeout Termination = "timeout"
	// TermCancelled: the caller cancelled the context.
	TermCancelled Termination = "cancelled"
	// TermInvalidData: the stream contained data that cannot be decoded
	// (invalid UTF-8 in a line).
	TermInvalidData Termination = "invalid_data"
	// TermLimitExceeded: a line, event, byte or event-count bound was
	// exceeded and parsing stopped.
	TermLimitExceeded Termination = "limit_exceeded"
	// TermReadError: reading the body failed for a reason other than the
	// context.
	TermReadError Termination = "read_error"
)

// Abnormal reports whether t means the stream was not read to its natural
// end (terminal marker or EOF).
func (t Termination) Abnormal() bool {
	return t != TermTerminalEvent && t != TermEOF
}

// Known reports whether t is one of the defined terminations.
func (t Termination) Known() bool {
	switch t {
	case TermTerminalEvent, TermEOF, TermTimeout, TermCancelled,
		TermInvalidData, TermLimitExceeded, TermReadError:
		return true
	}
	return false
}

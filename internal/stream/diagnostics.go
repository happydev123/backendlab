package stream

import "fmt"

// Parser stop and warning codes. They double as finding codes.
const (
	CodeLineTooLong     = "stream.line_too_long"
	CodeEventTooLarge   = "stream.event_too_large"
	CodeStreamTooLarge  = "stream.stream_too_large"
	CodeTooManyEvents   = "stream.too_many_events"
	CodeInvalidUTF8     = "stream.invalid_utf8"
	CodeIDContainsNUL   = "stream.id_contains_nul"
	CodeInvalidRetry    = "stream.invalid_retry"
	CodeIncompleteEvent = "stream.incomplete_event"
)

// maxNotices bounds the warnings a parser keeps; later ones are counted.
const maxNotices = 100

// ParseError stops a Parser. Once returned, every later Feed and Finish
// call returns the same error.
type ParseError struct {
	// Termination is TermLimitExceeded or TermInvalidData.
	Termination Termination
	// Code is the finding code describing the violated rule.
	Code string
	// Line is the 1-based line number where parsing stopped (0 when the
	// stop is not tied to a line, for example the stream byte bound).
	Line int
	// Message is a sanitized description that contains no stream data.
	Message string
}

func (e *ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s at line %d: %s", e.Code, e.Line, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func limitError(code string, line int, format string, args ...any) *ParseError {
	return &ParseError{Termination: TermLimitExceeded, Code: code, Line: line, Message: fmt.Sprintf(format, args...)}
}

// Notice is a non-fatal problem the parser ignored as the standard
// requires (an "id" containing NUL, a non-numeric "retry").
type Notice struct {
	Code string
	// Line is the 1-based line number of the offending field.
	Line int
	// Event is the index the next dispatched event would receive, which
	// locates the notice relative to the events around it.
	Event   int
	Message string
	// Value is the raw field value. It is stream data: callers must
	// redact and truncate it before printing.
	Value string
}

// notices collects Notice values up to maxNotices.
type notices struct {
	items   []Notice
	dropped int
}

func (n *notices) add(x Notice) {
	if len(n.items) >= maxNotices {
		n.dropped++
		return
	}
	n.items = append(n.items, x)
}

package stream

import (
	"bytes"

	"github.com/happydev123/backendlab/internal/limits"
)

// Parser incrementally interprets an event stream. Feed it chunks in
// arrival order and call Finish at EOF. A Parser is not safe for
// concurrent use.
//
// Bounds come from limits.Limits: MaxLineBytes (one line, terminator
// excluded), MaxEventBytes (all lines between two blank lines, comments
// included), MaxStreamBytes (all bytes fed, BOM included) and
// MaxStreamEvents (dispatched events). Exceeding a bound stops the parser
// with a *ParseError instead of truncating.
type Parser struct {
	lim   limits.Limits
	lines lineSplitter

	eventType []byte
	data      []byte
	hasData   bool // at least one data field since the last dispatch
	retry     *int // valid retry seen in the current block
	blockSize int  // bytes of lines in the current block

	lastID    string
	lastRetry *int

	fed      int64
	events   int
	comments int
	unknown  int
	notes    notices

	out []Event
	err *ParseError
}

// NewParser returns a parser bounded by lim. Zero fields in lim take
// their defaults.
func NewParser(lim limits.Limits) *Parser {
	lim = lim.Merge()
	return &Parser{lim: lim, lines: lineSplitter{maxLine: lim.MaxLineBytes}}
}

// Feed parses chunk and returns the events it completed. When a bound is
// exceeded or a line is not valid UTF-8, Feed returns the events completed
// before the problem together with a *ParseError; the parser then stays
// stopped.
func (p *Parser) Feed(chunk []byte) ([]Event, error) {
	if p.err != nil {
		return nil, p.err
	}
	p.out = nil
	var over *ParseError
	if room := p.lim.MaxStreamBytes - p.fed; int64(len(chunk)) > room {
		chunk = chunk[:room]
		over = limitError(CodeStreamTooLarge, 0, "stream exceeds max_stream_bytes (%d)", p.lim.MaxStreamBytes)
	}
	p.fed += int64(len(chunk))
	if err := p.lines.split(chunk, p.line); err != nil {
		p.err = err
	} else if over != nil {
		p.err = over
	}
	out := p.out
	p.out = nil
	if p.err != nil {
		return out, p.err
	}
	return out, nil
}

// Finish ends the stream. Per the standard, data after the last blank
// line (an unterminated line or a partially accumulated event) is
// discarded, never dispatched; Pending reports whether that happened.
// Finish returns no events; the signature mirrors Feed so callers can
// treat both the same way.
func (p *Parser) Finish() ([]Event, error) {
	if p.err != nil {
		return nil, p.err
	}
	return nil, nil
}

// line handles one complete line.
func (p *Parser) line(line []byte) *ParseError {
	kind := classify(line)
	if kind == lineBlank {
		if err := p.dispatch(); err != nil {
			return err
		}
		return nil
	}
	p.blockSize += len(line)
	if p.blockSize > p.lim.MaxEventBytes {
		return limitError(CodeEventTooLarge, p.lines.line, "event exceeds max_event_bytes (%d)", p.lim.MaxEventBytes)
	}
	if kind == lineComment {
		p.comments++
		return nil
	}
	name, value := splitField(line)
	switch string(name) {
	case "event":
		p.eventType = append(p.eventType[:0], value...)
	case "data":
		p.data = append(p.data, value...)
		p.data = append(p.data, '\n')
		p.hasData = true
	case "id":
		if bytes.IndexByte(value, 0) >= 0 {
			p.notes.add(Notice{Code: CodeIDContainsNUL, Line: p.lines.line, Event: p.events,
				Message: "id field contains NUL and was ignored", Value: string(value)})
			return nil
		}
		p.lastID = string(value)
	case "retry":
		n, ok := parseRetry(value)
		if !ok {
			p.notes.add(Notice{Code: CodeInvalidRetry, Line: p.lines.line, Event: p.events,
				Message: "retry field is not a number of at most 9 ASCII digits and was ignored", Value: string(value)})
			return nil
		}
		p.retry, p.lastRetry = &n, &n
	default:
		p.unknown++
	}
	return nil
}

// dispatch completes the current block. An empty data buffer means no
// event is dispatched; the event type is reset either way.
func (p *Parser) dispatch() *ParseError {
	defer p.resetBlock()
	if !p.hasData {
		return nil
	}
	if p.events >= p.lim.MaxStreamEvents {
		return limitError(CodeTooManyEvents, p.lines.line, "stream exceeds max_stream_events (%d)", p.lim.MaxStreamEvents)
	}
	ev := Event{
		Index: p.events,
		Type:  DefaultEventType,
		Data:  string(p.data[:len(p.data)-1]),
		ID:    p.lastID,
		Retry: p.retry,
	}
	if len(p.eventType) > 0 {
		ev.Type = string(p.eventType)
	}
	p.events++
	p.out = append(p.out, ev)
	return nil
}

func (p *Parser) resetBlock() {
	p.eventType = p.eventType[:0]
	p.data = p.data[:0]
	p.hasData = false
	p.retry = nil
	p.blockSize = 0
}

// Bytes returns the number of bytes accepted so far.
func (p *Parser) Bytes() int64 { return p.fed }

// Events returns the number of dispatched events.
func (p *Parser) Events() int { return p.events }

// Comments returns the number of comment lines seen.
func (p *Parser) Comments() int { return p.comments }

// UnknownFields returns the number of ignored lines with unknown field names.
func (p *Parser) UnknownFields() int { return p.unknown }

// Notices returns the ignored-field warnings, at most 100 of them.
func (p *Parser) Notices() []Notice { return p.notes.items }

// NoticesDropped returns how many warnings were dropped after the bound.
func (p *Parser) NoticesDropped() int { return p.notes.dropped }

// LastEventID returns the current last event ID.
func (p *Parser) LastEventID() string { return p.lastID }

// Retry returns the last valid reconnection time, or nil.
func (p *Parser) Retry() *int { return p.lastRetry }

// Pending reports whether data after the last blank line would be
// discarded at EOF: an unterminated line, or an event or data field that
// was never dispatched. It also returns the number of discarded bytes.
func (p *Parser) Pending() (bool, int) {
	n := p.lines.pending() + len(p.data) + len(p.eventType)
	return p.hasData || len(p.eventType) > 0 || p.lines.pending() > 0, n
}

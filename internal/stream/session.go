package stream

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/happydev123/backendlab/internal/finding"
)

// session drives one stream through the parser, the checker, the timing
// tracker and the optional adapter. Live reads, saved SSE text and timed
// recordings all feed a session, so they produce the same findings for
// the same bytes.
type session struct {
	opts    Options
	exp     Expectations
	parser  *Parser
	sink    *sink
	check   *checker
	adapter *chatDelta
	timing  *timing // nil for untimed input
	res     *Result

	// stop is set when the session itself ended reading (parse error or
	// MaxDuration); it overrides the caller's termination.
	stop   Termination
	lastAt *time.Duration
}

func newSession(exp Expectations, opts Options, format string, timed bool) *session {
	opts = opts.withDefaults()
	s := &session{
		opts:   opts,
		exp:    exp,
		parser: NewParser(opts.Limits),
		sink:   newSink(opts.CaseID, opts.Redactor, opts.Limits.MaxValuePreview, opts.Limits.MaxFindings),
		res:    &Result{Format: format, Timed: timed, TerminalIndex: -1, CaseID: opts.CaseID},
	}
	if exp.Adapter == AdapterChatDelta {
		s.adapter = newChatDelta(int(min(opts.Limits.MaxResponseBytes, 1<<30)))
	}
	s.check = newChecker(exp, s.sink, s.adapter)
	if timed {
		s.timing = newTiming(exp.MaxGap)
	}
	if err := exp.Validate(); err != nil {
		s.sink.issue(CodeInvalidExpectation, finding.KindUnsupported, finding.SeverityError, "", err.Error())
	}
	return s
}

// feed processes one chunk received at at (nil for untimed input). It
// returns false once the session has stopped reading.
func (s *session) feed(chunk []byte, at *time.Duration) bool {
	if s.stop != "" {
		return false
	}
	if at != nil && !s.checkDuration(*at) {
		return false
	}
	events, err := s.parser.Feed(chunk)
	for _, ev := range events {
		s.event(ev, at)
	}
	if err != nil {
		s.parseError(err)
		return false
	}
	return true
}

func (s *session) event(ev Event, at *time.Duration) {
	if at != nil {
		ev.At = durationPtr(*at)
		s.timing.event(ev.Index, *at)
	}
	if s.check.event(ev) {
		s.res.TerminalSeen, s.res.TerminalIndex = true, ev.Index
	}
	if len(s.res.Events) < s.opts.KeepEvents {
		s.res.Events = append(s.res.Events, ev)
	}
}

// terminalSeen reports whether the terminal marker has been received.
func (s *session) terminalSeen() bool { return s.res.TerminalSeen }

func (s *session) parseError(err error) {
	var pe *ParseError
	if !errors.As(err, &pe) {
		pe = &ParseError{Termination: TermInvalidData, Code: CodeInvalidUTF8, Message: err.Error()}
	}
	s.stop = pe.Termination
	f := finding.Finding{
		Code: pe.Code, Kind: finding.KindInconclusive, Severity: finding.SeverityError,
		Message: pe.Message + "; the rest of the stream was not analyzed",
		Details: map[string]string{"events_before_stop": strconv.Itoa(s.parser.Events())},
	}
	if pe.Termination == TermInvalidData {
		f.Kind = finding.KindAssertionFailure
	}
	if pe.Line > 0 {
		f.Location = lineLocation(pe.Line)
	}
	s.sink.add(f)
}

// checkDuration records at as the latest time and reports whether it is
// within MaxDuration. Exceeding it stops the session with a finding.
func (s *session) checkDuration(at time.Duration) bool {
	s.lastAt = durationPtr(at)
	if s.exp.MaxDuration > 0 && at > s.exp.MaxDuration && s.stop == "" {
		s.maxDuration(at)
		return false
	}
	return s.stop == ""
}

func (s *session) maxDuration(at time.Duration) {
	s.stop = TermTimeout
	s.sink.add(finding.Finding{
		Code: CodeMaxDuration, Kind: finding.KindAssertionFailure, Severity: finding.SeverityError,
		Message:  fmt.Sprintf("stream was still open after max_duration; reading stopped after %d events", s.parser.Events()),
		Expected: "<= " + s.exp.MaxDuration.String(),
		Observed: at.String(),
	})
}

// end finalizes the result. term is how the caller's reading ended
// (TermEOF when the input reached its end), at is the time reading
// ended (nil when untimed) and cause describes a read error, timeout or
// cancellation in sanitized form.
func (s *session) end(term Termination, at *time.Duration, cause string) *Result {
	reachedEOF := term == TermEOF && s.stop == ""
	switch {
	case s.stop != "":
		term = s.stop
	case term == TermEOF && s.res.TerminalSeen:
		term = TermTerminalEvent
	}
	if at == nil {
		at = s.lastAt
	}
	r := s.res
	r.Termination = term
	r.EventCount = s.parser.Events()
	r.Comments = s.parser.Comments()
	r.UnknownFields = s.parser.UnknownFields()
	r.Bytes = s.parser.Bytes()
	r.LastEventID = s.parser.LastEventID()

	if reachedEOF {
		s.incomplete()
	}
	s.terminationFinding(term, cause)
	s.notices()
	s.check.finish(term, r.EventCount)
	s.timingResults(at)
	if s.adapter != nil {
		s.adapter.report(s.sink, r.EventCount)
		r.Adapter = s.adapter.result()
		r.Adapter.Preview = s.sink.data(r.Adapter.Text)
		for i, reason := range r.Adapter.FinishReasons {
			r.Adapter.FinishReasons[i] = s.sink.data(reason)
		}
	}
	if r.EventCount == 0 && reachedEOF {
		s.sink.issue(CodeNoEvents, finding.KindObservation, finding.SeverityWarning, "",
			"the stream ended without dispatching any event")
	}
	r.Findings = s.sink.items()
	r.FindingsDropped = s.sink.dropped()
	return r
}

// incomplete reports data discarded at EOF.
func (s *session) incomplete() {
	_, _ = s.parser.Finish()
	if pending, n := s.parser.Pending(); pending {
		s.sink.add(finding.Finding{
			Code: CodeIncompleteEvent, Kind: finding.KindObservation, Severity: finding.SeverityWarning,
			Location: eventLocation(s.parser.Events()),
			Message:  "stream ended inside an event (no terminating blank line); per the standard it was not dispatched",
			Details:  map[string]string{"discarded_bytes": strconv.Itoa(n)},
		})
	}
}

func (s *session) terminationFinding(term Termination, cause string) {
	events := s.parser.Events()
	switch term {
	case TermTimeout:
		if s.stop == TermTimeout {
			return // max_duration finding already recorded
		}
		s.sink.add(finding.Finding{
			Code: CodeTimeout, Kind: finding.KindTransportError, Severity: finding.SeverityError,
			Message:  fmt.Sprintf("deadline expired while reading the stream after %d events", events),
			Observed: cause,
		})
	case TermCancelled:
		s.sink.add(finding.Finding{
			Code: CodeCancelled, Kind: finding.KindObservation, Severity: finding.SeverityInfo,
			Message: fmt.Sprintf("reading was cancelled after %d events", events),
		})
	case TermReadError:
		s.sink.add(finding.Finding{
			Code: CodeReadError, Kind: finding.KindTransportError, Severity: finding.SeverityError,
			Message:  fmt.Sprintf("reading the stream failed after %d events", events),
			Observed: cause,
		})
	}
}

// notices converts parser warnings into findings.
func (s *session) notices() {
	for _, n := range s.parser.Notices() {
		s.sink.add(finding.Finding{
			Code: n.Code, Kind: finding.KindObservation, Severity: finding.SeverityWarning,
			Location: lineLocation(n.Line),
			Message:  n.Message,
			Observed: s.sink.data(n.Value),
			Details:  map[string]string{"next_event": strconv.Itoa(n.Event)},
		})
	}
	if d := s.parser.NoticesDropped(); d > 0 {
		s.sink.issue(CodeNoticesDropped, finding.KindObservation, finding.SeverityInfo, "",
			fmt.Sprintf("%d further ignored-field warnings were not listed", d))
	}
	if u := s.parser.UnknownFields(); u > 0 {
		s.sink.issue(CodeUnknownFields, finding.KindObservation, finding.SeverityInfo, "",
			fmt.Sprintf("%d lines with unknown field names were ignored", u))
	}
}

// timingResults fills timing fields and applies MaxGap, or reports that
// timing checks could not be evaluated.
func (s *session) timingResults(at *time.Duration) {
	r := s.res
	if s.timing == nil {
		if s.exp.MaxGap > 0 || s.exp.MaxDuration > 0 {
			s.sink.issue(CodeTimingUnavailable, finding.KindInconclusive, finding.SeverityWarning, "",
				"max_gap and max_duration cannot be checked: the input carries no timing")
		}
		return
	}
	r.FirstEvent = s.timing.firstEvent()
	if at != nil {
		r.Duration = durationPtr(*at)
	}
	r.Gaps = s.timing.summary()
	if r.Gaps == nil {
		return
	}
	r.MaxGap = durationPtr(r.Gaps.Max)
	if r.Gaps.Exceeded > 0 {
		s.sink.add(finding.Finding{
			Code: CodeMaxGapExceeded, Kind: finding.KindAssertionFailure, Severity: finding.SeverityError,
			Location: eventLocation(r.Gaps.MaxAfter),
			Message:  fmt.Sprintf("%d of %d gaps between events exceeded max_gap", r.Gaps.Exceeded, r.Gaps.Count),
			Expected: "<= " + s.exp.MaxGap.String(),
			Observed: r.Gaps.Max.String(),
		})
	}
}

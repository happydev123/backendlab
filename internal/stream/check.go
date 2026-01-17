package stream

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/jsonutil"
)

// checker applies Expectations to events as they are dispatched. Checks
// that need the whole stream (required events, terminal marker) run in
// finish.
type checker struct {
	exp     Expectations
	sink    *sink
	adapter *chatDelta

	seen          map[string]bool
	terminalSeen  bool
	terminalIndex int
	afterTerminal int
	firstAfter    int
}

func newChecker(exp Expectations, s *sink, adapter *chatDelta) *checker {
	seen := make(map[string]bool, len(exp.RequiredEvents))
	for _, t := range exp.RequiredEvents {
		seen[t] = false
	}
	return &checker{exp: exp, sink: s, adapter: adapter, seen: seen}
}

// needsJSON reports whether payloads must be decoded at all.
func (c *checker) needsJSON() bool {
	return c.exp.JSONPayloads || c.exp.Schema != nil || c.adapter != nil
}

// event checks one event and reports whether it is the first terminal
// marker. Terminal marker events are exempt from payload checks and from
// the adapter: they are framing, not payload.
func (c *checker) event(ev Event) (terminal bool) {
	// Only required types are tracked, so the map stays bounded by the
	// RequiredEvents list whatever types the stream sends.
	if _, ok := c.seen[ev.Type]; ok {
		c.seen[ev.Type] = true
	}
	if c.terminalSeen {
		if c.afterTerminal == 0 {
			c.firstAfter = ev.Index
		}
		c.afterTerminal++
	}
	if c.exp.IsTerminal(ev) {
		if !c.terminalSeen {
			c.terminalSeen, c.terminalIndex = true, ev.Index
			return true
		}
		return false
	}
	if !c.needsJSON() {
		return false
	}
	payload, err := jsonutil.Decode([]byte(ev.Data))
	if c.adapter != nil {
		c.adapter.observe(ev.Index, payload, err == nil)
	}
	if err != nil {
		if c.exp.JSONPayloads || c.exp.Schema != nil {
			c.sink.add(finding.Finding{
				Code: CodeInvalidJSONPayload, Kind: finding.KindAssertionFailure, Severity: finding.SeverityError,
				Location: eventLocation(ev.Index),
				Message:  "event data is not valid JSON: " + c.sink.red.String(err.Error()),
				Observed: c.sink.data(ev.Data),
				Details:  map[string]string{"event_type": c.sink.data(ev.Type)},
			})
		}
		return false
	}
	if c.exp.Schema != nil {
		for _, v := range c.exp.Schema.Validate(payload) {
			f := finding.Finding{
				Code: CodeSchemaViolation, Kind: finding.KindAssertionFailure, Severity: finding.SeverityError,
				Location: eventLocation(ev.Index) + v.Location,
				Message:  c.sink.red.String(v.Message),
				Details:  map[string]string{"schema": c.exp.Schema.Name()},
			}
			if v.Keyword != "" {
				f.Details["keyword"] = v.Keyword
			}
			c.sink.add(f)
		}
	}
	return false
}

// finish adds whole-stream findings once reading has ended with term.
func (c *checker) finish(term Termination, events int) {
	missing := []string{}
	for _, t := range c.exp.RequiredEvents {
		if !c.seen[t] {
			missing = append(missing, t)
		}
	}
	sort.Strings(missing)
	for _, t := range dedupeSorted(missing) {
		c.sink.add(finding.Finding{
			Code: CodeMissingRequired, Kind: finding.KindAssertionFailure, Severity: finding.SeverityError,
			Location: "type:" + c.sink.data(t),
			Message:  fmt.Sprintf("required event type was not received (%d events read, termination %s)", events, term),
			Expected: c.sink.data(t),
		})
	}
	if c.afterTerminal > 0 {
		c.sink.add(finding.Finding{
			Code: CodeAfterTerminal, Kind: finding.KindObservation, Severity: finding.SeverityWarning,
			Location: eventLocation(c.firstAfter),
			Message:  fmt.Sprintf("%d events arrived after the terminal marker (event %d)", c.afterTerminal, c.terminalIndex),
			Details:  map[string]string{"terminal_index": strconv.Itoa(c.terminalIndex), "after_terminal": strconv.Itoa(c.afterTerminal)},
		})
	}
	if !c.exp.HasTerminal() || c.terminalSeen {
		return
	}
	marker := c.marker()
	switch {
	case term == TermEOF && c.exp.RequireTerminal:
		c.sink.add(finding.Finding{
			Code: CodeMissingTerminal, Kind: finding.KindAssertionFailure, Severity: finding.SeverityError,
			Message:  "stream ended (EOF) before the required terminal marker",
			Expected: marker, Observed: "eof",
		})
	case term == TermEOF:
		c.sink.add(finding.Finding{
			Code: CodeEOFBeforeTerminal, Kind: finding.KindObservation, Severity: finding.SeverityInfo,
			Message:  "stream ended (EOF) without the terminal marker; require_terminal is not set, so this is not a failure",
			Expected: marker, Observed: "eof",
		})
	case c.exp.RequireTerminal:
		// The stream stopped abnormally; the termination finding already
		// fails the run. Record that completion was not observed either.
		c.sink.add(finding.Finding{
			Code: CodeMissingTerminal, Kind: finding.KindInconclusive, Severity: finding.SeverityWarning,
			Message:  fmt.Sprintf("terminal marker not observed before termination %s", term),
			Expected: marker, Observed: string(term),
		})
	}
}

// marker describes the configured terminal marker for messages.
func (c *checker) marker() string {
	var parts []string
	if c.exp.TerminalEvent != "" {
		parts = append(parts, "event type "+c.sink.data(c.exp.TerminalEvent))
	}
	if c.exp.TerminalData != "" {
		parts = append(parts, "data "+c.sink.data(c.exp.TerminalData))
	}
	return strings.Join(parts, " or ")
}

func dedupeSorted(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

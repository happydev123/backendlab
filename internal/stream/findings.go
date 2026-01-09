package stream

import (
	"strconv"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/redact"
)

// Finding codes produced outside the parser.
const (
	CodeMissingTerminal     = "stream.missing_terminal"
	CodeEOFBeforeTerminal   = "stream.eof_before_terminal"
	CodeAfterTerminal       = "stream.event_after_terminal"
	CodeMissingRequired     = "stream.missing_required_event"
	CodeInvalidJSONPayload  = "stream.invalid_json_payload"
	CodeSchemaViolation     = "stream.schema_violation"
	CodeMaxGapExceeded      = "stream.max_gap_exceeded"
	CodeMaxDuration         = "stream.max_duration_exceeded"
	CodeTimingUnavailable   = "stream.timing_unavailable"
	CodeTimeout             = "stream.timeout"
	CodeCancelled           = "stream.cancelled"
	CodeReadError           = "stream.read_error"
	CodeNoEvents            = "stream.no_events"
	CodeNoticesDropped      = "stream.notices_dropped"
	CodeUnknownFields       = "stream.unknown_fields"
	CodeAdapterUnrecognized = "stream.adapter_unrecognized"
	CodeAdapterNoDeltas     = "stream.adapter_no_deltas"
	CodeAdapterTruncated    = "stream.adapter_text_truncated"
	CodeRecordingTruncated  = "stream.recording_truncated"
	CodeInvalidExpectation  = "stream.invalid_expectations"
)

// sink builds stream findings with the module and case ID filled in,
// keeps at most the configured number, and sanitizes stream data before
// it is placed into a finding.
type sink struct {
	caseID   string
	red      *redact.Redactor
	preview  int
	collect  *finding.Collector
	counters map[string]int
}

func newSink(caseID string, red *redact.Redactor, preview, max int) *sink {
	if red == nil {
		red = redact.Default()
	}
	return &sink{caseID: caseID, red: red, preview: preview, collect: finding.NewCollector(max), counters: map[string]int{}}
}

// add records a finding. Module and CaseID are always overwritten.
func (s *sink) add(f finding.Finding) {
	f.Module = finding.ModuleStream
	f.CaseID = s.caseID
	s.counters[f.Code]++
	s.collect.Add(f)
}

// issue is a shorthand for add with the common fields.
func (s *sink) issue(code string, kind finding.Kind, sev finding.Severity, loc, msg string) {
	s.add(finding.Finding{Code: code, Kind: kind, Severity: sev, Location: loc, Message: msg})
}

// data returns stream data that is safe to place into a finding:
// registered secrets are scrubbed first, then the text is truncated to the
// preview bound so a partially cut secret cannot survive truncation.
func (s *sink) data(v string) string {
	return jsonutil.Truncate(s.red.String(v), s.preview)
}

// count returns how many findings with code were offered, including
// dropped ones.
func (s *sink) count(code string) int { return s.counters[code] }

// items returns the retained findings sorted deterministically.
func (s *sink) items() []finding.Finding {
	out := append([]finding.Finding(nil), s.collect.Items()...)
	finding.Sort(out)
	return out
}

func (s *sink) dropped() int { return s.collect.Dropped() }

// eventLocation formats the Location of a finding about one event.
func eventLocation(index int) string {
	return "event:" + strconv.Itoa(index)
}

// lineLocation formats the Location of a finding about one input line.
func lineLocation(line int) string {
	return "line:" + strconv.Itoa(line)
}

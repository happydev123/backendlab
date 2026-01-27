package stream

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/report"
)

// CommandInspect is the report command name for offline inspection.
const CommandInspect = "stream.inspect"

// CodeSummary is the code of the observation that summarizes a stream in
// a report; CodeAdapterSummary summarizes the adapter output.
const (
	CodeSummary        = "stream.summary"
	CodeAdapterSummary = "stream.adapter_summary"
)

// Report builds a finalized offline report for one inspected stream. The
// report has no wall-clock timing, so the same input always gives the
// same document. Stream durations appear only when the input carried
// timing (live reads and recordings).
func (r *Result) Report(input report.Input) *report.Report {
	rep := report.New(CommandInspect)
	rep.Inputs = append(rep.Inputs, input)
	caseID := r.caseID()
	obs := report.Observation{Target: input.Name, ResponseBytes: r.Bytes, Events: r.EventCount}
	if r.Duration != nil {
		obs.DurationMS = report.Millis(*r.Duration)
	}
	if r.FirstEvent != nil {
		obs.FirstEventMS = report.Millis(*r.FirstEvent)
	}
	if r.Termination.Abnormal() {
		obs.Error = string(r.Termination)
	}
	rep.Findings = append(rep.Findings, r.Findings...)
	rep.AddFindings(r.summaryFinding(caseID))
	if r.Adapter != nil {
		rep.AddFindings(r.adapterFinding(caseID))
	}
	rep.Dropped = r.FindingsDropped
	for _, l := range r.Limitations {
		rep.AddLimitation(l)
	}
	if r.FindingsDropped > 0 {
		rep.AddLimitation(fmt.Sprintf("%d findings beyond the configured bound were dropped.", r.FindingsDropped))
	}
	rep.Cases = []report.Case{{ID: caseID, Status: r.caseStatus(), Observations: []report.Observation{obs}}}
	if r.Termination == TermCancelled {
		rep.Fail(report.StatusCancelled, "stream reading was cancelled")
	}
	rep.Finalize()
	return rep
}

func (r *Result) caseID() string {
	if r.CaseID != "" {
		return r.CaseID
	}
	return "stream"
}

func (r *Result) caseStatus() report.CaseStatus {
	inconclusive := false
	for _, f := range r.Findings {
		if f.Failing() {
			return report.CaseFailed
		}
		if f.Kind == finding.KindInconclusive {
			inconclusive = true
		}
	}
	switch {
	case r.Termination == TermCancelled:
		return report.CaseCancelled
	case inconclusive:
		return report.CaseInconclusive
	}
	return report.CasePassed
}

// summaryFinding records the facts of the stream as an info observation
// so they appear in every report format.
func (r *Result) summaryFinding(caseID string) finding.Finding {
	d := map[string]string{
		"termination":    string(r.Termination),
		"events":         strconv.Itoa(r.EventCount),
		"comments":       strconv.Itoa(r.Comments),
		"bytes":          strconv.FormatInt(r.Bytes, 10),
		"format":         r.Format,
		"terminal_seen":  strconv.FormatBool(r.TerminalSeen),
		"unknown_fields": strconv.Itoa(r.UnknownFields),
	}
	if r.TerminalSeen {
		d["terminal_index"] = strconv.Itoa(r.TerminalIndex)
	}
	putDuration(d, "first_event_ms", r.FirstEvent)
	putDuration(d, "duration_ms", r.Duration)
	if g := r.Gaps; g != nil {
		d["gaps"] = strconv.Itoa(g.Count)
		putDuration(d, "gap_max_ms", &g.Max)
		putDuration(d, "gap_p50_ms", &g.P50)
	}
	timing := "timing unknown"
	if r.Timed {
		timing = "timed"
	}
	return finding.Finding{
		Module: finding.ModuleStream, Code: CodeSummary, Kind: finding.KindObservation, Severity: finding.SeverityInfo,
		CaseID:  caseID,
		Message: fmt.Sprintf("%d events, termination %s, %s", r.EventCount, r.Termination, timing),
		Details: d,
	}
}

func (r *Result) adapterFinding(caseID string) finding.Finding {
	a := r.Adapter
	d := map[string]string{
		"adapter":      a.Name,
		"deltas":       strconv.Itoa(a.Deltas),
		"lifecycle":    strconv.Itoa(a.Lifecycle),
		"unrecognized": strconv.Itoa(a.Unrecognized),
		"text_bytes":   strconv.FormatInt(a.TextBytes, 10),
	}
	if len(a.FinishReasons) > 0 {
		d["finish_reasons"] = strings.Join(a.FinishReasons, ",")
	}
	return finding.Finding{
		Module: finding.ModuleStream, Code: CodeAdapterSummary, Kind: finding.KindObservation, Severity: finding.SeverityInfo,
		CaseID:   caseID,
		Message:  fmt.Sprintf("%s adapter assembled %d bytes of text from %d delta events", a.Name, a.TextBytes, a.Deltas),
		Observed: a.Preview,
		Details:  d,
	}
}

func putDuration(d map[string]string, key string, v *time.Duration) {
	if v == nil {
		return
	}
	d[key] = strconv.FormatFloat(*report.Millis(*v), 'f', -1, 64)
}

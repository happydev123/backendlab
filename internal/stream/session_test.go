package stream

import (
	"strings"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/redact"
)

type timedChunk struct {
	at   time.Duration
	data string
}

func runSession(exp Expectations, opts Options, timed bool, end time.Duration, chunks ...timedChunk) *Result {
	s := newSession(exp, opts, "test", timed)
	for _, c := range chunks {
		var at *time.Duration
		if timed {
			at = durationPtr(c.at)
		}
		if !s.feed([]byte(c.data), at) {
			return s.end(s.stop, at, "")
		}
	}
	var at *time.Duration
	if timed {
		at = &end
	}
	return s.end(TermEOF, at, "")
}

func findCode(fs []finding.Finding, code string) *finding.Finding {
	for i := range fs {
		if fs[i].Code == code {
			return &fs[i]
		}
	}
	return nil
}

func TestSessionUntimed(t *testing.T) {
	exp := Expectations{MaxGap: time.Second, TerminalData: "[DONE]"}
	r := runSession(exp, Options{CaseID: "c"}, false, 0, timedChunk{data: "data: a\n\ndata: [DONE]\n\n"})
	if r.Termination != TermTerminalEvent || !r.TerminalSeen || r.TerminalIndex != 1 || r.EventCount != 2 {
		t.Fatalf("result = %+v", r)
	}
	if r.FirstEvent != nil || r.Duration != nil || r.MaxGap != nil || r.Gaps != nil || r.Timed {
		t.Fatal("untimed input reported timing")
	}
	for _, ev := range r.Events {
		if ev.At != nil {
			t.Fatal("untimed event has a time")
		}
	}
	f := findCode(r.Findings, CodeTimingUnavailable)
	if f == nil || f.Kind != finding.KindInconclusive || f.Failing() || f.CaseID != "c" {
		t.Fatalf("timing_unavailable = %+v", f)
	}
}

func TestSessionTimed(t *testing.T) {
	exp := Expectations{MaxGap: 100 * time.Millisecond}
	r := runSession(exp, Options{}, true, 900*time.Millisecond,
		timedChunk{120 * time.Millisecond, "data: a\n\ndata: b\n\n"},
		timedChunk{200 * time.Millisecond, "data: c"},
		timedChunk{500 * time.Millisecond, "\n\n"},
	)
	if r.Termination != TermEOF || r.EventCount != 3 {
		t.Fatalf("result = %+v", r)
	}
	if *r.FirstEvent != 120*time.Millisecond || *r.Duration != 900*time.Millisecond || *r.MaxGap != 380*time.Millisecond {
		t.Fatalf("first=%v duration=%v maxgap=%v", *r.FirstEvent, *r.Duration, *r.MaxGap)
	}
	if *r.Events[2].At != 500*time.Millisecond {
		t.Fatalf("event 2 at %v (time of the completing chunk expected)", *r.Events[2].At)
	}
	f := findCode(r.Findings, CodeMaxGapExceeded)
	if f == nil || !f.Failing() || f.Location != "event:2" || f.Observed != "380ms" {
		t.Fatalf("max gap finding = %+v", f)
	}
}

func TestSessionMaxDuration(t *testing.T) {
	exp := Expectations{MaxDuration: time.Second}
	r := runSession(exp, Options{}, true, 3*time.Second,
		timedChunk{100 * time.Millisecond, "data: a\n\n"},
		timedChunk{1500 * time.Millisecond, "data: b\n\n"},
	)
	if r.Termination != TermTimeout || r.EventCount != 1 || *r.Duration != 1500*time.Millisecond {
		t.Fatalf("result = %+v", r)
	}
	if f := findCode(r.Findings, CodeMaxDuration); f == nil || !f.Failing() {
		t.Fatalf("findings = %+v", r.Findings)
	}
	if findCode(r.Findings, CodeTimeout) != nil {
		t.Fatal("max_duration also reported as a transport timeout")
	}
}

func TestSessionParseStop(t *testing.T) {
	r := runSession(Expectations{}, Options{Limits: limits.Limits{MaxLineBytes: 10}}, false, 0,
		timedChunk{data: "data: a\n\ndata: too long\n\n"})
	if r.Termination != TermLimitExceeded || r.EventCount != 1 {
		t.Fatalf("result = %+v", r)
	}
	f := findCode(r.Findings, CodeLineTooLong)
	if f == nil || !f.Failing() || f.Location != "line:3" || f.Kind != finding.KindInconclusive {
		t.Fatalf("finding = %+v", f)
	}
	if findCode(r.Findings, CodeIncompleteEvent) != nil {
		t.Fatal("incomplete event reported after a parser stop")
	}

	r = runSession(Expectations{}, Options{}, false, 0, timedChunk{data: "data: \xFF\n\n"})
	if r.Termination != TermInvalidData {
		t.Fatalf("termination = %s", r.Termination)
	}
	if f := findCode(r.Findings, CodeInvalidUTF8); f == nil || f.Kind != finding.KindAssertionFailure {
		t.Fatalf("findings = %+v", r.Findings)
	}
}

func TestSessionIncompleteAndEmpty(t *testing.T) {
	r := runSession(Expectations{}, Options{}, false, 0, timedChunk{data: "data: a\n\ndata: partial"})
	f := findCode(r.Findings, CodeIncompleteEvent)
	if f == nil || f.Severity != finding.SeverityWarning || f.Location != "event:1" || f.Details["discarded_bytes"] != "13" {
		t.Fatalf("incomplete = %+v", f)
	}
	if r.Failing() {
		t.Fatal("an incomplete trailing event must not fail the run")
	}
	r = runSession(Expectations{}, Options{}, false, 0, timedChunk{data: ": only a comment\n\n"})
	if f := findCode(r.Findings, CodeNoEvents); f == nil || r.Comments != 1 {
		t.Fatalf("findings = %+v", r.Findings)
	}
}

func TestSessionNoticesAreRedacted(t *testing.T) {
	opts := Options{Redactor: redact.Default().WithSecrets("tok-abcdef")}
	r := runSession(Expectations{}, opts, false, 0,
		timedChunk{data: "id: tok-abcdef\x00\nretry: 1e3\nx-custom: 1\ndata: a\n\n"})
	nul := findCode(r.Findings, CodeIDContainsNUL)
	if nul == nil || strings.Contains(nul.Observed, "tok-abcdef") || nul.Location != "line:1" {
		t.Fatalf("nul finding = %+v", nul)
	}
	if f := findCode(r.Findings, CodeInvalidRetry); f == nil || f.Observed != "1e3" || f.Failing() {
		t.Fatalf("retry finding = %+v", f)
	}
	if f := findCode(r.Findings, CodeUnknownFields); f == nil || r.UnknownFields != 1 {
		t.Fatalf("unknown = %+v", f)
	}
}

func TestSessionBounds(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 30; i++ {
		b.WriteString("data: not json\n\n")
	}
	opts := Options{KeepEvents: 5, Limits: limits.Limits{MaxFindings: 10}}
	r := runSession(Expectations{JSONPayloads: true}, opts, false, 0, timedChunk{data: b.String()})
	if len(r.Events) != 5 || r.EventCount != 30 {
		t.Fatalf("events kept=%d count=%d", len(r.Events), r.EventCount)
	}
	if len(r.Findings) != 10 || r.FindingsDropped != 20 {
		t.Fatalf("findings=%d dropped=%d", len(r.Findings), r.FindingsDropped)
	}
}

func TestSessionInvalidExpectations(t *testing.T) {
	r := runSession(Expectations{Adapter: "nope"}, Options{}, false, 0, timedChunk{data: "data: a\n\n"})
	if f := findCode(r.Findings, CodeInvalidExpectation); f == nil || f.Kind != finding.KindUnsupported {
		t.Fatalf("findings = %+v", r.Findings)
	}
}

func TestSessionAdapter(t *testing.T) {
	opts := Options{Redactor: redact.Default().WithSecrets("s3cr3t-value")}
	exp := Expectations{Adapter: AdapterChatDelta, TerminalData: "[DONE]"}
	r := runSession(exp, opts, false, 0, timedChunk{data: "data: {\"delta\":\"key s3cr3t-value\"}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"})
	a := r.Adapter
	if a == nil || a.Text != "key s3cr3t-value" || a.Preview != "key [REDACTED]" || a.Deltas != 2 || a.Unrecognized != 0 {
		t.Fatalf("adapter = %+v", a)
	}
	if len(a.FinishReasons) != 1 || a.FinishReasons[0] != "stop" {
		t.Fatalf("reasons = %v", a.FinishReasons)
	}
	if r.Failing() {
		t.Fatalf("findings = %+v", r.Findings)
	}
}

package stream

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/schema"
)

func TestInspectSSEText(t *testing.T) {
	exp := Expectations{JSONPayloads: true, TerminalData: "[DONE]", RequireTerminal: true, MaxGap: time.Second, Adapter: AdapterChatDelta}
	r, err := InspectFile(filepath.Join("testdata", "chat.sse"), exp, Options{CaseID: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Format != FormatSSE || r.Timed || r.Termination != TermTerminalEvent || r.EventCount != 5 || r.Comments != 2 {
		t.Fatalf("result = %+v", r)
	}
	if r.FirstEvent != nil || r.Duration != nil || r.MaxGap != nil || r.Gaps != nil {
		t.Fatal("saved SSE text must not report timing")
	}
	if len(r.Limitations) != 1 || !strings.Contains(r.Limitations[0], "none is inferred from position") {
		t.Fatalf("limitations = %v", r.Limitations)
	}
	if r.Failing() {
		t.Fatalf("findings = %+v", r.Findings)
	}
	if findCode(r.Findings, CodeTimingUnavailable) == nil {
		t.Fatal("max_gap on untimed input must be reported as inconclusive")
	}
	if r.Adapter == nil || r.Adapter.Text != "Bonjour, ça va ?" || r.Adapter.FinishReasons[0] != "stop" {
		t.Fatalf("adapter = %+v", r.Adapter)
	}
}

func TestInspectTruncatedSSE(t *testing.T) {
	sch, err := schema.CompileFile(filepath.Join("testdata", "schemas", "chunk.json"))
	if err != nil {
		t.Fatal(err)
	}
	exp := Expectations{Schema: sch, TerminalData: "[DONE]", RequireTerminal: true, RequiredEvents: []string{"start", "end"}}
	r, err := InspectFile(filepath.Join("testdata", "truncated.sse"), exp, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Termination != TermEOF || r.EventCount != 4 {
		t.Fatalf("result = %+v", r)
	}
	for _, code := range []string{CodeMissingTerminal, CodeIncompleteEvent, CodeIDContainsNUL, CodeInvalidRetry,
		CodeSchemaViolation, CodeInvalidJSONPayload, CodeMissingRequired} {
		if findCode(r.Findings, code) == nil {
			t.Errorf("missing finding %s", code)
		}
	}
	if f := findCode(r.Findings, CodeSchemaViolation); f.Location != "event:2/seq" {
		t.Fatalf("schema location = %q", f.Location)
	}
}

func TestInspectRecording(t *testing.T) {
	exp := Expectations{TerminalEvent: "done", RequiredEvents: []string{"delta"}, MaxGap: 500 * time.Millisecond, Adapter: AdapterChatDelta}
	r, err := InspectFile(filepath.Join("testdata", "chat.jsonl"), exp, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Format != FormatRecording || !r.Timed || r.Termination != TermTerminalEvent || r.EventCount != 5 {
		t.Fatalf("result = %+v", r)
	}
	if *r.FirstEvent != 120500*time.Microsecond || *r.Duration != 960*time.Millisecond || *r.MaxGap != 704750*time.Microsecond {
		t.Fatalf("first=%v duration=%v maxgap=%v", *r.FirstEvent, *r.Duration, *r.MaxGap)
	}
	// The event split inside "é" across two base64 chunks completes with
	// the second chunk.
	if r.Events[1].Type != "delta" || *r.Events[1].At != 195250*time.Microsecond {
		t.Fatalf("event 1 = %+v", r.Events[1])
	}
	if f := findCode(r.Findings, CodeMaxGapExceeded); f == nil || f.Location != "event:2" {
		t.Fatalf("findings = %+v", r.Findings)
	}
	if r.Adapter.Text != "héllo" || r.Adapter.Lifecycle != 1 || r.Adapter.FinishReasons[0] != "end_turn" {
		t.Fatalf("adapter = %+v", r.Adapter)
	}
}

func TestInspectStoppedRecording(t *testing.T) {
	exp := Expectations{TerminalData: "[DONE]", MaxDuration: 5 * time.Second}
	r, err := InspectFile(filepath.Join("testdata", "stopped.jsonl"), exp, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Termination != TermTimeout || r.EventCount != 2 || *r.Duration != 2*time.Second {
		t.Fatalf("result = %+v", r)
	}
	if findCode(r.Findings, CodeTimeout) == nil || findCode(r.Findings, CodeIncompleteEvent) != nil {
		t.Fatalf("findings = %+v", r.Findings)
	}
	if len(r.Limitations) != 2 {
		t.Fatalf("limitations = %v", r.Limitations)
	}
	// The same recording with a tighter max_duration stops at the bound.
	exp.MaxDuration = time.Second
	r, err = InspectFile(filepath.Join("testdata", "stopped.jsonl"), exp, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Termination != TermTimeout || findCode(r.Findings, CodeMaxDuration) == nil || findCode(r.Findings, CodeTimeout) != nil {
		t.Fatalf("findings = %+v", r.Findings)
	}
}

const header = `{"format":"backendlab.sse-recording/v1"}` + "\n"

func TestInspectRecordingEndings(t *testing.T) {
	tests := []struct {
		name string
		in   string
		term Termination
		lim  string
	}{
		{"no end record", header + `{"offset_ms":1,"chunk":"data: a\n\n"}` + "\n", TermEOF, "no end record"},
		{"read error", header + `{"offset_ms":1,"chunk":"data: a\n\n"}` + "\n" + `{"offset_ms":2,"end":"read_error"}`, TermReadError, ""},
		{"limit", header + `{"offset_ms":1,"chunk":"data: a\n\n"}` + "\n" + `{"offset_ms":2,"end":"limit_exceeded"}`, TermLimitExceeded, ""},
		{"stopped without termination", header + `{"offset_ms":1,"chunk":"data: a\n\n"}` + "\n" + `{"offset_ms":2,"end":"stopped"}`, TermCancelled, "stopped by the reader"},
		{"stopped after terminal", header + `{"offset_ms":1,"chunk":"data: [DONE]\n\n"}` + "\n" + `{"offset_ms":2,"end":"stopped","termination":"terminal_event"}`, TermTerminalEvent, ""},
		{"crlf records and blank lines", header + "\r\n" + `{"offset_ms":0,"chunk":"data: a\n\n"}` + "\r\n\r\n" + `{"offset_ms":0,"end":"eof"}` + "\r\n", TermEOF, ""},
		{"empty recording", header, TermEOF, "no end record"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := InspectReader(strings.NewReader(tt.in), "rec.jsonl", Expectations{TerminalData: "[DONE]"}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if r.Termination != tt.term {
				t.Fatalf("termination = %s, want %s", r.Termination, tt.term)
			}
			if tt.lim != "" && !strings.Contains(strings.Join(r.Limitations, " "), tt.lim) {
				t.Fatalf("limitations = %v", r.Limitations)
			}
			if tt.term == TermLimitExceeded && findCode(r.Findings, CodeRecordingTruncated) == nil {
				t.Fatalf("findings = %+v", r.Findings)
			}
		})
	}
}

func TestInspectMalformedRecording(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"unknown format", `{"format":"other/v2"}` + "\n", "unsupported recording format"},
		{"header unknown key", `{"format":"backendlab.sse-recording/v1","x":1}` + "\n", "recording header"},
		{"not json", "{oops\n", "recording header"},
		{"decreasing offsets", header + `{"offset_ms":5,"chunk":"a"}` + "\n" + `{"offset_ms":4,"chunk":"b"}`, "line 3: offset_ms 4 is smaller"},
		{"negative offset", header + `{"offset_ms":-1,"chunk":"a"}`, "outside"},
		{"huge offset", header + `{"offset_ms":1e300,"chunk":"a"}`, "outside"},
		{"missing offset", header + `{"chunk":"a"}`, "offset_ms is required"},
		{"two payloads", header + `{"offset_ms":1,"chunk":"a","chunk_b64":"YQ=="}`, "exactly one"},
		{"no payload", header + `{"offset_ms":1}`, "exactly one"},
		{"bad base64", header + `{"offset_ms":1,"chunk_b64":"!!"}`, "chunk_b64"},
		{"unknown field", header + `{"offset_ms":1,"chunk":"a","extra":true}`, "unknown field"},
		{"unknown end", header + `{"offset_ms":1,"end":"done"}`, "unknown end reason"},
		{"termination on eof", header + `{"offset_ms":1,"end":"eof","termination":"timeout"}`, "only allowed"},
		{"unknown termination", header + `{"offset_ms":1,"end":"stopped","termination":"later"}`, "unknown termination"},
		{"data after end", header + `{"offset_ms":1,"end":"eof"}` + "\n" + `{"offset_ms":2,"chunk":"a"}`, "data after the end record"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := InspectReader(strings.NewReader(tt.in), "bad.jsonl", Expectations{}, Options{})
			if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.HasPrefix(err.Error(), "bad.jsonl: ") {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestInspectRecordingLineBound(t *testing.T) {
	big := header + `{"offset_ms":1,"chunk":"` + strings.Repeat("a", maxRecordLine) + `"}`
	_, err := InspectReader(strings.NewReader(big), "big.jsonl", Expectations{}, Options{})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v", err)
	}
}

func TestInspectDetection(t *testing.T) {
	// Leading whitespace before the header is allowed; SSE text that does
	// not start with '{' is never treated as a recording.
	r, err := InspectReader(strings.NewReader("\n  "+header+`{"offset_ms":1,"end":"eof"}`), "r", Expectations{}, Options{})
	if err != nil || r.Format != FormatRecording {
		t.Fatalf("r = %+v, err = %v", r, err)
	}
	r, err = InspectReader(strings.NewReader("data: {\"a\":1}\n\n"), "s", Expectations{}, Options{})
	if err != nil || r.Format != FormatSSE || r.EventCount != 1 {
		t.Fatalf("r = %+v, err = %v", r, err)
	}
	r, err = InspectReader(strings.NewReader(""), "empty", Expectations{}, Options{})
	if err != nil || r.Format != FormatSSE || findCode(r.Findings, CodeNoEvents) == nil {
		t.Fatalf("r = %+v, err = %v", r, err)
	}
}

func TestInspectSSELimit(t *testing.T) {
	in := strings.Repeat("data: x\n\n", 100)
	r, err := InspectReader(strings.NewReader(in), "s", Expectations{}, Options{Limits: limits.Limits{MaxStreamEvents: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Termination != TermLimitExceeded || r.EventCount != 10 || findCode(r.Findings, CodeTooManyEvents) == nil {
		t.Fatalf("result = %+v", r)
	}
}

func TestInspectFileMissing(t *testing.T) {
	if _, err := InspectFile(filepath.Join("testdata", "none.sse"), Expectations{}, Options{}); err == nil {
		t.Fatal("expected an error")
	}
}

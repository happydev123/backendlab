package stream

import (
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/schema"
)

// runChecker passes events with the given types and data through a
// checker and finishes it with term.
func runChecker(t *testing.T, exp Expectations, term Termination, evs ...Event) ([]finding.Finding, *checker) {
	t.Helper()
	s := newSink("c", redact.Default().WithSecrets("hunter2-secret"), 40, 100)
	c := newChecker(exp, s, nil)
	for i := range evs {
		evs[i].Index = i
		if evs[i].Type == "" {
			evs[i].Type = DefaultEventType
		}
		c.event(evs[i])
	}
	c.finish(term, len(evs))
	return s.items(), c
}

func codesOf(fs []finding.Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Code + "@" + f.Location + "/" + string(f.Severity)
	}
	return out
}

func TestCheckerTerminalClassification(t *testing.T) {
	data := func(d string) Event { return Event{Data: d} }
	tests := []struct {
		name string
		exp  Expectations
		term Termination
		evs  []Event
		want string
	}{
		{"no marker configured", Expectations{}, TermEOF, []Event{data("a")}, ""},
		{"terminal data seen", Expectations{TerminalData: "[DONE]"}, TermEOF, []Event{data("a"), data("[DONE]")}, ""},
		{"terminal event seen", Expectations{TerminalEvent: "done"}, TermEOF, []Event{{Type: "done", Data: "x"}}, ""},
		{"eof before marker is info", Expectations{TerminalData: "[DONE]"}, TermEOF, []Event{data("a")},
			"stream.eof_before_terminal@/info"},
		{"eof before required marker fails", Expectations{TerminalData: "[DONE]", RequireTerminal: true}, TermEOF, []Event{data("a")},
			"stream.missing_terminal@/error"},
		{"timeout before required marker is inconclusive", Expectations{TerminalData: "[DONE]", RequireTerminal: true}, TermTimeout, nil,
			"stream.missing_terminal@/warning"},
		{"timeout without require adds nothing", Expectations{TerminalData: "[DONE]"}, TermTimeout, nil, ""},
		{"events after marker", Expectations{TerminalData: "[DONE]"}, TermEOF, []Event{data("[DONE]"), data("x"), data("[DONE]")},
			"stream.event_after_terminal@event:1/warning"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs, _ := runChecker(t, tt.exp, tt.term, tt.evs...)
			got := strings.Join(codesOf(fs), ",")
			if got != tt.want {
				t.Fatalf("findings = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckerRequiredEvents(t *testing.T) {
	exp := Expectations{RequiredEvents: []string{"start", "delta", "start", "end"}}
	fs, _ := runChecker(t, exp, TermEOF, Event{Type: "delta", Data: "x"}, Event{Type: "other", Data: "y"})
	got := strings.Join(codesOf(fs), ",")
	want := "stream.missing_required_event@type:end/error,stream.missing_required_event@type:start/error"
	if got != want {
		t.Fatalf("findings = %q", got)
	}
	if fs[0].Kind != finding.KindAssertionFailure || fs[0].Expected != "end" {
		t.Fatalf("finding = %+v", fs[0])
	}
}

func TestCheckerJSONPayloads(t *testing.T) {
	exp := Expectations{JSONPayloads: true, TerminalData: "[DONE]", TerminalEvent: "end"}
	fs, _ := runChecker(t, exp, TermEOF,
		Event{Data: `{"ok":true}`},
		Event{Data: `{"token":"hunter2-secret", broken`},
		Event{Type: "end", Data: "bye"},
		Event{Data: "[DONE]"},
		Event{Data: `1 2`},
	)
	got := strings.Join(codesOf(fs), ",")
	// The terminal event type and the terminal data payload are exempt;
	// the second [DONE] is not the first marker, but still matches the
	// marker and is exempt from payload checks.
	want := "stream.invalid_json_payload@event:1/error,stream.event_after_terminal@event:3/warning,stream.invalid_json_payload@event:4/error"
	if got != want {
		t.Fatalf("findings = %q", got)
	}
	for _, f := range fs {
		if strings.Contains(f.Observed, "hunter2") || strings.Contains(f.Message, "hunter2") {
			t.Fatalf("secret in finding: %+v", f)
		}
		if f.Code == CodeInvalidJSONPayload && len(f.Observed) > 43 {
			t.Fatalf("preview not truncated: %q", f.Observed)
		}
	}
}

func TestCheckerSchema(t *testing.T) {
	sch, err := schema.CompileFile("testdata/schemas/chunk.json")
	if err != nil {
		t.Fatal(err)
	}
	exp := Expectations{Schema: sch, TerminalData: "[DONE]"}
	fs, _ := runChecker(t, exp, TermEOF,
		Event{Data: `{"seq":0,"text":"a"}`},
		Event{Data: `{"seq":-1,"text":2}`},
		Event{Data: `nope`},
		Event{Data: "[DONE]"},
	)
	got := strings.Join(codesOf(fs), ",")
	want := "stream.schema_violation@event:1/seq/error,stream.schema_violation@event:1/text/error,stream.invalid_json_payload@event:2/error"
	if got != want {
		t.Fatalf("findings = %q", got)
	}
	if fs[0].Details["schema"] != "chunk.json" || fs[0].Details["keyword"] == "" {
		t.Fatalf("details = %v", fs[0].Details)
	}
}

package stream

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/suite"
)

func TestExpectationsFromSpec(t *testing.T) {
	spec := &suite.StreamSpec{
		RequiredEvents:  []string{"start", "delta"},
		TerminalData:    "[DONE]",
		RequireTerminal: true,
		JSONPayloads:    true,
		Schema:          "schemas/chunk.json",
		MaxGap:          suite.Duration(250 * time.Millisecond),
		MaxDuration:     suite.Duration(5 * time.Second),
		Adapter:         "chat-delta",
	}
	exp, err := ExpectationsFromSpec(spec, "testdata")
	if err != nil {
		t.Fatal(err)
	}
	if exp.Schema == nil || exp.Schema.Name() != "chunk.json" {
		t.Fatalf("schema = %v", exp.Schema)
	}
	if exp.MaxGap != 250*time.Millisecond || exp.MaxDuration != 5*time.Second || !exp.RequireTerminal {
		t.Fatalf("exp = %+v", exp)
	}
	// The spec slice is copied, not shared.
	spec.RequiredEvents[0] = "changed"
	if exp.RequiredEvents[0] != "start" {
		t.Fatal("required events alias the spec")
	}
	if v := exp.Schema.Validate(map[string]any{"seq": "x"}); len(v) == 0 {
		t.Fatal("schema did not reject an invalid instance")
	}
}

func TestExpectationsFromSpecNil(t *testing.T) {
	exp, err := ExpectationsFromSpec(nil, "")
	if err != nil || exp.HasTerminal() || exp.Schema != nil {
		t.Fatalf("exp = %+v, err = %v", exp, err)
	}
}

func TestExpectationsFromSpecErrors(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join("testdata", "schemas", "chunk.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ExpectationsFromSpec(&suite.StreamSpec{Schema: abs}, "elsewhere"); err != nil {
		t.Fatalf("absolute schema path rejected: %v", err)
	}
	tests := []struct {
		name string
		spec suite.StreamSpec
		want string
	}{
		{"missing schema", suite.StreamSpec{Schema: "schemas/none.json"}, "stream schema"},
		{"external ref", suite.StreamSpec{Schema: "schemas/broken.json"}, "stream schema"},
		{"unknown adapter", suite.StreamSpec{Adapter: "vendor-x"}, "unknown adapter"},
		{"require terminal without marker", suite.StreamSpec{RequireTerminal: true}, "require_terminal"},
		{"empty required type", suite.StreamSpec{RequiredEvents: []string{" "}}, "must not be empty"},
		{"negative gap", suite.StreamSpec{MaxGap: suite.Duration(-time.Second)}, "negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ExpectationsFromSpec(&tt.spec, "testdata")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestIsTerminal(t *testing.T) {
	exp := Expectations{TerminalEvent: "done", TerminalData: "[DONE]"}
	cases := map[Event]bool{
		{Type: "done", Data: "{}"}:          true,
		{Type: "message", Data: "[DONE]"}:   true,
		{Type: "message", Data: " [DONE]"}:  false,
		{Type: "message", Data: "[DONE]\n"}: false,
		{Type: "Done", Data: "x"}:           false,
	}
	for ev, want := range cases {
		if got := exp.IsTerminal(ev); got != want {
			t.Errorf("IsTerminal(%+v) = %v", ev, got)
		}
	}
	if (Expectations{}).IsTerminal(Event{Type: "message"}) {
		t.Fatal("no marker configured but event matched")
	}
}

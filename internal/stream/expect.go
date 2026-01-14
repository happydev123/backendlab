package stream

import (
	"fmt"
	"strings"
	"time"

	"github.com/happydev123/backendlab/internal/schema"
	"github.com/happydev123/backendlab/internal/suite"
)

// maxRequiredEvents bounds the RequiredEvents list.
const maxRequiredEvents = 100

// Expectations are the checks applied to one stream. The zero value
// checks nothing beyond well-formed framing.
type Expectations struct {
	// RequiredEvents lists event types that must each appear at least once.
	RequiredEvents []string
	// TerminalEvent is an event type that marks application completion.
	TerminalEvent string
	// TerminalData is a data payload that marks completion, compared
	// exactly against the event's data (for example "[DONE]").
	TerminalData string
	// RequireTerminal turns EOF before the terminal marker into a failure.
	RequireTerminal bool
	// JSONPayloads requires every non-terminal event's data to be one
	// JSON value.
	JSONPayloads bool
	// Schema, when set, validates every non-terminal event's data as JSON.
	Schema *schema.Schema
	// MaxGap bounds the time between consecutive events. It can only be
	// checked for live reads and timed recordings.
	MaxGap time.Duration
	// MaxDuration bounds the time from the start of reading to the end of
	// the stream. Live reads and recordings stop once it is exceeded.
	MaxDuration time.Duration
	// Adapter names an optional payload interpretation ("chat-delta").
	Adapter string
}

// HasTerminal reports whether a terminal marker is configured.
func (e Expectations) HasTerminal() bool {
	return e.TerminalEvent != "" || e.TerminalData != ""
}

// IsTerminal reports whether ev matches the configured terminal marker.
func (e Expectations) IsTerminal(ev Event) bool {
	return (e.TerminalEvent != "" && ev.Type == e.TerminalEvent) ||
		(e.TerminalData != "" && ev.Data == e.TerminalData)
}

// Validate checks the expectations themselves.
func (e Expectations) Validate() error {
	if len(e.RequiredEvents) > maxRequiredEvents {
		return fmt.Errorf("stream: at most %d required events", maxRequiredEvents)
	}
	for _, t := range e.RequiredEvents {
		if strings.TrimSpace(t) == "" {
			return fmt.Errorf("stream: required event type must not be empty")
		}
		if strings.ContainsAny(t, "\r\n") {
			return fmt.Errorf("stream: required event type %q contains a line break", t)
		}
	}
	if strings.ContainsAny(e.TerminalEvent, "\r\n") {
		return fmt.Errorf("stream: terminal_event contains a line break")
	}
	if e.RequireTerminal && !e.HasTerminal() {
		return fmt.Errorf("stream: require_terminal needs terminal_event or terminal_data")
	}
	if e.MaxGap < 0 || e.MaxDuration < 0 {
		return fmt.Errorf("stream: max_gap and max_duration must not be negative")
	}
	if e.Adapter != "" && !knownAdapter(e.Adapter) {
		return fmt.Errorf("stream: unknown adapter %q (supported: %s)", e.Adapter, AdapterChatDelta)
	}
	return nil
}

// ExpectationsFromSpec converts a suite stream specification. A schema
// reference is resolved relative to suiteDir and compiled.
func ExpectationsFromSpec(spec *suite.StreamSpec, suiteDir string) (Expectations, error) {
	if spec == nil {
		return Expectations{}, nil
	}
	exp := Expectations{
		RequiredEvents:  append([]string(nil), spec.RequiredEvents...),
		TerminalEvent:   spec.TerminalEvent,
		TerminalData:    spec.TerminalData,
		RequireTerminal: spec.RequireTerminal,
		JSONPayloads:    spec.JSONPayloads,
		MaxGap:          spec.MaxGap.Std(),
		MaxDuration:     spec.MaxDuration.Std(),
		Adapter:         spec.Adapter,
	}
	if spec.Schema != "" {
		path, err := suite.ResolveFile(suiteDir, spec.Schema, false)
		if err != nil {
			return Expectations{}, fmt.Errorf("stream schema: %w", err)
		}
		s, err := schema.CompileFile(path)
		if err != nil {
			return Expectations{}, fmt.Errorf("stream schema: %w", err)
		}
		exp.Schema = s
	}
	if err := exp.Validate(); err != nil {
		return Expectations{}, err
	}
	return exp, nil
}

// Package suite defines the YAML test-suite format: a suite is a list of
// cases, each a single HTTP request with assertions, comparison settings
// and optional captured values for later cases. Suites never contain
// destinations or credentials; those come from target configuration.
package suite

import "strings"

// CurrentVersion is the only suite format version accepted.
const CurrentVersion = 1

// Suite is a collection of cases executed in order.
type Suite struct {
	Version     int      `yaml:"version" json:"version"`
	Name        string   `yaml:"name" json:"name"`
	Description string   `yaml:"description" json:"description,omitempty"`
	Defaults    Defaults `yaml:"defaults" json:"defaults"`
	Cases       []Case   `yaml:"cases" json:"cases"`
}

// Defaults apply to every case unless the case overrides them.
type Defaults struct {
	Timeout Duration          `yaml:"timeout" json:"timeout,omitempty"`
	Headers map[string]string `yaml:"headers" json:"headers,omitempty"`
	Compare CompareSpec       `yaml:"compare" json:"compare"`
}

// Case is one request and its assertions. ID is stable across runs and is
// the key for results, findings and captured values.
type Case struct {
	ID          string            `yaml:"id" json:"id"`
	Description string            `yaml:"description" json:"description,omitempty"`
	Method      string            `yaml:"method" json:"method"`
	Path        string            `yaml:"path" json:"path"`
	PathParams  map[string]string `yaml:"path_params" json:"path_params,omitempty"`
	Query       map[string]string `yaml:"query" json:"query,omitempty"`
	Headers     map[string]string `yaml:"headers" json:"headers,omitempty"`
	Body        *Body             `yaml:"body" json:"body,omitempty"`
	Timeout     Duration          `yaml:"timeout" json:"timeout,omitempty"`
	Expect      Expect            `yaml:"expect" json:"expect"`
	Compare     CompareSpec       `yaml:"compare" json:"compare"`
	Capture     []Capture         `yaml:"capture" json:"capture,omitempty"`
	Stream      *StreamSpec       `yaml:"stream" json:"stream,omitempty"`
}

// Body is a JSON document or a small text payload; exactly one is set.
type Body struct {
	JSON        *JSONValue `yaml:"json" json:"json,omitempty"`
	Text        *string    `yaml:"text" json:"text,omitempty"`
	ContentType string     `yaml:"content_type" json:"content_type,omitempty"`
}

// Expect lists assertions evaluated against one target's response.
type Expect struct {
	// Status lists acceptable status codes; empty means any status.
	Status  StatusList        `yaml:"status" json:"status,omitempty"`
	Headers map[string]string `yaml:"headers" json:"headers,omitempty"`
	JSON    []Assertion       `yaml:"json" json:"json,omitempty"`
	// Schema is a JSON Schema file, relative to the suite file, that the
	// response body must satisfy.
	Schema string `yaml:"schema" json:"schema,omitempty"`
	// Rules is an ExtractCheck rules file, relative to the suite file,
	// applied to the response body.
	Rules string `yaml:"rules" json:"rules,omitempty"`
	// MaxDuration is an explicit performance threshold for one request.
	// Single observations are not statistical evidence; the threshold
	// only fails cases that exceed it.
	MaxDuration Duration `yaml:"max_duration" json:"max_duration,omitempty"`
}

// Assertion checks the value at a JSON Pointer. Exactly one operator
// (equals, exists, type, matches) is set.
type Assertion struct {
	Pointer string     `yaml:"pointer" json:"pointer"`
	Equals  *JSONValue `yaml:"equals" json:"equals,omitempty"`
	Exists  *bool      `yaml:"exists" json:"exists,omitempty"`
	Type    string     `yaml:"type" json:"type,omitempty"`
	Matches string     `yaml:"matches" json:"matches,omitempty"`
}

// CompareSpec configures baseline/candidate comparison for a case.
type CompareSpec struct {
	// Ignore lists JSON Pointer patterns excluded before comparison and
	// reporting (volatile IDs, timestamps). "*" matches one segment.
	Ignore []string `yaml:"ignore" json:"ignore,omitempty"`
	// Headers lists response headers compared by value. Headers are not
	// compared unless named here.
	Headers []string `yaml:"headers" json:"headers,omitempty"`
	// Tolerance applies to every numeric value unless a path tolerance
	// matches.
	Tolerance *Tolerance `yaml:"tolerance" json:"tolerance,omitempty"`
	// PathTolerances override Tolerance for matching pointers.
	PathTolerances []PathTolerance `yaml:"path_tolerances" json:"path_tolerances,omitempty"`
	// Unordered marks arrays whose elements are matched by a key field
	// rather than by position.
	Unordered []UnorderedArray `yaml:"unordered" json:"unordered,omitempty"`
	// SkipStatus disables status code comparison.
	SkipStatus bool `yaml:"skip_status" json:"skip_status,omitempty"`
}

// Tolerance allows numeric values to differ by an absolute amount or by a
// fraction of the baseline value. Values are decimal strings ("0.01").
type Tolerance struct {
	Absolute string `yaml:"absolute" json:"absolute,omitempty"`
	Relative string `yaml:"relative" json:"relative,omitempty"`
}

// PathTolerance is a tolerance scoped to a pointer pattern.
type PathTolerance struct {
	Pointer   string `yaml:"pointer" json:"pointer"`
	Tolerance `yaml:",inline"`
}

// UnorderedArray matches elements of the array at Pointer by the value of
// their Key member.
type UnorderedArray struct {
	Pointer string `yaml:"pointer" json:"pointer"`
	Key     string `yaml:"key" json:"key"`
}

// Capture stores the value at Pointer in the response body under Name for
// substitution into later cases.
type Capture struct {
	Name    string `yaml:"name" json:"name"`
	Pointer string `yaml:"pointer" json:"pointer"`
}

// StreamSpec marks a case whose response is a server-sent event stream
// and lists optional expectations about it.
type StreamSpec struct {
	RequiredEvents []string `yaml:"required_events" json:"required_events,omitempty"`
	// TerminalEvent is an event type that marks application completion.
	TerminalEvent string `yaml:"terminal_event" json:"terminal_event,omitempty"`
	// TerminalData is a data payload that marks completion, such as "[DONE]".
	TerminalData string `yaml:"terminal_data" json:"terminal_data,omitempty"`
	// RequireTerminal makes a stream that ends without the terminal marker
	// a failure. Without it, EOF before the marker is only reported.
	RequireTerminal bool     `yaml:"require_terminal" json:"require_terminal,omitempty"`
	JSONPayloads    bool     `yaml:"json_payloads" json:"json_payloads,omitempty"`
	Schema          string   `yaml:"schema" json:"schema,omitempty"`
	MaxGap          Duration `yaml:"max_gap" json:"max_gap,omitempty"`
	MaxDuration     Duration `yaml:"max_duration" json:"max_duration,omitempty"`
	// Adapter enables an optional payload interpretation ("chat-delta").
	Adapter string `yaml:"adapter" json:"adapter,omitempty"`
}

// ReadOnlyMethods are the methods allowed without --allow-writes.
var ReadOnlyMethods = map[string]bool{"GET": true, "HEAD": true, "OPTIONS": true}

// AllowedMethods are every method a suite may use.
var AllowedMethods = map[string]bool{
	"GET": true, "HEAD": true, "OPTIONS": true,
	"POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

// Mutating reports whether the case uses a method outside the read-only
// set. Such cases require explicit write permission and are never retried.
func (c *Case) Mutating() bool { return !ReadOnlyMethods[strings.ToUpper(c.Method)] }

// Mutating reports whether any case in the suite is mutating.
func (s *Suite) Mutating() bool {
	for i := range s.Cases {
		if s.Cases[i].Mutating() {
			return true
		}
	}
	return false
}

// MutatingCases returns the IDs of mutating cases in suite order.
func (s *Suite) MutatingCases() []string {
	var ids []string
	for i := range s.Cases {
		if s.Cases[i].Mutating() {
			ids = append(ids, s.Cases[i].ID)
		}
	}
	return ids
}

// Case returns the case with the given ID.
func (s *Suite) Case(id string) (*Case, bool) {
	for i := range s.Cases {
		if s.Cases[i].ID == id {
			return &s.Cases[i], true
		}
	}
	return nil, false
}

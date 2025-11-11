package suite

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// JSONValue holds a value written in YAML but interpreted with JSON
// semantics. Numbers keep their literal text as json.Number, so "10.10"
// is not rounded through float64, and only JSON-compatible YAML (no
// anchors with non-string keys, no binary or timestamp tags) is accepted.
type JSONValue struct {
	V any
}

// UnmarshalYAML converts a YAML node into the JSON value model.
func (j *JSONValue) UnmarshalYAML(n *yaml.Node) error {
	v, err := nodeToJSON(n, 0)
	if err != nil {
		return err
	}
	j.V = v
	return nil
}

// MarshalJSON encodes the held value.
func (j JSONValue) MarshalJSON() ([]byte, error) { return json.Marshal(j.V) }

// UnmarshalJSON decodes a value keeping numbers as json.Number.
func (j *JSONValue) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	return dec.Decode(&j.V)
}

const maxValueDepth = 64

func nodeToJSON(n *yaml.Node, depth int) (any, error) {
	if depth > maxValueDepth {
		return nil, fmt.Errorf("line %d: value nested deeper than %d levels", n.Line, maxValueDepth)
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) != 1 {
			return nil, fmt.Errorf("line %d: expected a single value", n.Line)
		}
		return nodeToJSON(n.Content[0], depth)
	case yaml.AliasNode:
		return nil, fmt.Errorf("line %d: YAML aliases are not supported in JSON values", n.Line)
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Kind != yaml.ScalarNode || (k.Tag != "!!str" && k.Tag != "") {
				return nil, fmt.Errorf("line %d: object keys must be strings", k.Line)
			}
			if _, dup := out[k.Value]; dup {
				return nil, fmt.Errorf("line %d: duplicate key %q", k.Line, k.Value)
			}
			cv, err := nodeToJSON(v, depth+1)
			if err != nil {
				return nil, err
			}
			out[k.Value] = cv
		}
		return out, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			cv, err := nodeToJSON(c, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, cv)
		}
		return out, nil
	case yaml.ScalarNode:
		return scalarToJSON(n)
	}
	return nil, fmt.Errorf("line %d: unsupported YAML node", n.Line)
}

func scalarToJSON(n *yaml.Node) (any, error) {
	switch n.ShortTag() {
	case "!!str":
		return n.Value, nil
	case "!!null":
		return nil, nil
	case "!!bool":
		b, err := strconv.ParseBool(strings.ToLower(n.Value))
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid boolean %q", n.Line, n.Value)
		}
		return b, nil
	case "!!int", "!!float":
		num := json.Number(strings.ReplaceAll(n.Value, "_", ""))
		// json.Number accepts only JSON number syntax; this rejects YAML
		// forms such as 0x1F, .inf and .nan.
		if !validJSONNumber(string(num)) {
			return nil, fmt.Errorf("line %d: number %q is not valid JSON; quote it as a string", n.Line, n.Value)
		}
		return num, nil
	}
	return nil, fmt.Errorf("line %d: YAML tag %s is not supported in JSON values", n.Line, n.ShortTag())
}

func validJSONNumber(s string) bool {
	var v any
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return false
	}
	_, ok := v.(json.Number)
	return ok && !dec.More()
}

// Duration is a time.Duration written as a Go duration string ("750ms").
type Duration time.Duration

// UnmarshalYAML parses a duration string. Bare numbers are rejected so a
// unit is always explicit.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode || n.ShortTag() != "!!str" {
		return fmt.Errorf("line %d: duration must be a string with a unit, such as \"5s\"", n.Line)
	}
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q", n.Line, n.Value)
	}
	if v < 0 {
		return fmt.Errorf("line %d: duration %q must not be negative", n.Line, n.Value)
	}
	*d = Duration(v)
	return nil
}

// MarshalJSON writes the duration in Go syntax.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON parses a Go duration string.
func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// Std returns the value as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// StatusList accepts either a single status code or a list.
type StatusList []int

// UnmarshalYAML accepts "200" or "[200, 204]".
func (s *StatusList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		var code int
		if err := n.Decode(&code); err != nil {
			return fmt.Errorf("line %d: status must be an integer", n.Line)
		}
		*s = StatusList{code}
		return nil
	case yaml.SequenceNode:
		var codes []int
		if err := n.Decode(&codes); err != nil {
			return fmt.Errorf("line %d: status list must contain integers", n.Line)
		}
		*s = codes
		return nil
	}
	return fmt.Errorf("line %d: status must be an integer or a list of integers", n.Line)
}

// Contains reports whether code is acceptable. An empty list accepts any.
func (s StatusList) Contains(code int) bool {
	if len(s) == 0 {
		return true
	}
	for _, c := range s {
		if c == code {
			return true
		}
	}
	return false
}

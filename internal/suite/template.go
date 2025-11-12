package suite

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Substitution is deliberately small: "${name}" inserts a value captured by
// an earlier case and "$$" writes a literal "$". There are no expressions,
// defaults, functions or environment lookups. Templates are only expanded
// in these locations:
//
//   - path_params values (then percent-encoded as one path segment)
//   - query values (then form-encoded)
//   - header values (CR and LF are rejected after expansion)
//   - string leaves of a JSON body (then JSON-encoded)
//
// Everything else, including the path itself, method and text bodies, is
// literal.

var varName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// ErrUndefined reports a reference to a variable with no captured value.
var ErrUndefined = errors.New("undefined variable")

// Template is a parsed string with variable references.
type Template struct {
	parts []templatePart
}

type templatePart struct {
	literal string
	ref     string
}

// ParseTemplate parses s.
func ParseTemplate(s string) (Template, error) {
	var t Template
	var lit strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '$' {
			lit.WriteByte(s[i])
			continue
		}
		if i+1 < len(s) && s[i+1] == '$' {
			lit.WriteByte('$')
			i++
			continue
		}
		if i+1 >= len(s) || s[i+1] != '{' {
			return Template{}, fmt.Errorf("template %q: '$' must start ${name} or be written as $$", s)
		}
		end := strings.IndexByte(s[i+2:], '}')
		if end < 0 {
			return Template{}, fmt.Errorf("template %q: unterminated ${", s)
		}
		name := s[i+2 : i+2+end]
		if !varName.MatchString(name) {
			return Template{}, fmt.Errorf("template %q: invalid variable name %q", s, name)
		}
		if lit.Len() > 0 {
			t.parts = append(t.parts, templatePart{literal: lit.String()})
			lit.Reset()
		}
		t.parts = append(t.parts, templatePart{ref: name})
		i += 2 + end
	}
	if lit.Len() > 0 {
		t.parts = append(t.parts, templatePart{literal: lit.String()})
	}
	return t, nil
}

// Refs returns referenced variable names in order of appearance.
func (t Template) Refs() []string {
	var out []string
	for _, p := range t.parts {
		if p.ref != "" {
			out = append(out, p.ref)
		}
	}
	return out
}

// Expand substitutes vars. Every reference must be defined.
func (t Template) Expand(vars map[string]string) (string, error) {
	var b strings.Builder
	for _, p := range t.parts {
		if p.ref == "" {
			b.WriteString(p.literal)
			continue
		}
		v, ok := vars[p.ref]
		if !ok {
			return "", fmt.Errorf("%w %q", ErrUndefined, p.ref)
		}
		b.WriteString(v)
	}
	return b.String(), nil
}

// ExpandString parses and expands s in one step.
func ExpandString(s string, vars map[string]string) (string, error) {
	t, err := ParseTemplate(s)
	if err != nil {
		return "", err
	}
	return t.Expand(vars)
}

// ExpandJSON returns a copy of a decoded JSON value with templates in
// string leaves expanded. Object keys are never expanded.
func ExpandJSON(v any, vars map[string]string) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, child := range t {
			cv, err := ExpandJSON(child, vars)
			if err != nil {
				return nil, err
			}
			out[k] = cv
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, child := range t {
			cv, err := ExpandJSON(child, vars)
			if err != nil {
				return nil, err
			}
			out[i] = cv
		}
		return out, nil
	case string:
		return ExpandString(t, vars)
	}
	return v, nil
}

// jsonRefs collects variable references from string leaves of v.
func jsonRefs(v any) ([]string, error) {
	var refs []string
	var walk func(any) error
	walk = func(v any) error {
		switch t := v.(type) {
		case map[string]any:
			for _, child := range t {
				if err := walk(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range t {
				if err := walk(child); err != nil {
					return err
				}
			}
		case string:
			tpl, err := ParseTemplate(t)
			if err != nil {
				return err
			}
			refs = append(refs, tpl.Refs()...)
		}
		return nil
	}
	err := walk(v)
	return refs, err
}

// Refs returns every variable the case references, with the location of
// each reference, for validation and documentation.
func (c *Case) Refs() (map[string][]string, error) {
	out := map[string][]string{}
	add := func(loc, s string) error {
		t, err := ParseTemplate(s)
		if err != nil {
			return fmt.Errorf("%s: %w", loc, err)
		}
		for _, r := range t.Refs() {
			out[r] = append(out[r], loc)
		}
		return nil
	}
	for _, k := range sortedKeys(c.PathParams) {
		if err := add("path_params."+k, c.PathParams[k]); err != nil {
			return nil, err
		}
	}
	for _, k := range sortedKeys(c.Query) {
		if err := add("query."+k, c.Query[k]); err != nil {
			return nil, err
		}
	}
	for _, k := range sortedKeys(c.Headers) {
		if err := add("headers."+k, c.Headers[k]); err != nil {
			return nil, err
		}
	}
	if c.Body != nil && c.Body.JSON != nil {
		refs, err := jsonRefs(c.Body.JSON.V)
		if err != nil {
			return nil, fmt.Errorf("body.json: %w", err)
		}
		for _, r := range refs {
			out[r] = append(out[r], "body.json")
		}
	}
	return out, nil
}

// sortedKeys returns the keys of m in sorted order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

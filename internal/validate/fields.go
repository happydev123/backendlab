package validate

import (
	"errors"
	"fmt"
	"sort"

	"github.com/happydev123/backendlab/internal/jsonptr"
)

// fieldRef is a JSON Pointer to exactly one field. Arithmetic and ordering
// rules use it: their operands must be unambiguous.
type fieldRef struct {
	ptr  jsonptr.Pointer
	text string
}

// parseFieldRef parses a single-field pointer. name identifies the rules
// file key in errors. The root pointer and "*" segments are rejected.
func parseFieldRef(name, s string) (fieldRef, error) {
	if s == "" {
		return fieldRef{}, fmt.Errorf("%s is required", name)
	}
	pt, err := jsonptr.ParsePattern(s)
	if err != nil {
		return fieldRef{}, fmt.Errorf("%s: %v", name, err)
	}
	p, err := jsonptr.Parse(s)
	if err != nil {
		return fieldRef{}, fmt.Errorf("%s: %v", name, err)
	}
	for _, seg := range p.Segments() {
		if seg == "*" {
			return fieldRef{}, fmt.Errorf("%s %q: wildcards are not allowed here", name, s)
		}
	}
	return fieldRef{ptr: p, text: pt.String()}, nil
}

// lookup resolves the field. found is false for absent fields and for
// segments that cannot apply (such as a key on a string).
func (f fieldRef) lookup(doc any) (value any, found bool) {
	v, ok, err := f.ptr.Get(doc)
	if err != nil || !ok {
		return nil, false
	}
	return v, true
}

// fieldPattern is a JSON Pointer whose segments may be "*", matching any
// one object key or array index ("/line_items/*/amount").
type fieldPattern struct {
	segs []string
	wild []bool
	text string
}

// match is one field selected by a pattern.
type match struct {
	ptr   jsonptr.Pointer
	value any
}

// parseFieldPattern parses a pointer pattern. The root pointer is rejected
// because rules always address fields inside a document.
func parseFieldPattern(name, s string) (fieldPattern, error) {
	if s == "" {
		return fieldPattern{}, fmt.Errorf("%s: empty field pointer (the document root cannot be a field)", name)
	}
	pt, err := jsonptr.ParsePattern(s)
	if err != nil {
		return fieldPattern{}, fmt.Errorf("%s: %v", name, err)
	}
	p, err := jsonptr.Parse(s)
	if err != nil {
		return fieldPattern{}, fmt.Errorf("%s: %v", name, err)
	}
	segs := p.Segments()
	fp := fieldPattern{segs: segs, wild: make([]bool, len(segs)), text: pt.String()}
	for i, seg := range segs {
		fp.wild[i] = seg == "*"
	}
	return fp, nil
}

// parseFieldPatterns parses a non-empty, bounded list of patterns.
func parseFieldPatterns(name string, ss []string) ([]fieldPattern, error) {
	if len(ss) == 0 {
		return nil, fmt.Errorf("%s must list at least one field", name)
	}
	if len(ss) > MaxFieldsPerRule {
		return nil, fmt.Errorf("%s lists more than %d fields", name, MaxFieldsPerRule)
	}
	out := make([]fieldPattern, 0, len(ss))
	seen := map[string]bool{}
	for i, s := range ss {
		fp, err := parseFieldPattern(fmt.Sprintf("%s[%d]", name, i), s)
		if err != nil {
			return nil, err
		}
		if seen[fp.text] {
			return nil, fmt.Errorf("%s lists %q twice", name, fp.text)
		}
		seen[fp.text] = true
		out = append(out, fp)
	}
	return out, nil
}

// hasWildcard reports whether any segment is "*".
func (p fieldPattern) hasWildcard() bool {
	for _, w := range p.wild {
		if w {
			return true
		}
	}
	return false
}

// split returns the pattern of the parent container and the final member
// name. The final segment must be literal.
func (p fieldPattern) split() (parent fieldPattern, member string, err error) {
	n := len(p.segs)
	if p.wild[n-1] {
		return fieldPattern{}, "", errors.New("the last segment must not be a wildcard")
	}
	parent = fieldPattern{segs: p.segs[:n-1], wild: p.wild[:n-1]}
	return parent, p.segs[n-1], nil
}

// errTooManyMatches reports a pattern that matched more than MaxMatches
// values; the caller turns it into an inconclusive finding.
var errTooManyMatches = fmt.Errorf("pattern matched more than %d values", MaxMatches)

// expand returns every value the pattern selects in doc, in document order
// (array indexes ascending, object keys sorted). Absent paths select
// nothing.
func (p fieldPattern) expand(doc any) ([]match, error) {
	var out []match
	var tooMany bool
	var walk func(cur any, at jsonptr.Pointer, i int)
	walk = func(cur any, at jsonptr.Pointer, i int) {
		if tooMany {
			return
		}
		if i == len(p.segs) {
			if len(out) >= MaxMatches {
				tooMany = true
				return
			}
			out = append(out, match{ptr: at, value: cur})
			return
		}
		if !p.wild[i] {
			next, found, err := jsonptr.Pointer{}.Append(p.segs[i]).Get(cur)
			if err != nil || !found {
				return
			}
			walk(next, at.Append(p.segs[i]), i+1)
			return
		}
		switch v := cur.(type) {
		case map[string]any:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(v[k], at.Append(k), i+1)
			}
		case []any:
			for idx, child := range v {
				walk(child, at.AppendIndex(idx), i+1)
			}
		}
	}
	walk(doc, jsonptr.Pointer{}, 0)
	if tooMany {
		return nil, errTooManyMatches
	}
	return out, nil
}

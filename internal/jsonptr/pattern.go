package jsonptr

import (
	"fmt"
	"strings"
)

// Pattern is a JSON Pointer in which a segment may be the single character
// "*", matching any one object key or array index. "**" is not supported;
// patterns stay explicit so that exclusions and redactions are reviewable.
type Pattern struct {
	segments []string
	wild     []bool
}

// ParsePattern parses a pointer pattern such as "/items/*/id".
func ParsePattern(s string) (Pattern, error) {
	if s == "" {
		return Pattern{}, nil
	}
	if s[0] != '/' {
		return Pattern{}, fmt.Errorf("%w %q: must start with '/'", ErrSyntax, s)
	}
	raw := strings.Split(s[1:], "/")
	if len(raw) > MaxSegments {
		return Pattern{}, fmt.Errorf("%w %q: more than %d segments", ErrSyntax, s, MaxSegments)
	}
	pt := Pattern{segments: make([]string, len(raw)), wild: make([]bool, len(raw))}
	for i, r := range raw {
		if r == "*" {
			pt.wild[i] = true
			continue
		}
		if r == "**" {
			return Pattern{}, fmt.Errorf("%w %q: '**' is not supported", ErrSyntax, s)
		}
		seg, err := unescape(r)
		if err != nil {
			return Pattern{}, fmt.Errorf("%w %q: %v", ErrSyntax, s, err)
		}
		pt.segments[i] = seg
	}
	return pt, nil
}

// Match reports whether p matches pattern pt exactly (same depth).
func (pt Pattern) Match(p Pointer) bool {
	if len(pt.segments) != len(p.segments) {
		return false
	}
	return pt.matchPrefix(p)
}

// MatchPrefix reports whether pt matches p or an ancestor of p. Excluding
// "/meta" therefore also excludes "/meta/request_id".
func (pt Pattern) MatchPrefix(p Pointer) bool {
	if len(pt.segments) > len(p.segments) {
		return false
	}
	return pt.matchPrefix(p)
}

func (pt Pattern) matchPrefix(p Pointer) bool {
	for i := range pt.segments {
		if pt.wild[i] {
			continue
		}
		if pt.segments[i] != p.segments[i] {
			return false
		}
	}
	return true
}

// String formats the pattern.
func (pt Pattern) String() string {
	var b strings.Builder
	for i, s := range pt.segments {
		b.WriteByte('/')
		if pt.wild[i] {
			b.WriteByte('*')
			continue
		}
		b.WriteString(Escape(s))
	}
	return b.String()
}

// PatternSet is an ordered list of patterns.
type PatternSet []Pattern

// ParsePatterns parses every entry of ss.
func ParsePatterns(ss []string) (PatternSet, error) {
	out := make(PatternSet, 0, len(ss))
	for _, s := range ss {
		p, err := ParsePattern(s)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// MatchPrefix reports whether any pattern matches p or an ancestor of p.
func (ps PatternSet) MatchPrefix(p Pointer) bool {
	for _, pt := range ps {
		if pt.MatchPrefix(p) {
			return true
		}
	}
	return false
}

// Match reports whether any pattern matches p exactly.
func (ps PatternSet) Match(p Pointer) bool {
	for _, pt := range ps {
		if pt.Match(p) {
			return true
		}
	}
	return false
}

package validate

import (
	"fmt"
	"regexp"
)

// allowedValuesRule checks that a string field holds one of a fixed set of
// values (for example ISO 4217 currency codes). Comparison is exact and
// case-sensitive. Absent and null fields are skipped; use a required rule
// to demand presence.
type allowedValuesRule struct {
	ruleBase
	field  fieldPattern
	values map[string]bool
	list   []string
}

func compileAllowedValues(b ruleBase, s ruleSpec) (*allowedValuesRule, error) {
	field, err := parseFieldPattern("field", s.Field)
	if err != nil {
		return nil, err
	}
	if len(s.Values) == 0 {
		return nil, fmt.Errorf("values must list at least one value")
	}
	if len(s.Values) > MaxAllowedValues {
		return nil, fmt.Errorf("values lists more than %d entries", MaxAllowedValues)
	}
	set := make(map[string]bool, len(s.Values))
	for _, v := range s.Values {
		if set[v] {
			return nil, fmt.Errorf("values lists %q twice", v)
		}
		set[v] = true
	}
	return &allowedValuesRule{ruleBase: b, field: field, values: set, list: append([]string(nil), s.Values...)}, nil
}

func (r *allowedValuesRule) checkDocument(c *docCheck) {
	expected := "one of " + c.san.list(r.list)
	r.eachMatch(c, r.field, func(m match) {
		switch v := m.value.(type) {
		case nil:
		case string:
			if !r.values[v] {
				c.emit(r.failure(m.ptr, expected, c.san.value(m.ptr, v),
					fmt.Sprintf("%s holds a value outside the allowed set (%s)", m.ptr, r.name())))
			}
		default:
			c.emit(r.failure(m.ptr, expected, c.san.value(m.ptr, v),
				fmt.Sprintf("%s is %s, allowed values are strings (%s)", m.ptr, aType(v), r.name())))
		}
	})
}

// patternRule checks a string field against an RE2 regular expression.
// Go's regexp package guarantees linear-time matching, so a hostile
// document cannot cause catastrophic backtracking. Matching is unanchored
// unless the pattern uses ^ and $.
type patternRule struct {
	ruleBase
	field fieldPattern
	re    *regexp.Regexp
}

func compilePattern(b ruleBase, s ruleSpec) (*patternRule, error) {
	field, err := parseFieldPattern("field", s.Field)
	if err != nil {
		return nil, err
	}
	if s.Pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	if len(s.Pattern) > MaxPatternLength {
		return nil, fmt.Errorf("pattern is longer than %d bytes", MaxPatternLength)
	}
	re, err := regexp.Compile(s.Pattern)
	if err != nil {
		return nil, fmt.Errorf("pattern: %v", err)
	}
	return &patternRule{ruleBase: b, field: field, re: re}, nil
}

func (r *patternRule) checkDocument(c *docCheck) {
	expected := "string matching " + c.san.text(r.re.String())
	r.eachMatch(c, r.field, func(m match) {
		switch v := m.value.(type) {
		case nil:
		case string:
			if !r.re.MatchString(v) {
				c.emit(r.failure(m.ptr, expected, c.san.value(m.ptr, v),
					fmt.Sprintf("%s does not match the pattern (%s)", m.ptr, r.name())))
			}
		default:
			c.emit(r.failure(m.ptr, expected, c.san.value(m.ptr, v),
				fmt.Sprintf("%s is %s, the pattern applies to strings (%s)", m.ptr, aType(v), r.name())))
		}
	})
}

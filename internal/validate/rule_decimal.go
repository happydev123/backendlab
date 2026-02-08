package validate

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

// decimalRule checks the representation and range of decimal fields:
// which JSON forms are accepted (numbers, decimal strings or both), the
// largest number of fractional digits, and optional inclusive bounds.
// Absent and null fields are skipped; use a required rule to demand
// presence.
type decimalRule struct {
	ruleBase
	fields   []fieldPattern
	accept   acceptSet
	maxScale int // -1 when unbounded
	min, max *decimal.Decimal
	expected string
}

func compileDecimal(b ruleBase, s ruleSpec) (*decimalRule, error) {
	fields, err := parseFieldPatterns("fields", s.Fields)
	if err != nil {
		return nil, err
	}
	r := &decimalRule{ruleBase: b, fields: fields, maxScale: -1}
	if r.accept, err = compileAccept(s.Accept); err != nil {
		return nil, err
	}
	if s.MaxScale != nil {
		if *s.MaxScale < 0 || *s.MaxScale > MaxDecimalExponent {
			return nil, fmt.Errorf("max_scale must be between 0 and %d", MaxDecimalExponent)
		}
		r.maxScale = *s.MaxScale
	}
	if s.Min != nil {
		d, err := parseRuleDecimal("min", *s.Min)
		if err != nil {
			return nil, err
		}
		r.min = &d
	}
	if s.Max != nil {
		d, err := parseRuleDecimal("max", *s.Max)
		if err != nil {
			return nil, err
		}
		r.max = &d
	}
	if r.min != nil && r.max != nil && r.min.GreaterThan(*r.max) {
		return nil, fmt.Errorf("min %s is greater than max %s", *s.Min, *s.Max)
	}
	parts := []string{r.accept.describe()}
	if r.maxScale >= 0 {
		parts = append(parts, fmt.Sprintf("at most %d decimal places", r.maxScale))
	}
	if r.min != nil {
		parts = append(parts, ">= "+*s.Min)
	}
	if r.max != nil {
		parts = append(parts, "<= "+*s.Max)
	}
	r.expected = strings.Join(parts, ", ")
	return r, nil
}

// compileAccept parses an accept list. Empty means both forms.
func compileAccept(list []string) (acceptSet, error) {
	if len(list) == 0 {
		return acceptBoth, nil
	}
	var a acceptSet
	for _, s := range list {
		switch s {
		case "number":
			if a.number {
				return acceptSet{}, fmt.Errorf("accept lists %q twice", s)
			}
			a.number = true
		case "string":
			if a.str {
				return acceptSet{}, fmt.Errorf("accept lists %q twice", s)
			}
			a.str = true
		default:
			return acceptSet{}, fmt.Errorf("unknown accept value %q (want number or string)", s)
		}
	}
	return a, nil
}

func (r *decimalRule) checkDocument(c *docCheck) {
	for _, f := range r.fields {
		r.eachMatch(c, f, func(m match) {
			if m.value == nil {
				return
			}
			d, _, err := decimalValue(m.value, r.accept)
			if err != nil {
				c.emit(r.failure(m.ptr, r.expected, c.san.value(m.ptr, m.value),
					fmt.Sprintf("%s is not a %s: %s (%s)", m.ptr, r.accept.describe(), describeDecimalError(c, m, err), r.name())))
				return
			}
			var problems []string
			if r.maxScale >= 0 && scaleOf(d) > r.maxScale {
				problems = append(problems, fmt.Sprintf("has %d decimal places, at most %d allowed", scaleOf(d), r.maxScale))
			}
			if r.min != nil && d.LessThan(*r.min) {
				problems = append(problems, "is below the minimum")
			}
			if r.max != nil && d.GreaterThan(*r.max) {
				problems = append(problems, "is above the maximum")
			}
			if len(problems) > 0 {
				c.emit(r.failure(m.ptr, r.expected, c.san.value(m.ptr, m.value),
					fmt.Sprintf("%s %s (%s)", m.ptr, strings.Join(problems, " and "), r.name())))
			}
		})
	}
}

// describeDecimalError returns the parse error text, or a neutral
// description when the value itself must stay hidden (parse errors quote
// the offending input).
func describeDecimalError(c *docCheck, m match, err error) string {
	if c.san.redacted(m.ptr) {
		return "value cannot be parsed"
	}
	return c.san.text(err.Error())
}

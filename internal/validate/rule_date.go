package validate

import (
	"fmt"
	"strings"
	"time"

	"github.com/happydev123/backendlab/internal/jsonptr"
)

// dateRule checks that string fields hold dates in a layout. Absent and
// null fields are skipped; use a required rule to demand presence.
type dateRule struct {
	ruleBase
	fields []fieldPattern
	layout dateLayout
}

func compileDate(b ruleBase, s ruleSpec) (*dateRule, error) {
	fields, err := parseFieldPatterns("fields", s.Fields)
	if err != nil {
		return nil, err
	}
	layout, err := compileLayout(s.Layout)
	if err != nil {
		return nil, err
	}
	return &dateRule{ruleBase: b, fields: fields, layout: layout}, nil
}

func (r *dateRule) checkDocument(c *docCheck) {
	expected := "date in layout " + r.layout.describe()
	for _, f := range r.fields {
		r.eachMatch(c, f, func(m match) {
			if m.value == nil {
				return
			}
			if _, why := dateAt(r.layout, m.value); why != "" {
				c.emit(r.failure(m.ptr, expected, c.san.value(m.ptr, m.value),
					fmt.Sprintf("%s %s (%s)", m.ptr, why, r.name())))
			}
		})
	}
}

// dateAt parses a decoded value as a date. why is empty on success and
// otherwise explains the problem without quoting the value.
func dateAt(l dateLayout, v any) (time.Time, string) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, fmt.Sprintf("is %s, dates are strings", aType(v))
	}
	t, err := l.parse(s)
	if err != nil {
		return time.Time{}, err.Error()
	}
	return t, ""
}

// dateOrderRule checks that one date is before another, for example
// issue_date before due_date. With allow_equal the dates may be equal. An
// absent or null date is inconclusive; a value that is not a date in the
// layout is an assertion failure.
type dateOrderRule struct {
	ruleBase
	before, after fieldRef
	layout        dateLayout
	allowEqual    bool
	expected      string
}

func compileDateOrder(b ruleBase, s ruleSpec) (*dateOrderRule, error) {
	r := &dateOrderRule{ruleBase: b, allowEqual: s.AllowEqual}
	var err error
	if r.before, err = parseFieldRef("before", s.Before); err != nil {
		return nil, err
	}
	if r.after, err = parseFieldRef("after", s.After); err != nil {
		return nil, err
	}
	if r.before.text == r.after.text {
		return nil, fmt.Errorf("before and after must be different fields")
	}
	if r.layout, err = compileLayout(s.Layout); err != nil {
		return nil, err
	}
	op := "<"
	if r.allowEqual {
		op = "<="
	}
	r.expected = fmt.Sprintf("%s %s %s (layout %s)", r.before.text, op, r.after.text, r.layout.describe())
	return r, nil
}

func (r *dateOrderRule) checkDocument(c *docCheck) {
	bv, bok := r.before.lookup(c.doc)
	av, aok := r.after.lookup(c.doc)
	var missing []string
	if !bok || bv == nil {
		missing = append(missing, r.before.text)
	}
	if !aok || av == nil {
		missing = append(missing, r.after.text)
	}
	if len(missing) > 0 {
		loc, _ := jsonptr.Parse(missing[0])
		c.emit(r.inconclusive(CodeInputMissing, loc, r.expected,
			fmt.Sprintf("%s cannot be evaluated: %s absent or null", r.name(), strings.Join(missing, ", "))))
		return
	}
	bt, bwhy := dateAt(r.layout, bv)
	if bwhy != "" {
		c.emit(r.failure(r.before.ptr, r.expected, c.san.value(r.before.ptr, bv),
			fmt.Sprintf("%s cannot be evaluated: %s %s", r.name(), r.before.text, bwhy)))
		return
	}
	at, awhy := dateAt(r.layout, av)
	if awhy != "" {
		c.emit(r.failure(r.after.ptr, r.expected, c.san.value(r.after.ptr, av),
			fmt.Sprintf("%s cannot be evaluated: %s %s", r.name(), r.after.text, awhy)))
		return
	}
	if bt.Before(at) || (r.allowEqual && bt.Equal(at)) {
		return
	}
	relation := "is after"
	if bt.Equal(at) {
		relation = "equals"
	}
	observed := fmt.Sprintf("%s %s, %s %s", r.before.text, c.san.value(r.before.ptr, bv), r.after.text, c.san.value(r.after.ptr, av))
	c.emit(r.failure(r.after.ptr, r.expected, observed,
		fmt.Sprintf("%s: %s %s %s", r.name(), r.before.text, relation, r.after.text)))
}

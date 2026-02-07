package validate

import (
	"fmt"

	"github.com/happydev123/backendlab/internal/jsonptr"
)

// requiredRule checks that fields are present. A field holding JSON null
// counts as present-but-null: it fails unless allow_null is true. A
// wildcard pattern such as "/line_items/*/amount" requires the member in
// every element that exists; it does not require the array itself, so list
// "/line_items" separately when the array must be present.
type requiredRule struct {
	ruleBase
	fields    []fieldPattern
	allowNull bool
}

func compileRequired(b ruleBase, s ruleSpec) (*requiredRule, error) {
	fields, err := parseFieldPatterns("fields", s.Fields)
	if err != nil {
		return nil, err
	}
	for _, f := range fields {
		if f.hasWildcard() {
			if _, _, err := f.split(); err != nil {
				return nil, fmt.Errorf("fields: %q: %v", f.text, err)
			}
		}
	}
	return &requiredRule{ruleBase: b, fields: fields, allowNull: s.AllowNull}, nil
}

func (r *requiredRule) expected() string {
	if r.allowNull {
		return "field present (null allowed)"
	}
	return "field present and not null"
}

func (r *requiredRule) checkDocument(c *docCheck) {
	for _, f := range r.fields {
		if !f.hasWildcard() {
			p, _ := jsonptr.Parse(f.text)
			v, found, err := p.Get(c.doc)
			r.report(c, p, v, found && err == nil)
			continue
		}
		parent, member, _ := f.split()
		r.eachMatch(c, parent, func(m match) {
			p := m.ptr.Append(member)
			v, found, err := jsonptr.Pointer{}.Append(member).Get(m.value)
			r.report(c, p, v, found && err == nil)
		})
	}
}

func (r *requiredRule) report(c *docCheck, p jsonptr.Pointer, v any, found bool) {
	switch {
	case !found:
		c.emit(r.failure(p, r.expected(), "", fmt.Sprintf("required field %s is absent (%s)", p, r.name())))
	case v == nil && !r.allowNull:
		c.emit(r.failure(p, r.expected(), "null", fmt.Sprintf("required field %s is null (%s; set allow_null: true to accept null)", p, r.name())))
	}
}

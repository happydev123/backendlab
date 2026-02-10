package validate

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

// sumRule checks that a signed sum of fields equals another field:
//
//	terms: [{field: /subtotal}, {field: /tax}, {field: /discount, sign: -1, optional: true}]
//	equals: /total
//
// describes subtotal + tax - discount = total without fixing any one
// invoice formula in code. An optional term that is absent or null
// contributes nothing; any other absent or null input makes the rule
// inconclusive (validate.input_missing). The computed sum is rounded only
// when round is configured, and compared with tolerance 0 (exact decimal
// equality) unless tolerance is set.
type sumRule struct {
	ruleBase
	terms    []sumTerm
	equals   fieldRef
	tol      tolerance
	rnd      rounding
	expected string
}

type sumTerm struct {
	ref      fieldRef
	negative bool
	optional bool
}

func compileSum(b ruleBase, s ruleSpec) (*sumRule, error) {
	if len(s.Terms) == 0 {
		return nil, fmt.Errorf("terms must list at least one term")
	}
	if len(s.Terms) > MaxFieldsPerRule {
		return nil, fmt.Errorf("terms lists more than %d entries", MaxFieldsPerRule)
	}
	r := &sumRule{ruleBase: b}
	seen := map[string]bool{}
	for i, ts := range s.Terms {
		ref, err := parseFieldRef(fmt.Sprintf("terms[%d].field", i), ts.Field)
		if err != nil {
			return nil, err
		}
		if seen[ref.text] {
			return nil, fmt.Errorf("terms lists %q twice", ref.text)
		}
		seen[ref.text] = true
		t := sumTerm{ref: ref, optional: ts.Optional}
		if ts.Sign != nil {
			switch *ts.Sign {
			case 1:
			case -1:
				t.negative = true
			default:
				return nil, fmt.Errorf("terms[%d].sign must be 1 or -1 (got %d)", i, *ts.Sign)
			}
		}
		r.terms = append(r.terms, t)
	}
	var err error
	if r.equals, err = parseFieldRef("equals", s.Equals); err != nil {
		return nil, err
	}
	if seen[r.equals.text] {
		return nil, fmt.Errorf("equals %q is also a term", r.equals.text)
	}
	if r.tol, err = compileTolerance(s.Tolerance); err != nil {
		return nil, err
	}
	if r.rnd, err = compileRounding(s.Round); err != nil {
		return nil, err
	}
	r.expected = fmt.Sprintf("%s = %s %s", r.formula(), r.equals.text, comparison(r.rnd, r.tol))
	return r, nil
}

// formula renders the signed terms: "/subtotal + /tax - /discount (optional)".
func (r *sumRule) formula() string {
	var b strings.Builder
	for i, t := range r.terms {
		switch {
		case i == 0 && t.negative:
			b.WriteString("-")
		case i > 0 && t.negative:
			b.WriteString(" - ")
		case i > 0:
			b.WriteString(" + ")
		}
		b.WriteString(t.ref.text)
		if t.optional {
			b.WriteString(" (optional)")
		}
	}
	return b.String()
}

func (r *sumRule) checkDocument(c *docCheck) {
	in := &inputs{}
	sum := decimal.Zero
	var parts []string
	for _, t := range r.terms {
		op := resolve(c.doc, t.ref.ptr)
		if op.state == opMissing && t.optional {
			continue
		}
		if !in.add(c, op) {
			continue
		}
		sign := "+"
		if t.negative {
			sum = sum.Sub(op.value)
			sign = "-"
		} else {
			sum = sum.Add(op.value)
		}
		part := fmt.Sprintf("%s (%s)", op.ptr, shown(c, op))
		switch {
		case len(parts) == 0 && sign == "-":
			part = "-" + part
		case len(parts) > 0:
			part = sign + " " + part
		}
		parts = append(parts, part)
	}
	stated := resolve(c.doc, r.equals.ptr)
	in.add(c, stated)
	if !r.settle(c, in, r.expected) {
		return
	}
	derivation := strings.Join(parts, " ")
	if derivation == "" {
		derivation = "no present terms"
	}
	r.judge(c, in, outcome{
		computed: sum, derivation: derivation, stated: stated,
		rnd: r.rnd, tol: r.tol, expected: r.expected, location: r.equals.ptr,
	})
}

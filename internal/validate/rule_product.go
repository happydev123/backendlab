package validate

import (
	"fmt"
	"strings"
)

// MaxFactors bounds the factors of a product rule. Each factor may carry
// MaxDecimalDigits digits, so the bound also bounds the product's size.
const MaxFactors = 8

// productRule checks that the product of factors equals a stated field,
// for example subtotal * tax_rate = tax. Products usually carry more
// fractional digits than the stated value, so rules normally configure
// round (and sometimes a tolerance). Absent or null factors are
// inconclusive.
type productRule struct {
	ruleBase
	factors  []fieldRef
	equals   fieldRef
	tol      tolerance
	rnd      rounding
	expected string
}

func compileProduct(b ruleBase, s ruleSpec) (*productRule, error) {
	if len(s.Factors) < 2 {
		return nil, fmt.Errorf("factors must list at least two fields")
	}
	if len(s.Factors) > MaxFactors {
		return nil, fmt.Errorf("factors lists more than %d fields", MaxFactors)
	}
	r := &productRule{ruleBase: b}
	names := make([]string, 0, len(s.Factors))
	for i, f := range s.Factors {
		ref, err := parseFieldRef(fmt.Sprintf("factors[%d]", i), f)
		if err != nil {
			return nil, err
		}
		r.factors = append(r.factors, ref)
		names = append(names, ref.text)
	}
	var err error
	if r.equals, err = parseFieldRef("equals", s.Equals); err != nil {
		return nil, err
	}
	for _, f := range r.factors {
		if f.text == r.equals.text {
			return nil, fmt.Errorf("equals %q is also a factor", r.equals.text)
		}
	}
	if r.tol, err = compileTolerance(s.Tolerance); err != nil {
		return nil, err
	}
	if r.rnd, err = compileRounding(s.Round); err != nil {
		return nil, err
	}
	r.expected = fmt.Sprintf("%s = %s %s", strings.Join(names, " * "), r.equals.text, comparison(r.rnd, r.tol))
	return r, nil
}

func (r *productRule) checkDocument(c *docCheck) {
	in := &inputs{}
	ops := make([]operand, 0, len(r.factors))
	for _, f := range r.factors {
		op := resolve(c.doc, f.ptr)
		if in.add(c, op) {
			ops = append(ops, op)
		}
	}
	stated := resolve(c.doc, r.equals.ptr)
	in.add(c, stated)
	if !r.settle(c, in, r.expected) {
		return
	}
	product := ops[0].value
	parts := []string{fmt.Sprintf("%s (%s)", ops[0].ptr, shown(c, ops[0]))}
	for _, op := range ops[1:] {
		product = product.Mul(op.value)
		parts = append(parts, fmt.Sprintf("%s (%s)", op.ptr, shown(c, op)))
	}
	r.judge(c, in, outcome{
		computed: product, derivation: strings.Join(parts, " * "), stated: stated,
		rnd: r.rnd, tol: r.tol, expected: r.expected, location: r.equals.ptr,
	})
}

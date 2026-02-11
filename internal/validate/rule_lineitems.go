package validate

import (
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/happydev123/backendlab/internal/jsonptr"
)

// lineItemsSumRule checks that the amounts of all elements of an array add
// up to a stated field, for example sum(line_items[*].amount) = subtotal.
// An empty array sums to 0. An element without the amount member (or with
// null) makes the rule inconclusive.
type lineItemsSumRule struct {
	ruleBase
	items    fieldRef
	amount   string
	equals   fieldRef
	tol      tolerance
	rnd      rounding
	expected string
}

func compileLineItemsSum(b ruleBase, s ruleSpec) (*lineItemsSumRule, error) {
	r := &lineItemsSumRule{ruleBase: b}
	var err error
	if r.items, err = parseFieldRef("items", s.Items); err != nil {
		return nil, err
	}
	if r.amount, err = memberName("amount", s.Amount); err != nil {
		return nil, err
	}
	if r.equals, err = parseFieldRef("equals", s.Equals); err != nil {
		return nil, err
	}
	if r.tol, err = compileTolerance(s.Tolerance); err != nil {
		return nil, err
	}
	if r.rnd, err = compileRounding(s.Round); err != nil {
		return nil, err
	}
	r.expected = fmt.Sprintf("sum of %s/*/%s = %s %s", r.items.text, jsonptr.Escape(r.amount), r.equals.text, comparison(r.rnd, r.tol))
	return r, nil
}

func (r *lineItemsSumRule) checkDocument(c *docCheck) {
	arr, ok := itemsArray(c, r.ruleBase, r.items, r.expected)
	if !ok {
		return
	}
	in := &inputs{}
	sum := decimal.Zero
	for i := range arr {
		op := resolve(c.doc, r.items.ptr.AppendIndex(i).Append(r.amount))
		if in.add(c, op) {
			sum = sum.Add(op.value)
		}
	}
	stated := resolve(c.doc, r.equals.ptr)
	in.add(c, stated)
	if !r.settle(c, in, r.expected) {
		return
	}
	r.judge(c, in, outcome{
		computed:   sum,
		derivation: fmt.Sprintf("the %s of %d elements of %s", r.amount, len(arr), r.items.text),
		stated:     stated, rnd: r.rnd, tol: r.tol, expected: r.expected, location: r.equals.ptr,
	})
}

// lineItemProductRule checks quantity * unit_price = amount for every
// element of an array, with optional rounding of the product and a
// tolerance. Each element is judged on its own; an element missing one of
// the three members is inconclusive.
type lineItemProductRule struct {
	ruleBase
	items                     fieldRef
	quantity, unitPrice, amnt string
	tol                       tolerance
	rnd                       rounding
}

func compileLineItemProduct(b ruleBase, s ruleSpec) (*lineItemProductRule, error) {
	r := &lineItemProductRule{ruleBase: b}
	var err error
	if r.items, err = parseFieldRef("items", s.Items); err != nil {
		return nil, err
	}
	if r.quantity, err = memberName("quantity", s.Quantity); err != nil {
		return nil, err
	}
	if r.unitPrice, err = memberName("unit_price", s.UnitPrice); err != nil {
		return nil, err
	}
	if r.amnt, err = memberName("amount", s.Amount); err != nil {
		return nil, err
	}
	if r.quantity == r.unitPrice || r.quantity == r.amnt || r.unitPrice == r.amnt {
		return nil, fmt.Errorf("quantity, unit_price and amount must name different members")
	}
	if r.tol, err = compileTolerance(s.Tolerance); err != nil {
		return nil, err
	}
	if r.rnd, err = compileRounding(s.Round); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *lineItemProductRule) expectedFor(item jsonptr.Pointer) string {
	return fmt.Sprintf("%s * %s = %s %s", item.Append(r.quantity), item.Append(r.unitPrice), item.Append(r.amnt), comparison(r.rnd, r.tol))
}

func (r *lineItemProductRule) checkDocument(c *docCheck) {
	generic := r.expectedFor(r.items.ptr.Append("*"))
	arr, ok := itemsArray(c, r.ruleBase, r.items, generic)
	if !ok {
		return
	}
	for i := range arr {
		item := r.items.ptr.AppendIndex(i)
		expected := r.expectedFor(item)
		in := &inputs{}
		q := resolve(c.doc, item.Append(r.quantity))
		p := resolve(c.doc, item.Append(r.unitPrice))
		a := resolve(c.doc, item.Append(r.amnt))
		in.add(c, q)
		in.add(c, p)
		in.add(c, a)
		if !r.settle(c, in, expected) {
			continue
		}
		r.judge(c, in, outcome{
			computed:   q.value.Mul(p.value),
			derivation: fmt.Sprintf("%s (%s) * %s (%s)", q.ptr, shown(c, q), p.ptr, shown(c, p)),
			stated:     a, rnd: r.rnd, tol: r.tol, expected: expected, location: a.ptr,
		})
	}
}

// itemsArray resolves an items pointer. Absent or null is inconclusive; a
// value that is not an array is an assertion failure.
func itemsArray(c *docCheck, b ruleBase, items fieldRef, expected string) ([]any, bool) {
	v, found := items.lookup(c.doc)
	if !found || v == nil {
		c.emit(b.inconclusive(CodeInputMissing, items.ptr, expected,
			fmt.Sprintf("%s cannot be evaluated: %s absent or null", b.name(), items.text)))
		return nil, false
	}
	arr, ok := v.([]any)
	if !ok {
		c.emit(b.failure(items.ptr, expected, c.san.value(items.ptr, v),
			fmt.Sprintf("%s cannot be evaluated: %s is %s, want array", b.name(), items.text, aType(v))))
		return nil, false
	}
	if len(arr) > MaxMatches {
		c.emit(b.inconclusive(CodeTooManyMatches, items.ptr, expected,
			fmt.Sprintf("%s was not evaluated: %s has more than %d elements", b.name(), items.text, MaxMatches)))
		return nil, false
	}
	return arr, true
}

// memberName validates the name of an object member inside array elements.
func memberName(key, s string) (string, error) {
	switch {
	case s == "":
		return "", fmt.Errorf("%s is required (a member name such as %q)", key, "amount")
	case len(s) > 256:
		return "", fmt.Errorf("%s is longer than 256 bytes", key)
	}
	return s, nil
}

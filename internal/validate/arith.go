package validate

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/happydev123/backendlab/internal/jsonptr"
	"github.com/happydev123/backendlab/internal/redact"
)

// operandState classifies a field referenced by an arithmetic rule.
type operandState int

const (
	opPresent operandState = iota
	// opMissing: the field is absent or null.
	opMissing
	// opInvalid: the field holds something other than a decimal.
	opInvalid
)

// operand is one resolved input of an arithmetic rule.
type operand struct {
	ptr      jsonptr.Pointer
	state    operandState
	value    decimal.Decimal
	raw      string
	original any
}

// resolve reads ptr from doc. Arithmetic rules accept both JSON numbers and
// decimal strings; use a decimal rule to restrict the representation.
func resolve(doc any, ptr jsonptr.Pointer) operand {
	v, found, err := ptr.Get(doc)
	if err != nil || !found || v == nil {
		return operand{ptr: ptr, state: opMissing}
	}
	d, raw, derr := decimalValue(v, acceptBoth)
	if derr != nil {
		return operand{ptr: ptr, state: opInvalid, original: v}
	}
	return operand{ptr: ptr, state: opPresent, value: d, raw: raw, original: v}
}

// inputs collects the operands of one evaluation and remembers which were
// missing or invalid.
type inputs struct {
	missing  []operand
	invalid  []operand
	redacted bool
}

// add records op and reports whether it can be used in the computation.
func (in *inputs) add(c *docCheck, op operand) bool {
	if c.san.redacted(op.ptr) {
		in.redacted = true
	}
	switch op.state {
	case opMissing:
		in.missing = append(in.missing, op)
		return false
	case opInvalid:
		in.invalid = append(in.invalid, op)
		return false
	}
	return true
}

// settle emits a finding when the evaluation cannot proceed and reports
// whether it can. Non-decimal values are assertion failures (the document
// is wrong); missing values are inconclusive (the rule could not be
// evaluated).
func (b ruleBase) settle(c *docCheck, in *inputs, expected string) bool {
	if len(in.invalid) > 0 {
		first := in.invalid[0]
		why := make([]string, len(in.invalid))
		for i, op := range in.invalid {
			why[i] = fmt.Sprintf("%s is %s that is not a decimal", op.ptr, aType(op.original))
		}
		c.emit(b.failure(first.ptr, expected, c.san.value(first.ptr, first.original),
			fmt.Sprintf("%s cannot be evaluated: %s", b.name(), strings.Join(why, "; "))))
		return false
	}
	if len(in.missing) > 0 {
		ptrs := make([]string, len(in.missing))
		for i, op := range in.missing {
			ptrs[i] = op.ptr.String()
		}
		c.emit(b.inconclusive(CodeInputMissing, in.missing[0].ptr, expected,
			fmt.Sprintf("%s cannot be evaluated: %s absent or null", b.name(), strings.Join(ptrs, ", "))))
		return false
	}
	return true
}

// shown renders an operand's value for a message.
func shown(c *docCheck, op operand) string {
	return c.san.raw(op.ptr, op.raw)
}

// outcome holds everything needed to judge and explain one computation.
type outcome struct {
	computed   decimal.Decimal
	derivation string // "/subtotal (100.00) + /tax (8.25)"
	stated     operand
	rnd        rounding
	tol        tolerance
	expected   string
	location   jsonptr.Pointer
}

// judge rounds the computed value if configured, compares it with the
// stated value within the tolerance and emits a failure with computed,
// stated and difference values when they disagree. When any input is at a
// redacted path the computed value and difference are hidden too, since
// they would reveal it.
func (b ruleBase) judge(c *docCheck, in *inputs, o outcome) {
	final := o.rnd.apply(o.computed)
	diff, ok := o.tol.within(final, o.stated.value)
	if ok {
		return
	}
	computed := formatDecimal(o.computed)
	if o.rnd.set && !final.Equal(o.computed) {
		computed = fmt.Sprintf("%s, %s after rounding %s to %d places", computed, formatDecimal(final), o.rnd.mode, o.rnd.scale)
	}
	diffText := formatDecimal(diff)
	if in.redacted {
		computed, diffText = redact.Marker, redact.Marker
	}
	verdict := fmt.Sprintf("difference %s", diffText)
	if !o.tol.exact() {
		verdict += " exceeds tolerance " + o.tol.text
	}
	msg := fmt.Sprintf("%s: computed %s from %s; stated %s is %s; %s",
		b.name(), computed, o.derivation, o.stated.ptr, shown(c, o.stated), verdict)
	c.emit(b.failure(o.location, o.expected, shown(c, o.stated), msg))
}

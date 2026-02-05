package validate

import (
	"errors"
	"fmt"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/jsonptr"
)

// Finding codes that do not name a rule type.
const (
	// CodeSchema reports a JSON Schema violation.
	CodeSchema = "validate.schema"
	// CodeInputMissing reports an arithmetic or ordering rule that could
	// not be evaluated because a referenced field is absent or null.
	CodeInputMissing = "validate.input_missing"
	// CodeTooManyMatches reports a wildcard pattern that matched more
	// values than MaxMatches; the rule was not evaluated.
	CodeTooManyMatches = "validate.too_many_matches"
	// CodeTruncated reports that findings were dropped after the
	// configured limit.
	CodeTruncated = "validate.findings_truncated"
)

// docRule is a rule evaluated against one document at a time.
type docRule interface {
	checkDocument(c *docCheck)
}

// docCheck carries one document through the per-document rules.
type docCheck struct {
	caseID string
	doc    any
	san    sanitizer
	out    *finding.Collector
}

// emit records f for the document being checked.
func (c *docCheck) emit(f finding.Finding) {
	f.CaseID = c.caseID
	c.out.Add(f)
}

// ruleBase holds what every rule type shares: its identity and the
// severity of its assertion failures.
type ruleBase struct {
	id       string
	typ      string
	severity finding.Severity
}

// newBase returns a ruleBase with error severity.
func newBase(id, typ string) ruleBase {
	return ruleBase{id: id, typ: typ, severity: finding.SeverityError}
}

// failure builds an assertion failure for this rule. Observed must already
// be sanitized.
func (b ruleBase) failure(loc jsonptr.Pointer, expected, observed, msg string) finding.Finding {
	return finding.Finding{
		Module:   finding.ModuleValidate,
		Code:     "validate." + b.typ,
		Kind:     finding.KindAssertionFailure,
		Severity: b.severity,
		Location: loc.String(),
		Message:  msg,
		Expected: expected,
		Observed: observed,
		Details:  map[string]string{"rule_id": b.id},
	}
}

// inconclusive builds a finding for a rule that could not be evaluated.
// These are warnings: the document is neither confirmed nor refuted.
func (b ruleBase) inconclusive(code string, loc jsonptr.Pointer, expected, msg string) finding.Finding {
	return finding.Finding{
		Module:   finding.ModuleValidate,
		Code:     code,
		Kind:     finding.KindInconclusive,
		Severity: finding.SeverityWarning,
		Location: loc.String(),
		Message:  msg,
		Expected: expected,
		Details:  map[string]string{"rule_id": b.id, "rule_type": b.typ},
	}
}

// name renders the rule for messages: `rule "totals"`.
func (b ruleBase) name() string { return fmt.Sprintf("rule %q", b.id) }

// eachMatch expands p in the document and calls fn for every selected
// value. A pattern that matches too many values yields one inconclusive
// finding instead.
func (b ruleBase) eachMatch(c *docCheck, p fieldPattern, fn func(m match)) {
	ms, err := p.expand(c.doc)
	if errors.Is(err, errTooManyMatches) {
		loc, _ := jsonptr.Parse(p.text)
		c.emit(b.inconclusive(CodeTooManyMatches, loc, "",
			fmt.Sprintf("%s was not evaluated: %s %v", b.name(), p.text, err)))
		return
	}
	for _, m := range ms {
		fn(m)
	}
}

// truncatedFinding reports findings dropped after the limit.
func truncatedFinding(caseID string, dropped, max int) finding.Finding {
	return finding.Finding{
		Module:   finding.ModuleValidate,
		Code:     CodeTruncated,
		Kind:     finding.KindObservation,
		Severity: finding.SeverityWarning,
		CaseID:   caseID,
		Message:  fmt.Sprintf("%d further findings were dropped after the limit of %d", dropped, max),
	}
}

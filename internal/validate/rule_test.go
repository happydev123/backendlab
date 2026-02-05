package validate

import (
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/jsonptr"
	"github.com/happydev123/backendlab/internal/redact"
)

// runRule checks one document with one rule using default sanitizing.
func runRule(t testing.TB, r docRule, doc any) []finding.Finding {
	t.Helper()
	return runRuleWith(t, r, doc, newSanitizer(nil, 0))
}

func runRuleWith(t testing.TB, r docRule, doc any, san sanitizer) []finding.Finding {
	t.Helper()
	c := &docCheck{caseID: "doc-1", doc: doc, san: san, out: finding.NewCollector(100)}
	r.checkDocument(c)
	for _, f := range c.out.Items() {
		if err := f.Validate(); err != nil {
			t.Fatalf("invalid finding: %v", err)
		}
		if f.CaseID != "doc-1" {
			t.Fatalf("case id not set: %+v", f)
		}
	}
	return c.out.Items()
}

// codes lists "code@location" for compact assertions.
func codes(fs []finding.Finding) string {
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = f.Code + "@" + f.Location
	}
	return strings.Join(parts, " ")
}

func TestSanitizer(t *testing.T) {
	red, err := redact.New(redact.Policy{JSONPaths: []string{"/customer/tax_id"}})
	if err != nil {
		t.Fatal(err)
	}
	s := newSanitizer(red.WithSecrets("s3cr3t-value"), 20)
	if got := s.value(jsonptr.MustParse("/customer/tax_id"), "DE123"); got != redact.Marker {
		t.Fatalf("path not redacted: %q", got)
	}
	if got := s.value(jsonptr.MustParse("/password"), "x"); got != redact.Marker {
		t.Fatalf("sensitive key not redacted: %q", got)
	}
	if got := newSanitizer(red, 0).value(jsonptr.MustParse("/meta"), map[string]any{"token": "abc"}); got != `{"token":"[REDACTED]"}` {
		t.Fatalf("nested key not redacted: %q", got)
	}
	if got := s.raw(jsonptr.MustParse("/note"), "has s3cr3t-value inside"); strings.Contains(got, "s3cr3t") {
		t.Fatalf("secret not scrubbed: %q", got)
	}
	if got := s.value(jsonptr.MustParse("/long"), strings.Repeat("a", 50)); len(got) > 23 || !strings.HasSuffix(got, "...") {
		t.Fatalf("preview not bounded: %q", got)
	}
	if got := s.list([]string{"EUR", "USD", "GBP", "CHF", "JPY", "SEK"}); got != "EUR, USD, GBP, CHF, ... (2 more)" {
		t.Fatalf("list = %q", got)
	}
	d := newSanitizer(nil, 0)
	if d.max != 160 || d.red == nil {
		t.Fatalf("defaults not applied: %+v", d)
	}
}

type matchRule struct {
	ruleBase
	p fieldPattern
}

func (r matchRule) checkDocument(c *docCheck) {
	r.eachMatch(c, r.p, func(m match) {
		c.emit(r.failure(m.ptr, "nothing", c.san.value(m.ptr, m.value), "matched"))
	})
}

func TestRuleBaseFindings(t *testing.T) {
	p, _ := parseFieldPattern("fields", "/items/*/a")
	r := matchRule{ruleBase: newBase("r1", "test"), p: p}
	fs := runRule(t, r, mustDecode(t, `{"items":[{"a":1},{"a":2}]}`))
	if codes(fs) != "validate.test@/items/0/a validate.test@/items/1/a" {
		t.Fatalf("codes = %s", codes(fs))
	}
	f := fs[0]
	if f.Kind != finding.KindAssertionFailure || f.Severity != finding.SeverityError || f.Details["rule_id"] != "r1" || f.Observed != "1" {
		t.Fatalf("finding = %+v", f)
	}

	items := make([]any, MaxMatches+1)
	for i := range items {
		items[i] = map[string]any{"a": "1"}
	}
	fs = runRule(t, r, map[string]any{"items": items})
	if len(fs) != 1 || fs[0].Code != CodeTooManyMatches || fs[0].Kind != finding.KindInconclusive || fs[0].Location != "/items/*/a" {
		t.Fatalf("too many matches: %+v", fs)
	}

	inc := r.inconclusive(CodeInputMissing, jsonptr.MustParse("/total"), "x", "missing")
	if inc.Severity != finding.SeverityWarning || inc.Details["rule_type"] != "test" || inc.Failing() {
		t.Fatalf("inconclusive = %+v", inc)
	}
	tr := truncatedFinding("doc", 3, 10)
	if err := tr.Validate(); err != nil || !strings.Contains(tr.Message, "3 further findings") {
		t.Fatalf("truncated = %+v %v", tr, err)
	}
}

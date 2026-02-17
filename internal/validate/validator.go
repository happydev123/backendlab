package validate

import (
	"errors"
	"fmt"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/jsonptr"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/schema"
)

// Options configures a Validator.
type Options struct {
	// Redactor hides sensitive values in findings. Nil uses the built-in
	// redaction rules.
	Redactor *redact.Redactor
	// MaxFindings bounds the findings returned by one Document, Batch or
	// Run call. Zero uses the default limit.
	MaxFindings int
	// MaxValuePreview bounds each observed value in a finding, in bytes.
	// Zero uses the default limit.
	MaxValuePreview int
	// ConfineFiles requires files referenced by the rules (the schema) to
	// stay inside the rules file's directory. Server mode sets it.
	ConfineFiles bool
}

// Validator checks documents against a JSON Schema and a rules file. It is
// immutable after New and safe for concurrent use.
type Validator struct {
	rules       *Rules
	schemas     []*schema.Schema
	san         sanitizer
	maxFindings int
}

// New builds a validator. rules may be nil when only a schema is used, and
// extraSchema may be nil; at least one of them is required. When the rules
// reference a schema file, New reads and compiles it (relative to the
// rules directory, confined when opts.ConfineFiles is set). Documents are
// checked against both the rules' schema and extraSchema when both exist.
func New(rules *Rules, extraSchema *schema.Schema, opts Options) (*Validator, error) {
	if rules == nil && extraSchema == nil {
		return nil, errors.New("validate: rules or a schema are required")
	}
	hard := limits.Hard()
	if opts.MaxFindings < 0 || opts.MaxFindings > hard.MaxFindings {
		return nil, fmt.Errorf("validate: max findings must be between 1 and %d", hard.MaxFindings)
	}
	if opts.MaxValuePreview < 0 || opts.MaxValuePreview > hard.MaxValuePreview {
		return nil, fmt.Errorf("validate: max value preview must be between 1 and %d", hard.MaxValuePreview)
	}
	v := &Validator{
		rules:       rules,
		san:         newSanitizer(opts.Redactor, opts.MaxValuePreview),
		maxFindings: opts.MaxFindings,
	}
	if v.maxFindings == 0 {
		v.maxFindings = limits.Default().MaxFindings
	}
	if rules != nil && rules.schemaRef != "" {
		path, err := resolveFile(rules.baseDir, rules.schemaRef, opts.ConfineFiles)
		if err != nil {
			return nil, fmt.Errorf("validate: schema: %w", err)
		}
		s, err := schema.CompileFile(path)
		if err != nil {
			return nil, fmt.Errorf("validate: %w", err)
		}
		v.schemas = append(v.schemas, s)
	}
	if extraSchema != nil {
		v.schemas = append(v.schemas, extraSchema)
	}
	return v, nil
}

// Document checks one document against the schemas and the per-document
// rules. Batch rules (unique) do not run. docID becomes the CaseID of
// every finding; replay passes its case ID here. When more than
// MaxFindings findings occur, the rest are dropped and a final
// validate.findings_truncated finding says how many.
func (v *Validator) Document(docID string, doc any) []finding.Finding {
	c := finding.NewCollector(v.maxFindings)
	v.checkDocument(docID, doc, c)
	out := c.Items()
	if n := c.Dropped(); n > 0 {
		out = append(out, truncatedFinding(docID, n, v.maxFindings))
	}
	return out
}

// checkDocument runs schemas, then rules in file order.
func (v *Validator) checkDocument(caseID string, doc any, out *finding.Collector) {
	c := &docCheck{caseID: caseID, doc: doc, san: v.san, out: out}
	for _, s := range v.schemas {
		for _, viol := range s.Validate(doc) {
			c.emit(v.schemaFinding(s, doc, viol))
		}
	}
	if v.rules == nil {
		return
	}
	for _, r := range v.rules.doc {
		r.checkDocument(c)
	}
}

// schemaFinding converts a schema violation. Library messages can quote
// the offending value, so at redacted locations only the keyword is kept.
func (v *Validator) schemaFinding(s *schema.Schema, doc any, viol schema.Violation) finding.Finding {
	f := finding.Finding{
		Module:   finding.ModuleValidate,
		Code:     CodeSchema,
		Kind:     finding.KindAssertionFailure,
		Severity: finding.SeverityError,
		Location: viol.Location,
		Expected: "document valid against schema " + s.Name(),
		Details:  map[string]string{"keyword": viol.Keyword, "schema": s.Name()},
	}
	p, err := jsonptr.Parse(viol.Location)
	switch {
	case err == nil && v.san.redacted(p):
		f.Message = fmt.Sprintf("schema %s: value violates %s", s.Name(), viol.Keyword)
		f.Observed = redact.Marker
	default:
		f.Message = fmt.Sprintf("schema %s: %s", s.Name(), v.san.text(viol.Message))
		if err == nil {
			if val, found, gerr := p.Get(doc); gerr == nil && found {
				f.Observed = v.san.value(p, val)
			}
		}
	}
	return f
}

package validate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/yamlutil"
)

// Rules is a parsed and validated rules file. It is immutable and safe for
// concurrent use.
type Rules struct {
	baseDir    string
	schemaRef  string
	documentID *fieldRef
	doc        []docRule
	batch      []batchRule
	input      *report.Input
}

// commonKeys apply to every rule type.
var commonKeys = []string{"id", "type", "description", "severity"}

// ruleKeys lists the keys each rule type accepts besides commonKeys.
var ruleKeys = map[string][]string{
	typeRequired:        {"fields", "allow_null"},
	typeAllowedValues:   {"field", "values"},
	typePattern:         {"field", "pattern"},
	typeDecimal:         {"fields", "accept", "max_scale", "min", "max"},
	typeSum:             {"terms", "equals", "tolerance", "round"},
	typeLineItemsSum:    {"items", "amount", "equals", "tolerance", "round"},
	typeLineItemProduct: {"items", "quantity", "unit_price", "amount", "tolerance", "round"},
	typeProduct:         {"factors", "equals", "tolerance", "round"},
	typeDate:            {"fields", "layout"},
	typeDateOrder:       {"before", "after", "layout", "allow_equal"},
	typeUnique:          {"field"},
}

var ruleIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// LoadRules reads and parses a rules file. Relative file references in it
// (the schema) resolve against the file's directory. The returned rules
// record the file name and SHA-256 digest for reports (see Rules.Input).
func LoadRules(path string) (*Rules, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open rules: %w", err)
	}
	defer f.Close()
	data, err := jsonutil.ReadAllLimit(f, MaxRulesBytes)
	if err != nil {
		return nil, fmt.Errorf("rules %s: %w", path, err)
	}
	r, err := ParseRules(data, filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("rules %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	r.input = &report.Input{Kind: "rules", Name: filepath.ToSlash(path), SHA256: hex.EncodeToString(sum[:])}
	return r, nil
}

// ParseRules parses rules file data. baseDir is the directory that a
// relative schema reference resolves against; the schema file itself is
// read by New, which also applies Options.ConfineFiles. Unknown keys,
// keys that do not apply to a rule's type, unknown rule types, duplicate
// rule IDs and invalid pointers are errors.
func ParseRules(data []byte, baseDir string) (*Rules, error) {
	if len(data) > MaxRulesBytes {
		return nil, fmt.Errorf("rules exceed %d bytes", MaxRulesBytes)
	}
	var file rulesFile
	if err := yamlutil.DecodeStrict(data, &file); err != nil {
		return nil, fmt.Errorf("parse rules: %w", err)
	}
	if file.Version != CurrentVersion {
		if file.Version == 0 {
			return nil, fmt.Errorf("version is required (version: %d)", CurrentVersion)
		}
		return nil, fmt.Errorf("unsupported rules version %d (want %d)", file.Version, CurrentVersion)
	}
	if len(file.Rules) == 0 && file.Schema == "" {
		return nil, errors.New("rules file lists no rules and no schema")
	}
	if len(file.Rules) > MaxRules {
		return nil, fmt.Errorf("rules file lists more than %d rules", MaxRules)
	}
	// Strict decoding validated the structure; a second, loose decode
	// recovers which keys each rule actually wrote.
	var loose struct {
		Rules []map[string]any `yaml:"rules"`
	}
	if err := yaml.Unmarshal(data, &loose); err != nil || len(loose.Rules) != len(file.Rules) {
		return nil, errors.New("parse rules: unexpected rules layout")
	}

	out := &Rules{baseDir: baseDir, schemaRef: file.Schema}
	if file.DocumentID != "" {
		ref, err := parseFieldRef("document_id", file.DocumentID)
		if err != nil {
			return nil, err
		}
		out.documentID = &ref
	}
	seen := map[string]bool{}
	for i, spec := range file.Rules {
		where := fmt.Sprintf("rules[%d]", i)
		if spec.ID != "" {
			where = fmt.Sprintf("rules[%d] (%s)", i, clip(spec.ID))
		}
		if err := checkRuleID(spec.ID, seen); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		if err := checkRuleKeys(spec.Type, loose.Rules[i]); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		b := newBase(spec.ID, spec.Type)
		switch spec.Severity {
		case "", "error":
		case "warning":
			b.severity = finding.SeverityWarning
		default:
			return nil, fmt.Errorf("%s: unknown severity %q (want error or warning)", where, spec.Severity)
		}
		if err := out.add(b, spec); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
	}
	return out, nil
}

// add compiles one rule of a known type.
func (r *Rules) add(b ruleBase, s ruleSpec) error {
	var (
		dr  docRule
		err error
	)
	switch s.Type {
	case typeRequired:
		dr, err = compileRequired(b, s)
	case typeAllowedValues:
		dr, err = compileAllowedValues(b, s)
	case typePattern:
		dr, err = compilePattern(b, s)
	case typeDecimal:
		dr, err = compileDecimal(b, s)
	case typeSum:
		dr, err = compileSum(b, s)
	case typeLineItemsSum:
		dr, err = compileLineItemsSum(b, s)
	case typeLineItemProduct:
		dr, err = compileLineItemProduct(b, s)
	case typeProduct:
		dr, err = compileProduct(b, s)
	case typeDate:
		dr, err = compileDate(b, s)
	case typeDateOrder:
		dr, err = compileDateOrder(b, s)
	case typeUnique:
		u, uerr := compileUnique(b, s)
		if uerr != nil {
			return uerr
		}
		r.batch = append(r.batch, u)
		return nil
	default:
		return fmt.Errorf("unknown rule type %q", s.Type)
	}
	if err != nil {
		return err
	}
	r.doc = append(r.doc, dr)
	return nil
}

func checkRuleID(id string, seen map[string]bool) error {
	switch {
	case id == "":
		return errors.New("id is required")
	case len(id) > MaxRuleIDLength:
		return fmt.Errorf("id is longer than %d bytes", MaxRuleIDLength)
	case !ruleIDPattern.MatchString(id):
		return fmt.Errorf("id %q may contain only letters, digits, '.', '_' and '-'", id)
	case seen[id]:
		return fmt.Errorf("duplicate rule id %q", id)
	}
	seen[id] = true
	return nil
}

// checkRuleKeys rejects keys that are valid for some rule type but not for
// this one, so a misplaced "tolerance" on a date rule is reported instead
// of ignored.
func checkRuleKeys(typ string, written map[string]any) error {
	if typ == "" {
		return errors.New("type is required")
	}
	allowed, ok := ruleKeys[typ]
	if !ok {
		known := make([]string, 0, len(ruleKeys))
		for k := range ruleKeys {
			known = append(known, k)
		}
		sort.Strings(known)
		return fmt.Errorf("unknown rule type %q (known: %s)", clip(typ), strings.Join(known, ", "))
	}
	keys := make([]string, 0, len(written))
	for k := range written {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !contains(commonKeys, k) && !contains(allowed, k) {
			return fmt.Errorf("key %q does not apply to rule type %s (allowed: %s)", k, typ, strings.Join(allowed, ", "))
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Len returns the number of rules.
func (r *Rules) Len() int { return len(r.doc) + len(r.batch) }

// SchemaRef returns the schema reference as written in the rules file, or
// "" when none is configured.
func (r *Rules) SchemaRef() string { return r.schemaRef }

// Input describes the rules file for a report. ok is false for rules
// parsed from memory with ParseRules.
func (r *Rules) Input() (in report.Input, ok bool) {
	if r.input == nil {
		return report.Input{}, false
	}
	return *r.input, true
}

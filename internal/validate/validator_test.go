package validate

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/schema"
)

func loadSchemaRules(t *testing.T) *Rules {
	t.Helper()
	r, err := LoadRules(filepath.Join("testdata", "schema", "rules.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestValidatorSchemaAndRules(t *testing.T) {
	v, err := New(loadSchemaRules(t), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	fs := v.Document("case-7", mustDecode(t, `{"id":5,"subtotal":"10.00","tax":"1.00","total":"11.10"}`))
	if got := codes(fs); got != "validate.schema@/id validate.sum@/total" {
		t.Fatalf("codes = %s", got)
	}
	s := fs[0]
	if s.CaseID != "case-7" || s.Kind != finding.KindAssertionFailure || s.Details["schema"] != "doc.schema.json" ||
		!strings.Contains(s.Details["keyword"], "type") || s.Observed != "5" || !strings.HasPrefix(s.Message, "schema doc.schema.json: ") {
		t.Fatalf("schema finding = %+v", s)
	}
	if fs[1].CaseID != "case-7" || fs[1].Details["rule_id"] != "totals" {
		t.Fatalf("rule finding = %+v", fs[1])
	}
	if fs := v.Document("ok", mustDecode(t, `{"id":"a","subtotal":"10.00","tax":"1.00","total":"11.00"}`)); len(fs) != 0 {
		t.Fatalf("valid document: %+v", fs)
	}
	// Missing required properties are reported by the schema and make the
	// sum inconclusive.
	fs = v.Document("c", mustDecode(t, `{"id":"a","subtotal":"1"}`))
	if got := codes(fs); got != "validate.schema@ validate.input_missing@/tax" {
		t.Fatalf("codes = %s", got)
	}
}

func TestValidatorExtraSchemaOnly(t *testing.T) {
	extra, err := schema.CompileFile(filepath.Join("testdata", "schema", "doc.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	red, _ := redact.New(redact.Policy{JSONPaths: []string{"/card_number"}})
	v, err := New(nil, extra, Options{Redactor: red})
	if err != nil {
		t.Fatal(err)
	}
	fs := v.Document("d", mustDecode(t, `{"id":"a","total":1,"card_number":"4111-1111"}`))
	if len(fs) != 1 || fs[0].Observed != redact.Marker || strings.Contains(fs[0].Message, "4111") {
		t.Fatalf("redacted schema finding: %+v", fs)
	}
	both, err := New(loadSchemaRules(t), extra, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(both.Document("d", mustDecode(t, `{"total":1}`))); got < 2 {
		t.Fatalf("both schemas should report: %d findings", got)
	}
}

func TestValidatorOptions(t *testing.T) {
	r := loadSchemaRules(t)
	if _, err := New(nil, nil, Options{}); err == nil {
		t.Fatal("nothing to validate accepted")
	}
	if _, err := New(r, nil, Options{MaxFindings: -1}); err == nil {
		t.Fatal("negative max findings accepted")
	}
	if _, err := New(r, nil, Options{MaxValuePreview: 1 << 20}); err == nil {
		t.Fatal("huge preview accepted")
	}
	v, err := New(r, nil, Options{MaxFindings: 1})
	if err != nil {
		t.Fatal(err)
	}
	fs := v.Document("x", mustDecode(t, `{"id":1,"subtotal":"x","total":"1"}`))
	if len(fs) != 2 || fs[1].Code != CodeTruncated || fs[1].CaseID != "x" || !strings.Contains(fs[1].Message, "dropped after the limit of 1") {
		t.Fatalf("truncation: %+v", fs)
	}
}

func TestValidatorConfinedSchema(t *testing.T) {
	escape, err := LoadRules(filepath.Join("testdata", "schema", "escape.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(escape, nil, Options{ConfineFiles: true}); err == nil || !strings.Contains(err.Error(), "escapes the rules directory") {
		t.Fatalf("escape: %v", err)
	}
	// Unconfined, the reference resolves (and fails because it is YAML,
	// not a schema), proving confinement is what rejected it above.
	if _, err := New(escape, nil, Options{}); err == nil || strings.Contains(err.Error(), "escapes") {
		t.Fatalf("unconfined: %v", err)
	}

	abs, err := filepath.Abs(filepath.Join("testdata", "schema", "doc.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	rulesPath := filepath.Join(dir, "rules.yaml")
	if err := os.WriteFile(rulesPath, []byte("version: 1\nschema: '"+filepath.ToSlash(abs)+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	absRules, err := LoadRules(rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(absRules, nil, Options{ConfineFiles: true}); err == nil || !strings.Contains(err.Error(), "must be relative") {
		t.Fatalf("absolute confined: %v", err)
	}
	if _, err := New(absRules, nil, Options{}); err != nil {
		t.Fatalf("absolute unconfined: %v", err)
	}

	mem, err := ParseRules([]byte("version: 1\nschema: doc.schema.json\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(mem, nil, Options{ConfineFiles: true}); err == nil || !strings.Contains(err.Error(), "needs a rules directory") {
		t.Fatalf("no base dir: %v", err)
	}
	if _, err := New(loadSchemaRules(t), nil, Options{ConfineFiles: true}); err != nil {
		t.Fatalf("confined sibling schema: %v", err)
	}
	missing, _ := ParseRules([]byte("version: 1\nschema: nope.json\n"), filepath.Join("testdata", "schema"))
	if _, err := New(missing, nil, Options{ConfineFiles: true}); err == nil {
		t.Fatal("missing schema accepted")
	}
}

func TestResolveFile(t *testing.T) {
	tests := []struct {
		dir, ref string
		confine  bool
		ok       bool
	}{
		{"rules", "s.json", true, true},
		{"rules", "sub/s.json", true, true},
		{"rules", "../s.json", true, false},
		{"rules", "sub/../../s.json", true, false},
		{"rules", "../s.json", false, true},
		{"rules", "", false, false},
	}
	for _, tt := range tests {
		// Lexical checks only: confined lookups of existing files are
		// covered in TestValidatorConfinedSchema.
		_, err := resolveFile(tt.dir, tt.ref, tt.confine)
		lexicalReject := err != nil && (strings.Contains(err.Error(), "escapes") || strings.Contains(err.Error(), "empty"))
		if tt.ok == lexicalReject {
			t.Errorf("%s + %s (confine=%v): err = %v", tt.dir, tt.ref, tt.confine, err)
		}
	}
}

// TestValidatorConcurrentUse backs the documented guarantee; run with -race.
func TestValidatorConcurrentUse(t *testing.T) {
	v, err := New(loadSchemaRules(t), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	doc := mustDecode(t, `{"id":"a","subtotal":"10.00","tax":"1.00","total":"11.10"}`)
	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if got := codes(v.Document("c", doc)); got != "validate.sum@/total" {
					errs <- got
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("concurrent result = %q", e)
	}
}

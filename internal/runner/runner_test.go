package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/suite"
)

// writeSuite writes files into a temporary suite directory and loads
// suite.yaml from it.
func writeSuite(t *testing.T, files map[string]string) *suite.Loaded {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	l, err := suite.LoadFile(filepath.Join(dir, "suite.yaml"), limits.Default())
	if err != nil {
		t.Fatal(err)
	}
	return l
}

const schemaDoc = `{"type":"object","required":["total"],"properties":{"total":{"type":"number"}}}`

const rulesDoc = `version: 1
rules:
  - id: total
    type: sum
    terms:
      - field: /subtotal
      - field: /tax
    equals: /total
`

func TestPrepareBuildsSharedValidators(t *testing.T) {
	l := writeSuite(t, map[string]string{
		"suite.yaml": `version: 1
name: t
cases:
  - {id: a, method: GET, path: /a, expect: {schema: schema.json, rules: rules.yaml}}
  - {id: b, method: GET, path: /b, expect: {schema: schema.json, rules: rules.yaml}}
  - {id: c, method: GET, path: /c, stream: {terminal_data: "[DONE]", json_payloads: true}}
`,
		"schema.json": schemaDoc,
		"rules.yaml":  rulesDoc,
	})
	r, err := New(Config{}, l)
	if err != nil {
		t.Fatal(err)
	}
	if r.checks.validators["a"] != r.checks.validators["b"] {
		t.Fatal("cases naming the same files should share one validator")
	}
	if _, ok := r.checks.streams["c"]; !ok {
		t.Fatal("stream expectations not prepared")
	}

	good, _ := jsonutil.Decode([]byte(`{"subtotal": 10.00, "tax": 1.90, "total": 11.90}`))
	if fs := r.checks.checkBody(&l.Suite.Cases[0], good); len(fs) != 0 {
		t.Fatalf("consistent body produced findings: %+v", fs)
	}
	bad, _ := jsonutil.Decode([]byte(`{"subtotal": 10.00, "tax": 1.90, "total": "11.95"}`))
	fs := r.checks.checkBody(&l.Suite.Cases[0], bad)
	codes := []string{}
	for _, f := range fs {
		codes = append(codes, f.Code)
		if f.CaseID != "a" {
			t.Errorf("finding case ID = %q, want the suite case ID", f.CaseID)
		}
	}
	if !strings.Contains(strings.Join(codes, ","), "validate.schema") {
		t.Fatalf("expected a schema violation, got %v", codes)
	}
	if fs := r.checks.checkBody(&l.Suite.Cases[2], bad); fs != nil {
		t.Fatalf("case without checks produced findings: %+v", fs)
	}
}

func TestPrepareRejectsBrokenReferences(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		confine bool
		want    string
	}{
		{"missing rules", map[string]string{
			"suite.yaml": "version: 1\nname: t\ncases:\n  - {id: a, method: GET, path: /a, expect: {rules: nope.yaml}}\n",
		}, false, "expect.rules"},
		{"invalid schema", map[string]string{
			"suite.yaml": "version: 1\nname: t\ncases:\n  - {id: a, method: GET, path: /a, expect: {schema: s.json}}\n",
			"s.json":     `{"type": 7}`,
		}, false, "expect.schema"},
		{"escape when confined", map[string]string{
			"suite.yaml": "version: 1\nname: t\ncases:\n  - {id: a, method: GET, path: /a, expect: {schema: ../outside.json}}\n",
		}, true, "escapes the suite directory"},
		{"stream schema escape when confined", map[string]string{
			"suite.yaml": "version: 1\nname: t\ncases:\n  - {id: a, method: GET, path: /a, stream: {json_payloads: true, schema: ../x.json}}\n",
		}, true, "escapes the suite directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := writeSuite(t, tt.files)
			_, err := New(Config{ConfineFiles: tt.confine}, l)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

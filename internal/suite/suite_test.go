package suite

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/limits"
)

func TestLoadValidSuite(t *testing.T) {
	l, err := LoadFile(filepath.Join("testdata", "valid.yaml"), limits.Default())
	if err != nil {
		t.Fatal(err)
	}
	s := l.Suite
	if s.Name != "invoice-api" || len(s.Cases) != 3 {
		t.Fatalf("suite = %+v", s)
	}
	if len(l.Source.SHA256) != 64 || l.Source.Size == 0 {
		t.Fatalf("source = %+v", l.Source)
	}
	get, ok := s.Case("get-invoice")
	if !ok {
		t.Fatal("missing case")
	}
	if get.Timeout.Std() != 2*time.Second {
		t.Errorf("timeout = %v", get.Timeout.Std())
	}
	if !get.Expect.Status.Contains(203) || get.Expect.Status.Contains(500) {
		t.Errorf("status list = %v", get.Expect.Status)
	}
	// Numbers keep their literal text.
	if v := get.Expect.JSON[1].Equals.V; v != json.Number("110.10") {
		t.Errorf("equals = %#v, want json.Number(\"110.10\")", v)
	}
	if !s.Mutating() || len(s.MutatingCases()) != 1 || s.MutatingCases()[0] != "create-note" {
		t.Errorf("mutating cases = %v", s.MutatingCases())
	}
	cmp := s.EffectiveCompare(get)
	if len(cmp.Ignore) != 2 || cmp.Tolerance == nil || cmp.Tolerance.Absolute != "0.01" {
		t.Errorf("effective compare = %+v", cmp)
	}
	h := s.EffectiveHeaders(get)
	if h["Accept"] != "application/json" {
		t.Errorf("headers = %v", h)
	}
	if got := s.EffectiveTimeout(&s.Cases[0], limits.Default()); got != 5*time.Second {
		t.Errorf("default timeout = %v", got)
	}
	refs, err := s.Cases[2].Refs()
	if err != nil {
		t.Fatal(err)
	}
	if locs := refs["first_id"]; len(locs) != 3 {
		t.Errorf("refs = %v", refs)
	}
}

func TestValidationErrors(t *testing.T) {
	base := `version: 1
name: t
cases:
`
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"unknown key", base + "  - id: a\n    method: GET\n    path: /x\n    bogus: 1\n", "field bogus not found"},
		{"duplicate id", base + "  - {id: a, method: GET, path: /x}\n  - {id: a, method: GET, path: /y}\n", "duplicate id"},
		{"bad id", base + "  - {id: 'a b', method: GET, path: /x}\n", "id must match"},
		{"lower method", base + "  - {id: a, method: get, path: /x}\n", "upper case"},
		{"unknown method", base + "  - {id: a, method: TRACE, path: /x}\n", "not supported"},
		{"absolute url", base + "  - {id: a, method: GET, path: 'http://evil/x'}\n", "start with '/'"},
		{"scheme relative", base + "  - {id: a, method: GET, path: //evil/x}\n", "'//'"},
		{"query in path", base + "  - {id: a, method: GET, path: '/x?y=1'}\n", "use query"},
		{"dot segment", base + "  - {id: a, method: GET, path: /a/../b}\n", "dot segments"},
		{"missing param", base + "  - {id: a, method: GET, path: '/x/{id}'}\n", "no path_params entry"},
		{"unused param", base + "  - {id: a, method: GET, path: /x, path_params: {id: '1'}}\n", "not used in path"},
		{"var in path", base + "  - {id: a, method: GET, path: '/x/${v}'}\n", "not expanded in path"},
		{"undefined var", base + "  - {id: a, method: GET, path: /x, query: {q: '${nope}'}}\n", "no earlier case captures"},
		{"self reference", base + "  - {id: a, method: GET, path: /x, query: {q: '${v}'}, capture: [{name: v, pointer: /id}]}\n", "no earlier case captures"},
		{"credential header", base + "  - {id: a, method: GET, path: /x, headers: {Authorization: x}}\n", "target configuration"},
		{"host header", base + "  - {id: a, method: GET, path: /x, headers: {Host: x}}\n", "controlled by the client"},
		{"body on get", base + "  - {id: a, method: GET, path: /x, body: {text: hi}}\n", "must not have a body"},
		{"two bodies", base + "  - {id: a, method: POST, path: /x, body: {text: hi, json: {}}}\n", "exactly one"},
		{"two operators", base + "  - {id: a, method: GET, path: /x, expect: {json: [{pointer: /a, exists: true, type: string}]}}\n", "exactly one of"},
		{"bad type", base + "  - {id: a, method: GET, path: /x, expect: {json: [{pointer: /a, type: integer}]}}\n", "not a JSON type"},
		{"bad regexp", base + "  - {id: a, method: GET, path: /x, expect: {json: [{pointer: /a, matches: '('}]}}\n", "regular expression"},
		{"bad status", base + "  - {id: a, method: GET, path: /x, expect: {status: 700}}\n", "not a valid HTTP status"},
		{"unordered without key", base + "  - {id: a, method: GET, path: /x, compare: {unordered: [{pointer: /items}]}}\n", "matching key"},
		{"negative tolerance", base + "  - {id: a, method: GET, path: /x, compare: {tolerance: {absolute: '-1'}}}\n", "must not be negative"},
		{"bad tolerance", base + "  - {id: a, method: GET, path: /x, compare: {tolerance: {relative: abc}}}\n", "not a decimal"},
		{"ignore root", base + "  - {id: a, method: GET, path: /x, compare: {ignore: ['']}}\n", "whole document"},
		{"bare duration", base + "  - {id: a, method: GET, path: /x, timeout: 5}\n", "unit"},
		{"stream terminal", base + "  - {id: a, method: GET, path: /x, stream: {require_terminal: true}}\n", "terminal_event or terminal_data"},
		{"stream adapter", base + "  - {id: a, method: GET, path: /x, stream: {adapter: vendor-x}}\n", "not supported"},
		{"bad version", "version: 2\nname: t\ncases:\n  - {id: a, method: GET, path: /x}\n", "version must be 1"},
		{"no cases", "version: 1\nname: t\ncases: []\n", "at least one case"},
		{"multi doc", base + "  - {id: a, method: GET, path: /x}\n---\nversion: 1\n", "multiple YAML documents"},
		{"duplicate yaml key", "version: 1\nname: t\nname: u\ncases:\n  - {id: a, method: GET, path: /x}\n", "already defined"},
		{"hex number", base + "  - {id: a, method: GET, path: /x, expect: {json: [{pointer: /a, equals: 0x1F}]}}\n", "not valid JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml), limits.Default())
			if err == nil {
				t.Fatalf("expected error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err, tt.want)
			}
		})
	}
}

func TestCaseLimit(t *testing.T) {
	var b strings.Builder
	b.WriteString("version: 1\nname: big\ncases:\n")
	for i := 0; i < 4; i++ {
		b.WriteString("  - {id: c" + string(rune('a'+i)) + ", method: GET, path: /x}\n")
	}
	lim := limits.Default()
	lim.MaxCases = 3
	if _, err := Parse([]byte(b.String()), lim); err == nil || !strings.Contains(err.Error(), "limit is 3") {
		t.Fatalf("err = %v", err)
	}
	lim.MaxSuiteBytes = 10
	if _, err := Parse([]byte(b.String()), lim); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidationCollectsAllProblems(t *testing.T) {
	_, err := Parse([]byte("version: 1\nname: ''\ncases:\n  - {id: a, method: get, path: x}\n"), limits.Default())
	if !IsValidationError(err) {
		t.Fatalf("expected validation error, got %v", err)
	}
	ve := err.(*ValidationError)
	if len(ve.Problems) < 3 {
		t.Fatalf("problems = %v", ve.Problems)
	}
}

func TestResolveFile(t *testing.T) {
	dir := filepath.Join("srv", "suites")
	if p, err := ResolveFile(dir, "schemas/a.json", true); err != nil || p != filepath.Join(dir, "schemas", "a.json") {
		t.Fatalf("got %q, %v", p, err)
	}
	if _, err := ResolveFile(dir, "../secrets.json", true); err == nil {
		t.Fatal("expected escape to be rejected")
	}
	if _, err := ResolveFile(dir, filepath.Join(os.TempDir(), "x"), true); err == nil {
		t.Fatal("expected absolute path to be rejected when confined")
	}
	if _, err := ResolveFile(dir, "../shared/a.json", false); err != nil {
		t.Fatalf("unconfined resolution failed: %v", err)
	}
}

func TestEqualsNull(t *testing.T) {
	s, err := Parse([]byte("version: 1\nname: t\ncases:\n  - id: a\n    method: GET\n    path: /x\n    expect:\n      json:\n        - pointer: /deleted_at\n          equals: null\n        - pointer: /n\n          equals: 1.50\n"), limits.Default())
	if err != nil {
		t.Fatal(err)
	}
	as := s.Cases[0].Expect.JSON
	if as[0].Equals == nil || as[0].Equals.V != nil {
		t.Fatalf("equals null = %#v", as[0].Equals)
	}
	if as[1].Equals == nil || as[1].Equals.V != json.Number("1.50") {
		t.Fatalf("equals 1.50 = %#v", as[1].Equals)
	}
	if _, err := Parse([]byte("version: 1\nname: t\ncases:\n  - id: a\n    method: GET\n    path: /x\n    expect:\n      json:\n        - pointer: /a\n          equal: 1\n"), limits.Default()); err == nil {
		t.Fatal("unknown assertion key accepted")
	}
}

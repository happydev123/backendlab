package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/jsonutil"
)

const invoiceSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["id", "total", "line_items"],
  "properties": {
    "id": {"type": "string"},
    "total": {"type": "number", "minimum": 0},
    "line_items": {"type": "array", "items": {"$ref": "#/$defs/item"}}
  },
  "$defs": {
    "item": {"type": "object", "required": ["sku"], "properties": {"sku": {"type": "string"}}}
  }
}`

func TestValidate(t *testing.T) {
	s, err := Compile("invoice.schema.json", []byte(invoiceSchema))
	if err != nil {
		t.Fatal(err)
	}
	good, _ := jsonutil.Decode([]byte(`{"id":"a","total":10.50,"line_items":[{"sku":"x"}]}`))
	if v := s.Validate(good); v != nil {
		t.Fatalf("valid instance reported %v", v)
	}
	bad, _ := jsonutil.Decode([]byte(`{"total":-1,"line_items":[{"sku":1},{}]}`))
	got := s.Validate(bad)
	locs := []string{}
	for _, v := range got {
		locs = append(locs, v.Location+" "+v.Keyword)
	}
	joined := strings.Join(locs, "\n")
	for _, want := range []string{" /required", "/line_items/0/sku", "/line_items/1 ", "/total /properties/total/minimum"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing violation %q in:\n%s", want, joined)
		}
	}
	// Deterministic order.
	again := s.Validate(bad)
	for i := range got {
		if got[i] != again[i] {
			t.Fatal("violation order is not stable")
		}
	}
}

func TestRejectsExternalReferences(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "other.json")
	if err := os.WriteFile(other, []byte(`{"type":"string"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"https://example.com/s.json", "file:///" + filepath.ToSlash(other), "other.json"} {
		doc := `{"$ref": "` + ref + `"}`
		if _, err := Compile("x.json", []byte(doc)); err == nil {
			t.Errorf("reference %q compiled", ref)
		}
	}
}

func TestCompileFileErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"type": 5}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileFile(bad); err == nil {
		t.Fatal("invalid schema compiled")
	}
	if _, err := CompileFile(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("missing file compiled")
	}
	big := filepath.Join(dir, "big.json")
	if err := os.WriteFile(big, []byte(`{"description":"`+strings.Repeat("x", MaxSchemaBytes)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileFile(big); err == nil {
		t.Fatal("oversized schema compiled")
	}
}

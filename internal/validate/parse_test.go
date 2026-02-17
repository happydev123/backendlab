package validate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
)

func TestLoadRulesAllTypes(t *testing.T) {
	path := filepath.Join("testdata", "rules", "all-types.yaml")
	r, err := LoadRules(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Len() != 11 || len(r.doc) != 10 || len(r.batch) != 1 {
		t.Fatalf("rules: len=%d doc=%d batch=%d", r.Len(), len(r.doc), len(r.batch))
	}
	if r.documentID == nil || r.documentID.text != "/invoice_number" || r.SchemaRef() != "" {
		t.Fatalf("top level: %+v", r)
	}
	in, ok := r.Input()
	data, _ := os.ReadFile(path)
	sum := sha256.Sum256(data)
	if !ok || in.Kind != "rules" || in.Name != "testdata/rules/all-types.yaml" || in.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("input = %+v", in)
	}
	pattern := r.doc[2].(*patternRule)
	if pattern.severity != finding.SeverityWarning {
		t.Fatalf("severity = %s", pattern.severity)
	}
	sum2 := r.doc[4].(*sumRule)
	if sum2.tol.text != "0.01" || !sum2.terms[2].negative || !sum2.terms[2].optional {
		t.Fatalf("sum = %+v", sum2)
	}
	if _, ok := (&Rules{}).Input(); ok {
		t.Fatal("in-memory rules report an input")
	}
}

func TestParseRulesErrors(t *testing.T) {
	const head = "version: 1\nrules:\n"
	tests := []struct {
		name, yaml, err string
	}{
		{"empty", "", "empty document"},
		{"missing version", "rules:\n  - {id: a, type: required, fields: [/a]}\n", "version is required"},
		{"future version", "version: 2\nrules:\n  - {id: a, type: required, fields: [/a]}\n", "unsupported rules version 2"},
		{"unknown top-level key", "version: 1\nrulez: []\n", "rulez"},
		{"no rules", "version: 1\n", "no rules and no schema"},
		{"unknown key", head + "  - {id: a, type: required, fields: [/a], colour: red}\n", "colour"},
		{"key of another type", head + "  - {id: a, type: date, fields: [/a], tolerance: 0.01}\n", `key "tolerance" does not apply to rule type date`},
		{"unknown type", head + "  - {id: a, type: checksum, fields: [/a]}\n", `unknown rule type "checksum"`},
		{"missing type", head + "  - {id: a, fields: [/a]}\n", "type is required"},
		{"missing id", head + "  - {type: required, fields: [/a]}\n", "id is required"},
		{"bad id", head + "  - {id: 'a b', type: required, fields: [/a]}\n", "may contain only"},
		{"duplicate id", head + "  - {id: a, type: required, fields: [/a]}\n  - {id: a, type: required, fields: [/b]}\n", `rules[1] (a): duplicate rule id "a"`},
		{"invalid pointer", head + "  - {id: a, type: required, fields: [total]}\n", "must start with '/'"},
		{"invalid escape", head + "  - {id: a, type: sum, terms: [{field: /a~2}], equals: /t}\n", "invalid escape"},
		{"bad document_id", "version: 1\ndocument_id: id\nrules:\n  - {id: a, type: required, fields: [/a]}\n", "document_id"},
		{"wildcard document_id", "version: 1\ndocument_id: /a/*\nrules:\n  - {id: a, type: required, fields: [/a]}\n", "wildcards"},
		{"bad severity", head + "  - {id: a, type: required, fields: [/a], severity: fatal}\n", "unknown severity"},
		{"negative tolerance", head + "  - {id: a, type: sum, terms: [{field: /a}], equals: /t, tolerance: -0.01}\n", "must not be negative"},
		{"float tolerance syntax", head + "  - {id: a, type: sum, terms: [{field: /a}], equals: /t, tolerance: 1e-2}\n", "tolerance must be a decimal string"},
		{"unknown round key", head + "  - {id: a, type: sum, terms: [{field: /a}], equals: /t, round: {scale: 2, mode: half_up, places: 2}}\n", "places"},
		{"unknown term key", head + "  - {id: a, type: sum, terms: [{field: /a, weight: 2}], equals: /t}\n", "weight"},
		{"duplicate yaml key", head + "  - {id: a, id: b, type: required, fields: [/a]}\n", "already defined"},
		{"multiple documents", head + "  - {id: a, type: required, fields: [/a]}\n---\nversion: 1\n", "multiple YAML documents"},
		{"invalid layout", head + "  - {id: a, type: date, fields: [/d], layout: DD.MM.YYYY}\n", "no Go reference-time elements"},
		{"rule is a string", head + "  - required\n", "cannot unmarshal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseRules([]byte(tt.yaml), "")
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Fatalf("err = %v, want %q", err, tt.err)
			}
		})
	}
}

func TestParseRulesBounds(t *testing.T) {
	var b strings.Builder
	b.WriteString("version: 1\nrules:\n")
	for i := 0; i <= MaxRules; i++ {
		fmt.Fprintf(&b, "  - {id: r%d, type: required, fields: [/a]}\n", i)
	}
	if _, err := ParseRules([]byte(b.String()), ""); err == nil || !strings.Contains(err.Error(), "more than 500 rules") {
		t.Fatalf("err = %v", err)
	}
	big := make([]byte, MaxRulesBytes+1)
	if _, err := ParseRules(big, ""); err == nil || !strings.Contains(err.Error(), "exceed") {
		t.Fatalf("err = %v", err)
	}
	long := "version: 1\nrules:\n  - {id: " + strings.Repeat("a", MaxRuleIDLength+1) + ", type: required, fields: [/a]}\n"
	if _, err := ParseRules([]byte(long), ""); err == nil || !strings.Contains(err.Error(), "longer than") {
		t.Fatalf("err = %v", err)
	}
	if _, err := LoadRules(filepath.Join("testdata", "rules", "missing.yaml")); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestParseRulesSchemaOnly(t *testing.T) {
	r, err := ParseRules([]byte("version: 1\nschema: invoice.schema.json\n"), "dir")
	if err != nil {
		t.Fatal(err)
	}
	if r.Len() != 0 || r.SchemaRef() != "invoice.schema.json" || r.baseDir != "dir" {
		t.Fatalf("rules = %+v", r)
	}
}

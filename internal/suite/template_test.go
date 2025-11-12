package suite

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestTemplateExpand(t *testing.T) {
	vars := map[string]string{"id": "a/b c", "n": "42"}
	tests := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"${id}", "a/b c"},
		{"x-${n}-y", "x-42-y"},
		{"$$5 and ${n}", "$5 and 42"},
		{"${n}${n}", "4242"},
		{"", ""},
	}
	for _, tt := range tests {
		got, err := ExpandString(tt.in, vars)
		if err != nil {
			t.Fatalf("ExpandString(%q): %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("ExpandString(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTemplateErrors(t *testing.T) {
	for _, in := range []string{"$x", "${", "${}", "${1a}", "${a b}", "trailing $", "${a-b}"} {
		if _, err := ParseTemplate(in); err == nil {
			t.Errorf("ParseTemplate(%q) succeeded", in)
		}
	}
	_, err := ExpandString("${missing}", map[string]string{})
	if !errors.Is(err, ErrUndefined) {
		t.Fatalf("err = %v, want ErrUndefined", err)
	}
}

func TestTemplateRefs(t *testing.T) {
	tpl, err := ParseTemplate("${a}-${b}-${a}")
	if err != nil {
		t.Fatal(err)
	}
	refs := tpl.Refs()
	if len(refs) != 3 || refs[0] != "a" || refs[1] != "b" {
		t.Fatalf("refs = %v", refs)
	}
}

func TestExpandJSON(t *testing.T) {
	in := map[string]any{
		"id":     "${id}",
		"${id}":  "keys are literal",
		"nested": []any{"x${n}", json.Number("1.50"), true, nil},
	}
	out, err := ExpandJSON(in, map[string]string{"id": "inv-1", "n": "2"})
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["id"] != "inv-1" || m["${id}"] != "keys are literal" {
		t.Fatalf("out = %v", m)
	}
	nested := m["nested"].([]any)
	if nested[0] != "x2" || nested[1] != json.Number("1.50") {
		t.Fatalf("nested = %v", nested)
	}
	if in["id"] != "${id}" {
		t.Fatal("input modified")
	}
	if _, err := ExpandJSON(map[string]any{"a": "${zz}"}, nil); !errors.Is(err, ErrUndefined) {
		t.Fatalf("err = %v", err)
	}
}

func FuzzParseTemplate(f *testing.F) {
	for _, s := range []string{"", "${a}", "$$", "x${y}z", "${", "$"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		tpl, err := ParseTemplate(s)
		if err != nil {
			return
		}
		vars := map[string]string{}
		for _, r := range tpl.Refs() {
			vars[r] = "v"
		}
		if _, err := tpl.Expand(vars); err != nil {
			t.Fatalf("expand with all refs defined failed: %v", err)
		}
	})
}

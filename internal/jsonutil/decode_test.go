package jsonutil

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestDecodeKeepsNumbers(t *testing.T) {
	v, err := Decode([]byte(`{"total": 0.30000000000000004, "big": 12345678901234567890}`))
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if m["total"] != json.Number("0.30000000000000004") || m["big"] != json.Number("12345678901234567890") {
		t.Fatalf("numbers changed: %#v", m)
	}
}

func TestDecodeRejectsTrailingData(t *testing.T) {
	for _, in := range []string{`{} {}`, `[1] x`, `1 2`} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("Decode(%q) succeeded", in)
		}
	}
	if _, err := Decode([]byte("  {}\n\t ")); err != nil {
		t.Errorf("whitespace should be allowed: %v", err)
	}
}

func TestReadAllLimit(t *testing.T) {
	if _, err := ReadAllLimit(strings.NewReader("12345"), 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
	b, err := ReadAllLimit(strings.NewReader("1234"), 4)
	if err != nil || string(b) != "1234" {
		t.Fatalf("got %q, %v", b, err)
	}
	if _, err := ReadAllLimit(strings.NewReader(""), 0); err == nil {
		t.Fatal("zero limit should be rejected")
	}
	if _, err := DecodeReader(strings.NewReader(`{"a":1}`), 3); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("DecodeReader err = %v", err)
	}
}

func TestDecodeStrict(t *testing.T) {
	var dst struct {
		A int `json:"a"`
	}
	if err := DecodeStrict([]byte(`{"a":1,"b":2}`), &dst); err == nil {
		t.Fatal("unknown field accepted")
	}
	if err := DecodeStrict([]byte(`{"a":1}`), &dst); err != nil || dst.A != 1 {
		t.Fatalf("got %v, %v", dst, err)
	}
}

func TestTypeNameAndPreview(t *testing.T) {
	v, _ := Decode([]byte(`[{"a":"x"},1,"s",true,null,[]]`))
	want := []string{"object", "number", "string", "boolean", "null", "array"}
	for i, item := range v.([]any) {
		if got := TypeName(item); got != want[i] {
			t.Errorf("TypeName(%v) = %s, want %s", item, got, want[i])
		}
	}
	if got := Preview(map[string]any{"k": strings.Repeat("x", 50)}, 10); got != `{"k":"xxxx...` {
		t.Errorf("Preview = %q", got)
	}
}

func TestTruncateKeepsUTF8(t *testing.T) {
	s := "aé日本"
	for max := 1; max < len(s); max++ {
		got := Truncate(s, max)
		trimmed := strings.TrimSuffix(got, "...")
		if !strings.HasPrefix(s, trimmed) {
			t.Fatalf("Truncate(%q, %d) = %q is not a prefix", s, max, got)
		}
		for _, r := range trimmed {
			if r == '�' {
				t.Fatalf("Truncate(%q, %d) split a rune: %q", s, max, got)
			}
		}
	}
	if Truncate("abc", 10) != "abc" {
		t.Fatal("short strings must be unchanged")
	}
}

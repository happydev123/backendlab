package stream

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// splitLines runs a lineSplitter over chunks and returns the lines.
func splitLines(t *testing.T, maxLine int, chunks ...string) ([]string, *ParseError) {
	t.Helper()
	s := &lineSplitter{maxLine: maxLine}
	var out []string
	for _, c := range chunks {
		err := s.split([]byte(c), func(line []byte) *ParseError {
			out = append(out, string(line))
			return nil
		})
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func TestLineSplitterEndings(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
		want   []string
	}{
		{"lf", []string{"a\nb\n"}, []string{"a", "b"}},
		{"cr", []string{"a\rb\r"}, []string{"a", "b"}},
		{"crlf", []string{"a\r\nb\r\n"}, []string{"a", "b"}},
		{"cr then lf is two endings", []string{"a\n\rb\r"}, []string{"a", "", "b"}},
		{"crlf split across chunks", []string{"a\r", "\nb\r\n"}, []string{"a", "b"}},
		{"cr at end then cr", []string{"a\r", "\rb\n"}, []string{"a", "", "b"}},
		{"cr at end then empty chunk then lf", []string{"a\r", "", "\nb\n"}, []string{"a", "b"}},
		{"line split across chunks", []string{"da", "ta: x", "\n"}, []string{"data: x"}},
		{"unterminated tail held", []string{"a\nb"}, []string{"a"}},
		{"bom stripped", []string{"\xEF\xBB\xBFa\n"}, []string{"a"}},
		{"bom split", []string{"\xEF", "\xBB", "\xBFa\n"}, []string{"a"}},
		{"second bom kept", []string{"\xEF\xBB\xBF\xEF\xBB\xBFa\n"}, []string{"\xEF\xBB\xBFa"}},
		{"bom after first byte not stripped", []string{"\n\xEF\xBB\xBFa\n"}, []string{"", "\xEF\xBB\xBFa"}},
		{"multibyte split", []string{"\xC3", "\xA9\n"}, []string{"é"}},
		{"four-byte split", []string{"\xF0\x9F", "\x98", "\x80\n"}, []string{"😀"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := splitLines(t, 64, tt.chunks...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) && !(len(got) == 0 && len(tt.want) == 0) {
				t.Fatalf("lines = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLineSplitterPending(t *testing.T) {
	s := &lineSplitter{maxLine: 64}
	noop := func([]byte) *ParseError { return nil }
	if err := s.split([]byte("\xEF\xBB"), noop); err != nil {
		t.Fatal(err)
	}
	if got := s.pending(); got != 2 {
		t.Fatalf("pending BOM prefix = %d, want 2", got)
	}
	s = &lineSplitter{maxLine: 64}
	if err := s.split([]byte("abc\ndef"), noop); err != nil {
		t.Fatal(err)
	}
	if got := s.pending(); got != 3 {
		t.Fatalf("pending = %d, want 3", got)
	}
}

func TestLineSplitterLimits(t *testing.T) {
	// Exactly at the bound is allowed; the terminator does not count.
	if _, err := splitLines(t, 4, "abcd\n"); err != nil {
		t.Fatalf("line at bound: %v", err)
	}
	_, err := splitLines(t, 4, "ab", "cde")
	if err == nil || err.Code != CodeLineTooLong || err.Termination != TermLimitExceeded {
		t.Fatalf("unterminated long line: got %v", err)
	}
	if err.Line != 1 {
		t.Fatalf("line = %d, want 1", err.Line)
	}
}

func TestLineSplitterInvalidUTF8(t *testing.T) {
	got, err := splitLines(t, 64, "ok\n", "bad \xFF\n", "never\n")
	if err == nil || err.Code != CodeInvalidUTF8 || err.Termination != TermInvalidData {
		t.Fatalf("got %v", err)
	}
	if err.Line != 2 || !reflect.DeepEqual(got, []string{"ok"}) {
		t.Fatalf("line %d, lines %q", err.Line, got)
	}
	var target *ParseError
	if !errors.As(error(err), &target) || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("error text %q", err.Error())
	}
}

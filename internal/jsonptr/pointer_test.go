package jsonptr

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/happydev123/backendlab/internal/jsonutil"
)

func TestParseAndString(t *testing.T) {
	tests := []struct {
		in   string
		segs []string
	}{
		{"", nil},
		{"/", []string{""}},
		{"/a/b", []string{"a", "b"}},
		{"/a~1b/c~0d", []string{"a/b", "c~d"}},
		{"/items/0/id", []string{"items", "0", "id"}},
		{"/~01", []string{"~1"}},
	}
	for _, tt := range tests {
		p, err := Parse(tt.in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tt.in, err)
		}
		got := p.Segments()
		if len(got) != len(tt.segs) {
			t.Fatalf("Parse(%q) = %q, want %q", tt.in, got, tt.segs)
		}
		for i := range got {
			if got[i] != tt.segs[i] {
				t.Fatalf("Parse(%q) = %q, want %q", tt.in, got, tt.segs)
			}
		}
		if s := p.String(); s != tt.in {
			t.Errorf("String() = %q, want %q", s, tt.in)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{"a", "a/b", "/a~", "/a~2", "#/a"} {
		if _, err := Parse(in); !errors.Is(err, ErrSyntax) {
			t.Errorf("Parse(%q) error = %v, want ErrSyntax", in, err)
		}
	}
}

func TestGet(t *testing.T) {
	doc, err := jsonutil.Decode([]byte(`{"a":{"b":[10,{"c":null}],"x/y":1},"":2}`))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		ptr   string
		want  any
		found bool
		err   bool
	}{
		{"/a/b/0", json.Number("10"), true, false},
		{"/a/b/1/c", nil, true, false},
		{"/a/b/2", nil, false, false},
		{"/a/x~1y", json.Number("1"), true, false},
		{"/", json.Number("2"), true, false},
		{"/a/missing", nil, false, false},
		{"/a/b/01", nil, false, true},
		{"/a/b/-", nil, false, true},
		{"/a/b/0/deeper", nil, false, false},
	}
	for _, tt := range tests {
		got, found, err := MustParse(tt.ptr).Get(doc)
		if (err != nil) != tt.err {
			t.Errorf("Get(%q) err = %v, want err %v", tt.ptr, err, tt.err)
			continue
		}
		if found != tt.found || got != tt.want {
			t.Errorf("Get(%q) = %v, %v; want %v, %v", tt.ptr, got, found, tt.want, tt.found)
		}
	}
}

func TestAppendDoesNotAlias(t *testing.T) {
	base := MustParse("/a")
	x := base.Append("x")
	y := base.Append("y")
	if x.String() != "/a/x" || y.String() != "/a/y" {
		t.Fatalf("append aliasing: %q %q", x, y)
	}
	if got := base.AppendIndex(3).Append("k/v").String(); got != "/a/3/k~1v" {
		t.Fatalf("got %q", got)
	}
}

func TestPatterns(t *testing.T) {
	ps, err := ParsePatterns([]string{"/items/*/id", "/meta"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		ptr         string
		prefixMatch bool
		exactMatch  bool
	}{
		{"/items/0/id", true, true},
		{"/items/12/id", true, true},
		{"/items/0/name", false, false},
		{"/items/0/id/extra", true, false},
		{"/meta", true, true},
		{"/meta/request_id", true, false},
		{"/metadata", false, false},
		{"/items", false, false},
	}
	for _, tt := range tests {
		p := MustParse(tt.ptr)
		if got := ps.MatchPrefix(p); got != tt.prefixMatch {
			t.Errorf("MatchPrefix(%q) = %v, want %v", tt.ptr, got, tt.prefixMatch)
		}
		if got := ps.Match(p); got != tt.exactMatch {
			t.Errorf("Match(%q) = %v, want %v", tt.ptr, got, tt.exactMatch)
		}
	}
	if _, err := ParsePattern("/a/**"); err == nil {
		t.Error("expected ** to be rejected")
	}
	if got := ps[0].String(); got != "/items/*/id" {
		t.Errorf("pattern String() = %q", got)
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"", "/", "/a/b", "/a~0~1", "/~", "/0/1/2"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		p, err := Parse(s)
		if err != nil {
			return
		}
		again, err := Parse(p.String())
		if err != nil {
			t.Fatalf("reparse of %q failed: %v", p.String(), err)
		}
		if again.String() != p.String() {
			t.Fatalf("round trip changed %q to %q", p.String(), again.String())
		}
	})
}

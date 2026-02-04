package validate

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/jsonutil"
)

func mustDecode(t testing.TB, s string) any {
	t.Helper()
	v, err := jsonutil.Decode([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParseFieldRef(t *testing.T) {
	tests := []struct {
		in, err string
	}{
		{in: "/total"},
		{in: "/a~1b/c~0d"},
		{in: "", err: "is required"},
		{in: "total", err: "must start with '/'"},
		{in: "/line_items/*/amount", err: "wildcards are not allowed"},
		{in: "/a/**", err: "not supported"},
		{in: "/bad~2", err: "invalid escape"},
	}
	for _, tt := range tests {
		f, err := parseFieldRef("equals", tt.in)
		if tt.err != "" {
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Errorf("%q: err = %v, want %q", tt.in, err, tt.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tt.in, err)
			continue
		}
		if f.text != tt.in {
			t.Errorf("%q: text = %q", tt.in, f.text)
		}
	}
	doc := mustDecode(t, `{"a/b":{"c~d":1},"s":"x"}`)
	f, _ := parseFieldRef("f", "/a~1b/c~0d")
	if v, ok := f.lookup(doc); !ok || fmt.Sprint(v) != "1" {
		t.Fatalf("lookup = %v %v", v, ok)
	}
	g, _ := parseFieldRef("f", "/s/x")
	if _, ok := g.lookup(doc); ok {
		t.Fatal("key on a string must be absent")
	}
	h, _ := parseFieldRef("f", "/list/x")
	if _, ok := h.lookup(mustDecode(t, `{"list":[1]}`)); ok {
		t.Fatal("non-index segment on an array must be absent")
	}
}

func TestFieldPatternExpand(t *testing.T) {
	doc := mustDecode(t, `{
		"line_items": [{"amount": "1.00"}, {"amount": "2.00"}, {"other": 1}, "scalar"],
		"totals": {"b": 2, "a": 1},
		"tax": null
	}`)
	tests := []struct {
		pattern string
		want    []string
	}{
		{"/line_items/*/amount", []string{"/line_items/0/amount", "/line_items/1/amount"}},
		{"/totals/*", []string{"/totals/a", "/totals/b"}},
		{"/tax", []string{"/tax"}},
		{"/missing/*", nil},
		{"/line_items/1/amount", []string{"/line_items/1/amount"}},
		{"/line_items/9/amount", nil},
	}
	for _, tt := range tests {
		p, err := parseFieldPattern("fields", tt.pattern)
		if err != nil {
			t.Fatal(err)
		}
		ms, err := p.expand(doc)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, m := range ms {
			got = append(got, m.ptr.String())
		}
		if strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Errorf("%s: got %v, want %v", tt.pattern, got, tt.want)
		}
	}
}

func TestFieldPatternBounds(t *testing.T) {
	items := make([]any, MaxMatches+1)
	for i := range items {
		items[i] = map[string]any{"a": "1"}
	}
	p, _ := parseFieldPattern("fields", "/items/*/a")
	if _, err := p.expand(map[string]any{"items": items}); !errors.Is(err, errTooManyMatches) {
		t.Fatalf("err = %v", err)
	}
	if _, err := parseFieldPatterns("fields", nil); err == nil {
		t.Fatal("empty list accepted")
	}
	if _, err := parseFieldPatterns("fields", []string{"/a", "/a"}); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("duplicate accepted: %v", err)
	}
	if _, err := parseFieldPatterns("fields", []string{""}); err == nil {
		t.Fatal("root pointer accepted")
	}
	many := make([]string, MaxFieldsPerRule+1)
	for i := range many {
		many[i] = fmt.Sprintf("/f%d", i)
	}
	if _, err := parseFieldPatterns("fields", many); err == nil {
		t.Fatal("over-long list accepted")
	}
}

func TestFieldPatternSplit(t *testing.T) {
	p, _ := parseFieldPattern("fields", "/line_items/*/amount")
	parent, member, err := p.split()
	if err != nil || member != "amount" || len(parent.segs) != 2 || !parent.hasWildcard() {
		t.Fatalf("split = %+v %q %v", parent, member, err)
	}
	q, _ := parseFieldPattern("fields", "/line_items/*")
	if _, _, err := q.split(); err == nil {
		t.Fatal("trailing wildcard accepted")
	}
}

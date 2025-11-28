package compare

import (
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/suite"
)

func TestBodies(t *testing.T) {
	cases := []struct {
		name      string
		base, cnd string
		spec      suite.CompareSpec
		want      string
	}{
		{"json formatting ignored", `{"a": 1,  "b": [1, 2]}`, "{\"b\":[1,2],\n\"a\":1.0}", suite.CompareSpec{}, ""},
		{"json value changed", `{"a":1}`, `{"a":2}`, suite.CompareSpec{}, "compare.value_changed@/a"},
		{"json scalar root", `"x"`, `"y"`, suite.CompareSpec{}, "compare.value_changed@body"},
		{"format changed", `{"a":1}`, `<html>error</html>`, suite.CompareSpec{}, "compare.body_format_changed@body"},
		{"json to empty", `[]`, ``, suite.CompareSpec{}, "compare.body_format_changed@body"},
		{"text equal", `plain text`, `plain text`, suite.CompareSpec{}, ""},
		{"text changed", `plain text`, `plain text!`, suite.CompareSpec{}, "compare.body_changed@body"},
		{"both empty", ``, ``, suite.CompareSpec{}, ""},
		{"trailing data is text", `{"a":1} x`, `{"a":1} y`, suite.CompareSpec{}, "compare.body_changed@body"},
		{"ignore applies to json bodies", `{"id":1}`, `{"id":2}`, suite.CompareSpec{Ignore: []string{"/id"}}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Bodies([]byte(c.base), []byte(c.cnd), specOptions(t, c.spec))
			if got := summary(r); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestBodyDescribe(t *testing.T) {
	r := Bodies([]byte("abc"), []byte("abcd"), Options{})
	d := r.Differences[0]
	if !strings.HasPrefix(d.Baseline, "text, 3 bytes, sha256 ba7816bf8f01cfea") {
		t.Fatalf("baseline description %q", d.Baseline)
	}
	if !strings.HasPrefix(d.Candidate, "text, 4 bytes, sha256 ") {
		t.Fatalf("candidate description %q", d.Candidate)
	}
	r = Bodies([]byte(`{}`), nil, Options{})
	if d := r.Differences[0]; !strings.HasPrefix(d.Baseline, "JSON, 2 bytes") || d.Candidate != "empty" {
		t.Fatalf("format descriptions %+v", d)
	}
}

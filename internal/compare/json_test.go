package compare

import (
	"fmt"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/suite"
)

func mustDecode(t testing.TB, s string) any {
	t.Helper()
	v, err := jsonutil.Decode([]byte(s))
	if err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	return v
}

// summary renders differences as "code@location" in order.
func summary(r Result) string {
	parts := make([]string, len(r.Differences))
	for i, d := range r.Differences {
		parts[i] = d.Code + "@" + d.Location
	}
	return strings.Join(parts, " ")
}

func specOptions(t *testing.T, spec suite.CompareSpec) Options {
	t.Helper()
	o, err := OptionsFromSpec(spec, limits.Default(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestJSONStructural(t *testing.T) {
	cases := []struct {
		name      string
		base, cnd string
		spec      suite.CompareSpec
		want      string
		within    int
	}{
		{name: "equal", base: `{"a":1,"b":[1,2]}`, cnd: `{"a":1,"b":[1,2]}`, want: ""},
		{name: "key order ignored", base: `{"a":1,"b":2}`, cnd: `{"b":2,"a":1}`, want: ""},
		{name: "decimal equal", base: `{"amount":1.0}`, cnd: `{"amount":1.00}`, want: ""},
		{name: "field missing", base: `{"a":1,"b":2}`, cnd: `{"a":1}`, want: "compare.field_missing@/b"},
		{name: "field added", base: `{"a":1}`, cnd: `{"a":1,"c":true}`, want: "compare.field_added@/c"},
		{name: "null vs absent", base: `{"a":null}`, cnd: `{}`, want: "compare.null_vs_absent@/a"},
		{name: "absent vs null", base: `{}`, cnd: `{"a":null}`, want: "compare.null_vs_absent@/a"},
		{name: "null vs value", base: `{"a":null}`, cnd: `{"a":0}`, want: "compare.type_changed@/a"},
		{name: "type changed", base: `{"a":"1"}`, cnd: `{"a":1}`, want: "compare.type_changed@/a"},
		{name: "root type", base: `[]`, cnd: `{}`, want: "compare.type_changed@body"},
		{name: "string changed", base: `{"s":"x"}`, cnd: `{"s":"y"}`, want: "compare.value_changed@/s"},
		{name: "bool changed", base: `[true]`, cnd: `[false]`, want: "compare.value_changed@/0"},
		{name: "number changed", base: `{"n":1}`, cnd: `{"n":2}`, want: "compare.value_changed@/n"},
		{name: "array order matters", base: `[1,2]`, cnd: `[2,1]`, want: "compare.value_changed@/0 compare.value_changed@/1"},
		{name: "array length", base: `{"x":[1,2,3]}`, cnd: `{"x":[1,9]}`, want: "compare.array_length_changed@/x compare.value_changed@/x/1"},
		{name: "escaped pointer", base: `{"a/b":{"c~d":1}}`, cnd: `{"a/b":{"c~d":2}}`, want: "compare.value_changed@/a~1b/c~0d"},
		{
			name: "ignore exact", base: `{"id":"1","v":1}`, cnd: `{"id":"2","v":1}`,
			spec: suite.CompareSpec{Ignore: []string{"/id"}}, want: "",
		},
		{
			name: "ignore prefix", base: `{"meta":{"rid":"a","at":1}}`, cnd: `{"meta":{"rid":"b"}}`,
			spec: suite.CompareSpec{Ignore: []string{"/meta"}}, want: "",
		},
		{
			name: "ignore wildcard", base: `{"items":[{"id":1,"ts":"a"},{"id":2,"ts":"b"}]}`,
			cnd:  `{"items":[{"id":1,"ts":"c"},{"id":2}]}`,
			spec: suite.CompareSpec{Ignore: []string{"/items/*/ts"}}, want: "",
		},
		{
			name: "ignore does not cover siblings", base: `{"items":[{"ts":"a","n":1}]}`, cnd: `{"items":[{"ts":"b","n":2}]}`,
			spec: suite.CompareSpec{Ignore: []string{"/items/*/ts"}}, want: "compare.value_changed@/items/0/n",
		},
		{
			name: "abs tolerance", base: `{"p":10.00,"q":5}`, cnd: `{"p":10.004,"q":5.5}`,
			spec: suite.CompareSpec{Tolerance: &suite.Tolerance{Absolute: "0.01"}},
			want: "compare.value_changed@/q", within: 1,
		},
		{
			name: "rel tolerance", base: `[100, 100]`, cnd: `[104, 106]`,
			spec: suite.CompareSpec{Tolerance: &suite.Tolerance{Relative: "0.05"}},
			want: "compare.value_changed@/1", within: 1,
		},
		{
			name: "path tolerance overrides global", base: `{"latency":100,"total":10}`, cnd: `{"latency":140,"total":10.5}`,
			spec: suite.CompareSpec{
				Tolerance:      &suite.Tolerance{Absolute: "1"},
				PathTolerances: []suite.PathTolerance{{Pointer: "/latency", Tolerance: suite.Tolerance{Relative: "0.5"}}},
			},
			want: "", within: 2,
		},
		{
			name: "path tolerance is stricter", base: `{"a":{"x":1},"b":1}`, cnd: `{"a":{"x":1.5},"b":1.5}`,
			spec: suite.CompareSpec{
				Tolerance:      &suite.Tolerance{Absolute: "1"},
				PathTolerances: []suite.PathTolerance{{Pointer: "/a", Tolerance: suite.Tolerance{Absolute: "0.1"}}},
			},
			want: "compare.value_changed@/a/x", within: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := JSON(mustDecode(t, c.base), mustDecode(t, c.cnd), specOptions(t, c.spec))
			if got := summary(r); got != c.want {
				t.Fatalf("differences:\n got  %s\n want %s", got, c.want)
			}
			if r.WithinTolerance != c.within {
				t.Fatalf("within tolerance = %d, want %d", r.WithinTolerance, c.within)
			}
		})
	}
}

func TestJSONUnordered(t *testing.T) {
	spec := suite.CompareSpec{Unordered: []suite.UnorderedArray{{Pointer: "/items", Key: "id"}}}
	cases := []struct {
		name      string
		base, cnd string
		want      string
	}{
		{"reordered equal", `{"items":[{"id":1,"v":"a"},{"id":2,"v":"b"}]}`, `{"items":[{"id":2,"v":"b"},{"id":1,"v":"a"}]}`, ""},
		{"matched element differs", `{"items":[{"id":1,"v":"a"},{"id":2,"v":"b"}]}`, `{"items":[{"id":2,"v":"x"},{"id":1,"v":"a"}]}`, "compare.value_changed@/items/1/v"},
		{"missing and added", `{"items":[{"id":1},{"id":2}]}`, `{"items":[{"id":3},{"id":1}]}`, "compare.element_missing@/items/1 compare.element_added@/items/0"},
		{"duplicate keys", `{"items":[{"id":1},{"id":1}]}`, `{"items":[{"id":1}]}`, "compare.unordered_unmatchable@/items"},
		{"missing key", `{"items":[{"id":1},{"name":"x"}]}`, `{"items":[{"name":"x"},{"id":1}]}`, "compare.unordered_unmatchable@/items"},
		{"non-scalar key", `{"items":[{"id":[1]}]}`, `{"items":[{"id":[2]}]}`, "compare.unordered_unmatchable@/items"},
		{"unusable key but identical", `{"items":[{"id":1},{"id":1}]}`, `{"items":[{"id":1},{"id":1}]}`, ""},
		{"other arrays stay ordered", `{"tags":[1,2]}`, `{"tags":[2,1]}`, "compare.value_changed@/tags/0 compare.value_changed@/tags/1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := JSON(mustDecode(t, c.base), mustDecode(t, c.cnd), specOptions(t, spec))
			if got := summary(r); got != c.want {
				t.Fatalf("differences:\n got  %s\n want %s", got, c.want)
			}
			if strings.Contains(c.want, "unmatchable") && !r.Inconclusive() {
				t.Fatal("unmatchable array must be inconclusive")
			}
		})
	}
}

func TestJSONUnorderedWildcard(t *testing.T) {
	spec := suite.CompareSpec{Unordered: []suite.UnorderedArray{{Pointer: "/orders/*/lines", Key: "sku"}}}
	base := `{"orders":[{"lines":[{"sku":"a","q":1},{"sku":"b","q":2}]}]}`
	cnd := `{"orders":[{"lines":[{"sku":"b","q":2},{"sku":"a","q":1}]}]}`
	if r := JSON(mustDecode(t, base), mustDecode(t, cnd), specOptions(t, spec)); !r.Equal() {
		t.Fatalf("unexpected differences: %s", summary(r))
	}
}

func TestJSONBounds(t *testing.T) {
	var b, c strings.Builder
	b.WriteString("{")
	c.WriteString("{")
	for i := 0; i < 50; i++ {
		if i > 0 {
			b.WriteString(",")
			c.WriteString(",")
		}
		fmt.Fprintf(&b, `"k%02d":%d`, i, i)
		fmt.Fprintf(&c, `"k%02d":%d`, i, i+1)
	}
	b.WriteString("}")
	c.WriteString("}")
	r := JSON(mustDecode(t, b.String()), mustDecode(t, c.String()), Options{MaxDifferences: 10})
	if len(r.Differences) != 10 || r.Dropped != 40 {
		t.Fatalf("kept %d dropped %d", len(r.Differences), r.Dropped)
	}
	if r.Differences[0].Location != "/k00" {
		t.Fatalf("differences not in sorted key order: %s", r.Differences[0].Location)
	}

	long := strings.Repeat("x", 500)
	r = JSON(map[string]any{"s": long}, map[string]any{"s": long + "y"}, Options{MaxValuePreview: 40})
	if len(r.Differences) != 1 || len(r.Differences[0].Baseline) > 43 {
		t.Fatalf("preview not bounded: %+v", r.Differences)
	}
}

func TestJSONDepthBound(t *testing.T) {
	deep := func(leaf any) any {
		v := leaf
		for i := 0; i < maxDepth+5; i++ {
			v = []any{v}
		}
		return v
	}
	r := JSON(deep("a"), deep("b"), Options{})
	if got := summary(r); !strings.HasPrefix(got, "compare.too_deep@") || !r.Inconclusive() {
		t.Fatalf("got %s", got)
	}
}

func TestJSONRedaction(t *testing.T) {
	red, err := redact.New(redact.Policy{JSONPaths: []string{"/customer/tax_id"}})
	if err != nil {
		t.Fatal(err)
	}
	red = red.WithSecrets("sk-live-1234567")
	base := `{"customer":{"tax_id":"111","name":"A"},"token":"t1","balance":{"secret":10},"note":"key sk-live-1234567"}`
	cnd := `{"customer":{"tax_id":"222","name":"B"},"token":"t2","balance":{"secret":99},"note":"key sk-live-1234567!"}`
	r := JSON(mustDecode(t, base), mustDecode(t, cnd), Options{Redactor: red})
	if len(r.Differences) != 5 {
		t.Fatalf("differences: %s", summary(r))
	}
	for _, d := range r.Differences {
		text := d.Baseline + d.Candidate + d.Message
		for _, leak := range []string{"111", "222", "t1", "t2", "99", "sk-live"} {
			if strings.Contains(text, leak) {
				t.Errorf("%s leaks %q: %+v", d.Location, leak, d)
			}
		}
		switch d.Location {
		case "/customer/tax_id", "/token", "/balance/secret":
			if d.Baseline != redact.Marker || d.Candidate != redact.Marker {
				t.Errorf("%s not redacted: %+v", d.Location, d)
			}
		case "/customer/name":
			if d.Baseline != `"A"` || d.Candidate != `"B"` {
				t.Errorf("%s over-redacted: %+v", d.Location, d)
			}
		}
	}
}

func TestEqual(t *testing.T) {
	if !Equal(mustDecode(t, `{"a":[1.0,{"b":null}]}`), mustDecode(t, `{"a":[1,{"b":null}]}`)) {
		t.Fatal("expected equal")
	}
	if Equal(mustDecode(t, `{"a":null}`), mustDecode(t, `{}`)) {
		t.Fatal("null and absent must differ")
	}
}

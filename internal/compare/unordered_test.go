package compare

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func obj(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func TestMatchByKey(t *testing.T) {
	base := []any{obj("id", "a", "v", 1), obj("id", "b"), obj("id", "c")}
	cand := []any{obj("id", "c"), obj("id", "d"), obj("id", "a")}
	m, problem := matchByKey(base, cand, "id")
	if problem != "" {
		t.Fatal(problem)
	}
	if want := []keyedPair{{0, 2}, {2, 0}}; !reflect.DeepEqual(m.pairs, want) {
		t.Errorf("pairs = %v", m.pairs)
	}
	if !reflect.DeepEqual(m.missing, []int{1}) || !reflect.DeepEqual(m.added, []int{1}) {
		t.Errorf("missing = %v added = %v", m.missing, m.added)
	}
}

func TestMatchByKeyNumericIdentity(t *testing.T) {
	base := []any{obj("id", json.Number("7")), obj("id", true)}
	cand := []any{obj("id", true), obj("id", json.Number("7.0"))}
	m, problem := matchByKey(base, cand, "id")
	if problem != "" {
		t.Fatal(problem)
	}
	if len(m.pairs) != 2 || len(m.missing) != 0 || len(m.added) != 0 {
		t.Fatalf("match = %+v", m)
	}
	// A string "7" is a different key from the number 7.
	m, _ = matchByKey([]any{obj("id", "7")}, []any{obj("id", json.Number("7"))}, "id")
	if len(m.pairs) != 0 {
		t.Fatal("string and number keys matched")
	}
}

func TestMatchByKeyProblems(t *testing.T) {
	cases := []struct {
		name       string
		base, cand []any
		want       string
	}{
		{"not object", []any{"x"}, nil, "baseline element 0 is string"},
		{"missing key", []any{obj("id", "a")}, []any{obj("name", "a")}, `candidate element 0 has no member "id"`},
		{"object key", []any{obj("id", obj())}, nil, `has a object "id"`},
		{"null key", []any{obj("id", nil)}, nil, `has a null "id"`},
		{"duplicate baseline", []any{obj("id", "a"), obj("id", "a")}, nil, `baseline elements 0 and 1 share`},
		{"duplicate candidate", nil, []any{obj("id", json.Number("1")), obj("id", json.Number("1.0"))}, `candidate elements 0 and 1 share`},
	}
	for _, c := range cases {
		_, problem := matchByKey(c.base, c.cand, "id")
		if !strings.Contains(problem, c.want) {
			t.Errorf("%s: problem = %q, want substring %q", c.name, problem, c.want)
		}
	}
}

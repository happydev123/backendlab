package validate

import (
	"fmt"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
)

func runBatchRule(t *testing.T, r batchRule, ids []string, docs []any) map[int][]finding.Finding {
	t.Helper()
	out := map[int][]finding.Finding{}
	r.checkBatch(&batchCheck{ids: ids, docs: docs, san: newSanitizer(nil, 0), emit: func(i int, f finding.Finding) {
		out[i] = append(out[i], f)
	}})
	return out
}

func TestUniqueRule(t *testing.T) {
	r, err := compileUnique(newBase("unique-number", typeUnique), ruleSpec{Field: "/invoice_number"})
	if err != nil {
		t.Fatal(err)
	}
	docs := []any{
		mustDecode(t, `{"invoice_number":"INV-1"}`),
		mustDecode(t, `{"invoice_number":"INV-2"}`),
		mustDecode(t, `{"invoice_number":"INV-1"}`),
		mustDecode(t, `{"invoice_number":null}`),
		mustDecode(t, `{}`),
		mustDecode(t, `{"invoice_number":null}`),
		mustDecode(t, `{"invoice_number":"INV-1"}`),
	}
	ids := []string{"a", "b", "c", "d", "e", "f", "g"}
	got := runBatchRule(t, r, ids, docs)
	if len(got) != 3 || len(got[0]) != 1 || len(got[2]) != 1 || len(got[6]) != 1 {
		t.Fatalf("findings = %+v", got)
	}
	f := got[2][0]
	if f.Code != "validate.unique" || f.Location != "/invoice_number" || f.Observed != `"INV-1"` ||
		f.Expected != "/invoice_number unique within the batch" || !strings.HasSuffix(f.Message, "also appears in a, g") {
		t.Fatalf("finding = %+v", f)
	}
}

func TestUniqueKeyTypes(t *testing.T) {
	r, _ := compileUnique(newBase("u", typeUnique), ruleSpec{Field: "/id"})
	tests := []struct {
		a, b string
		dup  bool
	}{
		{`{"id":1001}`, `{"id":"1001"}`, false},
		{`{"id":1.0}`, `{"id":1}`, true},
		{`{"id":10}`, `{"id":1e1}`, true},
		{`{"id":true}`, `{"id":"true"}`, false},
		{`{"id":{"x":1,"y":2}}`, `{"id":{"y":2,"x":1}}`, true},
		{`{"id":"a"}`, `{"id":"A"}`, false},
	}
	for _, tt := range tests {
		got := runBatchRule(t, r, []string{"x", "y"}, []any{mustDecode(t, tt.a), mustDecode(t, tt.b)})
		if (len(got) == 2) != tt.dup {
			t.Errorf("%s vs %s: findings %v, want duplicate=%v", tt.a, tt.b, got, tt.dup)
		}
	}
	if _, err := compileUnique(newBase("u", typeUnique), ruleSpec{Field: "/items/*/id"}); err == nil {
		t.Fatal("wildcard accepted")
	}
}

func TestListIDsBounded(t *testing.T) {
	ids := make([]string, 13)
	for i := range ids {
		ids[i] = fmt.Sprintf("d%d", i)
	}
	if got := listIDs(ids); !strings.HasSuffix(got, "d9 and 3 more") {
		t.Fatalf("listIDs = %q", got)
	}
}

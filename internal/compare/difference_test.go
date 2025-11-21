package compare

import (
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/jsonptr"
	"github.com/happydev123/backendlab/internal/redact"
)

func TestDiffListBound(t *testing.T) {
	l := newDiffList(2)
	if !l.empty() {
		t.Fatal("new list not empty")
	}
	for i := 0; i < 5; i++ {
		l.add(Difference{Code: CodeValueChanged})
	}
	if len(l.items) != 2 || l.dropped != 3 {
		t.Fatalf("items=%d dropped=%d", len(l.items), l.dropped)
	}
	other := newDiffList(10)
	other.add(Difference{Code: CodeFieldAdded})
	other.dropped = 4
	l.merge(other)
	if len(l.items) != 2 || l.dropped != 8 {
		t.Fatalf("after merge items=%d dropped=%d", len(l.items), l.dropped)
	}
	r := resultOf(l, 3)
	if r.Equal() || r.WithinTolerance != 3 || r.Dropped != 8 {
		t.Fatalf("result = %+v", r)
	}
}

func TestResultHelpers(t *testing.T) {
	r := Result{Differences: []Difference{{Code: CodeTypeChanged}, {Code: CodeUnorderedUnmatchable, Inconclusive: true}}}
	if !r.Inconclusive() {
		t.Fatal("expected inconclusive")
	}
	if got := strings.Join(r.Codes(), ","); got != "compare.type_changed,compare.unordered_unmatchable" {
		t.Fatalf("codes = %s", got)
	}
	if !(Result{}).Equal() {
		t.Fatal("empty result must be equal")
	}
	if (Result{Dropped: 1}).Equal() {
		t.Fatal("dropped differences are not equal")
	}
}

func TestPreviewRedaction(t *testing.T) {
	r, err := redact.New(redact.Policy{JSONPaths: []string{"/customer/tax_id"}})
	if err != nil {
		t.Fatal(err)
	}
	r = r.WithSecrets("hunter2-secret")
	pv := previewer{r: r, max: 20}

	if got := pv.value(jsonptr.MustParse("/customer/tax_id"), "123-45"); got != redact.Marker {
		t.Errorf("configured path: %q", got)
	}
	if got := pv.value(jsonptr.MustParse("/auth/token/value"), "abc"); got != redact.Marker {
		t.Errorf("sensitive key ancestor: %q", got)
	}
	if got := pv.value(jsonptr.MustParse("/note"), "pw is hunter2-secret"); strings.Contains(got, "hunter2") {
		t.Errorf("secret not scrubbed: %q", got)
	}
	obj := map[string]any{"password": "x", "name": "n"}
	if got := pv.value(jsonptr.MustParse("/user"), obj); strings.Contains(got, `"x"`) {
		t.Errorf("nested sensitive key not redacted: %q", got)
	}
	long := strings.Repeat("a", 100)
	if got := pv.value(jsonptr.MustParse("/long"), long); len(got) > 23 || !strings.HasSuffix(got, "...") {
		t.Errorf("preview not truncated: %q", got)
	}
	if got := pv.header("Authorization", "Bearer abc", true); got != redact.Marker {
		t.Errorf("sensitive header: %q", got)
	}
	if got := pv.header("X-Trace", "", false); got != absent {
		t.Errorf("absent header: %q", got)
	}
	if got := pv.text("hunter2-secret leaked"); strings.Contains(got, "hunter2") {
		t.Errorf("text not scrubbed: %q", got)
	}
}

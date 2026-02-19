package validate

import (
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/redact"
)

const batchRules = `
version: 1
document_id: /number
rules:
  - id: totals
    type: sum
    terms: [{field: /subtotal}, {field: /tax}]
    equals: /total
  - id: unique-number
    type: unique
    field: /number
`

func batchValidator(t *testing.T, opts Options) *Validator {
	t.Helper()
	r, err := ParseRules([]byte(batchRules), "")
	if err != nil {
		t.Fatal(err)
	}
	v, err := New(r, nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func docsOf(t *testing.T, jsons ...string) []Document {
	t.Helper()
	out := make([]Document, len(jsons))
	for i, s := range jsons {
		out[i] = Document{Index: i, Value: mustDecode(t, s)}
	}
	return out
}

func TestIdentify(t *testing.T) {
	v := batchValidator(t, Options{})
	docs := docsOf(t,
		`{"number":"INV-1"}`,
		`{"number":"INV-2"}`,
		`{"number":"INV-1"}`,
		`{}`,
		`{"number":1003}`,
		`{"number":""}`,
		`{"number":{"x":1}}`,
		`{"number":"#3"}`,
	)
	docs[1].ID = "preset"
	got := v.Identify(docs)
	want := []string{"INV-1", "preset", "INV-1#2", "#3", "1003", "#5", "#6", "#3#7"}
	for i, d := range got {
		if d.ID != want[i] {
			t.Errorf("doc %d: id %q, want %q", i, d.ID, want[i])
		}
	}
	if docs[0].ID != "" {
		t.Fatal("Identify modified its input")
	}
	red, _ := redact.New(redact.Policy{JSONPaths: []string{"/number"}})
	if id := batchValidator(t, Options{Redactor: red}).Identify(docsOf(t, `{"number":"secret"}`))[0].ID; id != "#0" {
		t.Fatalf("redacted id leaked: %q", id)
	}
	long := strings.Repeat("x", 300)
	if id := v.Identify(docsOf(t, `{"number":"`+long+`"}`))[0].ID; len(id) > MaxIDLength+3 {
		t.Fatalf("id not bounded: %d", len(id))
	}
}

func TestRunBatchDuplicatesAndTotals(t *testing.T) {
	v := batchValidator(t, Options{})
	res := v.Run(docsOf(t,
		`{"number":"INV-1","subtotal":"0.10","tax":"0.20","total":"0.30"}`,
		`{"number":"INV-2","subtotal":"1.00","tax":"0.19","total":"1.20"}`,
		`{"number":"INV-1","subtotal":"5","tax":"1","total":"6"}`,
		`{"number":"INV-3","subtotal":"5"}`,
	))
	got := make([]string, len(res.Findings))
	for i, f := range res.Findings {
		got[i] = f.CaseID + ":" + f.Code
	}
	want := "INV-1:validate.unique INV-1#2:validate.unique INV-2:validate.sum INV-3:validate.input_missing"
	if strings.Join(got, " ") != want {
		t.Fatalf("findings = %v", got)
	}
	if res.Dropped != 0 || len(res.Documents) != 4 || res.Documents[2].ID != "INV-1#2" {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(res.Findings[0].Message, "also appears in INV-1#2") {
		t.Fatalf("message = %q", res.Findings[0].Message)
	}
	// Document never runs batch rules.
	if fs := v.Document("x", mustDecode(t, `{"number":"INV-1","subtotal":"1","tax":"1","total":"2"}`)); len(fs) != 0 {
		t.Fatalf("document: %+v", fs)
	}
}

func TestRunBoundKeepsOneFindingPerDocument(t *testing.T) {
	v := batchValidator(t, Options{MaxFindings: 3})
	docs := docsOf(t,
		`{"number":"A","subtotal":"x","tax":"y","total":"1"}`,
		`{"number":"A","subtotal":"x","tax":"1","total":"1"}`,
		`{"number":"B","subtotal":"1","tax":"1","total":"3"}`,
	)
	res := v.Run(docs)
	cases := map[string]bool{}
	for _, f := range res.Findings {
		if f.Failing() {
			cases[f.CaseID] = true
		}
	}
	if len(res.Findings) != 3 || len(cases) != 3 || res.Dropped != 2 {
		t.Fatalf("findings = %+v dropped=%d", res.Findings, res.Dropped)
	}
	fs := v.Batch(docs)
	last := fs[len(fs)-1]
	if last.Code != CodeTruncated || last.Kind != finding.KindObservation || !strings.Contains(last.Message, "2 further findings") {
		t.Fatalf("batch truncation = %+v", last)
	}
}

func TestSelectFindingsUnderLimit(t *testing.T) {
	per := [][]finding.Finding{
		{{CaseID: "b", Code: "x", Severity: finding.SeverityError}},
		{{CaseID: "a", Code: "y", Severity: finding.SeverityWarning}},
	}
	out, dropped := selectFindings(per, 10)
	if dropped != 0 || len(out) != 2 || out[0].CaseID != "a" {
		t.Fatalf("out = %+v", out)
	}
}

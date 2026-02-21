package validate

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/report"
)

var update = flag.Bool("update", false, "rewrite golden files")

var exampleDir = filepath.Join("..", "..", "examples", "invoice")

// exampleReport validates the invoice example batch the way the CLI does.
func exampleReport(t *testing.T) (*report.Report, Result) {
	t.Helper()
	rules, err := LoadRules(filepath.Join(exampleDir, "invoice-rules.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := New(rules, nil, Options{ConfineFiles: true})
	if err != nil {
		t.Fatal(err)
	}
	docs, docsInput, err := LoadDocuments(filepath.Join(exampleDir, "invoices-batch.json"), limits.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	rulesInput, ok := rules.Input()
	if !ok {
		t.Fatal("rules input missing")
	}
	// Digests depend on line endings in the checkout; golden files do
	// not record them (LoadRules and LoadDocuments tests cover digests).
	rulesInput.SHA256, docsInput.SHA256 = "", ""
	rulesInput.Name, docsInput.Name = "examples/invoice/invoice-rules.yaml", "examples/invoice/invoices-batch.json"
	res := v.Run(docs)
	r := BuildReport([]report.Input{rulesInput, docsInput}, res.Documents, res.Findings, res.Dropped)
	r.Tool.Version = "test"
	return r, res
}

func TestInvoiceExample(t *testing.T) {
	r, res := exampleReport(t)
	if r.Status != report.StatusFailed || r.ExitCode() != 1 {
		t.Fatalf("status = %s", r.Status)
	}
	var got []string
	for _, c := range r.Cases {
		got = append(got, c.ID+"="+string(c.Status))
	}
	want := "INV-1001=failed INV-1002=failed INV-1003=passed INV-1001#3=failed"
	if strings.Join(got, " ") != want {
		t.Fatalf("cases = %v", got)
	}
	got = got[:0]
	for _, f := range res.Findings {
		got = append(got, f.CaseID+":"+f.Code+"@"+f.Location)
	}
	want = "INV-1001:validate.unique@/invoice_number INV-1001#3:validate.unique@/invoice_number INV-1002:validate.sum@/total"
	if strings.Join(got, " ") != want {
		t.Fatalf("findings = %v", got)
	}
	sum := res.Findings[2]
	if sum.Expected != "/subtotal + /tax - /discount (optional) = /total (tolerance 0.01)" ||
		!strings.Contains(sum.Message, "computed 179.96 from /subtotal (149.97) + /tax (29.99); stated /total is 180.00; difference 0.04 exceeds tolerance 0.01") {
		t.Fatalf("sum finding = %+v", sum)
	}
}

// mixedReport covers schema violations, redaction, inconclusive cases and
// control characters in document IDs.
func mixedReport(t *testing.T) *report.Report {
	t.Helper()
	rules, err := LoadRules(filepath.Join("testdata", "schema", "rules.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	red, err := redact.New(redact.Policy{JSONPaths: []string{"/card_number"}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := New(rules, nil, Options{Redactor: red})
	if err != nil {
		t.Fatal(err)
	}
	docs, in, err := LoadDocuments(filepath.Join("testdata", "documents", "mixed.jsonl"), limits.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	in.SHA256 = ""
	res := v.Run(docs)
	r := BuildReport([]report.Input{in}, res.Documents, res.Findings, res.Dropped)
	r.Tool.Version = "test"
	return r
}

func TestReportGolden(t *testing.T) {
	invoice, _ := exampleReport(t)
	reports := map[string]*report.Report{"invoice-batch": invoice, "mixed": mixedReport(t)}
	for _, name := range []string{"invoice-batch", "mixed"} {
		for _, format := range []report.Format{report.FormatText, report.FormatJSON, report.FormatMarkdown} {
			var buf bytes.Buffer
			if err := report.Render(&buf, reports[name], format, report.Options{}); err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", "golden", name+"."+string(format)+".golden")
			if *update {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				continue
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run go test -run Golden -update)", err)
			}
			want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
			if !bytes.Equal(buf.Bytes(), want) {
				t.Errorf("%s output differs from %s:\n%s", format, golden, buf.String())
			}
		}
	}
}

func TestMixedReportContent(t *testing.T) {
	r := mixedReport(t)
	var got []string
	for _, c := range r.Cases {
		got = append(got, c.ID+"="+string(c.Status))
	}
	want := "A-1=passed A-2=inconclusive 7=failed A-\x1b[31m4=passed"
	if strings.Join(got, " ") != want {
		t.Fatalf("cases = %q", got)
	}
	for _, f := range r.Findings {
		if strings.Contains(f.Message+f.Observed+f.Expected, "4111") {
			t.Fatalf("redacted value leaked: %+v", f)
		}
	}
}

func TestBuildReportStatuses(t *testing.T) {
	docs := []Document{{ID: "a", Index: 0}, {ID: "b", Index: 1}, {ID: "c", Index: 2}, {Index: 3}}
	fs := []finding.Finding{
		{Module: finding.ModuleValidate, Code: "validate.sum", Kind: finding.KindAssertionFailure, Severity: finding.SeverityError, CaseID: "a", Message: "m"},
		{Module: finding.ModuleValidate, Code: CodeInputMissing, Kind: finding.KindInconclusive, Severity: finding.SeverityWarning, CaseID: "a", Message: "m"},
		{Module: finding.ModuleValidate, Code: CodeInputMissing, Kind: finding.KindInconclusive, Severity: finding.SeverityWarning, CaseID: "b", Message: "m"},
		{Module: finding.ModuleValidate, Code: "validate.pattern", Kind: finding.KindAssertionFailure, Severity: finding.SeverityWarning, CaseID: "c", Message: "m"},
	}
	r := BuildReport(nil, docs, fs, 0)
	want := []report.CaseStatus{report.CaseFailed, report.CaseInconclusive, report.CasePassed, report.CasePassed}
	for i, c := range r.Cases {
		if c.Status != want[i] {
			t.Errorf("case %s = %s, want %s", c.ID, c.Status, want[i])
		}
	}
	if r.Cases[3].ID != "#3" || r.Command != "validate" || len(r.Inputs) != 0 || r.Inputs == nil {
		t.Fatalf("report = %+v", r)
	}
	if len(r.Limitations) != 1 || r.Limitations[0] != Limitation {
		t.Fatalf("limitations = %v", r.Limitations)
	}

	inc := BuildReport(nil, docs[1:2], fs[2:3], 0)
	if inc.Status != report.StatusPassed {
		t.Fatalf("inconclusive-only report = %s", inc.Status)
	}
	dropped := BuildReport(nil, docs[:1], fs[:1], 5)
	if dropped.Dropped != 5 || len(dropped.Limitations) != 2 || !strings.Contains(dropped.Limitations[1], "5 findings were omitted") {
		t.Fatalf("dropped report = %+v", dropped)
	}
}

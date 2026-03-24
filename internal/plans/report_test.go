package plans

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/report"
)

var update = flag.Bool("update", false, "rewrite golden files")

// regressionReport builds the report for the index-to-seq-scan fixtures.
// Input names are fixed and the tool version is pinned so the golden files
// do not depend on the checkout path, line endings or build flags.
func regressionReport(t *testing.T, opts Options) *report.Report {
	t.Helper()
	var inputs []report.Input
	var parsed []*Plan
	for _, name := range []string{"regress_before.json", "regress_after.json"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		p, err := Parse(data, name, limits.Default())
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, p)
		inputs = append(inputs, report.Input{Kind: InputKind, Name: name})
	}
	r := Compare(parsed[0], parsed[1], opts).Report(inputs)
	r.Tool.Version = "test"
	return r
}

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create): %v", err)
	}
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from golden file\n--- got ---\n%s", name, got)
	}
}

func TestReportGolden(t *testing.T) {
	failOn, err := ParseFailOn("scan,spill")
	if err != nil {
		t.Fatal(err)
	}
	r := regressionReport(t, Options{FailOn: failOn})
	for _, f := range []struct {
		format report.Format
		file   string
	}{
		{report.FormatJSON, "regress.json.golden"},
		{report.FormatMarkdown, "regress.md.golden"},
		{report.FormatText, "regress.txt.golden"},
	} {
		var buf bytes.Buffer
		if err := report.Render(&buf, r, f.format, report.Options{}); err != nil {
			t.Fatal(err)
		}
		checkGolden(t, f.file, buf.Bytes())
	}
}

func TestReportStatusAndShape(t *testing.T) {
	r := regressionReport(t, Options{})
	if r.Command != Command || r.Schema != report.SchemaVersion || r.Timing != nil {
		t.Fatalf("report header = %s %s %v", r.Command, r.Schema, r.Timing)
	}
	if r.Status != report.StatusPassed || r.ExitCode() != 0 {
		t.Fatalf("default status = %s", r.Status)
	}
	if len(r.Inputs) != 2 || r.Inputs[0].Name != "regress_before.json" {
		t.Fatalf("inputs = %+v", r.Inputs)
	}
	if r.Summary.Findings.Total != len(r.Findings) || r.Summary.Findings.Failing != 0 {
		t.Fatalf("summary = %+v", r.Summary.Findings)
	}
	failOn, _ := ParseFailOn("scan")
	failed := regressionReport(t, Options{FailOn: failOn})
	if failed.Status != report.StatusFailed || failed.ExitCode() != 1 || failed.Summary.Findings.Failing != 1 {
		t.Fatalf("fail-on status = %s failing %d", failed.Status, failed.Summary.Findings.Failing)
	}
	var buf bytes.Buffer
	if err := report.RenderJSON(&buf, r); err != nil {
		t.Fatal(err)
	}
	back, err := report.Decode(buf.Bytes())
	if err != nil || len(back.Findings) != len(r.Findings) {
		t.Fatalf("round trip: %v", err)
	}
	if strings.Contains(buf.String(), "started_at") {
		t.Fatal("offline report contains timestamps")
	}
}

func TestReportDropped(t *testing.T) {
	r := regressionReport(t, Options{MaxFindings: 2})
	if r.Dropped != 8 || len(r.Findings) != 2 {
		t.Fatalf("dropped = %d findings = %d", r.Dropped, len(r.Findings))
	}
	var nilInputs *Comparison = Compare(nil, nil, Options{})
	rr := nilInputs.Report(nil)
	if rr.Inputs == nil || rr.Findings == nil || rr.Status != report.StatusPassed {
		t.Fatalf("empty report = %+v", rr)
	}
}

func TestReportEscapesHostileNames(t *testing.T) {
	hostile := "inv\x1b[31m|oices](http://x)"
	p := planOf(t, "b", jn("Index Scan", `"Relation Name": "`+strings.ReplaceAll(hostile, "\x1b", `\u001b`)+`", "Index Name": "i_idx"`))
	q := planOf(t, "a", jn("Seq Scan", `"Relation Name": "`+strings.ReplaceAll(hostile, "\x1b", `\u001b`)+`"`))
	r := Compare(p, q, Options{}).Report(nil)
	var text, md bytes.Buffer
	if err := report.RenderText(&text, r, report.Options{}); err != nil {
		t.Fatal(err)
	}
	if err := report.RenderMarkdown(&md, r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text.String(), "\x1b") || strings.Contains(md.String(), "\x1b") {
		t.Fatal("escape sequence reached rendered output")
	}
	if strings.Contains(md.String(), "](http://x)") {
		t.Fatal("markdown link syntax not escaped")
	}
}

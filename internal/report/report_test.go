package report

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
)

var update = flag.Bool("update", false, "rewrite golden files")

func sampleReport() *Report {
	r := New("compare")
	r.Tool.Version = "test"
	r.Inputs = []Input{{Kind: "suite", Name: "examples/invoice.yaml", SHA256: "0123456789abcdef0123456789abcdef"}}
	r.Targets = []string{"baseline", "candidate"}
	r.Cases = []Case{
		{ID: "get-invoice", Status: CaseFailed, Observations: []Observation{
			{Target: "baseline", StatusCode: 200, DurationMS: ptr(12.5)},
			{Target: "candidate", StatusCode: 200, DurationMS: ptr(1520)},
		}},
		{ID: "list|invoices", Status: CaseError, Observations: []Observation{
			{Target: "baseline", Error: "connection refused"},
		}},
		{ID: "health", Status: CasePassed},
	}
	r.AddFindings(
		finding.Finding{Module: finding.ModuleCompare, Code: "compare.value_changed", Kind: finding.KindDifference,
			Severity: finding.SeverityError, CaseID: "get-invoice", Location: "/total",
			Message: "value changed", Expected: "100.00", Observed: "100.05"},
		finding.Finding{Module: finding.ModuleReplay, Code: "replay.connection_failed", Kind: finding.KindTransportError,
			Severity: finding.SeverityError, CaseID: "list|invoices", Message: "dial failed\x1b[31m",
			Details: map[string]string{"target": "baseline"}},
		finding.Finding{Module: finding.ModuleCompare, Code: "compare.unordered_key_missing", Kind: finding.KindInconclusive,
			Severity: finding.SeverityWarning, CaseID: "get-invoice", Location: "/items",
			Message: "element without key `sku` | [link](http://x)"},
	)
	r.AddLimitation("Single-run timings are observations, not statistically established regressions.")
	r.AddLimitation("Single-run timings are observations, not statistically established regressions.")
	r.Finalize()
	return r
}

func ptr(f float64) *float64 { return &f }

func TestFinalizeStatusAndSummary(t *testing.T) {
	r := sampleReport()
	if r.Status != StatusFailed {
		t.Fatalf("status = %s", r.Status)
	}
	if r.ExitCode() != 1 {
		t.Fatalf("exit = %d", r.ExitCode())
	}
	if r.Summary.Cases != 3 || r.Summary.CasesByStatus[CaseError] != 1 {
		t.Fatalf("summary = %+v", r.Summary)
	}
	if len(r.Limitations) != 1 {
		t.Fatalf("limitations not deduplicated: %v", r.Limitations)
	}
	if r.Findings[0].CaseID != "get-invoice" || r.Findings[0].Location != "/items" {
		t.Fatalf("findings not sorted: %+v", r.Findings[0])
	}

	p := New("plans.compare")
	p.Finalize()
	if p.Status != StatusPassed || p.ExitCode() != 0 {
		t.Fatalf("empty report = %s/%d", p.Status, p.ExitCode())
	}
	e := New("run")
	e.Fail(StatusError, "suite not found")
	e.Finalize()
	if e.Status != StatusError || e.ExitCode() != 2 {
		t.Fatalf("error report = %s/%d", e.Status, e.ExitCode())
	}
	c := New("run")
	c.Status = StatusCancelled
	c.Finalize()
	if c.ExitCode() != 2 {
		t.Fatalf("cancelled exit = %d", c.ExitCode())
	}
}

func TestGolden(t *testing.T) {
	for _, f := range []Format{FormatText, FormatJSON, FormatMarkdown} {
		var buf bytes.Buffer
		if err := Render(&buf, sampleReport(), f, Options{}); err != nil {
			t.Fatal(err)
		}
		golden := filepath.Join("testdata", "sample."+string(f)+".golden")
		if *update {
			if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%v (run with -update to create)", err)
		}
		if !bytes.Equal(normalize(buf.Bytes()), normalize(want)) {
			t.Errorf("%s output differs from %s:\n%s", f, golden, buf.String())
		}
	}
}

func normalize(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }

func TestTextHasNoControlCharacters(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderText(&buf, sampleReport(), Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "\x1b") {
		t.Fatal("escape character leaked into uncolored text output")
	}
	buf.Reset()
	if err := RenderText(&buf, sampleReport(), Options{Color: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), ansiRed) {
		t.Fatal("expected color codes when enabled")
	}
	if strings.Contains(buf.String(), "\x1b[31m\n") || strings.Contains(buf.String(), "failed\x1b[31m") {
		t.Fatal("input escape sequence was not neutralized")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderJSON(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	r, err := Decode(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != StatusFailed || len(r.Findings) != 3 {
		t.Fatalf("decoded = %+v", r)
	}
	if _, err := Decode([]byte(`{"schema":"other/v9"}`)); err == nil {
		t.Fatal("expected schema mismatch error")
	}
}

func TestParseFormat(t *testing.T) {
	for in, want := range map[string]Format{"": FormatText, "TEXT": FormatText, "json": FormatJSON, "md": FormatMarkdown, "markdown": FormatMarkdown} {
		got, err := ParseFormat(in)
		if err != nil || got != want {
			t.Errorf("ParseFormat(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseFormat("html"); err == nil {
		t.Error("expected error for html")
	}
}

package stream

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/schema"
)

var update = flag.Bool("update", false, "rewrite golden report files")

func TestInspectGoldenReports(t *testing.T) {
	sch, err := schema.CompileFile(filepath.Join("testdata", "schemas", "chunk.json"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		file string
		exp  Expectations
	}{
		{"chat.sse", Expectations{JSONPayloads: true, TerminalData: "[DONE]", RequireTerminal: true, MaxGap: time.Second, Adapter: AdapterChatDelta}},
		{"truncated.sse", Expectations{Schema: sch, TerminalData: "[DONE]", RequireTerminal: true, RequiredEvents: []string{"start", "end"}}},
		{"chat.jsonl", Expectations{TerminalEvent: "done", RequiredEvents: []string{"delta"}, MaxGap: 500 * time.Millisecond, Adapter: AdapterChatDelta}},
		{"stopped.jsonl", Expectations{TerminalData: "[DONE]", MaxDuration: 5 * time.Second}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			res, err := InspectFile(filepath.Join("testdata", tt.file), tt.exp, Options{CaseID: "golden"})
			if err != nil {
				t.Fatal(err)
			}
			rep := res.Report(report.Input{Kind: "stream", Name: tt.file})
			rep.Tool.Version = "test" // keep golden files independent of the build version
			var buf bytes.Buffer
			if err := report.RenderJSON(&buf, rep); err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", "golden", tt.file+".json")
			if *update {
				if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create)", err)
			}
			if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), buf.Bytes()) {
				t.Fatalf("report differs from %s:\n%s", golden, buf.String())
			}
			// The rendered document must decode as a valid report.
			if _, err := report.Decode(buf.Bytes()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReportStatus(t *testing.T) {
	tests := []struct {
		name   string
		res    Result
		status report.Status
		cs     report.CaseStatus
		exit   int
	}{
		{"passed", Result{Termination: TermEOF}, report.StatusPassed, report.CasePassed, 0},
		{"inconclusive does not fail", Result{Termination: TermEOF, Findings: []finding.Finding{
			{Module: finding.ModuleStream, Code: CodeTimingUnavailable, Kind: finding.KindInconclusive, Severity: finding.SeverityWarning, Message: "m"},
		}}, report.StatusPassed, report.CaseInconclusive, 0},
		{"failing finding", Result{Termination: TermEOF, Findings: []finding.Finding{
			{Module: finding.ModuleStream, Code: CodeMissingTerminal, Kind: finding.KindAssertionFailure, Severity: finding.SeverityError, Message: "m"},
		}}, report.StatusFailed, report.CaseFailed, 1},
		{"cancelled", Result{Termination: TermCancelled}, report.StatusCancelled, report.CaseCancelled, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep := tt.res.Report(report.Input{Kind: "stream", Name: "x"})
			if rep.Status != tt.status || rep.Cases[0].Status != tt.cs || rep.ExitCode() != tt.exit {
				t.Fatalf("status=%s case=%s exit=%d", rep.Status, rep.Cases[0].Status, rep.ExitCode())
			}
			if rep.Command != CommandInspect || rep.Timing != nil || rep.Cases[0].ID != "stream" {
				t.Fatalf("report = %+v", rep)
			}
		})
	}
}

func TestReportObservation(t *testing.T) {
	d := func(v time.Duration) *time.Duration { return &v }
	res := Result{
		Termination: TermTimeout, EventCount: 3, Bytes: 42, Timed: true, CaseID: "c1",
		FirstEvent: d(1500 * time.Microsecond), Duration: d(2 * time.Second),
		Gaps:            &GapSummary{Count: 2, Max: 30 * time.Millisecond, P50: 10 * time.Millisecond},
		FindingsDropped: 4,
	}
	rep := res.Report(report.Input{Kind: "stream", Name: "live"})
	obs := rep.Cases[0].Observations[0]
	if *obs.FirstEventMS != 1.5 || *obs.DurationMS != 2000 || obs.Events != 3 || obs.ResponseBytes != 42 || obs.Error != "timeout" {
		t.Fatalf("observation = %+v", obs)
	}
	if rep.Cases[0].ID != "c1" || rep.Dropped != 4 || len(rep.Limitations) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	var summary *finding.Finding
	for i := range rep.Findings {
		if rep.Findings[i].Code == CodeSummary {
			summary = &rep.Findings[i]
		}
	}
	if summary == nil || summary.Details["gap_max_ms"] != "30" || summary.Details["first_event_ms"] != "1.5" || summary.CaseID != "c1" {
		t.Fatalf("summary = %+v", summary)
	}
}

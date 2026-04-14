package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/store"
)

func testFinding(code string) finding.Finding {
	return finding.Finding{
		Module: finding.ModuleCompare, Code: code, Kind: finding.KindDifference,
		Severity: finding.SeverityError, CaseID: "c1", Message: "value changed",
	}
}

func TestCaseRecordValidation(t *testing.T) {
	var b Bounds
	for name, u := range map[string]CaseUpdate{
		"empty id":       {Index: 0, Status: "passed"},
		"negative index": {CaseID: "c", Index: -1, Status: "passed"},
		"bad status":     {CaseID: "c", Status: "ok"},
		"observations":   {CaseID: "c", Status: "passed", Observations: make([]report.Observation, maxObservations+1)},
	} {
		if _, err := b.CaseRecord(u); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCaseRecordBoundsFindingsAndBodies(t *testing.T) {
	r := redact.Default().WithSecrets("s3cr3t-token")
	b := Bounds{MaxCaseFindings: 2, RetainBodies: true, MaxBodyBytes: 16, Redactor: r}
	rec, err := b.CaseRecord(CaseUpdate{
		CaseID: "c1", Index: 3, Status: "failed",
		Findings:     []finding.Finding{testFinding("compare.c"), testFinding("compare.a"), testFinding("compare.b")},
		Observations: []report.Observation{{Target: "staging", Error: "dial with s3cr3t-token failed"}},
		Body:         map[string]string{"staging": "token=s3cr3t-token and a long tail of text", "prod": "short"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Findings) != 2 || rec.FindingsDropped != 1 || rec.Findings[0].Code != "compare.a" {
		t.Errorf("findings = %+v, dropped %d", rec.Findings, rec.FindingsDropped)
	}
	if strings.Contains(rec.Observations[0].Error, "s3cr3t") {
		t.Errorf("observation error not redacted: %q", rec.Observations[0].Error)
	}
	if rec.Bodies["prod"] != "short" {
		t.Errorf("short body = %q", rec.Bodies["prod"])
	}
	long := rec.Bodies["staging"]
	if strings.Contains(long, "s3cr3t") || !strings.Contains(long, "[body truncated: 16 of") {
		t.Errorf("long body = %q", long)
	}

	b.RetainBodies = false
	rec, err = b.CaseRecord(CaseUpdate{CaseID: "c1", Status: "passed", Body: map[string]string{"x": "y"}})
	if err != nil || rec.Bodies != nil {
		t.Errorf("bodies kept without retention: %v, %v", rec.Bodies, err)
	}
}

func bigReport(findings int) *report.Report {
	rep := report.New("run")
	rep.Cases = []report.Case{{ID: "c1", Status: report.CaseFailed}}
	for i := 0; i < findings; i++ {
		rep.AddFindings(testFinding(fmt.Sprintf("compare.rule_%04d", i)))
	}
	rep.Finalize()
	return rep
}

func TestEncodeReportWithinLimit(t *testing.T) {
	rep := bigReport(2)
	enc, err := Bounds{MaxReportBytes: 1 << 20}.encodeReport(rep)
	if err != nil {
		t.Fatal(err)
	}
	if enc.Truncated || enc.Note != "" {
		t.Errorf("small report truncated: %+v", enc)
	}
	back, err := report.Decode(enc.Data)
	if err != nil || len(back.Findings) != 2 {
		t.Errorf("decoded = %+v, %v", back, err)
	}
	var s report.Summary
	if err := json.Unmarshal(enc.Summary, &s); err != nil || s.Findings.Total != 2 {
		t.Errorf("summary = %+v, %v", s, err)
	}
}

func TestEncodeReportOverLimitKeepsSummary(t *testing.T) {
	rep := bigReport(200)
	enc, err := Bounds{MaxReportBytes: 4096}.encodeReport(rep)
	if err != nil {
		t.Fatal(err)
	}
	if !enc.Truncated || enc.Data == nil || len(enc.Data) > 4096 {
		t.Fatalf("enc: truncated=%v len=%d", enc.Truncated, len(enc.Data))
	}
	if !strings.Contains(enc.Note, "exceeded max_report_bytes") {
		t.Errorf("note = %q", enc.Note)
	}
	back, err := report.Decode(enc.Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Findings) != 0 || back.Cases != nil || back.Dropped != 200 || back.Summary.Findings.Total != 200 {
		t.Errorf("reduced report = %+v", back)
	}
	if len(back.Limitations) == 0 || !strings.Contains(back.Limitations[len(back.Limitations)-1], "reduced to a summary") {
		t.Errorf("limitations = %v", back.Limitations)
	}

	enc, err = Bounds{MaxReportBytes: 64}.encodeReport(rep)
	if err != nil {
		t.Fatal(err)
	}
	if enc.Data != nil || !enc.Truncated || !strings.Contains(enc.Note, "no report stored") {
		t.Errorf("tiny limit: %+v", enc)
	}
}

func TestBuildOutcome(t *testing.T) {
	b := Bounds{Redactor: redact.Default().WithSecrets("hunter22")}
	withStatus := func(s report.Status, msg string) *report.Report {
		rep := report.New("run")
		rep.Status, rep.Error = s, msg
		return rep
	}
	cases := []struct {
		name      string
		rep       *report.Report
		err       error
		cancelled bool
		want      store.Status
		errSub    string
	}{
		{"passed", withStatus(report.StatusPassed, ""), nil, false, store.StatusSucceeded, ""},
		{"passed despite cancel", withStatus(report.StatusPassed, ""), nil, true, store.StatusSucceeded, ""},
		{"failed", withStatus(report.StatusFailed, ""), nil, false, store.StatusFailed, ""},
		{"cancelled", withStatus(report.StatusCancelled, ""), nil, true, store.StatusCancelled, "operator"},
		{"error", withStatus(report.StatusError, "suite invalid"), nil, false, store.StatusFailed, "status error: suite invalid"},
		{"incomplete", withStatus(report.StatusIncomplete, ""), nil, false, store.StatusFailed, "incomplete"},
		{"exec error", nil, errors.New("connect with hunter22 refused"), false, store.StatusFailed, "execution failed"},
		{"exec error after cancel", nil, errors.New("context canceled"), true, store.StatusCancelled, "operator"},
		{"no report", nil, nil, false, store.StatusFailed, "no report"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, err := buildOutcome(b, tc.rep, tc.err, tc.cancelled)
			if err != nil {
				t.Fatal(err)
			}
			if o.Status != tc.want || !strings.Contains(o.Error, tc.errSub) {
				t.Errorf("outcome = %s %q, want %s containing %q", o.Status, o.Error, tc.want, tc.errSub)
			}
			if strings.Contains(o.Error, "hunter22") {
				t.Errorf("secret in error: %q", o.Error)
			}
			if tc.rep != nil && o.Report == nil {
				t.Error("report not encoded")
			}
		})
	}
}

package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/internal/store/storetest"
)

func TestRecordCaseIsFenced(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	q := NewQueue(db, newTestClock().Now)
	enqueue(t, db, false, 1)
	r := mustClaim(t, q, "w1", time.Minute)

	rec := CaseRecord{CaseID: "c1", Index: 0, Status: report.CaseFailed,
		Findings: []finding.Finding{testFinding("compare.value_changed")}, FindingsDropped: 2,
		Observations: []report.Observation{{Target: "staging", StatusCode: 500}}}
	if err := q.RecordCase(ctx, r.ID, r.LeaseToken, rec); err != nil {
		t.Fatal(err)
	}
	rec.Status = report.CasePassed
	rec.Findings = nil
	if err := q.RecordCase(ctx, r.ID, r.LeaseToken, rec); err != nil {
		t.Fatalf("re-recording the same case: %v", err)
	}
	if err := q.RecordCase(ctx, r.ID, r.LeaseToken+1, CaseRecord{CaseID: "c2", Index: 1, Status: report.CasePassed}); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("write with a future token: err = %v", err)
	}
	if err := q.RecordCase(ctx, r.ID, r.LeaseToken-1, CaseRecord{CaseID: "c2", Index: 1, Status: report.CasePassed}); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("write with an old token: err = %v", err)
	}

	results, err := db.ListCaseResults(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v", results)
	}
	got := results[0]
	if got.Status != report.CasePassed || len(got.Findings) != 0 || got.WrittenToken != r.LeaseToken ||
		got.Observations[0].StatusCode != 500 || got.Bodies != nil {
		t.Errorf("stored result = %+v", got)
	}
}

func TestCompleteIsFencedAndFinal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	clock := newTestClock()
	q := NewQueue(db, clock.Now)
	enqueue(t, db, false, 1)
	r := mustClaim(t, q, "w1", time.Minute)

	if err := q.Complete(ctx, r.ID, r.LeaseToken, Outcome{Status: store.StatusInterrupted}); err == nil {
		t.Error("Complete accepted interrupted")
	}
	if err := q.Complete(ctx, r.ID, r.LeaseToken+1, Outcome{Status: store.StatusSucceeded}); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("complete with a wrong token: err = %v", err)
	}

	o, err := buildOutcome(Bounds{}, bigReport(1), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(5 * time.Second)
	if err := q.Complete(ctx, r.ID, r.LeaseToken, o); err != nil {
		t.Fatal(err)
	}
	got := getRun(t, db, r.ID)
	if got.Status != store.StatusFailed || got.FinishedAt == nil || !got.FinishedAt.Equal(clock.Now()) ||
		got.LeaseOwner != "" || got.LeaseExpiresAt != nil || got.Summary == nil || got.Summary.Findings.Total != 1 {
		t.Errorf("completed run = %+v", got)
	}
	data, err := db.GetReport(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := report.Decode(data); err != nil {
		t.Errorf("stored report: %v", err)
	}

	// After completion every fenced write is rejected.
	if err := q.Complete(ctx, r.ID, r.LeaseToken, Outcome{Status: store.StatusSucceeded}); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("second completion: err = %v", err)
	}
	if err := q.RecordCase(ctx, r.ID, r.LeaseToken, CaseRecord{CaseID: "late", Status: report.CasePassed}); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("late case write: err = %v", err)
	}
	if _, err := q.Heartbeat(ctx, r.ID, r.LeaseToken, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("heartbeat after completion: err = %v", err)
	}
	if _, err := q.RequestCancel(ctx, r.ID); !errors.Is(err, ErrNotCancellable) {
		t.Errorf("cancel after completion: err = %v", err)
	}
}

func TestCompleteStoresBoundedReport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := storetest.New(t)
	q := NewQueue(db, nil)
	enqueue(t, db, false, 1)
	r := mustClaim(t, q, "w1", time.Minute)

	rep := bigReport(300)
	rep.Status = report.StatusPassed // completed run with only informational output
	o, err := buildOutcome(Bounds{MaxReportBytes: 8192}, rep, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Complete(ctx, r.ID, r.LeaseToken, o); err != nil {
		t.Fatal(err)
	}
	got := getRun(t, db, r.ID)
	if got.Status != store.StatusSucceeded || !got.ReportTruncated || !strings.Contains(got.Error, "max_report_bytes") {
		t.Errorf("run = status %s truncated %v error %q", got.Status, got.ReportTruncated, got.Error)
	}
	data, err := db.GetReport(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 8192 {
		t.Errorf("stored report is %d bytes, over the 8192 bound", len(data))
	}
	back, err := report.Decode(data)
	if err != nil || back.Summary.Findings.Total != 300 || len(back.Findings) != 0 {
		t.Errorf("stored report = %+v, %v", back, err)
	}
}

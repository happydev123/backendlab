package compare

import (
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/suite"
)

func TestFindings(t *testing.T) {
	o := specOptions(t, suite.CompareSpec{
		Tolerance: &suite.Tolerance{Absolute: "0.5"},
		Unordered: []suite.UnorderedArray{{Pointer: "/list", Key: "id"}},
	})
	o.MaxDifferences = 2
	base := mustDecode(t, `{"a":1,"b":"x","c":true,"d":1,"list":[{"id":1},{"id":1}]}`)
	cand := mustDecode(t, `{"a":5,"b":"y","c":false,"d":1.2,"list":[{"id":2}]}`)
	r := JSON(base, cand, o)
	ctx := Context{CaseID: "get-invoice", BaselineTarget: "prod", CandidateTarget: "staging"}
	fs := r.Findings(ctx)
	if len(fs) != 4 {
		t.Fatalf("got %d findings: %+v", len(fs), fs)
	}
	for _, f := range fs {
		if err := f.Validate(); err != nil {
			t.Fatal(err)
		}
		if f.Module != finding.ModuleCompare || f.CaseID != "get-invoice" {
			t.Fatalf("bad identity: %+v", f)
		}
		if f.Details["baseline_target"] != "prod" || f.Details["candidate_target"] != "staging" {
			t.Fatalf("targets missing: %+v", f.Details)
		}
	}
	first := fs[0]
	if first.Kind != finding.KindDifference || first.Severity != finding.SeverityError ||
		first.Location != "/a" || first.Expected != "1" || first.Observed != "5" {
		t.Fatalf("first finding: %+v", first)
	}
	dropped := fs[2]
	if dropped.Code != CodeDifferencesDropped || dropped.Details["dropped"] != "2" || !dropped.Failing() {
		t.Fatalf("dropped finding: %+v", dropped)
	}
	within := fs[3]
	if within.Code != CodeWithinTolerance || within.Kind != finding.KindObservation || within.Failing() ||
		within.Details["count"] != "1" {
		t.Fatalf("within finding: %+v", within)
	}
}

func TestFindingsInconclusive(t *testing.T) {
	r := Result{Differences: []Difference{{Code: CodeUnorderedUnmatchable, Location: "/list", Message: "m", Inconclusive: true}}}
	fs := r.Findings(Context{CaseID: "c"})
	if len(fs) != 1 || fs[0].Kind != finding.KindInconclusive || fs[0].Failing() {
		t.Fatalf("got %+v", fs)
	}
	if len(fs[0].Details) != 0 {
		t.Fatalf("empty targets must not produce details: %+v", fs[0].Details)
	}
	if fs := (Result{}).Findings(Context{}); len(fs) != 0 {
		t.Fatalf("equal result produced findings: %+v", fs)
	}
}

package validate

import (
	"fmt"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/report"
)

// Limitation is added to every validation report.
const Limitation = "Passing validation shows that the documents are consistent with the configured schema and rules; " +
	"it does not establish that they are authentic or factually correct, or that the extraction matched the source document."

// BuildReport assembles the "validate" report: one case per document in
// input order, the findings, and the dropped count. A case fails when it
// has a failing (error severity) finding, is inconclusive when it has an
// inconclusive finding but no failing one, and passes otherwise. The
// report is finalized and contains no timestamps, so identical input
// yields identical output.
func BuildReport(inputs []report.Input, docs []Document, findings []finding.Finding, dropped int) *report.Report {
	r := report.New("validate")
	r.Inputs = append(r.Inputs, inputs...)

	failing := map[string]bool{}
	inconclusive := map[string]bool{}
	for _, f := range findings {
		if f.Failing() {
			failing[f.CaseID] = true
		}
		if f.Kind == finding.KindInconclusive {
			inconclusive[f.CaseID] = true
		}
	}
	for _, d := range docs {
		id := d.ID
		if id == "" {
			id = fallbackID(d.Index)
		}
		status := report.CasePassed
		switch {
		case failing[id]:
			status = report.CaseFailed
		case inconclusive[id]:
			status = report.CaseInconclusive
		}
		r.Cases = append(r.Cases, report.Case{
			ID:          id,
			Description: fmt.Sprintf("document %d", d.Index),
			Status:      status,
		})
	}
	r.AddFindings(findings...)
	r.Dropped = dropped
	r.AddLimitation(Limitation)
	if dropped > 0 {
		r.AddLimitation(fmt.Sprintf("%d findings were omitted after the configured limit; case statuses reflect the retained findings only.", dropped))
	}
	r.Finalize()
	return r
}

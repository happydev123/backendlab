package dashboard

import (
	"sort"

	"github.com/happydev123/backendlab/internal/catalog"
	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/httpapi"
	"github.com/happydev123/backendlab/internal/plans"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/store"
)

// findingRow is one finding with its outcome label. Class only selects a
// CSS style; the label text carries the meaning.
type findingRow struct {
	F     finding.Finding
	Label string
	Class string
}

// Outcome labels, in display order.
const (
	labelFailure      = "Failure"
	labelInconclusive = "Inconclusive"
	labelUnsupported  = "Unsupported"
	labelWarning      = "Warning"
	labelObservation  = "Observation"
	labelNote         = "Note"
)

var labelOrder = []string{labelFailure, labelInconclusive, labelUnsupported, labelWarning, labelObservation, labelNote}

// classify separates failures (severity error) from analysis that could
// not conclude and from facts that are not judged.
func classify(f finding.Finding) findingRow {
	row := findingRow{F: f}
	switch {
	case f.Severity == finding.SeverityError:
		row.Label, row.Class = labelFailure, "failure"
	case f.Kind == finding.KindInconclusive:
		row.Label, row.Class = labelInconclusive, "inconclusive"
	case f.Kind == finding.KindUnsupported:
		row.Label, row.Class = labelUnsupported, "inconclusive"
	case f.Kind == finding.KindObservation:
		row.Label, row.Class = labelObservation, "observation"
	case f.Severity == finding.SeverityWarning:
		row.Label, row.Class = labelWarning, "warning"
	default:
		row.Label, row.Class = labelNote, "observation"
	}
	return row
}

// findingGroup holds the findings of one module.
type findingGroup struct {
	Module string
	Title  string
	Rows   []findingRow
}

var moduleOrder = []finding.Module{
	finding.ModuleReplay, finding.ModuleCompare, finding.ModuleStream, finding.ModuleValidate, finding.ModulePlans,
}

var moduleTitles = map[finding.Module]string{
	finding.ModuleReplay:   "Requests and assertions",
	finding.ModuleCompare:  "HTTP differences",
	finding.ModuleStream:   "Stream findings",
	finding.ModuleValidate: "Validation findings",
	finding.ModulePlans:    "Query plan findings",
}

// groupFindings groups the findings of all cases by module, in a fixed
// module order (unknown modules last, by name), keeping case order inside
// each group.
func groupFindings(cases []store.CaseResult) []findingGroup {
	byModule := map[finding.Module][]findingRow{}
	for _, c := range cases {
		for _, f := range c.Findings {
			byModule[f.Module] = append(byModule[f.Module], classify(f))
		}
	}
	order := append([]finding.Module(nil), moduleOrder...)
	var extra []finding.Module
	for m := range byModule {
		if _, known := moduleTitles[m]; !known {
			extra = append(extra, m)
		}
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i] < extra[j] })
	order = append(order, extra...)

	var groups []findingGroup
	for _, m := range order {
		rows := byModule[m]
		if len(rows) == 0 {
			continue
		}
		title := moduleTitles[m]
		if title == "" {
			title = "Findings from " + string(m)
		}
		groups = append(groups, findingGroup{Module: string(m), Title: title, Rows: rows})
	}
	return groups
}

// labelCount is a labelled count for summary lines.
type labelCount struct {
	Label string
	Count int
}

func outcomeCounts(groups []findingGroup) []labelCount {
	n := map[string]int{}
	for _, g := range groups {
		for _, r := range g.Rows {
			n[r.Label]++
		}
	}
	var out []labelCount
	for _, l := range labelOrder {
		if n[l] > 0 {
			out = append(out, labelCount{Label: l, Count: n[l]})
		}
	}
	return out
}

var caseOrder = []report.CaseStatus{
	report.CasePassed, report.CaseFailed, report.CaseError, report.CaseInconclusive, report.CaseSkipped, report.CaseCancelled,
}

func caseCounts(cases []store.CaseResult) []labelCount {
	n := map[report.CaseStatus]int{}
	for _, c := range cases {
		n[c.Status]++
	}
	var out []labelCount
	for _, s := range caseOrder {
		if n[s] > 0 {
			out = append(out, labelCount{Label: caseLabel(s), Count: n[s]})
		}
	}
	return out
}

type bodyView struct {
	Target string
	Body   string
}

type caseView struct {
	Index        int
	ID           string
	Status       report.CaseStatus
	Observations []report.Observation
	FindingCount int
	Dropped      int
	Bodies       []bodyView
}

// runDetailView is the content of the run page.
type runDetailView struct {
	Run             *store.Run
	ResubmittedFrom string
	Active          bool
	CancelPending   bool
	Cancellable     bool
	Resubmittable   bool
	HasReport       bool
	Cases           []caseView
	CaseCounts      []labelCount
	Groups          []findingGroup
	OutcomeCounts   []labelCount
}

func newRunDetailView(d *httpapi.RunDetail) runDetailView {
	r := d.Run
	v := runDetailView{
		Run:             r,
		ResubmittedFrom: d.ResubmittedFrom,
		Active:          !r.Status.Terminal(),
		CancelPending:   r.Status == store.StatusRunning && r.CancelRequested(),
		Resubmittable:   r.Status == store.StatusFailed || r.Status == store.StatusCancelled || r.Status == store.StatusInterrupted,
		HasReport:       r.Status.Terminal() && r.Summary != nil,
		CaseCounts:      caseCounts(d.Cases),
		Groups:          groupFindings(d.Cases),
	}
	v.Cancellable = v.Active && !v.CancelPending
	v.OutcomeCounts = outcomeCounts(v.Groups)
	for _, c := range d.Cases {
		cv := caseView{Index: c.Index, ID: c.CaseID, Status: c.Status, Observations: c.Observations,
			FindingCount: len(c.Findings), Dropped: c.FindingsDropped}
		targets := make([]string, 0, len(c.Bodies))
		for t := range c.Bodies {
			targets = append(targets, t)
		}
		sort.Strings(targets)
		for _, t := range targets {
			cv.Bodies = append(cv.Bodies, bodyView{Target: t, Body: c.Bodies[t]})
		}
		v.Cases = append(v.Cases, cv)
	}
	return v
}

type statusOption struct {
	Value    string
	Label    string
	Selected bool
}

// runListView is the content of the run list page.
type runListView struct {
	Runs         []store.RunSummary
	Total        int64
	Statuses     []statusOption
	FilterLabel  string
	Page, Pages  int
	PrevURL      string
	NextURL      string
	EmptyMessage string
}

// submitForm holds the submitted (or default) form values.
type submitForm struct {
	Suite       string
	Mode        string
	Target      string
	Baseline    string
	Candidate   string
	AllowWrites bool
}

type submitView struct {
	Suites      []catalog.Entry
	Problems    []catalog.Problem
	Targets     []string
	AllowWrites bool
	Form        submitForm
	Error       string
}

type planResultView struct {
	BeforeName  string
	AfterName   string
	Status      report.Status
	Before      plans.Summary
	After       plans.Summary
	Rows        []findingRow
	Dropped     int
	Limitations []string
}

func newPlanResultView(c *httpapi.PlanComparison) *planResultView {
	v := &planResultView{Status: c.Report.Status, Before: c.Before, After: c.After,
		Dropped: c.Report.Dropped, Limitations: c.Report.Limitations}
	if len(c.Report.Inputs) == 2 {
		v.BeforeName, v.AfterName = c.Report.Inputs[0].Name, c.Report.Inputs[1].Name
	}
	for _, f := range c.Report.Findings {
		v.Rows = append(v.Rows, classify(f))
	}
	return v
}

type plansView struct {
	MaxBytes int64
	Error    string
	Result   *planResultView
}

type loginView struct {
	Error string
}

type errorView struct {
	Heading string
	Message string
}

// Package report defines the versioned report document shared by every
// BackendLab command and renders it as text, JSON or Markdown.
package report

import (
	"time"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/version"
)

// SchemaVersion identifies the JSON report layout. It changes only when a
// field is removed or its meaning changes; additive fields keep it.
const SchemaVersion = "backendlab.report/v1"

// Status is the completion status of the command that produced a report.
type Status string

const (
	// StatusPassed: the command completed and every configured check passed.
	StatusPassed Status = "passed"
	// StatusFailed: the command completed but assertions, differences or
	// thresholds failed, or individual cases hit transport errors.
	StatusFailed Status = "failed"
	// StatusError: the command could not complete (invalid input,
	// configuration error, or a failure to execute the suite itself).
	StatusError Status = "error"
	// StatusCancelled: execution was cancelled before completion.
	StatusCancelled Status = "cancelled"
	// StatusIncomplete: the whole-run timeout expired before all cases ran.
	StatusIncomplete Status = "incomplete"
)

// CaseStatus is the outcome of one case.
type CaseStatus string

const (
	CasePassed       CaseStatus = "passed"
	CaseFailed       CaseStatus = "failed"
	CaseError        CaseStatus = "error"
	CaseSkipped      CaseStatus = "skipped"
	CaseInconclusive CaseStatus = "inconclusive"
	CaseCancelled    CaseStatus = "cancelled"
)

// Tool identifies the producing program.
type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Input identifies one input file or stream. SHA256 is the hex digest of
// the raw bytes when the input was read from a file.
type Input struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	SHA256 string `json:"sha256,omitempty"`
}

// Observation is what one target returned for one case. Timing fields are
// nil when unknown (for example offline inspection of saved files);
// unknown is never reported as zero.
type Observation struct {
	Target        string   `json:"target"`
	StatusCode    int      `json:"status_code,omitempty"`
	DurationMS    *float64 `json:"duration_ms,omitempty"`
	HeadersMS     *float64 `json:"headers_ms,omitempty"`
	FirstEventMS  *float64 `json:"first_event_ms,omitempty"`
	ResponseBytes int64    `json:"response_bytes,omitempty"`
	Events        int      `json:"events,omitempty"`
	Error         string   `json:"error,omitempty"`
}

// Case summarizes one case in a run.
type Case struct {
	ID           string        `json:"id"`
	Description  string        `json:"description,omitempty"`
	Status       CaseStatus    `json:"status"`
	Observations []Observation `json:"observations,omitempty"`
}

// Summary holds report-level counts.
type Summary struct {
	Cases         int                `json:"cases"`
	CasesByStatus map[CaseStatus]int `json:"cases_by_status"`
	Findings      finding.Summary    `json:"findings"`
}

// Timing records wall-clock execution for runtime reports. Offline
// commands leave it nil so their output is reproducible.
type Timing struct {
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	DurationMS float64   `json:"duration_ms"`
}

// Report is the document every command produces.
type Report struct {
	Schema  string   `json:"schema"`
	Tool    Tool     `json:"tool"`
	Command string   `json:"command"`
	Inputs  []Input  `json:"inputs"`
	Targets []string `json:"targets,omitempty"`
	Status  Status   `json:"status"`
	Error   string   `json:"error,omitempty"`
	Summary Summary  `json:"summary"`
	// Cases appear in suite order; producers place results by case index
	// so concurrent execution does not change the order.
	Cases       []Case            `json:"cases,omitempty"`
	Findings    []finding.Finding `json:"findings"`
	Dropped     int               `json:"findings_dropped,omitempty"`
	Limitations []string          `json:"limitations,omitempty"`
	Timing      *Timing           `json:"timing,omitempty"`
}

// New returns an empty report for command with the tool identity filled.
func New(command string) *Report {
	return &Report{
		Schema:   SchemaVersion,
		Tool:     Tool{Name: version.Name, Version: version.Version},
		Command:  command,
		Inputs:   []Input{},
		Findings: []finding.Finding{},
	}
}

// AddFindings appends findings to the report.
func (r *Report) AddFindings(fs ...finding.Finding) {
	r.Findings = append(r.Findings, fs...)
}

// AddLimitation records a limitation or inconclusive check once.
func (r *Report) AddLimitation(text string) {
	for _, l := range r.Limitations {
		if l == text {
			return
		}
	}
	r.Limitations = append(r.Limitations, text)
}

// Fail marks the report as an execution error with a sanitized message.
func (r *Report) Fail(status Status, msg string) {
	r.Status = status
	r.Error = msg
}

// Finalize sorts findings, computes the summary and, unless an
// execution status (error, cancelled, incomplete) was already set, derives
// passed or failed from the findings and case outcomes.
func (r *Report) Finalize() {
	finding.Sort(r.Findings)
	r.Summary = Summary{
		Cases: len(r.Cases),
		CasesByStatus: map[CaseStatus]int{
			CasePassed: 0, CaseFailed: 0, CaseError: 0,
			CaseSkipped: 0, CaseInconclusive: 0, CaseCancelled: 0,
		},
		Findings: finding.Summarize(r.Findings),
	}
	failedCase := false
	for _, c := range r.Cases {
		r.Summary.CasesByStatus[c.Status]++
		if c.Status == CaseFailed || c.Status == CaseError {
			failedCase = true
		}
	}
	switch r.Status {
	case StatusError, StatusCancelled, StatusIncomplete:
		return
	}
	if failedCase || r.Summary.Findings.Failing > 0 {
		r.Status = StatusFailed
	} else {
		r.Status = StatusPassed
	}
}

// ExitCode maps the report status to the documented process exit code:
// 0 passed, 1 failed, 2 for errors, cancellation and incomplete runs.
func (r *Report) ExitCode() int {
	switch r.Status {
	case StatusPassed:
		return 0
	case StatusFailed:
		return 1
	default:
		return 2
	}
}

// Millis converts d to a float millisecond pointer for observations.
func Millis(d time.Duration) *float64 {
	ms := float64(d.Microseconds()) / 1000
	return &ms
}

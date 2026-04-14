package jobs

import (
	"context"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/store"
)

// CaseUpdate is the result of one case, reported by the executor as soon
// as the case finishes. Status is a report.CaseStatus value. Body maps
// target name to the redacted response body; it is persisted only when
// raw body retention is enabled.
type CaseUpdate struct {
	CaseID       string
	Index        int
	Status       string
	Findings     []finding.Finding
	Observations []report.Observation
	Body         map[string]string
}

// Executor runs the suite of a claimed run. It is the seam between JobDock
// and the suite runner: the worker owns claiming, leases, persistence and
// final status; the executor only executes.
//
// Execute must return promptly once ctx is cancelled (operator
// cancellation, lost lease or shutdown), and must not retain run after
// returning. onCase persists one case result; it is safe to call from
// several goroutines. When onCase returns an error (for example
// ErrLeaseLost) the executor should stop and return. All strings in
// updates and in the report must already be redacted; the worker applies
// the server redactor to error text and bodies once more before storing.
type Executor interface {
	Execute(ctx context.Context, run *store.Run, onCase func(CaseUpdate) error) (*report.Report, error)
}

// ExecutorFunc adapts a function to Executor.
type ExecutorFunc func(ctx context.Context, run *store.Run, onCase func(CaseUpdate) error) (*report.Report, error)

// Execute calls f.
func (f ExecutorFunc) Execute(ctx context.Context, run *store.Run, onCase func(CaseUpdate) error) (*report.Report, error) {
	return f(ctx, run, onCase)
}

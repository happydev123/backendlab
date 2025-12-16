package replay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/suite"
)

// ErrWritesNotAllowed is returned, wrapped with the IDs of the mutating
// cases, when a suite contains cases that use methods outside GET, HEAD
// and OPTIONS and Options.AllowWrites is false. No request is sent.
var ErrWritesNotAllowed = errors.New("suite contains mutating cases and writes are not allowed")

// Hooks connect replay to the validation and stream modules without an
// import dependency in either direction.
type Hooks struct {
	// CheckBody validates a decoded JSON response body when the case
	// declares expect.schema or expect.rules. suiteDir is the suite source
	// directory for resolving files. Returns findings (module validate).
	CheckBody func(ctx context.Context, suiteDir string, c *suite.Case, target string, body any) []finding.Finding
	// ReadStream consumes an SSE response body for cases with a stream
	// spec. start is when the request was sent.
	ReadStream func(ctx context.Context, suiteDir string, c *suite.Case, target string, body io.Reader, start time.Time) StreamOutcome
}

// StreamOutcome is what a stream reader observed. FirstEvent is nil when
// no event arrived.
type StreamOutcome struct {
	Events      int
	FirstEvent  *time.Duration
	Bytes       int64
	Findings    []finding.Finding
	Termination string
}

// Options configures an Executor.
type Options struct {
	// Limits bounds the run; zero fields take the defaults.
	Limits limits.Limits
	// AllowWrites permits cases with methods outside GET/HEAD/OPTIONS.
	AllowWrites bool
	// Redactor sanitizes every message and preview; nil means the
	// built-in rules. Target secrets are added for each run.
	Redactor *redact.Redactor
	// Transport is used for every target when set. When nil, a transport
	// is built per target with NewTransport (honoring the target CA file).
	Transport http.RoundTripper
	// CheckRedirect is an extra policy consulted for each redirect hop
	// (server egress checks); nil means none.
	CheckRedirect func(req *http.Request) error
	Hooks         Hooks
	// Concurrency bounds parallel cases; <=0 means Limits.MaxConcurrency.
	// It never exceeds Limits.MaxConcurrency.
	Concurrency int
	// Now is the clock for timings; nil means time.Now.
	Now func() time.Time
	// OnCase, when set, is called once for each case that reached a final
	// result (including cases skipped for missing dependencies, excluding
	// cases never started because the run was cancelled or timed out).
	// Calls are serialized.
	OnCase func(CaseResult)
	// RetainBodyBytes keeps up to this many bytes of each response body,
	// redacted, in CaseResult.Bodies. Zero keeps none.
	RetainBodyBytes int64
}

// Executor runs suites. It is safe for concurrent use; each Run or
// Compare call has its own state.
type Executor struct {
	opts        Options
	lim         limits.Limits
	red         *redact.Redactor
	now         func() time.Time
	concurrency int
}

// New validates opts and returns an Executor.
func New(opts Options) (*Executor, error) {
	lim := opts.Limits.Merge()
	if err := lim.Validate(); err != nil {
		return nil, fmt.Errorf("replay: %w", err)
	}
	if opts.RetainBodyBytes < 0 {
		return nil, errors.New("replay: RetainBodyBytes must not be negative")
	}
	e := &Executor{opts: opts, lim: lim, red: opts.Redactor, now: opts.Now, concurrency: opts.Concurrency}
	if e.red == nil {
		e.red = redact.Default()
	}
	if e.now == nil {
		e.now = time.Now
	}
	if e.concurrency <= 0 || e.concurrency > lim.MaxConcurrency {
		e.concurrency = lim.MaxConcurrency
	}
	return e, nil
}

// Result is the outcome of Run or Compare.
type Result struct {
	Report *report.Report
	// Cases are in suite order.
	Cases []CaseResult
}

// CaseResult is the outcome of one case across its targets.
type CaseResult struct {
	CaseID       string
	Index        int
	Status       report.CaseStatus
	Mutating     bool
	Observations []report.Observation
	Findings     []finding.Finding
	// Bodies maps target name to the retained response body, only when
	// Options.RetainBodyBytes > 0; values are redacted and truncated.
	Bodies map[string]string
}

// newClient returns an HTTP client for target and a cleanup function.
func (e *Executor) newClient(t *config.ResolvedTarget) (*http.Client, func(), error) {
	rt := e.opts.Transport
	cleanup := func() {}
	if rt == nil {
		tr, err := NewTransport(e.lim, t.CAFile)
		if err != nil {
			return nil, nil, fmt.Errorf("target %s: %w", t.Name, err)
		}
		rt, cleanup = tr, tr.CloseIdleConnections
	}
	return &http.Client{Transport: rt, CheckRedirect: redirectPolicy(t, e.opts.CheckRedirect)}, cleanup, nil
}

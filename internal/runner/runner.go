// Package runner assembles a replay executor with the stream and
// validation modules plugged in through replay's hooks. The CLI and the
// background worker both build executors here, so a suite behaves the same
// way whether it runs locally or on the server.
package runner

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/replay"
	"github.com/happydev123/backendlab/internal/suite"
)

// Config configures an executor.
type Config struct {
	Limits      limits.Limits
	Redactor    *redact.Redactor
	AllowWrites bool
	// Transport and CheckRedirect are passed to replay unchanged. Server
	// mode sets them from the egress policy.
	Transport     http.RoundTripper
	CheckRedirect func(req *http.Request) error
	Concurrency   int
	// ConfineFiles requires schema and rules files referenced by a suite
	// to stay inside the suite directory (server mode).
	ConfineFiles    bool
	OnCase          func(replay.CaseResult)
	RetainBodyBytes int64
	Now             func() time.Time
}

// Runner owns one executor and the checkers it loaded for a suite.
type Runner struct {
	Executor *replay.Executor
	checks   *checkers
}

// New prepares the files referenced by the suite (schemas, rules files,
// stream schemas) and returns a runner. Problems with those files are
// returned as errors before any request is sent, because they make the
// suite itself unexecutable.
func New(cfg Config, l *suite.Loaded) (*Runner, error) {
	lim := cfg.Limits.Merge()
	red := cfg.Redactor
	if red == nil {
		red = redact.Default()
	}
	checks, err := prepare(l, lim, red, cfg.ConfineFiles)
	if err != nil {
		return nil, err
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	exec, err := replay.New(replay.Options{
		Limits:        lim,
		AllowWrites:   cfg.AllowWrites,
		Redactor:      red,
		Transport:     cfg.Transport,
		CheckRedirect: cfg.CheckRedirect,
		Concurrency:   cfg.Concurrency,
		Now:           now,
		OnCase:        cfg.OnCase,
		Hooks: replay.Hooks{
			CheckBody: func(_ context.Context, _ string, c *suite.Case, _ string, body any) []finding.Finding {
				return checks.checkBody(c, body)
			},
			ReadStream: func(ctx context.Context, _ string, c *suite.Case, _ string, body io.Reader, start time.Time) replay.StreamOutcome {
				return checks.readStream(ctx, c, body, start, now)
			},
		},
		RetainBodyBytes: cfg.RetainBodyBytes,
	})
	if err != nil {
		return nil, err
	}
	return &Runner{Executor: exec, checks: checks}, nil
}

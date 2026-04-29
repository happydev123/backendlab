package runner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/egress"
	"github.com/happydev123/backendlab/internal/jobs"
	"github.com/happydev123/backendlab/internal/replay"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/internal/suite"
)

// JobExecutor runs claimed server runs. It executes the suite snapshot
// stored with the run (never the current file), resolves only
// server-configured targets and allowlisted credential variables, routes
// every connection through the egress policy, and permits mutating cases
// only when both the server configuration and the run request allow
// writes.
type JobExecutor struct {
	cfg    *config.Server
	policy *egress.Policy
	env    config.EnvLookup
	// ConnWrapper, when set, wraps every connection after the egress
	// policy accepted it. Tests use it for platform workarounds; it cannot
	// change the destination.
	ConnWrapper func(net.Conn) net.Conn
}

// NewJobExecutor validates the egress configuration and returns an
// executor. env is normally config.OSEnv.
func NewJobExecutor(cfg *config.Server, env config.EnvLookup) (*JobExecutor, error) {
	policy, err := egress.New(cfg.Egress)
	if err != nil {
		return nil, err
	}
	if env == nil {
		env = config.OSEnv
	}
	return &JobExecutor{cfg: cfg, policy: policy, env: env}, nil
}

var _ jobs.Executor = (*JobExecutor)(nil)

// Execute implements jobs.Executor.
func (x *JobExecutor) Execute(ctx context.Context, run *store.Run, onCase func(jobs.CaseUpdate) error) (*report.Report, error) {
	l, err := x.loadSnapshot(run)
	if err != nil {
		return nil, err
	}
	targets, err := x.resolveTargets(run)
	if err != nil {
		return nil, err
	}
	transport, err := replay.NewTransport(x.cfg.Limits, "")
	if err != nil {
		return nil, err
	}
	defer transport.CloseIdleConnections()
	dial := x.policy.DialContext(&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second})
	if wrap := x.ConnWrapper; wrap != nil {
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := dial(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return wrap(c), nil
		}
	} else {
		transport.DialContext = dial
	}

	// A failed persist (typically a lost lease) stops the run: further
	// requests would produce results nobody may record.
	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	var persistErr error
	retain := int64(0)
	if x.cfg.Retention.RetainRawBodies {
		retain = x.cfg.Retention.RawBodyMaxBytes
	}
	r, err := New(Config{
		Limits:          x.cfg.Limits,
		Redactor:        x.cfg.Redactor(),
		AllowWrites:     x.cfg.AllowWrites && run.WritesRequested,
		Transport:       transport,
		CheckRedirect:   x.policy.CheckRedirect,
		ConfineFiles:    true,
		RetainBodyBytes: retain,
		OnCase: func(cr replay.CaseResult) {
			if err := onCase(caseUpdate(cr)); err != nil {
				mu.Lock()
				if persistErr == nil {
					persistErr = err
				}
				mu.Unlock()
				cancel()
			}
		},
	}, l)
	if err != nil {
		return nil, err
	}
	var res *replay.Result
	switch run.Mode {
	case store.ModeCompare:
		res, err = r.Executor.Compare(execCtx, l, targets[0], targets[1])
	default:
		res, err = r.Executor.Run(execCtx, l, targets[0])
	}
	mu.Lock()
	defer mu.Unlock()
	if persistErr != nil {
		return nil, persistErr
	}
	if err != nil {
		return nil, err
	}
	return res.Report, nil
}

// loadSnapshot parses the stored suite bytes and checks them against the
// digest recorded at submission.
func (x *JobExecutor) loadSnapshot(run *store.Run) (*suite.Loaded, error) {
	if got := suite.Digest(run.SuiteSnapshot); got != run.SuiteSHA256 {
		return nil, errors.New("suite snapshot does not match its recorded digest")
	}
	s, err := suite.Parse(run.SuiteSnapshot, x.cfg.Limits)
	if err != nil {
		return nil, err
	}
	clean := path.Clean("/" + filepath.ToSlash(run.SuiteFile))
	dir := filepath.Join(x.cfg.SuitesDir, filepath.FromSlash(path.Dir(clean)))
	return &suite.Loaded{Suite: s, Source: suite.Source{
		Path:   run.SuiteFile,
		Dir:    dir,
		SHA256: run.SuiteSHA256,
		Size:   int64(len(run.SuiteSnapshot)),
	}}, nil
}

func (x *JobExecutor) resolveTargets(run *store.Run) ([]*config.ResolvedTarget, error) {
	want := 1
	if run.Mode == store.ModeCompare {
		want = 2
	}
	if len(run.Targets) != want {
		return nil, fmt.Errorf("run mode %s needs %d targets, got %d", run.Mode, want, len(run.Targets))
	}
	out := make([]*config.ResolvedTarget, 0, want)
	for _, name := range run.Targets {
		t, ok := x.cfg.Targets[name]
		if !ok {
			return nil, fmt.Errorf("target %q is not configured on this server", name)
		}
		rt, err := t.Resolve(name, x.env, x.cfg.AllowedEnv())
		if err != nil {
			return nil, err
		}
		if err := x.policy.CheckURL(rt.BaseURL); err != nil {
			return nil, fmt.Errorf("target %s: %w", name, err)
		}
		out = append(out, rt)
	}
	return out, nil
}

func caseUpdate(cr replay.CaseResult) jobs.CaseUpdate {
	return jobs.CaseUpdate{
		CaseID:       cr.CaseID,
		Index:        cr.Index,
		Status:       string(cr.Status),
		Findings:     cr.Findings,
		Observations: cr.Observations,
		Body:         cr.Bodies,
	}
}

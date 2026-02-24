package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/replay"
	"github.com/happydev123/backendlab/internal/runner"
	"github.com/happydev123/backendlab/internal/suite"
)

// suiteFlags are shared by run and compare.
type suiteFlags struct {
	common      commonFlags
	allowWrites bool
	concurrency int
	baseURLs    baseURLFlag
}

func (s *suiteFlags) register(fs *flag.FlagSet) {
	s.common.register(fs)
	s.baseURLs = baseURLFlag{}
	fs.BoolVar(&s.allowWrites, "allow-writes", false, "permit cases using methods other than GET, HEAD and OPTIONS")
	fs.IntVar(&s.concurrency, "concurrency", 0, "maximum parallel cases (default from limits.max_concurrency)")
	fs.Var(s.baseURLs, "base-url", "override or define a target base URL as name=URL (repeatable)")
}

// suiteRun is everything a run or compare invocation needs after flags
// and files are validated.
type suiteRun struct {
	cfg    *config.CLI
	out    outputSettings
	loaded *suite.Loaded
	exec   *replay.Executor
}

func (a *App) prepareSuite(sf suiteFlags, suitePath string) (*suiteRun, error) {
	cfg, err := a.loadConfig(sf.common.configPath)
	if err != nil {
		return nil, err
	}
	out, err := a.resolveOutput(sf.common, cfg)
	if err != nil {
		return nil, err
	}
	if sf.concurrency < 0 {
		return nil, usagef("--concurrency must not be negative")
	}
	if sf.concurrency > cfg.Limits.MaxConcurrency {
		return nil, usagef("--concurrency %d exceeds limits.max_concurrency (%d)", sf.concurrency, cfg.Limits.MaxConcurrency)
	}
	loaded, err := suite.LoadFile(suitePath, cfg.Limits)
	if err != nil {
		return nil, err
	}
	if ids := loaded.Suite.MutatingCases(); len(ids) > 0 {
		if !sf.allowWrites {
			return nil, fmt.Errorf("%w: cases %v use methods other than GET, HEAD and OPTIONS; pass --allow-writes to run them", replay.ErrWritesNotAllowed, ids)
		}
		a.errorf("warning: --allow-writes permits %d mutating case(s) %v. They are never retried, and cancelling or timing out does not undo side effects already applied upstream.", len(ids), ids)
	}
	r, err := runner.New(runner.Config{
		Limits:      cfg.Limits,
		Redactor:    cfg.Redactor(),
		AllowWrites: sf.allowWrites,
		Concurrency: sf.concurrency,
		Transport:   a.Transport,
	}, loaded)
	if err != nil {
		return nil, err
	}
	return &suiteRun{cfg: cfg, out: out, loaded: loaded, exec: r.Executor}, nil
}

// finishRun maps the replay outcome to output and exit code. A returned
// error means the suite itself could not be executed (exit 2); per-case
// transport errors are inside the report (exit 1).
func (a *App) finishRun(ctx context.Context, sr *suiteRun, res *replay.Result, err error) int {
	if err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			a.errorf("interrupted")
		}
		return a.fail(err)
	}
	if ctx.Err() != nil {
		a.errorf("interrupted: remaining cases were cancelled; requests already sent may have taken effect upstream")
	}
	return a.finish(res.Report, sr.out)
}

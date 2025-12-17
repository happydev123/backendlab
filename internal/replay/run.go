package replay

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/suite"
)

// Run executes every case of l against target and returns the report.
// The returned error means the suite could not be executed at all
// (writes not allowed, invalid target, unresolvable files); problems with
// individual requests are findings in the report.
func (e *Executor) Run(ctx context.Context, l *suite.Loaded, target *config.ResolvedTarget) (*Result, error) {
	return e.execute(ctx, "run", l, []*config.ResolvedTarget{target}, nil)
}

// postCase adds findings after a case ran on every target; compare mode
// uses it to compare the responses.
type postCase func(rs *runState, c *suite.Case, runs []*targetRun, outs []outcome) []finding.Finding

func (e *Executor) execute(ctx context.Context, command string, l *suite.Loaded, targets []*config.ResolvedTarget, post postCase) (*Result, error) {
	if l == nil || l.Suite == nil {
		return nil, errors.New("replay: no suite")
	}
	if err := validateTargets(targets); err != nil {
		return nil, err
	}
	if ids := l.Suite.MutatingCases(); len(ids) > 0 && !e.opts.AllowWrites {
		return nil, fmt.Errorf("%w: %s", ErrWritesNotAllowed, strings.Join(ids, ", "))
	}
	if err := checkFiles(l); err != nil {
		return nil, err
	}

	runs := make([]*targetRun, len(targets))
	for i, t := range targets {
		client, cleanup, err := e.newClient(t)
		if err != nil {
			return nil, err
		}
		defer cleanup()
		runs[i] = &targetRun{target: t, client: client, scope: newScope()}
	}

	start := e.now()
	runCtx, cancel := context.WithTimeout(ctx, e.lim.RunTimeout)
	defer cancel()
	rs := newRunState(e, l, ctx, runCtx, targets)
	results := rs.schedule(mustRunSequentially(l.Suite), e.concurrency, func(idx int) CaseResult {
		return rs.runCase(idx, runs, post)
	})
	interrupted := rs.settleNotStarted(results)
	end := e.now()
	return &Result{Report: rs.buildReport(command, runs, results, interrupted, start, end), Cases: results}, nil
}

// validateTargets checks that targets are usable and distinctly named.
func validateTargets(targets []*config.ResolvedTarget) error {
	seen := map[string]bool{}
	for _, t := range targets {
		switch {
		case t == nil:
			return errors.New("replay: target is required")
		case t.Name == "":
			return errors.New("replay: target has no name")
		case t.BaseURL == nil || (t.BaseURL.Scheme != "http" && t.BaseURL.Scheme != "https") || t.BaseURL.Host == "":
			return fmt.Errorf("replay: target %s needs an absolute http or https base URL", t.Name)
		case seen[t.Name]:
			return fmt.Errorf("replay: baseline and candidate must have different names (both are %q)", t.Name)
		}
		seen[t.Name] = true
	}
	return nil
}

// checkFiles verifies that files referenced by the suite exist, so a
// missing schema fails the run before any request is sent.
func checkFiles(l *suite.Loaded) error {
	for i := range l.Suite.Cases {
		c := &l.Suite.Cases[i]
		refs := []struct{ field, ref string }{{"expect.schema", c.Expect.Schema}, {"expect.rules", c.Expect.Rules}}
		if c.Stream != nil {
			refs = append(refs, struct{ field, ref string }{"stream.schema", c.Stream.Schema})
		}
		for _, r := range refs {
			if r.ref == "" {
				continue
			}
			p, err := suite.ResolveFile(l.Source.Dir, r.ref, false)
			if err == nil {
				_, err = os.Stat(p)
			}
			if err != nil {
				return fmt.Errorf("replay: case %q: %s: %w", c.ID, r.field, err)
			}
		}
	}
	return nil
}

// cancelledByCaller reports whether the caller cancelled the run (as
// opposed to a deadline expiring).
func (rs *runState) cancelledByCaller() bool {
	return errors.Is(rs.parent.Err(), context.Canceled)
}

// runCase executes case idx on every target in order, then applies post
// when every target returned a complete response.
func (rs *runState) runCase(idx int, runs []*targetRun, post postCase) CaseResult {
	c := &rs.l.Suite.Cases[idx]
	cr := CaseResult{CaseID: c.ID, Index: idx, Mutating: c.Mutating()}

	refs, _ := c.Refs()
	for _, tr := range runs {
		if missing := tr.scope.missing(refs); len(missing) > 0 {
			cr.Status = report.CaseSkipped
			cr.Findings = []finding.Finding{inconclusive(CodeDependencyUnavailable, c.ID, tr.target.Name, tr.scope.explain(missing))}
			for _, other := range runs {
				other.scope.markFailed(c)
			}
			return cr
		}
	}

	outs := make([]outcome, len(runs))
	for i, tr := range runs {
		outs[i] = rs.runTarget(idx, tr)
		cr.Observations = append(cr.Observations, outs[i].obs)
		cr.Findings = append(cr.Findings, outs[i].findings...)
		if outs[i].retained {
			if cr.Bodies == nil {
				cr.Bodies = map[string]string{}
			}
			cr.Bodies[tr.target.Name] = outs[i].body
		}
	}
	if post != nil && allComplete(outs) {
		cr.Findings = append(cr.Findings, post(rs, c, runs, outs)...)
	}
	finding.Sort(cr.Findings)
	cr.Status = caseStatus(cr.Findings)
	return cr
}

// allComplete reports whether every target returned a complete response.
func allComplete(outs []outcome) bool {
	for _, o := range outs {
		if o.resp == nil {
			return false
		}
	}
	return true
}

package replay

import (
	"context"
	"net/http"
	"sync"

	"github.com/happydev123/backendlab/internal/compare"
	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/redact"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/suite"
)

// targetRun is the per-run state of one target.
type targetRun struct {
	target *config.ResolvedTarget
	client *http.Client
	// scope is mutated only by cases with captures, and suites with
	// captures run sequentially, so it needs no lock.
	scope *scope
}

// runState is shared by every case of one Run or Compare call.
type runState struct {
	e *Executor
	l *suite.Loaded
	// parent is the caller's context; run adds the whole-run timeout.
	parent, run context.Context

	mu  sync.Mutex
	red *redact.Redactor
}

func newRunState(e *Executor, l *suite.Loaded, parent, run context.Context, targets []*config.ResolvedTarget) *runState {
	var secrets []string
	for _, t := range targets {
		secrets = append(secrets, t.Secrets...)
	}
	return &runState{e: e, l: l, parent: parent, run: run, red: e.red.WithSecrets(secrets...)}
}

// redactor returns the current redactor, which includes target secrets
// and captured values found at redacted paths.
func (rs *runState) redactor() *redact.Redactor {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.red
}

func (rs *runState) addSecrets(values []string) {
	if len(values) == 0 {
		return
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.red = rs.red.WithSecrets(values...)
}

// outcome is one target's result for one case.
type outcome struct {
	obs      report.Observation
	findings []finding.Finding
	// resp is set only when a complete response was received.
	resp *compare.Response
	// body is the retained body when retention is enabled.
	body     string
	retained bool
}

// runTarget sends case idx to one target and evaluates its expectations.
func (rs *runState) runTarget(idx int, tr *targetRun) outcome {
	s, lim := rs.l.Suite, rs.e.lim
	c := &s.Cases[idx]
	red := rs.redactor()
	a := asserter{caseID: c.ID, target: tr.target.Name, red: red, max: lim.MaxValuePreview}
	out := outcome{obs: report.Observation{Target: tr.target.Name}}
	fail := func(code, msg string) outcome {
		tr.scope.markFailed(c)
		out.obs.Error = msg
		out.findings = append(out.findings, transportError(code, c.ID, tr.target.Name, msg))
		return out
	}

	timeout := s.EffectiveTimeout(c, lim)
	reqCtx, cancel := context.WithTimeout(rs.run, timeout)
	defer cancel()
	cx := contexts{parent: rs.parent, run: rs.run, request: reqCtx}

	req, err := buildRequest(reqCtx, tr.target, s, c, tr.scope.vars, lim)
	if err != nil {
		return fail(CodeRequestInvalid, "request could not be built: "+red.Error(err))
	}
	st, err := send(tr.client, req, rs.e.now)
	if err != nil {
		code := classify(err, cx)
		return fail(code, transportMessage(code, err, red, timeout, lim.MaxResponseBytes))
	}
	if c.Stream != nil {
		return rs.streamTarget(c, tr, a, st, reqCtx, cx, timeout)
	}

	rec, err := st.readAll(lim.MaxResponseBytes)
	out.obs.StatusCode = rec.status
	out.obs.HeadersMS = report.Millis(rec.headers)
	out.obs.DurationMS = report.Millis(rec.total)
	if err != nil {
		code := classify(err, cx)
		return fail(code, transportMessage(code, err, red, timeout, lim.MaxResponseBytes))
	}
	out.obs.ResponseBytes = rec.bytes

	fs := a.checkStatus(c.Expect.Status, rec.status)
	fs = append(fs, a.checkHeaders(c.Expect.Headers, rec.header)...)
	fs = append(fs, a.checkDuration(c.Expect.MaxDuration, rec.total)...)

	var decoded any
	isJSON := false
	if len(rec.body) > 0 {
		if v, err := jsonutil.Decode(rec.body); err == nil {
			decoded, isJSON = v, true
		}
	}
	if len(c.Expect.JSON) > 0 {
		if isJSON {
			fs = append(fs, a.checkJSON(c.Expect.JSON, decoded)...)
		} else {
			fs = append(fs, a.bodyNotJSON(len(rec.body), "expect.json"))
		}
	}
	fs = append(fs, rs.checkBody(c, tr, a, decoded, isJSON, len(rec.body))...)
	if len(c.Capture) > 0 {
		capFs, sensitive := a.capture(c, decoded, isJSON, tr.scope)
		fs = append(fs, capFs...)
		rs.addSecrets(sensitive)
	}

	out.findings = fs
	out.resp = &compare.Response{Status: rec.status, Header: rec.header, Body: rec.body}
	if n := rs.e.opts.RetainBodyBytes; n > 0 {
		out.body = retainedBody(rec.body, decoded, isJSON, rs.redactor(), n)
		out.retained = true
	}
	return out
}

// adopt attaches case and target identity to findings produced by hooks
// when the hook left them unset.
func adopt(fs []finding.Finding, caseID, target string) []finding.Finding {
	out := make([]finding.Finding, len(fs))
	for i, f := range fs {
		if f.CaseID == "" {
			f.CaseID = caseID
		}
		if _, ok := f.Details["target"]; !ok && target != "" {
			f = f.WithDetail("target", target)
		}
		out[i] = f
	}
	return out
}

// caseStatus derives a case status from its findings: error when any
// transport error occurred (cancelled when every transport error is a
// cancellation), failed when any finding fails, inconclusive when a check
// could not be completed, otherwise passed.
func caseStatus(fs []finding.Finding) report.CaseStatus {
	transport, onlyCancelled := false, true
	failing, undecided := false, false
	for _, f := range fs {
		switch {
		case f.Kind == finding.KindTransportError:
			transport = true
			if f.Code != CodeCancelled {
				onlyCancelled = false
			}
		case f.Failing():
			failing = true
		case f.Kind == finding.KindInconclusive || f.Kind == finding.KindUnsupported:
			undecided = true
		}
	}
	switch {
	case transport && onlyCancelled:
		return report.CaseCancelled
	case transport:
		return report.CaseError
	case failing:
		return report.CaseFailed
	case undecided:
		return report.CaseInconclusive
	}
	return report.CasePassed
}

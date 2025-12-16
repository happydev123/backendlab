package replay

import (
	"context"
	"time"

	"github.com/happydev123/backendlab/internal/compare"
	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/suite"
)

// checkBody runs the CheckBody hook for cases that declare expect.schema
// or expect.rules. Without a hook the check is reported as inconclusive
// rather than silently skipped.
func (rs *runState) checkBody(c *suite.Case, tr *targetRun, a asserter, decoded any, isJSON bool, size int) []finding.Finding {
	if c.Expect.Schema == "" && c.Expect.Rules == "" {
		return nil
	}
	hook := rs.e.opts.Hooks.CheckBody
	if hook == nil {
		return []finding.Finding{inconclusive(CodeValidationUnavailable, c.ID, tr.target.Name,
			"expect.schema/expect.rules were not evaluated: no body validator is configured")}
	}
	if !isJSON {
		return []finding.Finding{a.bodyNotJSON(size, "expect.schema/expect.rules")}
	}
	return adopt(hook(rs.run, rs.l.Source.Dir, c, tr.target.Name, decoded), c.ID, tr.target.Name)
}

// streamTarget handles a case with a stream spec. The body is never
// buffered: it is passed, bounded by Limits.MaxStreamBytes, to the
// ReadStream hook. The case timeout covers the whole stream.
func (rs *runState) streamTarget(c *suite.Case, tr *targetRun, a asserter, st *sent,
	reqCtx context.Context, cx contexts, timeout time.Duration) outcome {
	defer closeBody(st.resp.Body)
	lim := rs.e.lim
	out := outcome{obs: report.Observation{
		Target:     tr.target.Name,
		StatusCode: st.resp.StatusCode,
		HeadersMS:  report.Millis(st.headers),
	}}
	fs := a.checkStatus(c.Expect.Status, st.resp.StatusCode)
	fs = append(fs, a.checkHeaders(c.Expect.Headers, st.resp.Header)...)
	if len(c.Expect.JSON) > 0 || c.Expect.Schema != "" || c.Expect.Rules != "" {
		fs = append(fs, unsupported(CodeJSONOnStream, c.ID, tr.target.Name,
			"expect.json, expect.schema and expect.rules do not apply to stream responses; use the stream spec"))
	}

	hook := rs.e.opts.Hooks.ReadStream
	if hook == nil {
		fs = append(fs, inconclusive(CodeStreamUnavailable, c.ID, tr.target.Name,
			"stream expectations were not evaluated: no stream reader is configured"))
	} else {
		so := hook(reqCtx, rs.l.Source.Dir, c, tr.target.Name, newLimitedReader(st.resp.Body, lim.MaxStreamBytes), st.timer.start)
		out.obs.Events = so.Events
		out.obs.ResponseBytes = so.Bytes
		if so.FirstEvent != nil {
			out.obs.FirstEventMS = report.Millis(*so.FirstEvent)
		}
		fs = append(fs, adopt(so.Findings, c.ID, tr.target.Name)...)
	}
	total := st.timer.since()
	out.obs.DurationMS = report.Millis(total)
	if err := reqCtx.Err(); err != nil {
		code := classify(err, cx)
		msg := transportMessage(code, err, rs.redactor(), timeout, lim.MaxStreamBytes)
		out.obs.Error = msg
		fs = append(fs, transportError(code, c.ID, tr.target.Name, msg))
	}
	fs = append(fs, a.checkDuration(c.Expect.MaxDuration, total)...)
	out.findings = fs
	if out.obs.Error == "" {
		out.resp = &compare.Response{Status: st.resp.StatusCode, Header: st.resp.Header, SkipBody: true}
	}
	return out
}

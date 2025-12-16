package replay

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/suite"
	"github.com/happydev123/backendlab/internal/testutil"
)

const caseSuite = `
version: 1
name: cases
cases:
  - id: ok
    method: GET
    path: /ok
    expect:
      status: 200
      headers: {content-type: application/json}
      json:
        - {pointer: /total, equals: 10.5}
  - id: wrong
    method: GET
    path: /ok
    expect:
      status: [201, 202]
      json:
        - {pointer: /total, equals: 11}
  - id: text
    method: GET
    path: /text
    expect:
      json:
        - {pointer: /a, exists: true}
  - id: big
    method: GET
    path: /big
  - id: slow
    method: GET
    path: /slow
    timeout: 100ms
  - id: schema
    method: GET
    path: /ok
    expect:
      schema: invoice.schema.json
  - id: stream
    method: GET
    path: /stream
    stream:
      terminal_data: "[DONE]"
`

// caseHarness runs single cases against a test server.
type caseHarness struct {
	t  *testing.T
	rs *runState
	tr *targetRun
}

func newCaseHarness(t *testing.T, opts Options) *caseHarness {
	t.Helper()
	t.Setenv(testutil.EnvAsyncClose, "1")
	release := testutil.Release(t)
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"total":10.50,"note":"key tok-secret-1"}`)
		case "/text":
			io.WriteString(w, "plain")
		case "/big":
			io.WriteString(w, strings.Repeat("x", 2048))
		case "/slow":
			select {
			case <-r.Context().Done():
			case <-release:
			case <-time.After(2 * time.Second):
			}
		case "/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: one\n\ndata: [DONE]\n\n")
		}
	}))
	opts.Transport = testutil.Transport()
	if opts.Limits.MaxResponseBytes == 0 {
		opts.Limits.MaxResponseBytes = 1024
	}
	e, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	target := resolveTarget(t, "base", config.Target{
		BaseURL: srv.URL,
		Headers: map[string]config.HeaderValue{"Authorization": {Env: "TOKEN"}},
	}, map[string]string{"TOKEN": "tok-secret-1"})
	client, cleanup, err := e.newClient(target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	l := loaded(t, caseSuite)
	ctx := context.Background()
	return &caseHarness{
		t:  t,
		rs: newRunState(e, l, ctx, ctx, []*config.ResolvedTarget{target}),
		tr: &targetRun{target: target, client: client, scope: newScope()},
	}
}

func (h *caseHarness) run(id string) outcome {
	h.t.Helper()
	for i := range h.rs.l.Suite.Cases {
		if h.rs.l.Suite.Cases[i].ID == id {
			return h.rs.runTarget(i, h.tr)
		}
	}
	h.t.Fatalf("no case %s", id)
	return outcome{}
}

func TestRunTargetAssertions(t *testing.T) {
	h := newCaseHarness(t, Options{RetainBodyBytes: 100})
	out := h.run("ok")
	if len(out.findings) != 0 || caseStatus(out.findings) != report.CasePassed {
		t.Fatalf("ok case findings: %s", codes(out.findings))
	}
	if out.obs.StatusCode != 200 || out.obs.DurationMS == nil || out.obs.HeadersMS == nil || out.obs.ResponseBytes == 0 {
		t.Fatalf("observation %+v", out.obs)
	}
	if out.resp == nil || out.resp.Status != 200 {
		t.Fatal("complete response not kept for comparison")
	}
	if !out.retained || strings.Contains(out.body, "tok-secret-1") || !strings.Contains(out.body, "[REDACTED]") {
		t.Fatalf("retained body %q", out.body)
	}

	out = h.run("wrong")
	if got := codes(out.findings); got != "replay.status_unexpected@status replay.json_not_equal@/total" {
		t.Fatalf("wrong case: %s", got)
	}
	if caseStatus(out.findings) != report.CaseFailed {
		t.Fatal("assertion failures must fail the case")
	}

	out = h.run("text")
	if got := codes(out.findings); got != "replay.body_not_json@body" {
		t.Fatalf("text case: %s", got)
	}
}

func TestRunTargetTransportErrors(t *testing.T) {
	h := newCaseHarness(t, Options{})
	out := h.run("big")
	if got := codes(out.findings); got != "replay.response_too_large@" || out.resp != nil || out.obs.StatusCode != 200 {
		t.Fatalf("big case: %s %+v", got, out.obs)
	}
	if caseStatus(out.findings) != report.CaseError {
		t.Fatal("transport error must make the case an error")
	}

	start := time.Now()
	out = h.run("slow")
	if got := codes(out.findings); got != "replay.timeout@" {
		t.Fatalf("slow case: %s", got)
	}
	if !strings.Contains(out.obs.Error, "100ms") || time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("timeout not applied: %q after %s", out.obs.Error, time.Since(start))
	}
}

func TestRunTargetHooks(t *testing.T) {
	h := newCaseHarness(t, Options{})
	out := h.run("schema")
	if got := codes(out.findings); got != "replay.validation_unavailable@" || caseStatus(out.findings) != report.CaseInconclusive {
		t.Fatalf("schema without hook: %s", got)
	}
	out = h.run("stream")
	if got := codes(out.findings); got != "replay.stream_unavailable@" {
		t.Fatalf("stream without hook: %s", got)
	}

	var gotDir, gotTarget string
	var gotBody any
	first := 5 * time.Millisecond
	h = newCaseHarness(t, Options{Hooks: Hooks{
		CheckBody: func(ctx context.Context, dir string, c *suite.Case, target string, body any) []finding.Finding {
			gotDir, gotTarget, gotBody = dir, target, body
			return []finding.Finding{{Module: finding.ModuleValidate, Code: "validate.schema", Kind: finding.KindAssertionFailure,
				Severity: finding.SeverityError, Message: "bad"}}
		},
		ReadStream: func(ctx context.Context, dir string, c *suite.Case, target string, body io.Reader, start time.Time) StreamOutcome {
			n, events := int64(0), 0
			sc := bufio.NewScanner(body)
			for sc.Scan() {
				n += int64(len(sc.Bytes()) + 1)
				if strings.HasPrefix(sc.Text(), "data:") {
					events++
				}
			}
			return StreamOutcome{Events: events, FirstEvent: &first, Bytes: n, Termination: "eof"}
		},
	}})
	out = h.run("schema")
	if gotDir != h.rs.l.Source.Dir || gotTarget != "base" || gotBody == nil {
		t.Fatalf("hook arguments dir=%q target=%q body=%v", gotDir, gotTarget, gotBody)
	}
	if len(out.findings) != 1 || out.findings[0].CaseID != "schema" || out.findings[0].Details["target"] != "base" {
		t.Fatalf("hook findings not adopted: %+v", out.findings)
	}
	out = h.run("stream")
	if len(out.findings) != 0 || out.obs.Events != 2 || out.obs.FirstEventMS == nil || *out.obs.FirstEventMS != 5 {
		t.Fatalf("stream outcome: %s %+v", codes(out.findings), out.obs)
	}
	if out.resp == nil || !out.resp.SkipBody {
		t.Fatal("stream response must be comparable without its body")
	}
}

func TestCaseStatus(t *testing.T) {
	f := func(kind finding.Kind, sev finding.Severity, code string) finding.Finding {
		return finding.Finding{Kind: kind, Severity: sev, Code: code}
	}
	cases := []struct {
		fs   []finding.Finding
		want report.CaseStatus
	}{
		{nil, report.CasePassed},
		{[]finding.Finding{f(finding.KindObservation, finding.SeverityInfo, "x")}, report.CasePassed},
		{[]finding.Finding{f(finding.KindInconclusive, finding.SeverityWarning, "x")}, report.CaseInconclusive},
		{[]finding.Finding{f(finding.KindUnsupported, finding.SeverityWarning, "x"), f(finding.KindDifference, finding.SeverityError, "d")}, report.CaseFailed},
		{[]finding.Finding{f(finding.KindAssertionFailure, finding.SeverityError, "a"), f(finding.KindTransportError, finding.SeverityError, CodeTimeout)}, report.CaseError},
		{[]finding.Finding{f(finding.KindTransportError, finding.SeverityError, CodeCancelled)}, report.CaseCancelled},
	}
	for i, c := range cases {
		if got := caseStatus(c.fs); got != c.want {
			t.Errorf("case %d: got %s, want %s", i, got, c.want)
		}
	}
}

func TestNewOptions(t *testing.T) {
	e, err := New(Options{Concurrency: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if e.concurrency != e.lim.MaxConcurrency || e.now == nil || e.red == nil {
		t.Fatalf("defaults not applied: %+v", e)
	}
	if _, err := New(Options{RetainBodyBytes: -1}); err == nil {
		t.Fatal("negative retention accepted")
	}
	bad := Options{}
	bad.Limits.MaxConcurrency = 100000
	if _, err := New(bad); err == nil {
		t.Fatal("limits over the hard maximum accepted")
	}
}

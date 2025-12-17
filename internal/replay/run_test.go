package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/testutil"
)

func newExecutor(t *testing.T, opts Options) *Executor {
	t.Helper()
	if opts.Transport == nil {
		opts.Transport = testutil.Transport()
	}
	e, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func plainTarget(t *testing.T, name, url string) *config.ResolvedTarget {
	return resolveTarget(t, name, config.Target{BaseURL: url}, nil)
}

func statuses(res *Result) string {
	parts := make([]string, len(res.Cases))
	for i, c := range res.Cases {
		parts[i] = c.CaseID + "=" + string(c.Status)
	}
	return strings.Join(parts, " ")
}

func TestRunWritesNotAllowed(t *testing.T) {
	t.Setenv(testutil.EnvAsyncClose, "1")
	var hits atomic.Int32
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	l := loaded(t, `
version: 1
name: writes
cases:
  - {id: read, method: GET, path: /a}
  - {id: create, method: POST, path: /a}
  - {id: remove, method: DELETE, path: /a}
`)
	_, err := newExecutor(t, Options{}).Run(context.Background(), l, plainTarget(t, "base", srv.URL))
	if !errors.Is(err, ErrWritesNotAllowed) || !strings.Contains(err.Error(), "create, remove") {
		t.Fatalf("err = %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("requests were sent before the write check")
	}
	res, err := newExecutor(t, Options{AllowWrites: true}).Run(context.Background(), l, plainTarget(t, "base", srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 3 || !res.Cases[1].Mutating || res.Cases[0].Mutating {
		t.Fatalf("hits=%d cases=%+v", hits.Load(), res.Cases)
	}
	if !strings.Contains(strings.Join(res.Report.Limitations, " "), "do not undo side effects") {
		t.Fatalf("limitations: %v", res.Report.Limitations)
	}
}

func TestRunInvalidInputs(t *testing.T) {
	e := newExecutor(t, Options{})
	l := loaded(t, "version: 1\nname: x\ncases:\n  - {id: a, method: GET, path: /a, expect: {schema: missing.json}}\n")
	if _, err := e.Run(context.Background(), l, plainTarget(t, "base", "http://127.0.0.1:1")); err == nil || !strings.Contains(err.Error(), "expect.schema") {
		t.Fatalf("missing schema file: %v", err)
	}
	ok := loaded(t, "version: 1\nname: x\ncases:\n  - {id: a, method: GET, path: /a}\n")
	if _, err := e.Run(context.Background(), ok, nil); err == nil {
		t.Fatal("nil target accepted")
	}
	if _, err := e.Run(context.Background(), nil, plainTarget(t, "base", "http://127.0.0.1:1")); err == nil {
		t.Fatal("nil suite accepted")
	}
}

func TestRunCapturesAcrossCases(t *testing.T) {
	t.Setenv(testutil.EnvAsyncClose, "1")
	var mu sync.Mutex
	var paths []string
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login":
			io.WriteString(w, `{"id":"a/b c","n":42,"obj":{}}`)
		default:
			io.WriteString(w, `{}`)
		}
	}))
	l := loaded(t, `
version: 1
name: captures
cases:
  - id: login
    method: GET
    path: /login
    capture:
      - {name: id, pointer: /id}
      - {name: n, pointer: /n}
      - {name: obj, pointer: /obj}
  - id: use
    method: GET
    path: /items/{id}
    path_params: {id: "${id}"}
    query: {n: "${n}"}
  - id: dependent
    method: GET
    path: /objects/{o}
    path_params: {o: "${obj}"}
  - id: independent
    method: GET
    path: /health
`)
	var seen []string
	res, err := newExecutor(t, Options{OnCase: func(cr CaseResult) { seen = append(seen, cr.CaseID) }}).
		Run(context.Background(), l, plainTarget(t, "base", srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(res); got != "login=failed use=passed dependent=skipped independent=passed" {
		t.Fatalf("statuses: %s", got)
	}
	if strings.Join(seen, ",") != "login,use,dependent,independent" {
		t.Fatalf("OnCase order: %v", seen)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(paths, " ") != "/login? /items/a%2Fb%20c?n=42 /health?" {
		t.Fatalf("requests: %v", paths)
	}
	dep := res.Cases[2].Findings
	if len(dep) != 1 || dep[0].Code != CodeDependencyUnavailable || dep[0].Kind != finding.KindInconclusive ||
		!strings.Contains(dep[0].Message, `${obj} (capture in case "login" failed)`) {
		t.Fatalf("dependency finding: %+v", dep)
	}
	if res.Report.Status != report.StatusFailed {
		t.Fatalf("report status %s", res.Report.Status)
	}
}

func TestRunConcurrencyBound(t *testing.T) {
	t.Setenv(testutil.EnvAsyncClose, "1")
	release := testutil.Release(t)
	const limit = 3
	var inflight, maxInflight, arrivals atomic.Int32
	gate := make(chan struct{})
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inflight.Add(1)
		defer inflight.Add(-1)
		for {
			m := maxInflight.Load()
			if n <= m || maxInflight.CompareAndSwap(m, n) {
				break
			}
		}
		// The first wave waits until all of its requests have arrived,
		// which proves they ran at the same time.
		if arrivals.Add(1) == limit {
			close(gate)
		}
		select {
		case <-gate:
		case <-release:
		case <-time.After(2 * time.Second):
		}
	}))
	var b strings.Builder
	b.WriteString("version: 1\nname: pool\ncases:\n")
	for i := 0; i < 9; i++ {
		fmt.Fprintf(&b, "  - {id: c%d, method: GET, path: /c%d}\n", i, i)
	}
	res, err := newExecutor(t, Options{Concurrency: limit}).Run(context.Background(), loaded(t, b.String()), plainTarget(t, "base", srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if got := maxInflight.Load(); got != limit {
		t.Fatalf("max in-flight = %d, want %d", got, limit)
	}
	for i, c := range res.Cases {
		if c.Index != i || c.CaseID != fmt.Sprintf("c%d", i) || c.Status != report.CasePassed {
			t.Fatalf("case %d out of order or failed: %+v", i, c)
		}
	}
}

func TestRunSequentialWithCaptures(t *testing.T) {
	t.Setenv(testutil.EnvAsyncClose, "1")
	var inflight, maxInflight atomic.Int32
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inflight.Add(1)
		defer inflight.Add(-1)
		if n > maxInflight.Load() {
			maxInflight.Store(n)
		}
		io.WriteString(w, `{"v":"1"}`)
	}))
	l := loaded(t, `
version: 1
name: seq
cases:
  - {id: a, method: GET, path: /a, capture: [{name: v, pointer: /v}]}
  - {id: b, method: GET, path: /b}
  - {id: c, method: GET, path: /c}
  - {id: d, method: GET, path: /d}
`)
	if _, err := newExecutor(t, Options{Concurrency: 4}).Run(context.Background(), l, plainTarget(t, "base", srv.URL)); err != nil {
		t.Fatal(err)
	}
	if maxInflight.Load() != 1 {
		t.Fatalf("suite with captures ran %d requests at once", maxInflight.Load())
	}
}

const slowSuite = `
version: 1
name: slow
cases:
  - {id: first, method: GET, path: /slow}
  - {id: second, method: GET, path: /fast}
  - {id: third, method: GET, path: /fast}
`

func slowServer(t *testing.T, arrived chan<- struct{}) string {
	t.Helper()
	t.Setenv(testutil.EnvAsyncClose, "1")
	release := testutil.Release(t)
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/slow" {
			return
		}
		if arrived != nil {
			arrived <- struct{}{}
		}
		select {
		case <-r.Context().Done():
		case <-release:
		case <-time.After(2 * time.Second):
		}
	}))
	return srv.URL
}

func TestRunCancellation(t *testing.T) {
	arrived := make(chan struct{}, 1)
	url := slowServer(t, arrived)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
		}
		cancel()
	}()
	res, err := newExecutor(t, Options{Concurrency: 1}).Run(ctx, loaded(t, slowSuite), plainTarget(t, "base", url))
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(res); got != "first=cancelled second=cancelled third=cancelled" {
		t.Fatalf("statuses: %s", got)
	}
	if res.Report.Status != report.StatusCancelled || res.Report.ExitCode() != 2 {
		t.Fatalf("report status %s", res.Report.Status)
	}
	if fs := res.Cases[0].Findings; len(fs) != 1 || fs[0].Code != CodeCancelled {
		t.Fatalf("in-flight case findings: %+v", fs)
	}
}

func TestRunTimeoutIncomplete(t *testing.T) {
	url := slowServer(t, nil)
	opts := Options{Concurrency: 1}
	opts.Limits.RunTimeout = 150 * time.Millisecond
	res, err := newExecutor(t, opts).Run(context.Background(), loaded(t, slowSuite), plainTarget(t, "base", url))
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(res); got != "first=error second=skipped third=skipped" {
		t.Fatalf("statuses: %s", got)
	}
	if fs := res.Cases[0].Findings; len(fs) != 1 || fs[0].Code != CodeRunTimeout {
		t.Fatalf("in-flight case findings: %+v", fs)
	}
	if res.Report.Status != report.StatusIncomplete || !strings.Contains(res.Report.Error, "150ms") {
		t.Fatalf("report %s %q", res.Report.Status, res.Report.Error)
	}
}

func TestRunConnectionFailed(t *testing.T) {
	t.Setenv(testutil.EnvAsyncClose, "1")
	srv := testutil.NewServer(t, http.NotFoundHandler())
	url := srv.URL
	srv.Listener.Close() // nothing accepts connections any more
	l := loaded(t, "version: 1\nname: x\ncases:\n  - {id: a, method: GET, path: /a}\n")
	opts := Options{}
	opts.Limits.RequestTimeout = 2 * time.Second
	res, err := newExecutor(t, opts).Run(context.Background(), l, plainTarget(t, "base", url))
	if err != nil {
		t.Fatal(err)
	}
	fs := res.Cases[0].Findings
	if len(fs) != 1 || fs[0].Kind != finding.KindTransportError || res.Cases[0].Status != report.CaseError {
		t.Fatalf("findings: %+v", fs)
	}
	if fs[0].Code != CodeConnectionFailed && fs[0].Code != CodeTimeout {
		t.Fatalf("code %s", fs[0].Code)
	}
	if res.Report.Status != report.StatusFailed {
		t.Fatalf("per-case transport errors must fail the report, got %s", res.Report.Status)
	}
}

func TestRunSecretsNeverInReport(t *testing.T) {
	t.Setenv(testutil.EnvAsyncClose, "1")
	const secret = "sk-test-0123456789abcdef"
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		w.Header().Set("X-Echo", auth)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/text" {
			io.WriteString(w, "token was "+auth)
			return
		}
		fmt.Fprintf(w, `{"echo":%q,"access_token":"captured-token-value"}`, auth)
	}))
	l := loaded(t, `
version: 1
name: secrets
cases:
  - id: echo
    method: GET
    path: /echo
    capture: [{name: tok, pointer: /access_token}]
    expect:
      status: 201
      headers: {x-echo: nope}
      json:
        - {pointer: /echo, equals: "nope"}
        - {pointer: /echo, matches: "^nope$"}
  - id: reuse
    method: GET
    path: /echo/{t}
    path_params: {t: "${tok}"}
    expect:
      json:
        - {pointer: /access_token, equals: "x"}
  - id: text
    method: GET
    path: /text
    expect:
      json: [{pointer: /a, exists: true}]
`)
	target := resolveTarget(t, "base", config.Target{
		BaseURL: srv.URL,
		Headers: map[string]config.HeaderValue{"Authorization": {Env: "API_TOKEN", Prefix: "Bearer "}},
	}, map[string]string{"API_TOKEN": secret})
	res, err := newExecutor(t, Options{RetainBodyBytes: 4096}).Run(context.Background(), l, target)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{secret, "captured-token-value"} {
		if strings.Contains(string(data), leak) {
			t.Fatalf("result leaks %q:\n%s", leak, data)
		}
	}
	if !strings.Contains(string(data), "[REDACTED]") {
		t.Fatal("expected redaction markers in the result")
	}
	if res.Cases[0].Status != report.CaseFailed {
		t.Fatalf("case status %s", res.Cases[0].Status)
	}
}

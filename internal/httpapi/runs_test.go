package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/happydev123/backendlab/internal/auth"
	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/store"
)

func runRoutes(e *env) http.Handler {
	return e.router(func(r chi.Router) {
		r.Route("/api/v1", func(r chi.Router) {
			r.Use(e.api.LoadSession, e.api.RequireSession, e.api.RequireCSRF)
			r.Get("/suites", e.api.handleSuites)
			r.Get("/runs", e.api.handleListRuns)
			r.With(LimitBody(e.api.Limits().MaxJSONBytes)).Post("/runs", e.api.handleSubmit)
			r.Get("/runs/{id}", e.api.handleGetRun)
		})
	})
}

func decodeBody(t *testing.T, data []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
}

type runResp struct {
	ID    string `json:"id"`
	Suite struct {
		Name, File, SHA256 string
	} `json:"suite"`
	Mode            string   `json:"mode"`
	Targets         []string `json:"targets"`
	WritesRequested bool     `json:"writes_requested"`
	Mutating        bool     `json:"mutating"`
	Status          string   `json:"status"`
	MaxAttempts     int      `json:"max_attempts"`
	CancelRequested bool     `json:"cancel_requested"`
	ResubmittedFrom string   `json:"resubmitted_from"`
	Links           struct{ Self, Report string }
}

func submit(t *testing.T, h http.Handler, sess *auth.Session, body string) runResp {
	t.Helper()
	rec := serve(h, http.MethodPost, "/api/v1/runs", body, withBearer(sess.Token))
	if rec.Code != http.StatusCreated {
		t.Fatalf("submit %s: %d %s", body, rec.Code, rec.Body.String())
	}
	var r runResp
	decodeBody(t, rec.Body.Bytes(), &r)
	if rec.Header().Get("Location") != "/api/v1/runs/"+r.ID {
		t.Errorf("Location = %q", rec.Header().Get("Location"))
	}
	return r
}

func TestSuitesEndpoint(t *testing.T) {
	e := newEnv(t, func(c *config.Server) { c.AllowWrites = true })
	sess := e.auth.NewSession()
	rec := serve(runRoutes(e), http.MethodGet, "/api/v1/suites", "", withBearer(sess.Token))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Suites []struct {
			File          string   `json:"file"`
			Mutating      bool     `json:"mutating"`
			MutatingCases []string `json:"mutating_cases"`
			SHA256        string   `json:"sha256"`
		} `json:"suites"`
		Problems    []any    `json:"problems"`
		Targets     []string `json:"targets"`
		AllowWrites bool     `json:"allow_writes"`
	}
	decodeBody(t, rec.Body.Bytes(), &out)
	if len(out.Suites) != 2 || out.Suites[1].File != "writes.yaml" || !out.Suites[1].Mutating ||
		out.Suites[1].MutatingCases[0] != "increment" || len(out.Suites[0].SHA256) != 64 {
		t.Errorf("suites %+v", out.Suites)
	}
	if strings.Join(out.Targets, ",") != "candidate,staging" || !out.AllowWrites || out.Problems == nil {
		t.Errorf("targets %v allow %v problems %v", out.Targets, out.AllowWrites, out.Problems)
	}
	if strings.Contains(rec.Body.String(), "example.test") {
		t.Error("target URLs exposed")
	}
	expectError(t, serve(runRoutes(e), http.MethodGet, "/api/v1/suites", ""), http.StatusUnauthorized, CodeUnauthenticated)
}

func TestSubmitRun(t *testing.T) {
	e := newEnv(t)
	h := runRoutes(e)
	sess := e.auth.NewSession()

	r := submit(t, h, sess, `{"suite":"health.yaml","targets":["staging"]}`)
	if r.Mode != "run" || r.Status != "queued" || r.Suite.Name != "health-checks" || r.Mutating || r.MaxAttempts != 2 ||
		len(r.Suite.SHA256) != 64 || r.Links.Report != "/api/v1/runs/"+r.ID+"/report" {
		t.Errorf("run %+v", r)
	}
	c := submit(t, h, sess, `{"suite":"health.yaml","mode":"compare","targets":["staging","candidate"]}`)
	if c.Mode != "compare" || strings.Join(c.Targets, ",") != "staging,candidate" {
		t.Errorf("compare run %+v", c)
	}

	// Cookie-authenticated submissions need the CSRF header.
	expectError(t, serve(h, http.MethodPost, "/api/v1/runs", `{"suite":"health.yaml","targets":["staging"]}`, withCookie(sess)),
		http.StatusForbidden, CodeCSRF)
	rec := serve(h, http.MethodPost, "/api/v1/runs", `{"suite":"health.yaml","targets":["staging"]}`, withCookie(sess), withCSRF(sess.CSRFToken))
	if rec.Code != http.StatusCreated {
		t.Errorf("cookie + CSRF: %d", rec.Code)
	}
	expectError(t, serve(h, http.MethodPost, "/api/v1/runs", `{"suite":"health.yaml","targets":["staging"]}`), http.StatusUnauthorized, CodeUnauthenticated)
}

func TestSubmitPolicy(t *testing.T) {
	cases := []struct {
		name        string
		allowWrites bool
		body        string
		status      int
		code        string
		text        string
	}{
		{"unknown target", false, `{"suite":"health.yaml","targets":["production"]}`, 400, CodeInvalidSubmission, "unknown target"},
		{"url as target", false, `{"suite":"health.yaml","targets":["http://169.254.169.254/latest"]}`, 400, CodeInvalidSubmission, "unknown target"},
		{"url field rejected", false, `{"suite":"health.yaml","targets":["staging"],"url":"http://evil.example"}`, 400, CodeInvalidRequest, "unknown field"},
		{"base_url field rejected", false, `{"suite":"health.yaml","targets":["staging"],"base_url":"http://evil.example"}`, 400, CodeInvalidRequest, "unknown field"},
		{"env field rejected", false, `{"suite":"health.yaml","targets":["staging"],"env":{"TOKEN":"x"}}`, 400, CodeInvalidRequest, "unknown field"},
		{"suite content rejected", false, `{"suite":"health.yaml","targets":["staging"],"snapshot":"version: 1"}`, 400, CodeInvalidRequest, "unknown field"},
		{"unknown suite", false, `{"suite":"../etc/passwd","targets":["staging"]}`, 400, CodeUnknownSuite, "not an approved suite"},
		{"missing suite", false, `{"targets":["staging"]}`, 400, CodeInvalidRequest, "suite is required"},
		{"bad mode", false, `{"suite":"health.yaml","mode":"shell","targets":["staging"]}`, 400, CodeInvalidSubmission, "mode must be"},
		{"compare needs two", false, `{"suite":"health.yaml","mode":"compare","targets":["staging"]}`, 400, CodeInvalidSubmission, "exactly two"},
		{"run needs one", false, `{"suite":"health.yaml","targets":["staging","candidate"]}`, 400, CodeInvalidSubmission, "exactly one"},
		{"writes requested, server disallows", false, `{"suite":"health.yaml","targets":["staging"],"allow_writes":true}`, 403, CodeWritesNotPermitted, "does not allow writes"},
		{"mutating suite, server disallows", false, `{"suite":"writes.yaml","targets":["staging"],"allow_writes":true}`, 403, CodeWritesNotPermitted, "does not allow writes"},
		{"mutating suite, request does not ask", true, `{"suite":"writes.yaml","targets":["staging"]}`, 403, CodeWritesNotPermitted, "must set allow_writes"},
		{"not json", false, `suite=health.yaml`, 400, CodeInvalidRequest, "not valid JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, func(c *config.Server) { c.AllowWrites = tc.allowWrites })
			sess := e.auth.NewSession()
			rec := serve(runRoutes(e), http.MethodPost, "/api/v1/runs", tc.body, withBearer(sess.Token))
			msg := expectError(t, rec, tc.status, tc.code)
			if !strings.Contains(msg, tc.text) {
				t.Errorf("message %q lacks %q", msg, tc.text)
			}
			if runs, _, _ := e.store.ListRuns(t.Context(), store.ListRunsParams{}); len(runs) != 0 {
				t.Errorf("rejected submission was stored: %+v", runs)
			}
		})
	}
}

func TestSubmitMutatingWithBothPermissions(t *testing.T) {
	e := newEnv(t, func(c *config.Server) { c.AllowWrites = true })
	sess := e.auth.NewSession()
	r := submit(t, runRoutes(e), sess, `{"suite":"writes.yaml","targets":["staging"],"allow_writes":true}`)
	if !r.Mutating || !r.WritesRequested || r.MaxAttempts != 1 {
		t.Errorf("mutating run %+v", r)
	}
}

func TestSubmitStoreFailureIsGeneric(t *testing.T) {
	e := newEnv(t)
	sess := e.auth.NewSession()
	e.store.Err = errors.New("pq: password authentication failed for user backendlab at 10.9.9.9")
	rec := serve(runRoutes(e), http.MethodPost, "/api/v1/runs", `{"suite":"health.yaml","targets":["staging"]}`, withBearer(sess.Token))
	expectError(t, rec, http.StatusInternalServerError, CodeInternal)
	if strings.Contains(rec.Body.String(), "10.9.9.9") {
		t.Error("store error leaked")
	}
	if !strings.Contains(e.logs.String(), "enqueue run") || !strings.Contains(e.logs.String(), rec.Header().Get(RequestIDHeader)) {
		t.Errorf("failure not logged with request id: %s", e.logs.String())
	}
}

func TestListRunsPagination(t *testing.T) {
	e := newEnv(t)
	h := runRoutes(e)
	sess := e.auth.NewSession()
	var ids []string
	for i := 0; i < 5; i++ {
		ids = append(ids, submit(t, h, sess, `{"suite":"health.yaml","targets":["staging"]}`).ID)
	}
	e.store.Finish(ids[1], store.StatusFailed, nil, nil)

	type page struct {
		Runs []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"runs"`
		Total  int64 `json:"total"`
		Limit  int   `json:"limit"`
		Offset int   `json:"offset"`
	}
	get := func(q string) page {
		rec := serve(h, http.MethodGet, "/api/v1/runs"+q, "", withBearer(sess.Token))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", q, rec.Code, rec.Body.String())
		}
		var p page
		decodeBody(t, rec.Body.Bytes(), &p)
		return p
	}
	p := get("")
	if p.Total != 5 || p.Limit != store.DefaultListLimit || len(p.Runs) != 5 || p.Runs[0].ID != ids[4] {
		t.Errorf("default page %+v", p)
	}
	p = get("?limit=2&offset=2")
	if len(p.Runs) != 2 || p.Runs[0].ID != ids[2] || p.Runs[1].ID != ids[1] || p.Offset != 2 {
		t.Errorf("second page %+v", p)
	}
	p = get("?status=failed")
	if p.Total != 1 || p.Runs[0].ID != ids[1] || p.Runs[0].Status != "failed" {
		t.Errorf("failed filter %+v", p)
	}
	p = get("?status=failed,queued&status=queued")
	if p.Total != 5 {
		t.Errorf("combined filter total %d", p.Total)
	}
	p = get("?offset=100")
	if len(p.Runs) != 0 || p.Total != 5 || p.Runs == nil {
		t.Errorf("past the end %+v", p)
	}

	for _, q := range []string{"?limit=0", "?limit=201", "?limit=-1", "?limit=ten", "?offset=-1", "?offset=100001", "?status=exploded"} {
		expectError(t, serve(h, http.MethodGet, "/api/v1/runs"+q, "", withBearer(sess.Token)), http.StatusBadRequest, CodeInvalidRequest)
	}
	if rec := serve(h, http.MethodGet, "/api/v1/runs?limit=200&offset=100000", "", withBearer(sess.Token)); rec.Code != http.StatusOK {
		t.Errorf("upper bounds rejected: %d", rec.Code)
	}
}

func TestParseListQuery(t *testing.T) {
	p, e := ParseListQuery(url.Values{"status": {"running, queued", "running"}, "limit": {"10"}})
	if e != nil || p.Limit != 10 || len(p.Statuses) != 2 || p.Statuses[0] != store.StatusRunning {
		t.Errorf("params %+v, %v", p, e)
	}
	_, e = ParseListQuery(url.Values{"status": {strings.Repeat("x", 500)}})
	if e == nil || len(e.Message) > 200 {
		t.Errorf("long status echoed: %v", e)
	}
}

func TestGetRunDetail(t *testing.T) {
	e := newEnv(t)
	h := runRoutes(e)
	sess := e.auth.NewSession()
	r := submit(t, h, sess, `{"suite":"health.yaml","targets":["staging"]}`)
	ms := 12.5
	e.store.Finish(r.ID, store.StatusFailed, nil, []store.CaseResult{{
		Index: 0, CaseID: "health", Status: report.CaseFailed,
		Findings: []finding.Finding{{Module: finding.ModuleReplay, Code: "replay.status_mismatch", Kind: finding.KindAssertionFailure,
			Severity: finding.SeverityError, CaseID: "health", Message: "<script>alert(1)</script>"}},
		Observations: []report.Observation{{Target: "staging", StatusCode: 500, DurationMS: &ms}},
	}})

	rec := serve(h, http.MethodGet, "/api/v1/runs/"+r.ID, "", withBearer(sess.Token))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var d struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Cases  []struct {
			ID           string            `json:"id"`
			Status       string            `json:"status"`
			Findings     []finding.Finding `json:"findings"`
			Observations []map[string]any  `json:"observations"`
		} `json:"cases"`
	}
	decodeBody(t, rec.Body.Bytes(), &d)
	if d.ID != r.ID || d.Status != "failed" || len(d.Cases) != 1 || d.Cases[0].Findings[0].Message != "<script>alert(1)</script>" ||
		d.Cases[0].Observations[0]["status_code"] != float64(500) {
		t.Errorf("detail %+v", d)
	}
	if strings.Contains(rec.Body.String(), "snapshot") || strings.Contains(rec.Body.String(), "lease") {
		t.Errorf("detail exposes internals: %s", rec.Body.String())
	}

	for _, id := range []string{"run_doesnotexist0000", "run_aaaaaaaaaaaaaaaz", "1", "run_AAAAAAAAAAAAAAAA"} {
		expectError(t, serve(h, http.MethodGet, "/api/v1/runs/"+id, "", withBearer(sess.Token)), http.StatusNotFound, CodeNotFound)
	}
}

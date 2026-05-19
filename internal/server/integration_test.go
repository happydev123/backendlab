package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/auth"
	"github.com/happydev123/backendlab/internal/catalog"
	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/jobs"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/internal/store/storetest"
	"github.com/happydev123/backendlab/internal/testutil"
)

const operatorPassword = "operator password 2026"

var operatorHash = sync.OnceValue(func() string {
	h, err := auth.HashPassword(operatorPassword)
	if err != nil {
		panic(err)
	}
	return h
})

// client is a cookie-keeping HTTP client for the live test server.
type client struct {
	t    *testing.T
	base string
	http *http.Client
	csrf string
}

func (c *client) do(method, path, ctype string, body io.Reader, headers ...string) (*http.Response, []byte) {
	c.t.Helper()
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		c.t.Fatal(err)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp, data
}

func (c *client) json(method, path, body string, wantStatus int, out any, headers ...string) {
	c.t.Helper()
	var rd io.Reader
	ctype := ""
	if body != "" {
		rd, ctype = strings.NewReader(body), "application/json"
	}
	resp, data := c.do(method, path, ctype, rd, headers...)
	if resp.StatusCode != wantStatus {
		c.t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, wantStatus, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			c.t.Fatalf("%s %s: decode %s: %v", method, path, data, err)
		}
	}
}

func liveServer(t *testing.T) (*client, *jobs.Queue) {
	t.Helper()
	db, _ := storetest.New(t)
	cfg := testConfig(t)
	cat, err := catalog.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := auth.NewService(db, auth.OptionsFromConfig(cfg.Auth, operatorHash()))
	if err != nil {
		t.Fatal(err)
	}
	queue := jobs.NewQueue(db, nil)
	srv, err := New(Deps{Config: cfg, DB: db, Auth: svc, Queue: queue, Catalog: cat})
	if err != nil {
		t.Fatal(err)
	}
	ts := testutil.NewServer(t, srv.Handler())
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{
		Transport:     testutil.Transport(),
		Jar:           jar,
		Timeout:       60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &client{t: t, base: ts.URL, http: hc}, queue
}

type runJSON struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	CancelRequested bool   `json:"cancel_requested"`
	ResubmittedFrom string `json:"resubmitted_from"`
	Cases           []struct {
		ID       string            `json:"id"`
		Status   string            `json:"status"`
		Findings []finding.Finding `json:"findings"`
	} `json:"cases"`
}

// TestPostgresSubmitListDetailCancelReport runs the operator flow against
// a real PostgreSQL schema: readiness, login, submission, listing, detail,
// cancellation, resubmission, a simulated worker attempt, report
// downloads, the dashboard page, logout and login throttling.
func TestPostgresSubmitListDetailCancelReport(t *testing.T) {
	c, queue := liveServer(t)
	ctx := context.Background()

	c.json(http.MethodGet, "/readyz", "", http.StatusOK, nil)
	c.json(http.MethodGet, "/api/v1/runs", "", http.StatusUnauthorized, nil)

	var login struct {
		CSRFToken string `json:"csrf_token"`
	}
	c.json(http.MethodPost, "/api/v1/session", `{"password":"wrong password!!"}`, http.StatusUnauthorized, nil)
	c.json(http.MethodPost, "/api/v1/session", `{"password":"`+operatorPassword+`"}`, http.StatusOK, &login)
	csrf := []string{"X-CSRF-Token", login.CSRFToken}

	submitBody := `{"suite":"health.yaml","targets":["staging"]}`
	c.json(http.MethodPost, "/api/v1/runs", submitBody, http.StatusForbidden, nil)
	c.json(http.MethodPost, "/api/v1/runs", `{"suite":"health.yaml","targets":["prod"]}`, http.StatusBadRequest, nil, csrf...)
	var first runJSON
	c.json(http.MethodPost, "/api/v1/runs", submitBody, http.StatusCreated, &first, csrf...)
	if first.Status != "queued" {
		t.Fatalf("submitted run %+v", first)
	}

	var list struct {
		Runs  []runJSON `json:"runs"`
		Total int       `json:"total"`
	}
	c.json(http.MethodGet, "/api/v1/runs?status=queued&limit=10", "", http.StatusOK, &list)
	if list.Total != 1 || list.Runs[0].ID != first.ID {
		t.Fatalf("list %+v", list)
	}
	c.json(http.MethodGet, "/api/v1/runs?limit=500", "", http.StatusBadRequest, nil)

	var detail runJSON
	c.json(http.MethodGet, "/api/v1/runs/"+first.ID, "", http.StatusOK, &detail)
	if detail.ID != first.ID || len(detail.Cases) != 0 {
		t.Fatalf("detail %+v", detail)
	}

	var cancelled runJSON
	c.json(http.MethodPost, "/api/v1/runs/"+first.ID+"/cancel", "", http.StatusAccepted, &cancelled, csrf...)
	if cancelled.Status != "cancelled" {
		t.Fatalf("cancelled run %+v", cancelled)
	}
	c.json(http.MethodPost, "/api/v1/runs/"+first.ID+"/cancel", "", http.StatusConflict, nil, csrf...)
	c.json(http.MethodGet, "/api/v1/runs/"+first.ID+"/report", "", http.StatusNotFound, nil)

	var second runJSON
	c.json(http.MethodPost, "/api/v1/runs/"+first.ID+"/resubmit", "", http.StatusCreated, &second, csrf...)
	if second.ResubmittedFrom != first.ID || second.Status != "queued" {
		t.Fatalf("resubmitted run %+v", second)
	}
	c.json(http.MethodGet, "/api/v1/runs/"+second.ID+"/report", "", http.StatusConflict, nil)

	// A worker attempt: claim, record one case, complete with a report.
	claimed, err := queue.Claim(ctx, "test-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.PublicID != second.ID {
		t.Fatalf("claimed %s, want %s", claimed.PublicID, second.ID)
	}
	hostile := finding.Finding{Module: finding.ModuleReplay, Code: "replay.status_mismatch", Kind: finding.KindAssertionFailure,
		Severity: finding.SeverityError, CaseID: "health", Message: "<script>alert(1)</script>", Expected: "200", Observed: "503"}
	rec, err := jobs.Bounds{}.CaseRecord(jobs.CaseUpdate{CaseID: "health", Index: 0, Status: string(report.CaseFailed),
		Findings: []finding.Finding{hostile}, Observations: []report.Observation{{Target: "staging", StatusCode: 503}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.RecordCase(ctx, claimed.ID, claimed.LeaseToken, rec); err != nil {
		t.Fatal(err)
	}
	rep := report.New("run")
	rep.Targets = []string{"staging"}
	rep.Cases = []report.Case{{ID: "health", Status: report.CaseFailed}}
	rep.AddFindings(hostile)
	rep.Finalize()
	repJSON, _ := json.Marshal(rep)
	summary, _ := json.Marshal(rep.Summary)
	if err := queue.Complete(ctx, claimed.ID, claimed.LeaseToken, jobs.Outcome{Status: store.StatusFailed, Report: repJSON, Summary: summary}); err != nil {
		t.Fatal(err)
	}

	c.json(http.MethodGet, "/api/v1/runs/"+second.ID, "", http.StatusOK, &detail)
	if detail.Status != "failed" || len(detail.Cases) != 1 || detail.Cases[0].Findings[0].Message != hostile.Message {
		t.Fatalf("finished detail %+v", detail)
	}
	for format, want := range map[string]string{
		"json":     `"status": "failed"`,
		"markdown": `\<script\>`,
		"text":     "replay.status_mismatch",
	} {
		resp, body := c.do(http.MethodGet, "/api/v1/runs/"+second.ID+"/report?format="+format, "", nil)
		if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(want)) ||
			!strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") {
			t.Errorf("report %s: %d %q %s", format, resp.StatusCode, resp.Header.Get("Content-Disposition"), body)
		}
	}

	resp, page := c.do(http.MethodGet, "/runs/"+second.ID, "", nil)
	if resp.StatusCode != http.StatusOK || !bytes.Contains(page, []byte("&lt;script&gt;alert(1)&lt;/script&gt;")) ||
		bytes.Contains(page, []byte("<script>alert(1)")) || !bytes.Contains(page, []byte("Failure")) {
		t.Errorf("dashboard run page: %d", resp.StatusCode)
	}

	c.json(http.MethodDelete, "/api/v1/session", "", http.StatusNoContent, nil, csrf...)
	c.json(http.MethodGet, "/api/v1/runs", "", http.StatusUnauthorized, nil)

	// Login throttling is enforced by PostgreSQL: three attempts per
	// window from this client address (the successful login above cleared
	// the earlier failure).
	for i := 0; i < 3; i++ {
		c.json(http.MethodPost, "/api/v1/session", `{"password":"wrong password!!"}`, http.StatusUnauthorized, nil)
	}
	resp, _ = c.do(http.MethodPost, "/api/v1/session", "application/json", strings.NewReader(`{"password":"`+operatorPassword+`"}`))
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "60" {
		t.Errorf("throttled login: %d Retry-After %q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
}

// TestPostgresReadiness checks /readyz against an unmigrated schema and
// again after migrating it.
func TestPostgresReadiness(t *testing.T) {
	schema := storetest.NewSchema(t)
	db := schema.Open(t, 2)
	cfg := testConfig(t)
	cat, err := catalog.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := auth.NewService(db, auth.OptionsFromConfig(cfg.Auth, operatorHash()))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Deps{Config: cfg, DB: db, Auth: svc, Queue: jobs.NewQueue(db, nil), Catalog: cat})
	if err != nil {
		t.Fatal(err)
	}
	ts := testutil.NewServer(t, srv.Handler())
	c := &client{t: t, base: ts.URL, http: &http.Client{Transport: testutil.Transport(), Timeout: 60 * time.Second}}

	resp, body := c.do(http.MethodGet, "/readyz", "", nil)
	if resp.StatusCode != http.StatusServiceUnavailable || !bytes.Contains(body, []byte("pending migrations")) {
		t.Fatalf("unmigrated readyz: %d %s", resp.StatusCode, body)
	}
	c.json(http.MethodGet, "/healthz", "", http.StatusOK, nil)
	storetest.Migrate(t, db)
	c.json(http.MethodGet, "/readyz", "", http.StatusOK, nil)
}

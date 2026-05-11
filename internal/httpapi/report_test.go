package httpapi

import (
	"encoding/json"
	"mime"
	"net/http"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/store"
)

// sampleReport returns a finalized report with a hostile finding message.
func sampleReport(t *testing.T) []byte {
	t.Helper()
	r := report.New("run")
	r.Targets = []string{"staging"}
	r.Cases = []report.Case{{ID: "health", Status: report.CaseFailed}}
	r.AddFindings(finding.Finding{Module: finding.ModuleReplay, Code: "replay.status_mismatch", Kind: finding.KindAssertionFailure,
		Severity: finding.SeverityError, CaseID: "health", Message: "<script>alert(1)</script> \x1b[31mred", Expected: "200", Observed: "500"})
	r.Finalize()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestReportDownloadFormats(t *testing.T) {
	e := newEnv(t)
	hd := actionRoutes(e)
	sess := e.auth.NewSession()
	r := submit(t, hd, sess, `{"suite":"health.yaml","targets":["staging"]}`)
	e.store.Finish(r.ID, store.StatusFailed, sampleReport(t), nil)

	cases := []struct {
		query, ctype, ext string
		check             func(string) bool
	}{
		{"", "application/json", "json", func(b string) bool { return strings.Contains(b, `"schema": "backendlab.report/v1"`) }},
		{"?format=json", "application/json", "json", func(b string) bool { return strings.Contains(b, `"status": "failed"`) }},
		{"?format=markdown", "text/markdown", "md", func(b string) bool { return strings.Contains(b, `\<script\>`) && !strings.Contains(b, "<script>") }},
		{"?format=md", "text/markdown", "md", func(b string) bool { return strings.Contains(b, "#") }},
		{"?format=text", "text/plain", "txt", func(b string) bool { return strings.Contains(b, `\x1b`) && !strings.Contains(b, "\x1b") }},
	}
	for _, tc := range cases {
		rec := serve(hd, http.MethodGet, "/api/v1/runs/"+r.ID+"/report"+tc.query, "", withCookie(sess))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", tc.query, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, tc.ctype) {
			t.Errorf("%s: content type %q", tc.query, ct)
		}
		disp, params, err := mime.ParseMediaType(rec.Header().Get("Content-Disposition"))
		if err != nil || disp != "attachment" || params["filename"] != r.ID+"-report."+tc.ext {
			t.Errorf("%s: disposition %q", tc.query, rec.Header().Get("Content-Disposition"))
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: nosniff missing", tc.query)
		}
		if !tc.check(rec.Body.String()) {
			t.Errorf("%s: body %s", tc.query, rec.Body.String())
		}
	}
	expectError(t, serve(hd, http.MethodGet, "/api/v1/runs/"+r.ID+"/report?format=html", "", withCookie(sess)), http.StatusBadRequest, CodeInvalidRequest)
	expectError(t, serve(hd, http.MethodGet, "/api/v1/runs/"+r.ID+"/report", ""), http.StatusUnauthorized, CodeUnauthenticated)
}

func TestReportDownloadStates(t *testing.T) {
	e := newEnv(t)
	hd := actionRoutes(e)
	sess := e.auth.NewSession()
	queued := submit(t, hd, sess, `{"suite":"health.yaml","targets":["staging"]}`)
	expectError(t, serve(hd, http.MethodGet, "/api/v1/runs/"+queued.ID+"/report", "", withBearer(sess.Token)), http.StatusConflict, CodeInvalidState)

	noReport := submit(t, hd, sess, `{"suite":"health.yaml","targets":["staging"]}`)
	e.store.Finish(noReport.ID, store.StatusFailed, nil, nil)
	expectError(t, serve(hd, http.MethodGet, "/api/v1/runs/"+noReport.ID+"/report", "", withBearer(sess.Token)), http.StatusNotFound, CodeNoReport)

	corrupt := submit(t, hd, sess, `{"suite":"health.yaml","targets":["staging"]}`)
	e.store.Finish(corrupt.ID, store.StatusFailed, []byte(`{"schema":"other/v9"}`), nil)
	expectError(t, serve(hd, http.MethodGet, "/api/v1/runs/"+corrupt.ID+"/report", "", withBearer(sess.Token)), http.StatusInternalServerError, CodeInternal)

	expectError(t, serve(hd, http.MethodGet, "/api/v1/runs/run_zzzzzzzzzzzzzzzz/report", "", withBearer(sess.Token)), http.StatusNotFound, CodeNotFound)
}

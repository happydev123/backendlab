package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/happydev123/backendlab/internal/store"
)

const planBefore = `[{"Plan": {"Node Type": "Index Scan", "Relation Name": "invoices", "Index Name": "invoices_pkey",
  "Startup Cost": 0.29, "Total Cost": 8.31, "Plan Rows": 1, "Plan Width": 64}}]`

const planAfter = `[{"Plan": {"Node Type": "Seq Scan", "Relation Name": "invoices",
  "Startup Cost": 0.00, "Total Cost": 1834.00, "Plan Rows": 1, "Plan Width": 64}}]`

type formPart struct {
	field, filename, content string
}

func multipartBody(t *testing.T, parts ...formPart) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range parts {
		var w interface{ Write([]byte) (int, error) }
		var err error
		if p.filename != "" {
			w, err = mw.CreateFormFile(p.field, p.filename)
		} else {
			w, err = mw.CreateFormField(p.field)
		}
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(p.content))
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, mw.FormDataContentType()
}

func planRoutes(e *env) http.Handler {
	return e.router(func(r chi.Router) {
		r.Route("/api/v1", func(r chi.Router) {
			r.Use(e.api.LoadSession, e.api.RequireSession, e.api.RequireCSRF)
			r.With(LimitBody(e.api.Limits().MaxUploadBytes())).Post("/plans/compare", e.api.handleComparePlans)
		})
	})
}

func upload(t *testing.T, h http.Handler, token string, parts ...formPart) *httptest.ResponseRecorder {
	t.Helper()
	body, ctype := multipartBody(t, parts...)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/plans/compare", body)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestComparePlansUpload(t *testing.T) {
	e := newEnv(t)
	sess := e.auth.NewSession()
	rec := upload(t, planRoutes(e), sess.Token,
		formPart{"before", "before.json", planBefore}, formPart{"after", `..\..\tmp\after.json`, planAfter})
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	var rep struct {
		Schema  string `json:"schema"`
		Command string `json:"command"`
		Status  string `json:"status"`
		Inputs  []struct {
			Kind, Name, SHA256 string
		} `json:"inputs"`
		Findings []struct {
			Code     string `json:"code"`
			Severity string `json:"severity"`
		} `json:"findings"`
	}
	decodeBody(t, rec.Body.Bytes(), &rep)
	if rep.Schema != "backendlab.report/v1" || rep.Command != "plans.compare" || rep.Status != "passed" {
		t.Errorf("report header %+v", rep)
	}
	if len(rep.Inputs) != 2 || rep.Inputs[0].Name != "before.json" || rep.Inputs[1].Name != "after.json" || len(rep.Inputs[0].SHA256) != 64 {
		t.Errorf("inputs %+v", rep.Inputs)
	}
	found := false
	for _, f := range rep.Findings {
		if f.Code == "plans.scan_strategy_changed" {
			found = true
		}
	}
	if !found {
		t.Errorf("no scan strategy finding: %s", rec.Body.String())
	}

	// fail_on promotes the finding to an error.
	rec = upload(t, planRoutes(e), sess.Token, formPart{"fail_on", "", "scan"},
		formPart{"before", "b.json", planBefore}, formPart{"after", "a.json", planAfter})
	decodeBody(t, rec.Body.Bytes(), &rep)
	if rep.Status != "failed" {
		t.Errorf("fail_on=scan status %q", rep.Status)
	}
	if runs, _, _ := e.store.ListRuns(t.Context(), store.ListRunsParams{}); len(runs) != 0 {
		t.Error("plan comparison persisted a run")
	}
}

func TestComparePlansRejects(t *testing.T) {
	e := newEnv(t)
	e.api.limits.MaxPlanBytes = 400
	sess := e.auth.NewSession()
	h := planRoutes(e)
	cases := []struct {
		name   string
		parts  []formPart
		status int
		code   string
		text   string
	}{
		{"text plan", []formPart{{"before", "b.txt", "Seq Scan on invoices  (cost=0.00..18.34 rows=1 width=64)"}, {"after", "a.json", planAfter}},
			400, CodeInvalidRequest, "FORMAT JSON"},
		{"not a plan", []formPart{{"before", "b.json", `{"rows": []}`}, {"after", "a.json", planAfter}}, 400, CodeInvalidRequest, "Plan"},
		{"missing after", []formPart{{"before", "b.json", planBefore}}, 400, CodeInvalidRequest, `"after"`},
		{"duplicate", []formPart{{"before", "b.json", planBefore}, {"before", "b.json", planBefore}}, 400, CodeInvalidRequest, "twice"},
		{"unexpected field", []formPart{{"sql", "", "DROP TABLE runs"}}, 400, CodeInvalidRequest, "unexpected form field"},
		{"bad fail_on", []formPart{{"fail_on", "", "everything"}}, 400, CodeInvalidRequest, "fail-on"},
		{"oversize plan", []formPart{{"before", "b.json", planBefore + strings.Repeat(" ", 400)}, {"after", "a.json", planAfter}},
			413, CodeTooLarge, "exceeds 400 bytes"},
		{"csrf field ignored for bearer", []formPart{{"csrf_token", "", "x"}, {"before", "b.json", planBefore}}, 400, CodeInvalidRequest, `"after"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := upload(t, h, sess.Token, tc.parts...)
			if msg := expectError(t, rec, tc.status, tc.code); !strings.Contains(msg, tc.text) {
				t.Errorf("message %q lacks %q", msg, tc.text)
			}
		})
	}

	// Whole body above the upload bound: refused before parsing.
	body, ctype := multipartBody(t, formPart{"before", "b.json", strings.Repeat("x", int(e.api.Limits().MaxUploadBytes()))})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/plans/compare", body)
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("Authorization", "Bearer "+sess.Token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	expectError(t, rec, http.StatusRequestEntityTooLarge, CodeTooLarge)

	rec = serve(h, http.MethodPost, "/api/v1/plans/compare", `{"before":"x"}`, withBearer(sess.Token))
	expectError(t, rec, http.StatusUnsupportedMediaType, CodeUnsupportedMedia)
}

func TestComparePlansFormCSRF(t *testing.T) {
	e := newEnv(t)
	check := func(tok string) bool { return tok == "good-token" }
	run := func(parts ...formPart) *Error {
		body, ctype := multipartBody(t, parts...)
		req := httptest.NewRequest(http.MethodPost, "/plans", body)
		req.Header.Set("Content-Type", ctype)
		_, err := e.api.ComparePlans(req, check)
		return err
	}
	if err := run(formPart{"csrf_token", "", "good-token"}, formPart{"before", "b.json", planBefore}, formPart{"after", "a.json", planAfter}); err != nil {
		t.Errorf("token first: %v", err)
	}
	for name, parts := range map[string][]formPart{
		"file before token": {{"before", "b.json", planBefore}, {"csrf_token", "", "good-token"}, {"after", "a.json", planAfter}},
		"wrong token":       {{"csrf_token", "", "bad"}, {"before", "b.json", planBefore}, {"after", "a.json", planAfter}},
		"no token":          {{"fail_on", "", "scan"}},
	} {
		if err := run(parts...); err == nil || err.Code != CodeCSRF {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The comparison result carries summaries for the dashboard.
	body, ctype := multipartBody(t, formPart{"before", "b.json", planBefore}, formPart{"after", "a.json", planAfter})
	req := httptest.NewRequest(http.MethodPost, "/plans", body)
	req.Header.Set("Content-Type", ctype)
	cmp, err := e.api.ComparePlans(req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.Before.NodeCount != 1 || cmp.After.ScanStrategies["Seq Scan"] != 1 {
		b, _ := json.Marshal(cmp.After)
		t.Errorf("summaries %+v / %s", cmp.Before, b)
	}
}

func TestUploadName(t *testing.T) {
	for in, want := range map[string]string{
		"plan.json":               "plan.json",
		`C:\Users\op\before.json`: "before.json",
		"../../etc/after.json":    "after.json",
		"evil\x1b[31m.json":       `evil\x1b[31m.json`,
		"":                        "before",
		"/":                       "before",
		strings.Repeat("p", 300):  strings.Repeat("p", 100) + "...",
	} {
		if got := uploadName(in, "before"); got != want {
			t.Errorf("uploadName(%q) = %q, want %q", in, got, want)
		}
	}
}

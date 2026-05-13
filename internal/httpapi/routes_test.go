package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/auth"
	"github.com/happydev123/backendlab/internal/store"
)

func registered(e *env) http.Handler { return e.router(e.api.Register) }

func TestRegisteredRoutesRequireSession(t *testing.T) {
	e := newEnv(t)
	h := registered(e)
	for _, rt := range []struct{ method, path string }{
		{http.MethodDelete, "/api/v1/session"},
		{http.MethodGet, "/api/v1/suites"},
		{http.MethodGet, "/api/v1/runs"},
		{http.MethodPost, "/api/v1/runs"},
		{http.MethodGet, "/api/v1/runs/run_aaaaaaaaaaaaaaab"},
		{http.MethodPost, "/api/v1/runs/run_aaaaaaaaaaaaaaab/cancel"},
		{http.MethodPost, "/api/v1/runs/run_aaaaaaaaaaaaaaab/resubmit"},
		{http.MethodGet, "/api/v1/runs/run_aaaaaaaaaaaaaaab/report"},
		{http.MethodPost, "/api/v1/plans/compare"},
	} {
		rec := serve(h, rt.method, rt.path, "")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without session: %d", rt.method, rt.path, rec.Code)
		}
	}
	for _, p := range []string{"/healthz", "/readyz"} {
		if rec := serve(h, http.MethodGet, p, ""); rec.Code != http.StatusOK {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
}

func TestRegisteredRoutesErrors(t *testing.T) {
	e := newEnv(t)
	h := registered(e)
	sess := e.auth.NewSession()
	expectError(t, serve(h, http.MethodGet, "/api/v1/nothing", ""), http.StatusNotFound, CodeNotFound)
	expectError(t, serve(h, http.MethodPut, "/api/v1/runs", "{}", withBearer(sess.Token)), http.StatusMethodNotAllowed, CodeMethodNotAllowed)
	expectError(t, serve(h, http.MethodPost, "/api/v1/runs/run_aaaaaaaaaaaaaaab/cancel", strings.Repeat(" ", 2048), withBearer(sess.Token)),
		http.StatusRequestEntityTooLarge, CodeTooLarge)
	rec := serve(h, http.MethodGet, "/api/v1/runs", "", withBearer(sess.Token))
	if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get(RequestIDHeader) == "" {
		t.Errorf("shared middleware missing: %v", rec.Header())
	}
}

// TestAPIFlow drives the registered API the way a curl user would:
// login, submit, list, detail, cancel, resubmit, report, logout.
func TestAPIFlow(t *testing.T) {
	e := newEnv(t)
	h := registered(e)

	rec := serve(h, http.MethodPost, "/api/v1/session", `{"password":"`+testPassword+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	var login struct {
		Token     string `json:"token"`
		CSRFToken string `json:"csrf_token"`
	}
	decodeBody(t, rec.Body.Bytes(), &login)
	sess := &auth.Session{Token: login.Token}
	csrf := withCSRF(login.CSRFToken)

	run := submit(t, h, sess, `{"suite":"health.yaml","targets":["staging"]}`)

	rec = serve(h, http.MethodGet, "/api/v1/runs?status=queued", "", withCookie(sess))
	var list struct {
		Runs []struct {
			ID string `json:"id"`
		} `json:"runs"`
		Total int `json:"total"`
	}
	decodeBody(t, rec.Body.Bytes(), &list)
	if list.Total != 1 || list.Runs[0].ID != run.ID {
		t.Fatalf("list %+v", list)
	}

	if rec := serve(h, http.MethodGet, "/api/v1/runs/"+run.ID, "", withCookie(sess)); rec.Code != http.StatusOK {
		t.Fatalf("detail: %d", rec.Code)
	}
	rec = serve(h, http.MethodPost, "/api/v1/runs/"+run.ID+"/cancel", "", withCookie(sess), csrf)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body.String())
	}
	expectError(t, serve(h, http.MethodGet, "/api/v1/runs/"+run.ID+"/report", "", withCookie(sess)), http.StatusNotFound, CodeNoReport)

	rec = serve(h, http.MethodPost, "/api/v1/runs/"+run.ID+"/resubmit", "", withCookie(sess), csrf)
	if rec.Code != http.StatusCreated {
		t.Fatalf("resubmit: %d %s", rec.Code, rec.Body.String())
	}
	var again runResp
	decodeBody(t, rec.Body.Bytes(), &again)
	e.store.Finish(again.ID, store.StatusFailed, sampleReport(t), nil)
	rec = serve(h, http.MethodGet, "/api/v1/runs/"+again.ID+"/report?format=json", "", withCookie(sess))
	var rep map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil || rep["status"] != "failed" {
		t.Fatalf("report: %d %s", rec.Code, rec.Body.String())
	}

	if rec := serve(h, http.MethodDelete, "/api/v1/session", "", withCookie(sess), csrf); rec.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", rec.Code)
	}
	expectError(t, serve(h, http.MethodGet, "/api/v1/runs", "", withCookie(sess)), http.StatusUnauthorized, CodeUnauthenticated)
}

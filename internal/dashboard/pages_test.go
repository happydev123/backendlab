package dashboard

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/httpapi"
	"github.com/happydev123/backendlab/internal/store"
)

// basicRoutes registers the pages of the sign-in, list and submission
// handlers the way Register does.
func basicRoutes(e *denv) http.Handler {
	form := e.dash.limitBody(e.api.Limits().MaxFormBytes)
	return e.mux(func(r chi.Router) {
		r.Use(e.api.LoadSession)
		r.Get("/login", e.dash.handleLoginPage)
		r.With(form).Post("/login", e.dash.handleLogin)
		r.Group(func(r chi.Router) {
			r.Use(e.dash.requireLogin)
			r.With(form, e.dash.requireCSRF).Post("/logout", e.dash.handleLogout)
			r.Get("/runs", e.dash.handleRuns)
			r.Get("/runs/new", e.dash.handleSubmitForm)
			r.With(form, e.dash.requireCSRF).Post("/runs", e.dash.handleSubmit)
		})
	})
}

func TestNewRequiresAPI(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Error("nil api accepted")
	}
}

func TestLoginPageFlow(t *testing.T) {
	e := newDenv(t)
	h := basicRoutes(e)

	expectRedirect(t, get(h, "/runs"), "/login")
	expectPage(t, get(h, "/login"), http.StatusOK, `<label for="password">`, `type="password"`)

	rec := postForm(h, "/login", url.Values{"password": {"not the password"}})
	body := expectPage(t, rec, http.StatusUnauthorized, "The password is not correct.")
	if strings.Contains(body, "not the password") {
		t.Error("password echoed")
	}

	rec = postForm(h, "/login", url.Values{"password": {testPassword}})
	expectRedirect(t, rec, "/runs")
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != httpapi.SessionCookie || !cookies[0].HttpOnly || !cookies[0].Secure ||
		cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie %+v", cookies)
	}
	// A signed-in operator visiting /login goes to the run list.
	expectRedirect(t, get(h, "/login", asOperator(e.sess)), "/runs")
}

func TestLoginPageThrottled(t *testing.T) {
	e := newDenv(t)
	h := basicRoutes(e)
	for i := 0; i < 2; i++ {
		postForm(h, "/login", url.Values{"password": {"wrong password!!"}})
	}
	rec := postForm(h, "/login", url.Values{"password": {testPassword}})
	expectPage(t, rec, http.StatusTooManyRequests, "too many login attempts")
	if rec.Header().Get("Retry-After") != "60" || len(rec.Result().Cookies()) != 0 {
		t.Errorf("throttled: Retry-After %q cookies %v", rec.Header().Get("Retry-After"), rec.Result().Cookies())
	}
}

func TestLogoutNeedsCSRF(t *testing.T) {
	e := newDenv(t)
	h := basicRoutes(e)
	expectPage(t, postForm(h, "/logout", url.Values{}, asOperator(e.sess)), http.StatusForbidden, "CSRF token")
	expectPage(t, postForm(h, "/logout", url.Values{"csrf_token": {"forged"}}, asOperator(e.sess)), http.StatusForbidden)
	if e.auth.Sessions() != 1 {
		t.Fatal("session removed without a valid token")
	}
	rec := postForm(h, "/logout", url.Values{"csrf_token": {e.sess.CSRFToken}}, asOperator(e.sess))
	expectRedirect(t, rec, "/login")
	if e.auth.Sessions() != 0 || rec.Result().Cookies()[0].MaxAge >= 0 {
		t.Error("logout did not end the session")
	}
}

func TestRunListPage(t *testing.T) {
	e := newDenv(t)
	h := basicRoutes(e)
	expectPage(t, get(h, "/runs", asOperator(e.sess)), http.StatusOK, "No runs yet.", `<nav aria-label="Main">`)

	var ids []string
	for i := 0; i < pageSize+3; i++ {
		run, err := e.store.EnqueueRun(t.Context(), store.NewRun{SuiteName: "health-checks", SuiteFile: "health.yaml",
			SuiteSnapshot: []byte("x"), Mode: store.ModeRun, Targets: []string{"staging"}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, run.PublicID)
	}
	e.store.Finish(ids[0], store.StatusInterrupted, nil, nil)

	body := expectPage(t, get(h, "/runs", asOperator(e.sess)), http.StatusOK, "Page 1 of 2", `href="/runs?page=2"`, ids[len(ids)-1])
	if strings.Contains(body, ids[0]) || strings.Contains(body, "Previous page") {
		t.Error("first page shows the oldest run or a previous link")
	}
	expectPage(t, get(h, "/runs?page=2", asOperator(e.sess)), http.StatusOK, ids[0], "Previous page", "Page 2 of 2")
	expectPage(t, get(h, "/runs?status=interrupted", asOperator(e.sess)), http.StatusOK,
		ids[0], "1 run with status Interrupted (needs review)", `<option value="interrupted" selected>`)
	expectPage(t, get(h, "/runs?status=failed", asOperator(e.sess)), http.StatusOK, "No runs with status Failed.")
	expectPage(t, get(h, "/runs?page=9", asOperator(e.sess)), http.StatusOK, "past the end")
	for _, q := range []string{"?page=0", "?page=x", "?page=999999", "?status=bogus"} {
		expectPage(t, get(h, "/runs"+q, asOperator(e.sess)), http.StatusBadRequest)
	}
}

func TestSubmitPage(t *testing.T) {
	e := newDenv(t)
	h := basicRoutes(e)
	body := expectPage(t, get(h, "/runs/new", asOperator(e.sess)), http.StatusOK,
		"health-checks", "counter-writes", "mutating: increment", "Suites that failed to load", "broken.yaml",
		`<option value="candidate" selected>`, "does not allow writes")
	if strings.Contains(body, "<script>") && !strings.Contains(body, `<script src="/static/app.js" defer></script>`) {
		t.Error("problem text not escaped")
	}
	if strings.Contains(body, `name="allow_writes"`) {
		t.Error("writes checkbox shown while writes are disallowed")
	}
	if strings.Contains(body, "example.test") {
		t.Error("target URLs shown")
	}

	ok := url.Values{"csrf_token": {e.sess.CSRFToken}, "suite": {"health.yaml"}, "mode": {"compare"},
		"target": {"staging"}, "baseline": {"staging"}, "candidate": {"candidate"}}
	rec := postForm(h, "/runs", ok, asOperator(e.sess))
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/runs/run_") {
		t.Fatalf("submit: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	run, err := e.store.GetRun(t.Context(), strings.TrimPrefix(rec.Header().Get("Location"), "/runs/"))
	if err != nil || run.Mode != store.ModeCompare || strings.Join(run.Targets, ",") != "staging,candidate" {
		t.Fatalf("stored run %+v, %v", run, err)
	}

	noToken := url.Values{"suite": {"health.yaml"}, "mode": {"run"}, "target": {"staging"}}
	expectPage(t, postForm(h, "/runs", noToken, asOperator(e.sess)), http.StatusForbidden, "CSRF token")
}

func TestSubmitPagePolicyErrors(t *testing.T) {
	e := newDenv(t)
	h := basicRoutes(e)
	base := func(kv ...string) url.Values {
		v := url.Values{"csrf_token": {e.sess.CSRFToken}, "mode": {"run"}, "target": {"staging"}}
		for i := 0; i < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return v
	}
	expectPage(t, postForm(h, "/runs", base("suite", "writes.yaml", "allow_writes", "1"), asOperator(e.sess)),
		http.StatusForbidden, "does not allow writes", `value="writes.yaml" required checked`)
	expectPage(t, postForm(h, "/runs", base("suite", "health.yaml", "target", "production"), asOperator(e.sess)),
		http.StatusBadRequest, "unknown target")
	expectPage(t, postForm(h, "/runs", base("suite", "nope.yaml"), asOperator(e.sess)), http.StatusBadRequest, "not an approved suite")

	allowing := newDenv(t, func(c *config.Server) { c.AllowWrites = true })
	h = basicRoutes(allowing)
	v := url.Values{"csrf_token": {allowing.sess.CSRFToken}, "suite": {"writes.yaml"}, "mode": {"run"}, "target": {"staging"}}
	expectPage(t, postForm(h, "/runs", v, asOperator(allowing.sess)), http.StatusForbidden, "must set allow_writes", `name="allow_writes"`)
	v.Set("allow_writes", "1")
	if rec := postForm(h, "/runs", v, asOperator(allowing.sess)); rec.Code != http.StatusSeeOther {
		t.Errorf("mutating suite with both permissions: %d", rec.Code)
	}
	big := url.Values{"csrf_token": {allowing.sess.CSRFToken}, "suite": {strings.Repeat("x", 70<<10)}}
	expectPage(t, postForm(h, "/runs", big, asOperator(allowing.sess)), http.StatusRequestEntityTooLarge)
}

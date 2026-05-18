package dashboard

import (
	"net/http"
	"strings"
	"testing"
)

func TestRegisterStaticAndFallbacks(t *testing.T) {
	e := newDenv(t)
	h := fullRoutes(e)

	rec := get(h, "/static/app.css")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "prefers-color-scheme: dark") ||
		rec.Header().Get("Cache-Control") != "public, max-age=300" {
		t.Errorf("app.css: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if rec := get(h, "/static/app.js"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "data-confirm") {
		t.Errorf("app.js: %d", rec.Code)
	}
	for _, p := range []string{"/static/", "/static/missing.css", "/static/../embed.go"} {
		if rec := get(h, p); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}

	expectRedirect(t, get(h, "/", asOperator(e.sess)), "/runs")
	expectPage(t, get(h, "/no/such/page", asOperator(e.sess)), http.StatusNotFound, "the page does not exist", "Back to the run list")
	expectPage(t, get(h, "/no/such/page"), http.StatusNotFound, "Sign in")

	// The API keeps its JSON 404 under /api/v1.
	rec = get(h, "/api/v1/nothing")
	if rec.Code != http.StatusNotFound || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("API 404: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec := get(h, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("healthz: %d", rec.Code)
	}

	// Pages carry the shared security headers.
	rec = get(h, "/login")
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("login page headers %v", rec.Header())
	}
}

func TestRegisteredPagesRequireLogin(t *testing.T) {
	e := newDenv(t)
	h := fullRoutes(e)
	for _, p := range []string{"/runs", "/runs/new", "/plans", "/runs/run_aaaaaaaaaaaaaaab"} {
		expectRedirect(t, get(h, p), "/login")
	}
	for _, p := range []string{"/runs", "/logout", "/plans", "/runs/run_aaaaaaaaaaaaaaab/cancel"} {
		expectRedirect(t, do(h, http.MethodPost, p, "", "application/x-www-form-urlencoded"), "/login")
	}
}

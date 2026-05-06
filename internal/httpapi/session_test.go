package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/happydev123/backendlab/internal/auth"
	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/httpapi/apitest"
)

func protected(e *env) http.Handler {
	return e.router(func(r chi.Router) {
		r.Route("/api/v1", func(r chi.Router) {
			r.Use(e.api.LoadSession, e.api.RequireSession, e.api.RequireCSRF)
			r.Get("/thing", func(w http.ResponseWriter, r *http.Request) {
				if _, ok := SessionFrom(r.Context()); !ok {
					t := "session missing in handler"
					http.Error(w, t, 500)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
			r.Post("/thing", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
		})
	})
}

func TestNewValidatesDeps(t *testing.T) {
	e := newEnv(t)
	base := Deps{Config: e.cfg, Store: e.store, Queue: apitest.Queue{Store: e.store}, Auth: e.auth, Catalog: e.api.Catalog()}
	for name, mod := range map[string]func(*Deps){
		"config":  func(d *Deps) { d.Config = nil },
		"store":   func(d *Deps) { d.Store = nil },
		"queue":   func(d *Deps) { d.Queue = nil },
		"auth":    func(d *Deps) { d.Auth = nil },
		"catalog": func(d *Deps) { d.Catalog = nil },
	} {
		d := base
		mod(&d)
		if _, err := New(d); err == nil {
			t.Errorf("missing %s accepted", name)
		}
	}
	a, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	l := a.Limits()
	if l.MaxJSONBytes != 64<<10 || l.MaxPlanBytes != e.cfg.Limits.MaxPlanBytes || l.ReadyTimeout != 2*time.Second {
		t.Errorf("default limits %+v", l)
	}
	if l.MaxUploadBytes() <= 2*l.MaxPlanBytes {
		t.Error("upload bound does not leave room for multipart framing")
	}
}

func TestRequireSession(t *testing.T) {
	e := newEnv(t)
	h := protected(e)
	sess := e.auth.NewSession()

	rec := serve(h, http.MethodGet, "/api/v1/thing", "")
	expectError(t, rec, http.StatusUnauthorized, CodeUnauthenticated)
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("401 without WWW-Authenticate")
	}
	for name, opt := range map[string]reqOpt{
		"unknown cookie":    withCookie(&auth.Session{Token: "nope"}),
		"unknown bearer":    withBearer("nope"),
		"basic auth":        withHeader("Authorization", "Basic dXNlcjpwYXNz"),
		"bearer w/o token":  withHeader("Authorization", "Bearer"),
		"bearer and cookie": func(r *http.Request) { withCookie(sess)(r); withBearer("invalid")(r) },
	} {
		if rec := serve(h, http.MethodGet, "/api/v1/thing", "", opt); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status %d", name, rec.Code)
		}
	}
	if rec := serve(h, http.MethodGet, "/api/v1/thing", "", withCookie(sess)); rec.Code != http.StatusNoContent {
		t.Errorf("cookie session: %d %s", rec.Code, rec.Body.String())
	}
	if rec := serve(h, http.MethodGet, "/api/v1/thing", "", withHeader("Authorization", "bearer "+sess.Token)); rec.Code != http.StatusNoContent {
		t.Errorf("bearer session (lower-case scheme): %d", rec.Code)
	}
}

func TestCSRFForCookieAuthenticatedWrites(t *testing.T) {
	e := newEnv(t)
	h := protected(e)
	sess := e.auth.NewSession()

	rec := serve(h, http.MethodPost, "/api/v1/thing", "{}", withCookie(sess))
	expectError(t, rec, http.StatusForbidden, CodeCSRF)
	rec = serve(h, http.MethodPost, "/api/v1/thing", "{}", withCookie(sess), withCSRF("wrong-token"))
	expectError(t, rec, http.StatusForbidden, CodeCSRF)
	other := e.auth.NewSession()
	rec = serve(h, http.MethodPost, "/api/v1/thing", "{}", withCookie(sess), withCSRF(other.CSRFToken))
	expectError(t, rec, http.StatusForbidden, CodeCSRF)

	if rec := serve(h, http.MethodPost, "/api/v1/thing", "{}", withCookie(sess), withCSRF(sess.CSRFToken)); rec.Code != http.StatusNoContent {
		t.Errorf("valid CSRF token: %d", rec.Code)
	}
	// Bearer-authenticated requests are not subject to CSRF.
	if rec := serve(h, http.MethodPost, "/api/v1/thing", "{}", withBearer(sess.Token)); rec.Code != http.StatusNoContent {
		t.Errorf("bearer without CSRF token: %d", rec.Code)
	}
	// Safe methods pass without a token.
	if rec := serve(h, http.MethodGet, "/api/v1/thing", "", withCookie(sess)); rec.Code != http.StatusNoContent {
		t.Errorf("GET with cookie: %d", rec.Code)
	}
}

func TestCSRFValidFormToken(t *testing.T) {
	e := newEnv(t)
	sess := e.auth.NewSession()
	var results []bool
	h := e.router(func(r chi.Router) {
		r.Use(e.api.LoadSession)
		r.Post("/form", func(w http.ResponseWriter, r *http.Request) {
			results = append(results, CSRFValid(r, r.Header.Get("X-Test-Form")))
		})
	})
	serve(h, http.MethodPost, "/form", "", withCookie(sess), withHeader("X-Test-Form", sess.CSRFToken))
	serve(h, http.MethodPost, "/form", "", withCookie(sess), withHeader("X-Test-Form", "bad"))
	serve(h, http.MethodPost, "/form", "", withCookie(sess))
	serve(h, http.MethodPost, "/form", "")
	if len(results) != 4 || !results[0] || results[1] || results[2] || !results[3] {
		t.Errorf("form CSRF results %v (want true false false true)", results)
	}
}

func TestSessionStoreFailureIs503(t *testing.T) {
	e := newEnv(t)
	h := protected(e)
	sess := e.auth.NewSession()
	e.auth.Err = errors.New("dial tcp 10.0.0.5:5432: connection refused password=hunter2")
	rec := serve(h, http.MethodGet, "/api/v1/thing", "", withCookie(sess))
	msg := expectError(t, rec, http.StatusServiceUnavailable, CodeUnavailable)
	if strings.Contains(msg, "10.0.0.5") || strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("internal error leaked: %s", rec.Body.String())
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	sess := &auth.Session{Token: "tok", ExpiresAt: time.Now().Add(time.Hour)}
	for name, tc := range map[string]struct {
		secure *bool
		want   bool
	}{
		"default": {nil, true},
		"enabled": {boolPtr(true), true},
		"off":     {boolPtr(false), false},
	} {
		e := newEnv(t, func(c *config.Server) { c.Auth.SecureCookies = tc.secure })
		rec := httptest.NewRecorder()
		e.api.SetSessionCookie(rec, sess)
		cookies := rec.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("%s: cookies %v", name, cookies)
		}
		c := cookies[0]
		if c.Name != SessionCookie || c.Value != "tok" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode ||
			c.Path != "/" || c.Secure != tc.want || c.MaxAge < 3500 || c.MaxAge > 3600 {
			t.Errorf("%s: cookie %+v", name, c)
		}

		rec = httptest.NewRecorder()
		e.api.ClearSessionCookie(rec)
		c = rec.Result().Cookies()[0]
		if c.MaxAge >= 0 || c.Value != "" || !c.HttpOnly || c.Secure != tc.want {
			t.Errorf("%s: cleared cookie %+v", name, c)
		}
	}
}

func boolPtr(b bool) *bool { return &b }

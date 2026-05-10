package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func sessionRoutes(e *env) http.Handler {
	return e.router(func(r chi.Router) {
		r.Route("/api/v1", func(r chi.Router) {
			r.Use(e.api.LoadSession)
			r.With(LimitBody(e.api.Limits().MaxJSONBytes)).Post("/session", e.api.handleLogin)
			r.With(e.api.RequireSession, e.api.RequireCSRF).Delete("/session", e.api.handleLogout)
		})
	})
}

func TestLoginSetsCookieAndReturnsCSRF(t *testing.T) {
	e := newEnv(t)
	h := sessionRoutes(e)
	rec := serve(h, http.MethodPost, "/api/v1/session", `{"password":"`+testPassword+`"}`, withRemote("192.0.2.10:51234"))
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Token     string `json:"token"`
		CSRFToken string `json:"csrf_token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Token == "" || resp.CSRFToken == "" || resp.ExpiresAt == "" {
		t.Errorf("login response %+v", resp)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != SessionCookie || cookies[0].Value != resp.Token || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Errorf("cookies %+v", cookies)
	}
	if got := e.auth.Keys; len(got) != 1 || got[0] != "192.0.2.10" {
		t.Errorf("throttle keys %v", got)
	}
	if strings.Contains(e.logs.String(), testPassword) {
		t.Error("password logged")
	}
}

func TestLoginErrors(t *testing.T) {
	e := newEnv(t)
	h := sessionRoutes(e)
	msg := expectError(t, serve(h, http.MethodPost, "/api/v1/session", `{"password":"wrong password here"}`), http.StatusUnauthorized, CodeInvalidCredentials)
	if strings.Contains(msg, "wrong password here") {
		t.Error("password echoed")
	}
	expectError(t, serve(h, http.MethodPost, "/api/v1/session", `{"password":1}`), http.StatusBadRequest, CodeInvalidRequest)
	expectError(t, serve(h, http.MethodPost, "/api/v1/session", `{"password":"x","user":"admin"}`), http.StatusBadRequest, CodeInvalidRequest)
	expectError(t, serve(h, http.MethodPost, "/api/v1/session", "password="+testPassword,
		withHeader("Content-Type", "application/x-www-form-urlencoded")), http.StatusUnsupportedMediaType, CodeUnsupportedMedia)
	big := `{"password":"` + strings.Repeat("a", 70<<10) + `"}`
	expectError(t, serve(h, http.MethodPost, "/api/v1/session", big), http.StatusRequestEntityTooLarge, CodeTooLarge)
}

func TestLoginThrottlingReturns429(t *testing.T) {
	e := newEnv(t)
	h := sessionRoutes(e)
	from := withRemote("198.51.100.7:40000")
	for i := 0; i < 3; i++ {
		expectError(t, serve(h, http.MethodPost, "/api/v1/session", `{"password":"wrong password!!"}`, from), http.StatusUnauthorized, CodeInvalidCredentials)
	}
	rec := serve(h, http.MethodPost, "/api/v1/session", `{"password":"`+testPassword+`"}`, from)
	expectError(t, rec, http.StatusTooManyRequests, CodeThrottled)
	if rec.Header().Get("Retry-After") != "900" {
		t.Errorf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("throttled login set a cookie")
	}
	// X-Forwarded-For does not change the throttle key.
	rec = serve(h, http.MethodPost, "/api/v1/session", `{"password":"`+testPassword+`"}`, from, withHeader("X-Forwarded-For", "203.0.113.99"))
	expectError(t, rec, http.StatusTooManyRequests, CodeThrottled)
	// Another client address is unaffected.
	if rec := serve(h, http.MethodPost, "/api/v1/session", `{"password":"`+testPassword+`"}`, withRemote("198.51.100.8:40000")); rec.Code != http.StatusOK {
		t.Errorf("other client: %d", rec.Code)
	}
}

func TestLogout(t *testing.T) {
	e := newEnv(t)
	h := sessionRoutes(e)
	sess := e.auth.NewSession()

	expectError(t, serve(h, http.MethodDelete, "/api/v1/session", ""), http.StatusUnauthorized, CodeUnauthenticated)
	expectError(t, serve(h, http.MethodDelete, "/api/v1/session", "", withCookie(sess)), http.StatusForbidden, CodeCSRF)

	rec := serve(h, http.MethodDelete, "/api/v1/session", "", withCookie(sess), withCSRF(sess.CSRFToken))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: %d %s", rec.Code, rec.Body.String())
	}
	if c := rec.Result().Cookies(); len(c) != 1 || c[0].MaxAge >= 0 {
		t.Errorf("logout cookie %+v", c)
	}
	if e.auth.Sessions() != 0 {
		t.Error("session not deleted")
	}
	expectError(t, serve(h, http.MethodDelete, "/api/v1/session", "", withBearer(sess.Token)), http.StatusUnauthorized, CodeUnauthenticated)
}

func TestClientIP(t *testing.T) {
	for addr, want := range map[string]string{
		"192.0.2.1:1234":   "192.0.2.1",
		"[2001:db8::1]:80": "2001:db8::1",
		"pipe":             "pipe",
		"":                 "unknown",
	} {
		r, _ := http.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = addr
		if got := ClientIP(r); got != want {
			t.Errorf("ClientIP(%q) = %q, want %q", addr, got, want)
		}
	}
}

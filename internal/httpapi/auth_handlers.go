package httpapi

import (
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/happydev123/backendlab/internal/auth"
)

// defaultRetryAfter is used when the login window is not configured.
const defaultRetryAfter = time.Minute

// ClientIP returns the address used as the login throttle key: the host
// part of the TCP peer address. X-Forwarded-For and similar headers are
// ignored because there is no trusted-proxy setting; behind a reverse
// proxy every client therefore shares the proxy's throttle key.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		if r.RemoteAddr != "" {
			return r.RemoteAddr
		}
		return "unknown"
	}
	return host
}

// Login checks the operator password for the client of r and returns the
// new session. Throttled attempts get 429 with Retry-After set to the
// login window (the longest possible wait); wrong passwords get a generic
// 401.
func (a *API) Login(r *http.Request, password string) (*auth.Session, *Error) {
	sess, err := a.auth.Login(r.Context(), password, ClientIP(r))
	switch {
	case err == nil:
		return sess, nil
	case errors.Is(err, auth.ErrThrottled):
		retry := a.cfg.Auth.LoginWindow
		if retry <= 0 {
			retry = defaultRetryAfter
		}
		a.logFor(r.Context()).Warn("login throttled")
		return nil, &Error{Status: http.StatusTooManyRequests, Code: CodeThrottled,
			Message: "too many login attempts; try again later", RetryAfter: retry}
	case errors.Is(err, auth.ErrInvalidCredentials):
		return nil, Errorf(http.StatusUnauthorized, CodeInvalidCredentials, "invalid password")
	}
	return nil, a.fail(r.Context(), "login", err)
}

// Logout deletes the session of r, if any, and expires the cookie.
func (a *API) Logout(w http.ResponseWriter, r *http.Request) *Error {
	if sess, ok := SessionFrom(r.Context()); ok {
		if err := a.auth.Logout(r.Context(), sess.Token); err != nil {
			return a.fail(r.Context(), "logout", err)
		}
	}
	a.ClearSessionCookie(w)
	return nil
}

type loginRequest struct {
	Password string `json:"password"`
}

type loginResponse struct {
	// Token is the session token, equal to the cookie value. API clients
	// send it as "Authorization: Bearer <token>".
	Token     string    `json:"token"`
	CSRFToken string    `json:"csrf_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// handleLogin serves POST /api/v1/session.
func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if e := decodeJSON(r, &req); e != nil {
		WriteError(w, e)
		return
	}
	sess, e := a.Login(r, req.Password)
	if e != nil {
		WriteError(w, e)
		return
	}
	a.SetSessionCookie(w, sess)
	writeJSON(w, http.StatusOK, loginResponse{Token: sess.Token, CSRFToken: sess.CSRFToken, ExpiresAt: sess.ExpiresAt.UTC()})
}

// handleLogout serves DELETE /api/v1/session.
func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	if e := a.Logout(w, r); e != nil {
		WriteError(w, e)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

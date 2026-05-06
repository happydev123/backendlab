package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/happydev123/backendlab/internal/auth"
)

// Session transport names.
const (
	// SessionCookie holds the session token for browsers.
	SessionCookie = "backendlab_session"
	// CSRFHeader carries the CSRF token on cookie-authenticated API
	// requests that change state.
	CSRFHeader = "X-CSRF-Token"
	// CSRFField carries the CSRF token in dashboard forms.
	CSRFField = "csrf_token"
)

type sessionKey struct{}

// sessionInfo is what LoadSession stores in the request context.
type sessionInfo struct {
	sess *auth.Session
	// viaCookie is true when the token came from the session cookie. Only
	// those requests need a CSRF token: browsers attach cookies to
	// cross-site requests automatically, but never an Authorization
	// header.
	viaCookie bool
}

// SetSessionCookie writes the session cookie: HttpOnly, SameSite=Lax,
// Path=/, expiring with the session, and Secure unless the server
// configuration disables secure cookies (auth.secure_cookies: false, for
// plain-HTTP development on localhost).
func (a *API) SetSessionCookie(w http.ResponseWriter, s *auth.Session) {
	maxAge := int(time.Until(s.ExpiresAt) / time.Second)
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    s.Token,
		Path:     "/",
		Expires:  s.ExpiresAt.UTC(),
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   a.cfg.Auth.SecureCookiesEnabled(),
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie expires the session cookie in the browser.
func (a *API) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.cfg.Auth.SecureCookiesEnabled(),
		SameSite: http.SameSiteLaxMode,
	})
}

// LoadSession resolves the session of the request, if any, and stores it
// in the context. API clients may send "Authorization: Bearer <token>";
// when that header is present the cookie is ignored, and a malformed or
// unknown bearer token leaves the request unauthenticated. A failure of
// the session store answers 503 rather than treating the request as
// anonymous.
func (a *API) LoadSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, viaCookie, ok := sessionToken(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		sess, err := a.auth.Authenticate(r.Context(), token)
		switch {
		case errors.Is(err, auth.ErrUnauthenticated):
			next.ServeHTTP(w, r)
			return
		case err != nil:
			a.logFor(r.Context()).Error("session lookup failed", "error", a.cfg.Redactor().Error(err))
			writeProblem(w, r, Errorf(http.StatusServiceUnavailable, CodeUnavailable, "session store is unavailable; try again later"))
			return
		}
		ctx := context.WithValue(r.Context(), sessionKey{}, sessionInfo{sess: sess, viaCookie: viaCookie})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// sessionToken extracts the token from the Authorization header or the
// session cookie.
func sessionToken(r *http.Request) (token string, viaCookie, ok bool) {
	if h := r.Header.Get("Authorization"); h != "" {
		scheme, value, found := strings.Cut(h, " ")
		if !found || !strings.EqualFold(scheme, "Bearer") {
			return "", false, false
		}
		return strings.TrimSpace(value), false, true
	}
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return "", false, false
	}
	return c.Value, true, true
}

// SessionFrom returns the session stored by LoadSession.
func SessionFrom(ctx context.Context) (*auth.Session, bool) {
	info, ok := ctx.Value(sessionKey{}).(sessionInfo)
	return info.sess, ok && info.sess != nil
}

// RequireSession answers 401 with the JSON envelope when the request has
// no valid session.
func (a *API) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := SessionFrom(r.Context()); !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="backendlab"`)
			WriteError(w, Errorf(http.StatusUnauthorized, CodeUnauthenticated, "authentication required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireCSRF rejects state-changing API requests authenticated by cookie
// unless they carry the session's CSRF token in the X-CSRF-Token header.
// Safe methods and bearer-authenticated requests pass.
func (a *API) RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !CSRFValid(r, "") {
			WriteError(w, Errorf(http.StatusForbidden, CodeCSRF, "missing or invalid CSRF token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CSRFValid reports whether r passes the CSRF check. formToken is the
// value of the csrf_token form field for dashboard forms ("" for API
// requests); the X-CSRF-Token header is accepted as well. Requests with
// safe methods, without a session, or authenticated by bearer token need
// no CSRF token. The comparison is constant-time (auth.ValidCSRF).
func CSRFValid(r *http.Request, formToken string) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	info, ok := r.Context().Value(sessionKey{}).(sessionInfo)
	if !ok || !info.viaCookie {
		return true
	}
	token := r.Header.Get(CSRFHeader)
	if token == "" {
		token = formToken
	}
	return auth.ValidCSRF(info.sess, token)
}

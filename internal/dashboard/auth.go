package dashboard

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/happydev123/backendlab/internal/httpapi"
)

// handleLoginPage serves GET /login.
func (d *Dashboard) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := httpapi.SessionFrom(r.Context()); ok {
		http.Redirect(w, r, "/runs", http.StatusSeeOther)
		return
	}
	d.render(w, r, http.StatusOK, "login", "Sign in", "", loginView{})
}

// handleLogin serves POST /login. It needs no CSRF token: there is no
// session yet, and the password is the only credential.
func (d *Dashboard) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		status := http.StatusBadRequest
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			status = http.StatusRequestEntityTooLarge
		}
		d.render(w, r, status, "login", "Sign in", "", loginView{Error: "The form could not be read."})
		return
	}
	sess, e := d.api.Login(r, r.PostForm.Get("password"))
	if e != nil {
		if e.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(e.RetryAfter/time.Second)))
		}
		msg := e.Message
		if e.Status == http.StatusUnauthorized {
			msg = "The password is not correct."
		}
		d.render(w, r, e.Status, "login", "Sign in", "", loginView{Error: msg})
		return
	}
	d.api.SetSessionCookie(w, sess)
	http.Redirect(w, r, "/runs", http.StatusSeeOther)
}

// handleLogout serves POST /logout (CSRF-checked by requireCSRF).
func (d *Dashboard) handleLogout(w http.ResponseWriter, r *http.Request) {
	if e := d.api.Logout(w, r); e != nil {
		d.renderError(w, r, e)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

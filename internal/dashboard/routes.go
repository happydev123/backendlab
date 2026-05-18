package dashboard

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Register adds the dashboard pages to r. The caller installs the shared
// middleware (httpapi.Stack) first and registers the API routes as well;
// unknown paths outside /api/v1 get the HTML 404 page.
//
//	GET  /static/*               embedded CSS and JavaScript
//	GET  /                       redirect to /runs
//	GET  /login, POST /login     sign in
//	POST /logout                 sign out
//	GET  /runs                   run list (?status=, ?page=)
//	GET  /runs/new, POST /runs   submission form
//	GET  /runs/{id}              run detail
//	POST /runs/{id}/cancel       cancel
//	POST /runs/{id}/resubmit     resubmit
//	GET  /plans, POST /plans     plan comparison upload
//
// Every POST except /login carries the session's CSRF token in the
// csrf_token form field.
func (d *Dashboard) Register(r chi.Router) {
	limits := d.api.Limits()
	form := d.limitBody(limits.MaxFormBytes)
	r.Handle("/static/*", d.static)
	r.Group(func(r chi.Router) {
		r.Use(d.api.LoadSession)
		r.NotFound(d.notFound)
		r.Get("/", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/runs", http.StatusSeeOther) })
		r.Get("/login", d.handleLoginPage)
		r.With(form).Post("/login", d.handleLogin)

		r.Group(func(r chi.Router) {
			r.Use(d.requireLogin)
			r.With(form, d.requireCSRF).Post("/logout", d.handleLogout)
			r.Get("/runs", d.handleRuns)
			r.Get("/runs/new", d.handleSubmitForm)
			r.With(form, d.requireCSRF).Post("/runs", d.handleSubmit)
			r.Get("/runs/{id}", d.handleRun)
			r.With(form, d.requireCSRF).Post("/runs/{id}/cancel", d.handleCancel)
			r.With(form, d.requireCSRF).Post("/runs/{id}/resubmit", d.handleResubmit)
			r.Get("/plans", d.handlePlansForm)
			r.With(d.limitBody(limits.MaxUploadBytes())).Post("/plans", d.handleComparePlans)
		})
	})
}

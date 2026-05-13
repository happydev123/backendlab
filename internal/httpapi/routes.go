package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Register adds the liveness and readiness endpoints and the JSON API
// under /api/v1 to r. The caller installs the shared middleware (Stack)
// on r first.
//
//	GET    /healthz
//	GET    /readyz
//	POST   /api/v1/session                 login (no session required)
//	DELETE /api/v1/session                 logout
//	GET    /api/v1/suites
//	POST   /api/v1/runs
//	GET    /api/v1/runs?status=&limit=&offset=
//	GET    /api/v1/runs/{id}
//	POST   /api/v1/runs/{id}/cancel
//	POST   /api/v1/runs/{id}/resubmit
//	GET    /api/v1/runs/{id}/report?format=json|markdown|text
//	POST   /api/v1/plans/compare           multipart: before, after, fail_on
//
// Every /api/v1 route except login requires a session; cookie-
// authenticated requests that change state also need X-CSRF-Token.
func (a *API) Register(r chi.Router) {
	r.Get("/healthz", a.handleHealthz)
	r.Get("/readyz", a.handleReadyz)
	r.Route("/api/v1", func(r chi.Router) {
		r.Use(a.LoadSession)
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			WriteError(w, Errorf(http.StatusNotFound, CodeNotFound, "no such endpoint"))
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
			WriteError(w, Errorf(http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed"))
		})
		r.With(LimitBody(a.limits.MaxJSONBytes)).Post("/session", a.handleLogin)

		r.Group(func(r chi.Router) {
			r.Use(a.RequireSession, a.RequireCSRF)
			r.With(LimitBody(smallBodyBytes)).Delete("/session", a.handleLogout)
			r.Get("/suites", a.handleSuites)
			r.Get("/runs", a.handleListRuns)
			r.With(LimitBody(a.limits.MaxJSONBytes)).Post("/runs", a.handleSubmit)
			r.Get("/runs/{id}", a.handleGetRun)
			r.With(LimitBody(smallBodyBytes)).Post("/runs/{id}/cancel", a.handleCancel)
			r.With(LimitBody(smallBodyBytes)).Post("/runs/{id}/resubmit", a.handleResubmit)
			r.Get("/runs/{id}/report", a.handleReport)
			r.With(LimitBody(a.limits.MaxUploadBytes())).Post("/plans/compare", a.handleComparePlans)
		})
	})
}

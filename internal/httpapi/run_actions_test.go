package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/happydev123/backendlab/internal/catalog"
	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/store"
)

func actionRoutes(e *env) http.Handler {
	return e.router(func(r chi.Router) {
		r.Route("/api/v1", func(r chi.Router) {
			r.Use(e.api.LoadSession, e.api.RequireSession, e.api.RequireCSRF)
			r.With(LimitBody(e.api.Limits().MaxJSONBytes)).Post("/runs", e.api.handleSubmit)
			r.With(LimitBody(smallBodyBytes)).Post("/runs/{id}/cancel", e.api.handleCancel)
			r.With(LimitBody(smallBodyBytes)).Post("/runs/{id}/resubmit", e.api.handleResubmit)
			r.Get("/runs/{id}/report", e.api.handleReport)
		})
	})
}

func TestCancelRun(t *testing.T) {
	e := newEnv(t)
	h := actionRoutes(e)
	sess := e.auth.NewSession()
	queued := submit(t, h, sess, `{"suite":"health.yaml","targets":["staging"]}`)
	running := submit(t, h, sess, `{"suite":"health.yaml","targets":["staging"]}`)
	e.store.Update(running.ID, func(r *store.Run) { r.Status = store.StatusRunning })

	// Cookie without CSRF token is refused and changes nothing.
	expectError(t, serve(h, http.MethodPost, "/api/v1/runs/"+queued.ID+"/cancel", "", withCookie(sess)), http.StatusForbidden, CodeCSRF)
	if r, _ := e.store.GetRun(t.Context(), queued.ID); r.Status != store.StatusQueued {
		t.Fatalf("status after refused cancel: %s", r.Status)
	}

	rec := serve(h, http.MethodPost, "/api/v1/runs/"+queued.ID+"/cancel", "", withCookie(sess), withCSRF(sess.CSRFToken))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("cancel queued: %d %s", rec.Code, rec.Body.String())
	}
	var got runResp
	decodeBody(t, rec.Body.Bytes(), &got)
	if got.Status != "cancelled" || !got.CancelRequested {
		t.Errorf("cancelled queued run %+v", got)
	}

	rec = serve(h, http.MethodPost, "/api/v1/runs/"+running.ID+"/cancel", "", withBearer(sess.Token))
	decodeBody(t, rec.Body.Bytes(), &got)
	if rec.Code != http.StatusAccepted || got.Status != "running" || !got.CancelRequested {
		t.Errorf("cancel running: %d %+v", rec.Code, got)
	}

	msg := expectError(t, serve(h, http.MethodPost, "/api/v1/runs/"+queued.ID+"/cancel", "", withBearer(sess.Token)), http.StatusConflict, CodeInvalidState)
	if msg == "" {
		t.Error("empty conflict message")
	}
	expectError(t, serve(h, http.MethodPost, "/api/v1/runs/run_zzzzzzzzzzzzzzzz/cancel", "", withBearer(sess.Token)), http.StatusNotFound, CodeNotFound)
	expectError(t, serve(h, http.MethodPost, "/api/v1/runs/"+running.ID+"/cancel", ""), http.StatusUnauthorized, CodeUnauthenticated)
}

func TestResubmitRun(t *testing.T) {
	e := newEnv(t)
	h := actionRoutes(e)
	sess := e.auth.NewSession()
	ids := map[store.Status]string{}
	for _, s := range []store.Status{store.StatusQueued, store.StatusRunning, store.StatusSucceeded, store.StatusFailed, store.StatusCancelled, store.StatusInterrupted} {
		r := submit(t, h, sess, `{"suite":"health.yaml","targets":["staging"]}`)
		e.store.Update(r.ID, func(run *store.Run) { run.Status = s })
		ids[s] = r.ID
	}
	for _, s := range []store.Status{store.StatusQueued, store.StatusRunning, store.StatusSucceeded} {
		expectError(t, serve(h, http.MethodPost, "/api/v1/runs/"+ids[s]+"/resubmit", "", withBearer(sess.Token)), http.StatusConflict, CodeInvalidState)
	}
	for _, s := range []store.Status{store.StatusFailed, store.StatusCancelled, store.StatusInterrupted} {
		rec := serve(h, http.MethodPost, "/api/v1/runs/"+ids[s]+"/resubmit", "", withBearer(sess.Token))
		if rec.Code != http.StatusCreated {
			t.Fatalf("resubmit %s: %d %s", s, rec.Code, rec.Body.String())
		}
		var got runResp
		decodeBody(t, rec.Body.Bytes(), &got)
		if got.Status != "queued" || got.ResubmittedFrom != ids[s] || got.ID == ids[s] {
			t.Errorf("resubmitted %s run %+v", s, got)
		}
	}
	expectError(t, serve(h, http.MethodPost, "/api/v1/runs/"+ids[store.StatusFailed]+"/resubmit", "", withCookie(sess)), http.StatusForbidden, CodeCSRF)
}

func TestResubmitAppliesCurrentPolicy(t *testing.T) {
	// Submitted while writes were allowed.
	e := newEnv(t, func(c *config.Server) { c.AllowWrites = true })
	sess := e.auth.NewSession()
	r := submit(t, actionRoutes(e), sess, `{"suite":"writes.yaml","targets":["staging"],"allow_writes":true}`)
	e.store.Update(r.ID, func(run *store.Run) { run.Status = store.StatusInterrupted })

	// The operator turns writes off: resubmission is refused like a new
	// submission would be.
	e.cfg.AllowWrites = false
	expectError(t, serve(actionRoutes(e), http.MethodPost, "/api/v1/runs/"+r.ID+"/resubmit", "", withBearer(sess.Token)), http.StatusForbidden, CodeWritesNotPermitted)

	// Writes allowed again, but the suite file was edited: refused, the
	// stored snapshot no longer matches the approved file.
	e.cfg.AllowWrites = true
	if err := os.WriteFile(filepath.Join(e.dir, "writes.yaml"), []byte(writesSuite+"  - id: extra\n    method: GET\n    path: /x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Load(e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.api.catalog = cat
	expectError(t, serve(actionRoutes(e), http.MethodPost, "/api/v1/runs/"+r.ID+"/resubmit", "", withBearer(sess.Token)), http.StatusConflict, CodeSuiteChanged)
}

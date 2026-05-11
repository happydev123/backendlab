package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/happydev123/backendlab/internal/catalog"
	"github.com/happydev123/backendlab/internal/jobs"
	"github.com/happydev123/backendlab/internal/store"
)

// resubmittable lists the statuses an operator may resubmit. Succeeded
// runs are excluded: there is nothing to recover, and running the suite
// again is a new submission.
var resubmittable = map[store.Status]bool{
	store.StatusInterrupted: true,
	store.StatusFailed:      true,
	store.StatusCancelled:   true,
}

// CancelRun requests cancellation of the run with public ID id and
// returns the run as stored afterwards. A queued run is cancelled at
// once; a running run keeps running until its worker observes the request
// on its next heartbeat. Cancellation does not undo upstream side effects
// of requests already sent.
func (a *API) CancelRun(ctx context.Context, id string) (*store.Run, *Error) {
	run, e := a.LookupRun(ctx, id)
	if e != nil {
		return nil, e
	}
	status, err := a.queue.RequestCancel(ctx, run.ID)
	switch {
	case errors.Is(err, jobs.ErrNotCancellable):
		return nil, Errorf(http.StatusConflict, CodeInvalidState, "run %s is already %s and cannot be cancelled", run.PublicID, status)
	case errors.Is(err, store.ErrNotFound):
		return nil, Errorf(http.StatusNotFound, CodeNotFound, "run not found")
	case err != nil:
		return nil, a.fail(ctx, "cancel run", err)
	}
	a.logFor(ctx).Info("run cancellation requested", slog.String("run_id", run.PublicID), slog.String("status", string(status)))
	updated, err := a.store.GetRun(ctx, run.PublicID)
	if err != nil {
		return nil, a.fail(ctx, "reload run", err)
	}
	return updated, nil
}

// ResubmitRun creates a new run from the stored snapshot of an
// interrupted, failed or cancelled run, after applying the current server
// policy with catalog.CheckResubmission. The original run is unchanged.
func (a *API) ResubmitRun(ctx context.Context, id string) (*store.Run, *Error) {
	run, e := a.LookupRun(ctx, id)
	if e != nil {
		return nil, e
	}
	if !resubmittable[run.Status] {
		return nil, Errorf(http.StatusConflict, CodeInvalidState,
			"only interrupted, failed or cancelled runs can be resubmitted; run %s is %s", run.PublicID, run.Status)
	}
	if err := catalog.CheckResubmission(a.cfg, a.catalog, run); err != nil {
		return nil, a.policyError(ctx, "resubmit", err)
	}
	created, err := a.store.Resubmit(ctx, run.ID)
	switch {
	case errors.Is(err, store.ErrNotTerminal):
		return nil, Errorf(http.StatusConflict, CodeInvalidState, "run %s is not in a terminal state", run.PublicID)
	case err != nil:
		return nil, a.fail(ctx, "resubmit run", err)
	}
	a.logFor(ctx).Info("run resubmitted", slog.String("run_id", created.PublicID), slog.String("from", run.PublicID))
	return created, nil
}

// handleCancel serves POST /api/v1/runs/{id}/cancel.
func (a *API) handleCancel(w http.ResponseWriter, r *http.Request) {
	run, e := a.CancelRun(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		WriteError(w, e)
		return
	}
	writeJSON(w, http.StatusAccepted, runView(run, ""))
}

// handleResubmit serves POST /api/v1/runs/{id}/resubmit.
func (a *API) handleResubmit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	run, e := a.ResubmitRun(r.Context(), id)
	if e != nil {
		WriteError(w, e)
		return
	}
	w.Header().Set("Location", linksFor(run.PublicID).Self)
	writeJSON(w, http.StatusCreated, runView(run, id))
}

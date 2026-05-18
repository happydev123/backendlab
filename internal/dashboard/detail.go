package dashboard

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// handleRun serves GET /runs/{id}: status, case results, findings grouped
// by module, and report downloads. Pages of queued and running runs ask
// app.js to refresh them.
func (d *Dashboard) handleRun(w http.ResponseWriter, r *http.Request) {
	detail, e := d.api.RunDetail(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		d.renderError(w, r, e)
		return
	}
	v := newRunDetailView(detail)
	d.renderPage(w, r, http.StatusOK, "run", page{
		Title:   "Run " + detail.Run.PublicID,
		Active:  "runs",
		Refresh: v.Active,
		Content: v,
	})
}

// handleCancel serves POST /runs/{id}/cancel (CSRF-checked).
func (d *Dashboard) handleCancel(w http.ResponseWriter, r *http.Request) {
	run, e := d.api.CancelRun(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		d.renderError(w, r, e)
		return
	}
	http.Redirect(w, r, "/runs/"+run.PublicID, http.StatusSeeOther)
}

// handleResubmit serves POST /runs/{id}/resubmit (CSRF-checked) and
// redirects to the new run.
func (d *Dashboard) handleResubmit(w http.ResponseWriter, r *http.Request) {
	run, e := d.api.ResubmitRun(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		d.renderError(w, r, e)
		return
	}
	http.Redirect(w, r, "/runs/"+run.PublicID, http.StatusSeeOther)
}

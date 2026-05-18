package dashboard

import (
	"net/http"

	"github.com/happydev123/backendlab/internal/httpapi"
)

// handlePlansForm serves GET /plans.
func (d *Dashboard) handlePlansForm(w http.ResponseWriter, r *http.Request) {
	d.render(w, r, http.StatusOK, "plans", "Compare plans", "plans", plansView{MaxBytes: d.api.Limits().MaxPlanBytes})
}

// handleComparePlans serves POST /plans. The upload is multipart, so the
// CSRF token is read from the first form part rather than by
// requireCSRF. The result is rendered and discarded.
func (d *Dashboard) handleComparePlans(w http.ResponseWriter, r *http.Request) {
	check := func(token string) bool { return httpapi.CSRFValid(r, token) }
	cmp, e := d.api.ComparePlans(r, check)
	v := plansView{MaxBytes: d.api.Limits().MaxPlanBytes}
	if e != nil {
		v.Error = e.Message
		d.render(w, r, e.Status, "plans", "Compare plans", "plans", v)
		return
	}
	v.Result = newPlanResultView(cmp)
	d.render(w, r, http.StatusOK, "plans", "Compare plans", "plans", v)
}

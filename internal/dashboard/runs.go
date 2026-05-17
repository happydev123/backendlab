package dashboard

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/happydev123/backendlab/internal/httpapi"
	"github.com/happydev123/backendlab/internal/store"
)

// pageSize is the number of runs per dashboard page.
const pageSize = 25

// handleRuns serves GET /runs?status=&page=.
func (d *Dashboard) handleRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pageNum := 1
	if v := q.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || (n-1)*pageSize > httpapi.MaxListOffset {
			d.renderError(w, r, httpapi.Errorf(http.StatusBadRequest, httpapi.CodeInvalidRequest,
				"page must be a number between 1 and %d", httpapi.MaxListOffset/pageSize+1))
			return
		}
		pageNum = n
	}
	params := store.ListRunsParams{Limit: pageSize, Offset: (pageNum - 1) * pageSize}
	filter := q.Get("status")
	if filter != "" {
		s, err := store.ParseStatus(filter)
		if err != nil {
			d.renderError(w, r, httpapi.Errorf(http.StatusBadRequest, httpapi.CodeInvalidRequest, "unknown status filter"))
			return
		}
		params.Statuses = []store.Status{s}
	}
	runs, total, e := d.api.ListRuns(r.Context(), params)
	if e != nil {
		d.renderError(w, r, e)
		return
	}

	v := runListView{Runs: runs, Total: total, Page: pageNum, Pages: int((total + pageSize - 1) / pageSize)}
	if v.Pages < 1 {
		v.Pages = 1
	}
	for _, s := range store.Statuses {
		v.Statuses = append(v.Statuses, statusOption{Value: string(s), Label: statusLabel(s), Selected: string(s) == filter})
	}
	if filter != "" {
		v.FilterLabel = statusLabel(store.Status(filter))
		v.EmptyMessage = "No runs with status " + v.FilterLabel + "."
	} else {
		v.EmptyMessage = "No runs yet."
	}
	if pageNum > 1 {
		v.PrevURL = pageURL(filter, pageNum-1)
	}
	if int64(pageNum*pageSize) < total {
		v.NextURL = pageURL(filter, pageNum+1)
	}
	if len(runs) == 0 && pageNum > 1 {
		v.EmptyMessage = "This page is past the end of the list."
	}
	d.render(w, r, http.StatusOK, "runs", "Runs", "runs", v)
}

func pageURL(status string, page int) string {
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	q.Set("page", strconv.Itoa(page))
	return "/runs?" + q.Encode()
}

// newSubmitView returns the submission form with the approved suites,
// configured targets and the given form values.
func (d *Dashboard) newSubmitView(form submitForm, errMsg string) submitView {
	cat := d.api.Catalog()
	targets := d.api.TargetNames()
	if form.Target == "" && len(targets) > 0 {
		form.Target = targets[0]
	}
	if form.Baseline == "" && len(targets) > 0 {
		form.Baseline = targets[0]
	}
	if form.Candidate == "" && len(targets) > 1 {
		form.Candidate = targets[1]
	}
	return submitView{
		Suites:      cat.List(),
		Problems:    cat.Problems(),
		Targets:     targets,
		AllowWrites: d.api.Config().AllowWrites,
		Form:        form,
		Error:       errMsg,
	}
}

// handleSubmitForm serves GET /runs/new.
func (d *Dashboard) handleSubmitForm(w http.ResponseWriter, r *http.Request) {
	d.render(w, r, http.StatusOK, "submit", "Submit a run", "submit", d.newSubmitView(submitForm{Mode: "run"}, ""))
}

// handleSubmit serves POST /runs. The form's target fields are mapped to
// the API submission: one target in run mode, baseline then candidate in
// compare mode. A refused submission shows the form again with the reason
// and the status the API would return.
func (d *Dashboard) handleSubmit(w http.ResponseWriter, r *http.Request) {
	f := r.PostForm
	form := submitForm{
		Suite:       f.Get("suite"),
		Mode:        f.Get("mode"),
		Target:      f.Get("target"),
		Baseline:    f.Get("baseline"),
		Candidate:   f.Get("candidate"),
		AllowWrites: f.Get("allow_writes") != "",
	}
	req := httpapi.SubmitRequest{Suite: form.Suite, Mode: form.Mode, AllowWrites: form.AllowWrites}
	if form.Mode == string(store.ModeCompare) {
		req.Targets = []string{form.Baseline, form.Candidate}
	} else {
		req.Targets = []string{form.Target}
	}
	run, e := d.api.SubmitRun(r.Context(), req)
	if e != nil {
		d.render(w, r, e.Status, "submit", "Submit a run", "submit", d.newSubmitView(form, e.Message))
		return
	}
	http.Redirect(w, r, "/runs/"+run.PublicID, http.StatusSeeOther)
}

package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/happydev123/backendlab/internal/catalog"
	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/store"
)

// MaxListOffset is the largest offset accepted by the run list; it
// matches the bound enforced by the store.
const MaxListOffset = 100000

// runIDPattern matches the public run IDs issued by the store. Other
// values are answered with 404 without a database query.
var runIDPattern = regexp.MustCompile(`^run_[a-z2-7]{16}$`)

// SubmitRequest is the body of POST /api/v1/runs. It has no field for a
// URL, an environment variable or suite content; unknown fields are
// rejected.
type SubmitRequest struct {
	// Suite is the file name of an approved suite.
	Suite string `json:"suite"`
	// Mode is "run" (default) or "compare".
	Mode string `json:"mode"`
	// Targets names server-configured targets: one for run, baseline then
	// candidate for compare.
	Targets     []string `json:"targets"`
	AllowWrites bool     `json:"allow_writes"`
}

// SubmitRun validates req against the catalog and server policy and
// enqueues the run.
func (a *API) SubmitRun(ctx context.Context, req SubmitRequest) (*store.Run, *Error) {
	if strings.TrimSpace(req.Suite) == "" {
		return nil, Errorf(http.StatusBadRequest, CodeInvalidRequest, "suite is required")
	}
	entry, ok := a.catalog.Get(req.Suite)
	if !ok {
		return nil, Errorf(http.StatusBadRequest, CodeUnknownSuite, "suite %q is not an approved suite", jsonutil.Truncate(req.Suite, 64))
	}
	mode := store.Mode(req.Mode)
	if mode == "" {
		mode = store.ModeRun
	}
	n, err := catalog.ValidateSubmission(a.cfg, entry, catalog.Submission{
		SuiteFile: req.Suite, Mode: mode, Targets: req.Targets, AllowWrites: req.AllowWrites,
	})
	if err != nil {
		return nil, a.policyError(ctx, "submit", err)
	}
	run, err := a.store.EnqueueRun(ctx, n)
	if errors.Is(err, store.ErrInvalidRun) {
		return nil, Errorf(http.StatusBadRequest, CodeInvalidSubmission, "%s", strings.TrimPrefix(err.Error(), "store: "))
	}
	if err != nil {
		return nil, a.fail(ctx, "enqueue run", err)
	}
	a.logFor(ctx).Info("run submitted", slog.String("run_id", run.PublicID), slog.String("suite", run.SuiteFile),
		slog.String("mode", string(run.Mode)), slog.Bool("mutating", run.Mutating))
	return run, nil
}

// policyError maps catalog policy errors to API errors.
func (a *API) policyError(ctx context.Context, op string, err error) *Error {
	switch {
	case errors.Is(err, catalog.ErrWritesNotPermitted):
		return Errorf(http.StatusForbidden, CodeWritesNotPermitted, "%s", err)
	case errors.Is(err, catalog.ErrSuiteChanged):
		return Errorf(http.StatusConflict, CodeSuiteChanged, "%s", err)
	case errors.Is(err, catalog.ErrInvalidSubmission):
		return Errorf(http.StatusBadRequest, CodeInvalidSubmission, "%s", err)
	}
	return a.fail(ctx, op, err)
}

// ParseListQuery reads status, limit and offset from q. status may be
// repeated or comma-separated. limit defaults to store.DefaultListLimit
// and must be 1 to store.MaxListLimit; offset must be 0 to MaxListOffset.
func ParseListQuery(q url.Values) (store.ListRunsParams, *Error) {
	p := store.ListRunsParams{Limit: store.DefaultListLimit}
	bad := func(format string, args ...any) (store.ListRunsParams, *Error) {
		return store.ListRunsParams{}, Errorf(http.StatusBadRequest, CodeInvalidRequest, format, args...)
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > store.MaxListLimit {
			return bad("limit must be an integer between 1 and %d", store.MaxListLimit)
		}
		p.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > MaxListOffset {
			return bad("offset must be an integer between 0 and %d", MaxListOffset)
		}
		p.Offset = n
	}
	seen := map[store.Status]bool{}
	for _, v := range q["status"] {
		for _, name := range strings.Split(v, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			s, err := store.ParseStatus(name)
			if err != nil {
				return bad("unknown status %q (want one of %s)", jsonutil.Truncate(name, 32), statusNames())
			}
			if !seen[s] {
				seen[s] = true
				p.Statuses = append(p.Statuses, s)
			}
		}
	}
	return p, nil
}

func statusNames() string {
	names := make([]string, len(store.Statuses))
	for i, s := range store.Statuses {
		names[i] = string(s)
	}
	return strings.Join(names, ", ")
}

// ListRuns returns one page of runs and the total matching the filter.
func (a *API) ListRuns(ctx context.Context, p store.ListRunsParams) ([]store.RunSummary, int64, *Error) {
	runs, total, err := a.store.ListRuns(ctx, p)
	if err != nil {
		return nil, 0, a.fail(ctx, "list runs", err)
	}
	return runs, total, nil
}

// LookupRun returns the run with public ID id, or a 404 error.
func (a *API) LookupRun(ctx context.Context, id string) (*store.Run, *Error) {
	if !runIDPattern.MatchString(id) {
		return nil, Errorf(http.StatusNotFound, CodeNotFound, "run not found")
	}
	run, err := a.store.GetRun(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, Errorf(http.StatusNotFound, CodeNotFound, "run not found")
	}
	if err != nil {
		return nil, a.fail(ctx, "get run", err)
	}
	return run, nil
}

// RunDetail is a run with its case results.
type RunDetail struct {
	Run   *store.Run
	Cases []store.CaseResult
	// ResubmittedFrom is the public ID of the original run, or "" when
	// there is none or it was deleted by retention cleanup.
	ResubmittedFrom string
}

// RunDetail loads the run with public ID id and its case results.
func (a *API) RunDetail(ctx context.Context, id string) (*RunDetail, *Error) {
	run, e := a.LookupRun(ctx, id)
	if e != nil {
		return nil, e
	}
	cases, err := a.store.ListCaseResults(ctx, run.ID)
	if err != nil {
		return nil, a.fail(ctx, "list case results", err)
	}
	d := &RunDetail{Run: run, Cases: cases}
	if run.ResubmittedFrom != nil {
		orig, err := a.store.GetRunByID(ctx, *run.ResubmittedFrom)
		switch {
		case err == nil:
			d.ResubmittedFrom = orig.PublicID
		case !errors.Is(err, store.ErrNotFound):
			return nil, a.fail(ctx, "get original run", err)
		}
	}
	return d, nil
}

// TargetNames returns the configured target names, sorted.
func (a *API) TargetNames() []string {
	names := make([]string, 0, len(a.cfg.Targets))
	for n := range a.cfg.Targets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// handleSuites serves GET /api/v1/suites.
func (a *API) handleSuites(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, catalogView(a.catalog, a.TargetNames(), a.cfg.AllowWrites))
}

// handleSubmit serves POST /api/v1/runs.
func (a *API) handleSubmit(w http.ResponseWriter, r *http.Request) {
	var req SubmitRequest
	if e := decodeJSON(r, &req); e != nil {
		WriteError(w, e)
		return
	}
	run, e := a.SubmitRun(r.Context(), req)
	if e != nil {
		WriteError(w, e)
		return
	}
	w.Header().Set("Location", linksFor(run.PublicID).Self)
	writeJSON(w, http.StatusCreated, runView(run, ""))
}

// handleListRuns serves GET /api/v1/runs.
func (a *API) handleListRuns(w http.ResponseWriter, r *http.Request) {
	p, e := ParseListQuery(r.URL.Query())
	if e != nil {
		WriteError(w, e)
		return
	}
	runs, total, e := a.ListRuns(r.Context(), p)
	if e != nil {
		WriteError(w, e)
		return
	}
	out := runListJSON{Runs: make([]runListItemJSON, len(runs)), Total: total, Limit: p.Limit, Offset: p.Offset}
	for i, s := range runs {
		out.Runs[i] = listItemView(s)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetRun serves GET /api/v1/runs/{id}.
func (a *API) handleGetRun(w http.ResponseWriter, r *http.Request) {
	d, e := a.RunDetail(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		WriteError(w, e)
		return
	}
	writeJSON(w, http.StatusOK, detailView(d))
}

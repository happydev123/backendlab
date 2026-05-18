package dashboard

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/store"
)

// fullRoutes registers the API and the dashboard as the server does.
func fullRoutes(e *denv) http.Handler {
	return e.mux(func(r chi.Router) {
		e.api.Register(r)
		e.dash.Register(r)
	})
}

func enqueue(t *testing.T, e *denv, file string, mutating bool) *store.Run {
	t.Helper()
	entry, ok := e.api.Catalog().Get(file)
	if !ok {
		t.Fatalf("%s not in catalog", file)
	}
	run, err := e.store.EnqueueRun(t.Context(), store.NewRun{SuiteName: entry.Name, SuiteFile: file,
		SuiteSnapshot: entry.Snapshot(), Mode: store.ModeRun, Targets: []string{"staging"}, Mutating: mutating, WritesRequested: mutating})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestRunDetailPage(t *testing.T) {
	e := newDenv(t)
	h := fullRoutes(e)
	run := enqueue(t, e, "health.yaml", false)

	body := expectPage(t, get(h, "/runs/"+run.PublicID, asOperator(e.sess)), http.StatusOK,
		"Queued", `data-refresh="5"`, "Cancel run", "No case results have been recorded yet.")
	if strings.Contains(body, "Download JSON report") {
		t.Error("queued run offers a report")
	}

	rep := report.New("run")
	rep.Finalize()
	data, _ := json.Marshal(rep)
	e.store.Finish(run.PublicID, store.StatusFailed, data, []store.CaseResult{{
		Index: 0, CaseID: "health", Status: report.CaseFailed,
		Findings: []finding.Finding{{Module: finding.ModuleCompare, Code: "compare.value_changed", Kind: finding.KindDifference,
			Severity: finding.SeverityError, CaseID: "health", Location: "/total", Message: "<script>steal()</script>"}},
	}})
	e.store.Update(run.PublicID, func(r *store.Run) { r.Summary = &rep.Summary })
	body = expectPage(t, get(h, "/runs/"+run.PublicID, asOperator(e.sess)), http.StatusOK,
		"HTTP differences", "Failure", "&lt;script&gt;steal()&lt;/script&gt;", "Download JSON report", "Resubmit as a new run")
	if strings.Contains(body, "<script>steal") || strings.Contains(body, "data-refresh") {
		t.Error("finished run page: unescaped text or refresh")
	}

	expectPage(t, get(h, "/runs/run_zzzzzzzzzzzzzzzz", asOperator(e.sess)), http.StatusNotFound, "Not found")
	expectPage(t, get(h, "/runs/not-a-run", asOperator(e.sess)), http.StatusNotFound)
	expectRedirect(t, get(h, "/runs/"+run.PublicID), "/login")
}

func TestCancelAndResubmitForms(t *testing.T) {
	e := newDenv(t)
	h := fullRoutes(e)
	run := enqueue(t, e, "health.yaml", false)
	token := url.Values{"csrf_token": {e.sess.CSRFToken}}

	expectPage(t, postForm(h, "/runs/"+run.PublicID+"/cancel", url.Values{}, asOperator(e.sess)), http.StatusForbidden, "CSRF")
	if r, _ := e.store.GetRun(t.Context(), run.PublicID); r.Status != store.StatusQueued {
		t.Fatal("cancel without token changed the run")
	}
	expectRedirect(t, postForm(h, "/runs/"+run.PublicID+"/cancel", token, asOperator(e.sess)), "/runs/"+run.PublicID)
	if r, _ := e.store.GetRun(t.Context(), run.PublicID); r.Status != store.StatusCancelled {
		t.Fatalf("status after cancel: %s", r.Status)
	}
	expectPage(t, postForm(h, "/runs/"+run.PublicID+"/cancel", token, asOperator(e.sess)), http.StatusConflict, "already cancelled")

	rec := postForm(h, "/runs/"+run.PublicID+"/resubmit", token, asOperator(e.sess))
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/runs/run_") ||
		rec.Header().Get("Location") == "/runs/"+run.PublicID {
		t.Fatalf("resubmit: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// A mutating run cannot be resubmitted while the server disallows
	// writes, and a run whose suite file changed is refused.
	m := enqueue(t, e, "writes.yaml", true)
	e.store.Finish(m.PublicID, store.StatusInterrupted, nil, nil)
	expectPage(t, postForm(h, "/runs/"+m.PublicID+"/resubmit", token, asOperator(e.sess)), http.StatusForbidden, "does not allow writes")
	e.store.Update(run.PublicID, func(r *store.Run) { r.SuiteSHA256 = strings.Repeat("0", 64) })
	expectPage(t, postForm(h, "/runs/"+run.PublicID+"/resubmit", token, asOperator(e.sess)), http.StatusConflict, "modified after run")
}

func TestPlansPage(t *testing.T) {
	e := newDenv(t)
	h := fullRoutes(e)
	expectPage(t, get(h, "/plans", asOperator(e.sess)), http.StatusOK, `enctype="multipart/form-data"`, `<label for="before">`, "no SQL is executed")

	before := `[{"Plan": {"Node Type": "Index Scan", "Relation Name": "invoices", "Index Name": "invoices_pkey", "Total Cost": 8.31, "Plan Rows": 1}}]`
	after := `[{"Plan": {"Node Type": "Seq Scan", "Relation Name": "<b>invoices</b>", "Total Cost": 1834.0, "Plan Rows": 1}}]`
	upload := func(token string, parts ...[3]string) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		if token != "" {
			mw.WriteField("csrf_token", token)
		}
		for _, p := range parts {
			w, _ := mw.CreateFormFile(p[0], p[1])
			w.Write([]byte(p[2]))
		}
		mw.Close()
		return do(h, http.MethodPost, "/plans", buf.String(), mw.FormDataContentType(), asOperator(e.sess))
	}
	body := expectPage(t, upload(e.sess.CSRFToken, [3]string{"before", "before.json", before}, [3]string{"after", "after.json", after}),
		http.StatusOK, "Comparison of before.json and after.json", "Seq Scan: 1", "Index Scan: 1", "Limitations", "unknown")
	if strings.Contains(body, "<b>invoices</b>") {
		t.Error("relation name not escaped")
	}
	expectPage(t, upload("", [3]string{"before", "b.json", before}, [3]string{"after", "a.json", after}), http.StatusForbidden, "CSRF")
	expectPage(t, upload("forged", [3]string{"before", "b.json", before}), http.StatusForbidden)
	expectPage(t, upload(e.sess.CSRFToken, [3]string{"before", "b.json", "not json"}, [3]string{"after", "a.json", after}),
		http.StatusBadRequest, "before plan")
	if runs, _, _ := e.store.ListRuns(t.Context(), store.ListRunsParams{}); len(runs) != 0 {
		t.Error("plan comparison stored something")
	}
}

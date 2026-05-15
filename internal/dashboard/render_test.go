package dashboard

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/happydev123/backendlab/internal/catalog"
	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/httpapi"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/web"
)

const hostile = `<script>alert("x")</script>`

func mustRenderer(t *testing.T) *renderer {
	t.Helper()
	r, err := parseTemplates(web.Templates)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func renderPage(t *testing.T, name string, p page) string {
	t.Helper()
	var buf bytes.Buffer
	if err := mustRenderer(t).render(&buf, name, p); err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	return buf.String()
}

func loggedIn(title, active string, content any) page {
	return page{Title: title, Active: active, LoggedIn: true, CSRFToken: "csrf-token-value", RequestID: "req_test", Content: content}
}

func hostileDetail() *httpapi.RunDetail {
	now := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	ms := 41.25
	run := &store.Run{
		PublicID: "run_abcdefghijklmnop", SuiteName: "suite " + hostile, SuiteFile: "health.yaml",
		SuiteSHA256: strings.Repeat("a", 64), Mode: store.ModeCompare, Targets: []string{"staging", "candidate"},
		Status: store.StatusFailed, Attempt: 1, MaxAttempts: 2, CreatedAt: now, FinishedAt: &now,
		Error: "upstream said " + hostile, Summary: &report.Summary{Cases: 2},
	}
	f := func(m finding.Module, code string, k finding.Kind, s finding.Severity, msg string) finding.Finding {
		return finding.Finding{Module: m, Code: code, Kind: k, Severity: s, CaseID: "case-1", Message: msg}
	}
	return &httpapi.RunDetail{Run: run, Cases: []store.CaseResult{
		{
			Index: 0, CaseID: "case-1", Status: report.CaseFailed,
			Findings: []finding.Finding{
				f(finding.ModuleCompare, "compare.value_changed", finding.KindDifference, finding.SeverityError, "value changed: "+hostile).
					WithDetail("<b>key</b>", `"><img src=x onerror=alert(1)>`),
				f(finding.ModuleStream, "stream.missing_terminal", finding.KindInconclusive, finding.SeverityWarning, "stream ended before the terminal event"),
				f(finding.ModuleValidate, "validate.schema", finding.KindUnsupported, finding.SeverityInfo, "schema keyword not supported"),
				f(finding.ModuleReplay, "replay.timing", finding.KindObservation, finding.SeverityInfo, "took 41 ms"),
			},
			Observations: []report.Observation{{Target: "staging", StatusCode: 200, DurationMS: &ms}, {Target: "candidate", Error: hostile}},
			Bodies:       map[string]string{"staging": `</pre><img src=x onerror=alert(1)>`},
		},
		{Index: 1, CaseID: "case-<i>2</i>", Status: report.CaseInconclusive},
	}}
}

func TestTemplatesParse(t *testing.T) {
	r := mustRenderer(t)
	for _, name := range pageNames {
		if r.pages[name] == nil {
			t.Errorf("page %s missing", name)
		}
	}
	if err := r.render(&bytes.Buffer{}, "nope", page{}); err == nil {
		t.Error("unknown page rendered")
	}
}

func TestRunPageEscapesHostileText(t *testing.T) {
	out := renderPage(t, "run", loggedIn("Run", "runs", newRunDetailView(hostileDetail())))
	for _, bad := range []string{hostile, "<img", "<b>key</b>", "<i>2</i>", `onerror=alert(1)>`} {
		if strings.Contains(out, bad) {
			t.Errorf("rendered page contains unescaped %q", bad)
		}
	}
	for _, want := range []string{"&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;", "&lt;img src=x onerror=alert(1)&gt;", "case-&lt;i&gt;2&lt;/i&gt;"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered page lacks escaped %q", want)
		}
	}
	checkNoInlineCode(t, out)
}

func TestRunPageOutcomeLabels(t *testing.T) {
	out := renderPage(t, "run", loggedIn("Run", "runs", newRunDetailView(hostileDetail())))
	for _, want := range []string{
		`<span class="label label-failure">Failure</span>`,
		`<span class="label label-inconclusive">Inconclusive</span>`,
		`<span class="label label-inconclusive">Unsupported</span>`,
		`<span class="label label-observation">Observation</span>`,
		"HTTP differences", "Stream findings", "Validation findings", "Requests and assertions",
		"Failure: 1, Inconclusive: 1, Unsupported: 1, Observation: 1",
		"Failed: 1, Inconclusive: 1",
		"duration unknown", "41.2 ms",
		"Download Markdown report", `/api/v1/runs/run_abcdefghijklmnop/report?format=text`,
		`action="/runs/run_abcdefghijklmnop/resubmit"`, `value="csrf-token-value"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("run page lacks %q", want)
		}
	}
	// Module groups appear in the fixed order.
	order := []string{"Requests and assertions", "HTTP differences", "Stream findings", "Validation findings"}
	last := -1
	for _, h := range order {
		i := strings.Index(out, h)
		if i < last {
			t.Errorf("group %q out of order", h)
		}
		last = i
	}
	if strings.Contains(out, "Cancel run") {
		t.Error("finished run offers cancellation")
	}
}

func TestRunPageActiveRun(t *testing.T) {
	d := hostileDetail()
	d.Run.Status, d.Run.FinishedAt, d.Run.Summary = store.StatusRunning, nil, nil
	d.Cases = nil
	p := loggedIn("Run", "runs", newRunDetailView(d))
	p.Refresh = true
	out := renderPage(t, "run", p)
	for _, want := range []string{`data-refresh="5"`, "Cancel run", "data-confirm=", "Refresh this page", "No case results have been recorded yet.", "No findings were recorded yet."} {
		if !strings.Contains(out, want) {
			t.Errorf("active run page lacks %q", want)
		}
	}
	if strings.Contains(out, "Download JSON report") || strings.Contains(out, "Resubmit") {
		t.Error("active run offers report or resubmission")
	}

	now := time.Now()
	d.Run.CancelRequestedAt = &now
	out = renderPage(t, "run", loggedIn("Run", "runs", newRunDetailView(d)))
	if strings.Contains(out, "Cancel run") || !strings.Contains(out, "cancellation requested") {
		t.Error("pending cancellation not shown")
	}
}

func TestRunsEmptyStates(t *testing.T) {
	out := renderPage(t, "runs", loggedIn("Runs", "runs", runListView{EmptyMessage: "No runs yet.", Page: 1, Pages: 1}))
	if !strings.Contains(out, "No runs yet.") || strings.Contains(out, "<table") {
		t.Errorf("empty run list:\n%s", out)
	}
	now := time.Now()
	out = renderPage(t, "runs", loggedIn("Runs", "runs", runListView{
		Runs:  []store.RunSummary{{PublicID: "run_aaaaaaaaaaaaaaab", SuiteName: hostile, Status: store.StatusInterrupted, CreatedAt: now}},
		Total: 1, Page: 1, Pages: 3, NextURL: "/runs?page=2&status=interrupted", FilterLabel: "Interrupted (needs review)",
	}))
	for _, want := range []string{"Interrupted (needs review)", `href="/runs?page=2&amp;status=interrupted"`, "Page 1 of 3", "1 run with status"} {
		if !strings.Contains(out, want) {
			t.Errorf("run list lacks %q", want)
		}
	}
	if strings.Contains(out, hostile) {
		t.Error("suite name not escaped")
	}

	out = renderPage(t, "submit", loggedIn("Submit", "submit", submitView{}))
	if !strings.Contains(out, "No approved suites are available.") {
		t.Error("empty catalog message missing")
	}
	out = renderPage(t, "plans", loggedIn("Plans", "plans", plansView{MaxBytes: 1024, Result: &planResultView{}}))
	if !strings.Contains(out, "no differences") {
		t.Error("empty plan comparison message missing")
	}
}

func TestSubmitWritesCheckboxOnlyWhenAllowed(t *testing.T) {
	v := submitView{
		Suites:  []catalog.Entry{{File: "writes.yaml", Name: "writes", Cases: 1, Mutating: true, MutatingCases: []string{"increment"}}},
		Targets: []string{"candidate", "staging"},
		Form:    submitForm{Mode: "run", Target: "staging"},
	}
	out := renderPage(t, "submit", loggedIn("Submit", "submit", v))
	if strings.Contains(out, `name="allow_writes"`) || !strings.Contains(out, "does not allow writes") {
		t.Error("writes checkbox shown although the server disallows writes")
	}
	v.AllowWrites = true
	out = renderPage(t, "submit", loggedIn("Submit", "submit", v))
	if !strings.Contains(out, `name="allow_writes"`) || !strings.Contains(out, "does not undo side effects") {
		t.Error("writes checkbox or warning missing")
	}
	if !strings.Contains(out, "mutating: increment") || !strings.Contains(out, `<option value="staging" selected>`) {
		t.Errorf("submit form state:\n%s", out)
	}
}

var (
	controlPattern = regexp.MustCompile(`<(input|select|textarea)\b[^>]*>`)
	idPattern      = regexp.MustCompile(`\bid="([^"]+)"`)
)

func TestFormControlsHaveLabelsAndLandmarks(t *testing.T) {
	pages := map[string]page{
		"login":  {Title: "Sign in", Content: loginView{Error: "bad"}},
		"runs":   loggedIn("Runs", "runs", runListView{EmptyMessage: "No runs yet.", Statuses: []statusOption{{Value: "queued", Label: "Queued"}}}),
		"submit": loggedIn("Submit", "submit", submitView{Suites: []catalog.Entry{{File: "a.yaml", Name: "a"}}, Targets: []string{"x"}, AllowWrites: true}),
		"run":    loggedIn("Run", "runs", newRunDetailView(hostileDetail())),
		"plans":  loggedIn("Plans", "plans", plansView{MaxBytes: 10}),
		"error":  loggedIn("Not found", "", errorView{Heading: "Not found", Message: hostile}),
	}
	for name, p := range pages {
		out := renderPage(t, name, p)
		if !strings.Contains(out, "<main") {
			t.Errorf("%s: no <main> landmark", name)
		}
		if p.LoggedIn && !strings.Contains(out, `<nav aria-label="Main">`) {
			t.Errorf("%s: no main <nav>", name)
		}
		for _, ctl := range controlPattern.FindAllString(out, -1) {
			if strings.Contains(ctl, `type="hidden"`) {
				continue
			}
			m := idPattern.FindStringSubmatch(ctl)
			if m == nil {
				t.Errorf("%s: control without id: %s", name, ctl)
				continue
			}
			if !strings.Contains(out, `for="`+m[1]+`"`) {
				t.Errorf("%s: control %q has no label", name, m[1])
			}
		}
		checkNoInlineCode(t, out)
	}
}

// checkNoInlineCode fails on inline scripts, inline styles and event
// handler attributes, which the Content-Security-Policy would block.
func checkNoInlineCode(t *testing.T, out string) {
	t.Helper()
	scripts := regexp.MustCompile(`<script\b[^>]*>`).FindAllString(out, -1)
	if len(scripts) != 1 || scripts[0] != `<script src="/static/app.js" defer>` {
		t.Errorf("unexpected scripts %v", scripts)
	}
	if regexp.MustCompile(`\sstyle=|<style|\son[a-z]+="`).MatchString(out) {
		t.Error("inline style or event handler in page")
	}
}

func TestFormatters(t *testing.T) {
	if formatWhen(nil) != "not yet" || formatWhen((*time.Time)(nil)) != "not yet" || formatWhen(time.Time{}) != "not yet" {
		t.Error("missing time not shown as not yet")
	}
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	if formatWhen(ts) != "2026-01-02 02:04:05 UTC" || formatWhen(&ts) != "2026-01-02 02:04:05 UTC" {
		t.Errorf("formatWhen = %q", formatWhen(ts))
	}
	if formatMS(nil) != "duration unknown" {
		t.Error("unknown duration shown as a number")
	}
	if formatNumber(nil) != "unknown" || formatCounts(nil) != "none" || formatCounts(map[string]int{"b": 1, "a": 2}) != "a: 2, b: 1" {
		t.Error("plan formatters")
	}
	if statusLabel("weird") != "weird" || caseLabel("odd") != "odd" {
		t.Error("unknown labels")
	}
}

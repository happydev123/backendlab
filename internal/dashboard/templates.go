// Package dashboard renders the operator dashboard of "backendlab serve":
// server-side html/template pages for signing in, listing and submitting
// runs, inspecting run results, and comparing query plans. Pages reuse the
// operations of package httpapi, so the dashboard and the JSON API apply
// the same policy and answer the same conditions with the same status.
//
// Templates rely on html/template's contextual escaping. Finding text,
// suite names, error messages and retained bodies are untrusted and are
// always passed as plain strings; template.HTML is never used. Pages carry
// no inline scripts or styles (the Content-Security-Policy forbids them)
// and work without JavaScript.
package dashboard

import (
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/happydev123/backendlab/internal/plans"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/store"
)

// pageNames lists the page templates; each is parsed together with
// layout.html and partials.html.
var pageNames = []string{"login", "runs", "submit", "run", "plans", "error"}

// page is the data every template receives.
type page struct {
	Title     string
	Active    string // navigation entry: runs, submit or plans
	LoggedIn  bool
	CSRFToken string
	RequestID string
	// Refresh asks app.js to reload the page every few seconds.
	Refresh bool
	Content any
}

// renderer holds one parsed template set per page.
type renderer struct {
	pages map[string]*template.Template
}

// parseTemplates parses the page templates from fsys (web.Templates).
func parseTemplates(fsys fs.FS) (*renderer, error) {
	r := &renderer{pages: make(map[string]*template.Template, len(pageNames))}
	for _, name := range pageNames {
		t, err := template.New(name).Funcs(funcs).ParseFS(fsys,
			"templates/layout.html", "templates/partials.html", "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("dashboard: parse %s template: %w", name, err)
		}
		r.pages[name] = t
	}
	return r, nil
}

// render executes the layout with p for the named page.
func (r *renderer) render(w io.Writer, name string, p page) error {
	t, ok := r.pages[name]
	if !ok {
		return fmt.Errorf("dashboard: unknown page %q", name)
	}
	return t.ExecuteTemplate(w, "layout", p)
}

var funcs = template.FuncMap{
	"join":        strings.Join,
	"when":        formatWhen,
	"ms":          formatMS,
	"statusLabel": statusLabel,
	"caseLabel":   caseLabel,
	"yesno":       yesNo,
	"num":         formatNumber,
	"counts":      formatCounts,
}

// formatWhen renders a time or *time.Time in UTC; nil and zero times are
// shown as "not yet".
func formatWhen(v any) string {
	var t time.Time
	switch x := v.(type) {
	case time.Time:
		t = x
	case *time.Time:
		if x != nil {
			t = *x
		}
	}
	if t.IsZero() {
		return "not yet"
	}
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

// formatMS renders an optional duration; unknown is never shown as zero.
func formatMS(v *float64) string {
	if v == nil {
		return "duration unknown"
	}
	return fmt.Sprintf("%.1f ms", *v)
}

func formatNumber(n *plans.Number) string {
	if n == nil {
		return "unknown"
	}
	return n.String()
}

// formatCounts renders {"Seq Scan": 2} as "Seq Scan: 2", sorted by name.
func formatCounts(m map[string]int) string {
	if len(m) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s: %d", k, m[k])
	}
	return strings.Join(parts, ", ")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

var statusLabels = map[store.Status]string{
	store.StatusQueued:      "Queued",
	store.StatusRunning:     "Running",
	store.StatusSucceeded:   "Succeeded",
	store.StatusFailed:      "Failed",
	store.StatusCancelled:   "Cancelled",
	store.StatusInterrupted: "Interrupted (needs review)",
}

func statusLabel(s store.Status) string {
	if l, ok := statusLabels[s]; ok {
		return l
	}
	return string(s)
}

var caseLabels = map[report.CaseStatus]string{
	report.CasePassed:       "Passed",
	report.CaseFailed:       "Failed",
	report.CaseError:        "Error",
	report.CaseSkipped:      "Skipped",
	report.CaseInconclusive: "Inconclusive",
	report.CaseCancelled:    "Cancelled",
}

func caseLabel(s report.CaseStatus) string {
	if l, ok := caseLabels[s]; ok {
		return l
	}
	return string(s)
}

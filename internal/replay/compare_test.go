package replay

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/testutil"
)

var update = flag.Bool("update", false, "rewrite golden files")

const compareSuite = `
version: 1
name: invoices
description: Baseline and candidate invoice API comparison
defaults:
  timeout: 5s
  compare:
    ignore: [/meta]
    headers: [Content-Type]
cases:
  - id: create-session
    description: Obtain a session identifier
    method: POST
    path: /sessions
    expect:
      status: 201
    compare:
      ignore: [/session]
    capture:
      - {name: session, pointer: /session}
  - id: get-invoice
    description: Invoice totals and lines
    method: GET
    path: /invoices/{id}
    path_params: {id: "inv-7"}
    headers: {X-Session: "${session}"}
    expect:
      status: 200
      json:
        - {pointer: /id, equals: "inv-7"}
    compare:
      tolerance: {absolute: "0.01"}
      unordered:
        - {pointer: /lines, key: sku}
  - id: list-tags
    method: GET
    path: /tags
  - id: health
    method: GET
    path: /health
    compare:
      skip_status: true
`

// invoiceServer serves the compare suite; candidate changes some values.
func invoiceServer(t *testing.T, candidate bool, sessions *[]string, mu *sync.Mutex) string {
	t.Helper()
	srv := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*sessions = append(*sessions, r.Header.Get("X-Session"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/sessions":
			w.WriteHeader(http.StatusCreated)
			if candidate {
				io.WriteString(w, `{"session":"cand-1"}`)
			} else {
				io.WriteString(w, `{"session":"base-1"}`)
			}
		case "/invoices/inv-7":
			if candidate {
				io.WriteString(w, `{"id":"inv-7","total":100.004,"currency":"EUR","status":null,`+
					`"lines":[{"sku":"b","qty":2},{"sku":"a","qty":1},{"sku":"c","qty":5}],"meta":{"request_id":"r2"}}`)
			} else {
				io.WriteString(w, `{"id":"inv-7","total":100.00,"currency":"USD","discount":0,`+
					`"lines":[{"sku":"a","qty":1},{"sku":"b","qty":3}],"meta":{"request_id":"r1"}}`)
			}
		case "/tags":
			if candidate {
				w.Header().Set("Content-Type", "text/plain")
				io.WriteString(w, "tags unavailable")
			} else {
				io.WriteString(w, `["x","y"]`)
			}
		case "/health":
			if candidate {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			io.WriteString(w, `{"ok":true}`)
		}
	}))
	return srv.URL
}

func runCompareSuite(t *testing.T) (*Result, []string, []string) {
	t.Helper()
	t.Setenv(testutil.EnvAsyncClose, "1")
	var mu sync.Mutex
	var baseSessions, candSessions []string
	baseURL := invoiceServer(t, false, &baseSessions, &mu)
	candURL := invoiceServer(t, true, &candSessions, &mu)
	opts := Options{AllowWrites: true}
	res, err := newExecutor(t, opts).Compare(context.Background(), loaded(t, compareSuite),
		plainTarget(t, "baseline", baseURL), plainTarget(t, "candidate", candURL))
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	return res, append([]string(nil), baseSessions...), append([]string(nil), candSessions...)
}

func TestCompareEndToEnd(t *testing.T) {
	res, baseSessions, candSessions := runCompareSuite(t)
	if strings.Join(baseSessions, ",") != ",base-1,," || strings.Join(candSessions, ",") != ",cand-1,," {
		t.Fatalf("capture scopes mixed: baseline %v candidate %v", baseSessions, candSessions)
	}
	if got := statuses(res); got != "create-session=passed get-invoice=failed list-tags=failed health=passed" {
		t.Fatalf("statuses: %s", got)
	}
	var got []string
	for _, f := range res.Cases[1].Findings {
		got = append(got, f.Code+"@"+f.Location)
	}
	// Case findings are sorted with finding.Sort (by location).
	want := []string{
		"compare.within_tolerance@",
		"compare.value_changed@/currency",
		"compare.field_missing@/discount",
		"compare.value_changed@/lines/1/qty",
		"compare.element_added@/lines/2",
		"compare.null_vs_absent@/status",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("get-invoice findings:\n got  %v\n want %v", got, want)
	}
	tags := res.Cases[2].Findings
	if len(tags) != 2 || tags[0].Code != "compare.body_format_changed" || tags[1].Code != "compare.header_changed" {
		t.Fatalf("list-tags findings: %+v", tags)
	}
	for _, f := range res.Cases[1].Findings {
		if f.Details["baseline_target"] != "baseline" || f.Details["candidate_target"] != "candidate" {
			t.Fatalf("finding without targets: %+v", f)
		}
	}
	if len(res.Cases[0].Observations) != 2 || res.Cases[0].Observations[1].Target != "candidate" {
		t.Fatalf("observations: %+v", res.Cases[0].Observations)
	}
	if res.Report.Command != "compare" || strings.Join(res.Report.Targets, ",") != "baseline,candidate" {
		t.Fatalf("report header: %s %v", res.Report.Command, res.Report.Targets)
	}
}

func TestCompareTargetNames(t *testing.T) {
	l := loaded(t, "version: 1\nname: x\ncases:\n  - {id: a, method: GET, path: /a}\n")
	same := plainTarget(t, "base", "http://127.0.0.1:1")
	if _, err := newExecutor(t, Options{}).Compare(context.Background(), l, same, same); err == nil {
		t.Fatal("identical target names accepted")
	}
}

func TestCompareSkipsOnTransportError(t *testing.T) {
	t.Setenv(testutil.EnvAsyncClose, "1")
	base := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"a":1}`)
	}))
	cand := testutil.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat("x", 4096))
	}))
	opts := Options{}
	opts.Limits.MaxResponseBytes = 1024
	l := loaded(t, "version: 1\nname: x\ncases:\n  - {id: a, method: GET, path: /a}\n")
	res, err := newExecutor(t, opts).Compare(context.Background(), l, plainTarget(t, "baseline", base.URL), plainTarget(t, "candidate", cand.URL))
	if err != nil {
		t.Fatal(err)
	}
	fs := res.Cases[0].Findings
	if len(fs) != 1 || fs[0].Code != CodeResponseTooLarge || fs[0].Details["target"] != "candidate" {
		t.Fatalf("findings: %+v", fs)
	}
	if res.Cases[0].Status != report.CaseError {
		t.Fatalf("status %s", res.Cases[0].Status)
	}
}

// normalize replaces run-dependent values (timings) in a report document.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			switch k {
			case "duration_ms", "headers_ms", "first_event_ms":
				t[k] = 0
			case "started_at", "finished_at":
				t[k] = "2000-01-01T00:00:00Z"
			default:
				t[k] = normalize(child)
			}
		}
	case []any:
		for i := range t {
			t[i] = normalize(t[i])
		}
	}
	return v
}

func TestCompareGoldenReport(t *testing.T) {
	res, _, _ := runCompareSuite(t)
	var buf bytes.Buffer
	if err := report.RenderJSON(&buf, res.Report); err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	got, err := json.MarshalIndent(normalize(doc), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "compare_report.golden.json")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Fatalf("report differs from %s; rerun with -update and review the diff:\n%s", path, got)
	}
	if err := res.Report.Findings[0].Validate(); err != nil {
		t.Fatal(err)
	}
	if res.Report.Summary.Findings.ByKind[finding.KindDifference] == 0 {
		t.Fatal("golden run should contain differences")
	}
}

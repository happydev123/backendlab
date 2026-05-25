//go:build demo

package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/cli"
	"github.com/happydev123/backendlab/internal/finding"
	"github.com/happydev123/backendlab/internal/mock"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/testutil"
)

// env is the demonstration environment shared by the CLI steps.
type env struct {
	mockURL string
	config  string
	client  *http.Client
}

func newEnv(t *testing.T, m *mock.Server) *env {
	t.Helper()
	srv := testutil.NewServer(t, m.Handler())
	dir := t.TempDir()
	cfg := filepath.Join(dir, "backendlab.yaml")
	body := "version: 1\ntargets:\n  baseline:\n    base_url: " + srv.URL + "/baseline\n  candidate:\n    base_url: " + srv.URL + "/candidate\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return &env{mockURL: srv.URL, config: cfg, client: &http.Client{Transport: testutil.Transport()}}
}

// cliRun runs one backendlab command in-process and returns the exit code
// and the decoded JSON report from stdout.
func (e *env) cliRun(t *testing.T, args ...string) (int, *report.Report, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	app := &cli.App{Stdout: &stdout, Stderr: &stderr, Env: func(string) (string, bool) { return "", false }, Transport: testutil.Transport()}
	args = append(args, "--format", "json", "--config", e.config)
	code := app.Run(context.Background(), args)
	if stdout.Len() == 0 {
		t.Fatalf("%v: no report on stdout (exit %d)\nstderr: %s", args, code, stderr.String())
	}
	r, err := report.Decode(stdout.Bytes())
	if err != nil {
		t.Fatalf("%v: stdout is not a report: %v", args, err)
	}
	return code, r, stderr.String()
}

func (e *env) getJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	resp, err := e.client.Get(e.mockURL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return v
}

func example(parts ...string) string {
	return filepath.Join(append([]string{"..", "examples"}, parts...)...)
}

func findingsFor(r *report.Report, caseID string) []finding.Finding {
	var out []finding.Finding
	for _, f := range r.Findings {
		if f.CaseID == caseID {
			out = append(out, f)
		}
	}
	return out
}

func hasFinding(fs []finding.Finding, code, location string) bool {
	for _, f := range fs {
		if f.Code == code && (location == "" || f.Location == location) {
			return true
		}
	}
	return false
}

func caseStatus(r *report.Report, id string) report.CaseStatus {
	for _, c := range r.Cases {
		if c.ID == id {
			return c.Status
		}
	}
	return ""
}

func TestOfflineScenario(t *testing.T) {
	m := mock.New(mock.DefaultConfig())
	e := newEnv(t, m)

	code, cmp, _ := e.cliRun(t, "compare", example("invoice-api", "compare-suite.yaml"))

	t.Run("1 structural comparison", func(t *testing.T) {
		if code != cli.ExitFailed || cmp.Status != report.StatusFailed {
			t.Fatalf("exit=%d status=%s, want 1/failed", code, cmp.Status)
		}
		fs := findingsFor(cmp, "invoice-detail")
		for _, want := range []struct{ code, loc string }{
			{"compare.value_changed", "/total"},
			{"compare.field_missing", "/customer/email"},
			{"compare.field_added", "/customer/email_address"},
		} {
			if !hasFinding(fs, want.code, want.loc) {
				t.Errorf("missing %s at %s in %+v", want.code, want.loc, fs)
			}
		}
		if caseStatus(cmp, "health") != report.CasePassed || caseStatus(cmp, "list-invoices") != report.CasePassed {
			t.Errorf("unchanged cases should pass: %+v", cmp.Cases)
		}
		t.Logf("compare: exit %d, %d findings, invoice-detail differences at /total, /customer/email, /customer/email_address", code, len(cmp.Findings))
	})

	t.Run("2 volatile field exclusion", func(t *testing.T) {
		a, b := e.getJSON(t, "/baseline/health"), e.getJSON(t, "/candidate/health")
		if a["request_id"] == b["request_id"] {
			t.Fatal("expected request_id to differ between responses")
		}
		for _, f := range cmp.Findings {
			if strings.HasPrefix(f.Location, "/request_id") || strings.HasPrefix(f.Location, "/generated_at") {
				t.Errorf("excluded field reported: %+v", f)
			}
		}
		t.Logf("request_id differed (%v vs %v) and was excluded", a["request_id"], b["request_id"])
	})

	t.Run("3 numeric tolerance", func(t *testing.T) {
		a, b := e.getJSON(t, "/baseline/invoices/inv-1001"), e.getJSON(t, "/candidate/invoices/inv-1001")
		if a["exchange_rate"] == b["exchange_rate"] {
			t.Fatal("expected exchange_rate to differ")
		}
		if hasFinding(cmp.Findings, "compare.value_changed", "/exchange_rate") {
			t.Error("exchange_rate difference within tolerance was reported")
		}
		for _, f := range cmp.Findings {
			if strings.HasPrefix(f.Location, "/line_items") {
				t.Errorf("reordered line items reported: %+v", f)
			}
		}
		t.Logf("exchange_rate %v vs %v absorbed by absolute tolerance 0.00001", a["exchange_rate"], b["exchange_rate"])
	})

	t.Run("4 inconsistent invoice totals", func(t *testing.T) {
		code, r, _ := e.cliRun(t, "run", example("invoice-api", "validate-suite.yaml"), "--target", "candidate")
		if code != cli.ExitFailed {
			t.Fatalf("exit = %d, want 1", code)
		}
		if !hasFinding(r.Findings, "validate.sum", "") {
			t.Fatalf("no validate.sum finding: %+v", r.Findings)
		}
		code, r, _ = e.cliRun(t, "run", example("invoice-api", "validate-suite.yaml"), "--target", "baseline")
		if code != cli.ExitOK {
			t.Fatalf("baseline exit = %d, findings %+v", code, r.Findings)
		}
		var buf bytes.Buffer
		app := &cli.App{Stdout: &buf, Stderr: io.Discard}
		vcode := app.Run(context.Background(), []string{"validate", example("invoice", "invoices-batch.json"), "--rules", example("invoice", "invoice-rules.yaml"), "--format", "json"})
		vr, err := report.Decode(buf.Bytes())
		if err != nil || vcode != cli.ExitFailed {
			t.Fatalf("validate exit=%d err=%v", vcode, err)
		}
		if !hasFinding(vr.Findings, "validate.sum", "") || !hasFinding(vr.Findings, "validate.unique", "") {
			t.Fatalf("validate findings = %+v", vr.Findings)
		}
		t.Logf("candidate total mismatch failed validate.sum; baseline passed; batch file: %d findings", len(vr.Findings))
	})

	code, streams, _ := e.cliRun(t, "run", example("invoice-api", "stream-suite.yaml"), "--target", "baseline")

	t.Run("5 SSE across fragmented writes", func(t *testing.T) {
		if caseStatus(streams, "chat") != report.CasePassed {
			t.Fatalf("chat case = %s, findings %+v", caseStatus(streams, "chat"), findingsFor(streams, "chat"))
		}
		for _, c := range streams.Cases {
			if c.ID == "chat" && (len(c.Observations) != 1 || c.Observations[0].Events < 3 || c.Observations[0].FirstEventMS == nil) {
				t.Fatalf("chat observations = %+v", c.Observations)
			}
		}
		t.Log("chat stream split inside lines, CRLF pairs and UTF-8 characters parsed with all expectations met")
	})

	t.Run("6 stream timeout and completion classification", func(t *testing.T) {
		if code != cli.ExitFailed {
			t.Fatalf("exit = %d, want 1", code)
		}
		if !hasFinding(findingsFor(streams, "truncated"), "stream.missing_terminal", "") {
			t.Errorf("truncated stream: %+v", findingsFor(streams, "truncated"))
		}
		if !hasFinding(findingsFor(streams, "gap"), "stream.max_gap_exceeded", "") {
			t.Errorf("gap stream: %+v", findingsFor(streams, "gap"))
		}
		scode, slow, _ := e.cliRun(t, "run", example("invoice-api", "slow-suite.yaml"), "--target", "baseline")
		if scode != cli.ExitFailed || caseStatus(slow, "fast") != report.CasePassed || caseStatus(slow, "slow") != report.CaseError {
			t.Fatalf("slow suite exit=%d cases=%+v", scode, slow.Cases)
		}
		if !hasFinding(findingsFor(slow, "slow"), "replay.timeout", "") {
			t.Errorf("slow case should be a timeout transport error: %+v", findingsFor(slow, "slow"))
		}
		t.Log("truncated stream: missing terminal marker; gap stream: gap over limit; slow request: replay.timeout transport error")
	})

	t.Run("7 offline plan comparison", func(t *testing.T) {
		var buf bytes.Buffer
		app := &cli.App{Stdout: &buf, Stderr: io.Discard}
		pcode := app.Run(context.Background(), []string{"plans", "compare", example("plans", "before.json"), example("plans", "after.json"), "--fail-on", "scan,spill", "--format", "json"})
		pr, err := report.Decode(buf.Bytes())
		if err != nil || pcode != cli.ExitFailed {
			t.Fatalf("plans exit=%d err=%v", pcode, err)
		}
		if !hasFinding(pr.Findings, "plans.scan_strategy_changed", "") || !hasFinding(pr.Findings, "plans.sort_spill_introduced", "") {
			t.Fatalf("plan findings = %+v", pr.Findings)
		}
		t.Logf("plan comparison: %d findings incl. scan strategy change and sort spill", len(pr.Findings))
	})
}

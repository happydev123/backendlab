package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/mock"
	"github.com/happydev123/backendlab/internal/report"
	"github.com/happydev123/backendlab/internal/testutil"
)

// mockApp starts the demonstration service and returns an App whose
// config file points baseline and candidate at it.
func mockApp(t *testing.T) (*App, *bytes.Buffer, *bytes.Buffer, string) {
	t.Helper()
	srv := testutil.NewServer(t, mock.New(mock.DefaultConfig()).Handler())
	dir := t.TempDir()
	cfg := "version: 1\ntargets:\n  baseline:\n    base_url: " + srv.URL + "/baseline\n  candidate:\n    base_url: " + srv.URL + "/candidate\n"
	cfgPath := filepath.Join(dir, "backendlab.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	app, stdout, stderr := testApp(nil)
	app.Transport = testutil.Transport()
	return app, stdout, stderr, cfgPath
}

func example(parts ...string) string {
	return filepath.Join(append([]string{"..", "..", "examples"}, parts...)...)
}

func decodeReport(t *testing.T, data []byte) *report.Report {
	t.Helper()
	r, err := report.Decode(data)
	if err != nil {
		t.Fatalf("stdout is not a JSON report: %v\n%s", err, data)
	}
	return r
}

func TestCompareExampleAgainstMock(t *testing.T) {
	app, stdout, stderr, cfg := mockApp(t)
	code := app.Run(context.Background(), []string{"compare", example("invoice-api", "compare-suite.yaml"), "--config", cfg, "--format", "json"})
	if code != ExitFailed {
		t.Fatalf("exit = %d, want 1\nstderr: %s", code, stderr)
	}
	r := decodeReport(t, stdout.Bytes())
	if r.Status != report.StatusFailed {
		t.Fatalf("status = %s", r.Status)
	}
	locations := map[string]bool{}
	for _, f := range r.Findings {
		if f.CaseID == "invoice-detail" && f.Module == "compare" {
			locations[f.Location] = true
		}
		if strings.HasPrefix(f.Location, "/request_id") || strings.HasPrefix(f.Location, "/generated_at") {
			t.Errorf("volatile field reported: %+v", f)
		}
		if strings.HasPrefix(f.Location, "/exchange_rate") || strings.HasPrefix(f.Location, "/line_items") {
			t.Errorf("tolerated or reordered field reported: %+v", f)
		}
	}
	for _, want := range []string{"/total", "/customer/email", "/customer/email_address"} {
		if !locations[want] {
			t.Errorf("missing difference at %s; got %v", want, locations)
		}
	}
}

func TestRunWritesRequireFlag(t *testing.T) {
	app, stdout, stderr, cfg := mockApp(t)
	code := app.Run(context.Background(), []string{"run", example("invoice-api", "writes-suite.yaml"), "--target", "candidate", "--config", cfg})
	if code != ExitError {
		t.Fatalf("exit = %d, want 2", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "--allow-writes") {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}

	app, stdout, stderr, cfg = mockApp(t)
	code = app.Run(context.Background(), []string{"run", example("invoice-api", "writes-suite.yaml"), "--target", "candidate", "--config", cfg, "--allow-writes", "--format", "json"})
	if code == ExitError {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr.String(), "never retried") {
		t.Fatalf("missing write warning on stderr: %q", stderr)
	}
	decodeReport(t, stdout.Bytes())
}

func TestStdoutStderrSeparation(t *testing.T) {
	app, stdout, stderr, cfg := mockApp(t)
	out := filepath.Join(t.TempDir(), "report.md")
	code := app.Run(context.Background(), []string{"run", example("invoice-api", "compare-suite.yaml"), "--target", "baseline", "--config", cfg, "--format", "markdown", "--output", out})
	if code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout should be empty with --output, got %q", stdout)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "# BackendLab report: run") {
		t.Fatalf("report file = %q", data)
	}
	if !strings.Contains(stderr.String(), "report written to") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestOfflineCommands(t *testing.T) {
	tests := []struct {
		name string
		args []string
		code int
	}{
		{"validate failing batch", []string{"validate", example("invoice", "invoices-batch.json"), "--rules", example("invoice", "invoice-rules.yaml"), "--format", "json"}, ExitFailed},
		{"plans default passes", []string{"plans", "compare", example("plans", "before.json"), example("plans", "after.json"), "--format", "json"}, ExitOK},
		{"plans fail-on scan", []string{"plans", "compare", example("plans", "before.json"), example("plans", "after.json"), "--fail-on", "scan", "--format", "json"}, ExitFailed},
		{"stream inspect", []string{"stream", "inspect", filepath.Join("..", "stream", "testdata", "chat.sse"), "--terminal-data", "[DONE]", "--format", "json"}, ExitOK},
		{"stream require terminal", []string{"stream", "inspect", filepath.Join("..", "stream", "testdata", "truncated.sse"), "--terminal-data", "[DONE]", "--require-terminal", "--format", "json"}, ExitFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, stdout, stderr := testApp(nil)
			code := app.Run(context.Background(), tt.args)
			if code != tt.code {
				t.Fatalf("exit = %d, want %d\nstderr: %s\nstdout: %s", code, tt.code, stderr, stdout)
			}
			var v map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &v); err != nil {
				t.Fatalf("stdout is not pure JSON: %v", err)
			}
		})
	}
}

func TestInputErrorsExitTwo(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("version: 1\nname: x\ncases:\n  - {id: a, method: GET, path: /x, bogus: 1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	notPlan := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(notPlan, []byte(`{"rows": []}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := [][]string{
		{"run", bad, "--target", "x", "--base-url", "x=http://127.0.0.1:1"},
		{"run", filepath.Join(dir, "missing.yaml"), "--target", "x"},
		{"run", example("invoice-api", "compare-suite.yaml")},
		{"run", example("invoice-api", "compare-suite.yaml"), "--target", "nope", "--config", filepath.Join(dir, "none.yaml")},
		{"compare", example("invoice-api", "compare-suite.yaml"), "--baseline", "a", "--candidate", "a"},
		{"plans", "compare", notPlan, notPlan},
		{"validate", notPlan},
		{"stream", "inspect"},
		{"run", "--format", "xml", example("invoice-api", "compare-suite.yaml"), "--target", "x"},
	}
	for _, args := range tests {
		app, stdout, stderr := testApp(nil)
		if code := app.Run(context.Background(), args); code != ExitError {
			t.Errorf("%v: exit = %d, want 2 (stderr %q)", args, code, stderr)
		}
		if stdout.Len() != 0 {
			t.Errorf("%v: stdout should be empty on error, got %q", args, stdout)
		}
	}
}

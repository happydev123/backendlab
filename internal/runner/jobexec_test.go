package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/jobs"
	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/internal/suite"
)

const serverYAML = `version: 1
suites_dir: suites
targets:
  internal:
    base_url: http://10.1.2.3/api
  public:
    base_url: https://api.example.com
egress:
  allow_hosts: [api.example.com]
`

const readSuite = "version: 1\nname: t\ncases:\n  - {id: a, method: GET, path: /a}\n"

func newExecutor(t *testing.T) *JobExecutor {
	t.Helper()
	cfg, err := config.ParseServer([]byte(serverYAML))
	if err != nil {
		t.Fatal(err)
	}
	x, err := NewJobExecutor(cfg, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	return x
}

func run(mode store.Mode, targets []string, snapshot string) *store.Run {
	return &store.Run{
		SuiteFile:     "read.yaml",
		SuiteSnapshot: []byte(snapshot),
		SuiteSHA256:   suite.Digest([]byte(snapshot)),
		Mode:          mode,
		Targets:       targets,
	}
}

func noCase(jobs.CaseUpdate) error { return nil }

func TestJobExecutorRejectsBeforeSending(t *testing.T) {
	x := newExecutor(t)
	tampered := run(store.ModeRun, []string{"public"}, readSuite)
	tampered.SuiteSnapshot = []byte(readSuite + "# edited\n")
	tests := []struct {
		name string
		run  *store.Run
		want string
	}{
		{"digest mismatch", tampered, "digest"},
		{"unknown target", run(store.ModeRun, []string{"elsewhere"}, readSuite), "not configured"},
		{"wrong target count", run(store.ModeCompare, []string{"public"}, readSuite), "needs 2 targets"},
		{"egress denied", run(store.ModeRun, []string{"internal"}, readSuite), "internal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := x.Execute(context.Background(), tt.run, noCase)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestJobExecutorNeedsBothWritePermissions(t *testing.T) {
	x := newExecutor(t)
	writes := "version: 1\nname: w\ncases:\n  - {id: a, method: POST, path: /a}\n"
	r := run(store.ModeRun, []string{"public"}, writes)
	r.WritesRequested = true // the server configuration does not allow writes
	_, err := x.Execute(context.Background(), r, noCase)
	if err == nil || !strings.Contains(err.Error(), "writes are not allowed") {
		t.Fatalf("err = %v", err)
	}
}

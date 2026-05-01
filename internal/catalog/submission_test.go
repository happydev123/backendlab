package catalog

import (
	"errors"
	"strings"
	"testing"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/store"
)

func testConfig(allowWrites bool) *config.Server {
	return &config.Server{
		SuitesDir:   "unused",
		AllowWrites: allowWrites,
		Targets: map[string]config.Target{
			"staging":   {BaseURL: "https://staging.example.test"},
			"candidate": {BaseURL: "https://candidate.example.test"},
		},
		Worker: config.Worker{MaxReadOnlyAttempts: 3},
		Limits: limits.Default(),
	}
}

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "health.yaml", readOnlySuite)
	writeFile(t, dir, "writes.yaml", mutatingSuite)
	c, err := LoadDir(dir, limits.Default())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustGet(t *testing.T, c *Catalog, file string) Entry {
	t.Helper()
	e, ok := c.Get(file)
	if !ok {
		t.Fatalf("%s not in catalog: %+v", file, c.Problems())
	}
	return e
}

func TestValidateSubmissionReadOnly(t *testing.T) {
	c := testCatalog(t)
	e := mustGet(t, c, "health.yaml")
	n, err := ValidateSubmission(testConfig(false), e, Submission{
		SuiteFile: "health.yaml", Mode: store.ModeRun, Targets: []string{"staging"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n.SuiteName != "health-checks" || n.SuiteFile != "health.yaml" || n.Mutating || n.WritesRequested ||
		n.MaxAttempts != 3 || string(n.SuiteSnapshot) != readOnlySuite || n.Targets[0] != "staging" {
		t.Errorf("new run = %+v", n)
	}

	n, err = ValidateSubmission(testConfig(false), e, Submission{
		Mode: store.ModeCompare, Targets: []string{"staging", "candidate"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n.Mode != store.ModeCompare || strings.Join(n.Targets, ",") != "staging,candidate" {
		t.Errorf("compare run = %+v", n)
	}
}

func TestValidateSubmissionRejects(t *testing.T) {
	c := testCatalog(t)
	health := mustGet(t, c, "health.yaml")
	writes := mustGet(t, c, "writes.yaml")
	cases := []struct {
		name  string
		allow bool
		entry Entry
		sub   Submission
		want  error
		text  string
	}{
		{"unknown target", false, health, Submission{Mode: store.ModeRun, Targets: []string{"prod"}}, ErrInvalidSubmission, `unknown target "prod"`},
		{"url as target", false, health, Submission{Mode: store.ModeRun, Targets: []string{"http://169.254.169.254/"}}, ErrInvalidSubmission, "unknown target"},
		{"no targets", false, health, Submission{Mode: store.ModeRun}, ErrInvalidSubmission, "exactly one target"},
		{"two targets for run", false, health, Submission{Mode: store.ModeRun, Targets: []string{"staging", "candidate"}}, ErrInvalidSubmission, "exactly one target"},
		{"one target for compare", false, health, Submission{Mode: store.ModeCompare, Targets: []string{"staging"}}, ErrInvalidSubmission, "exactly two targets"},
		{"same target twice", false, health, Submission{Mode: store.ModeCompare, Targets: []string{"staging", "staging"}}, ErrInvalidSubmission, "different targets"},
		{"bad mode", false, health, Submission{Mode: "replay", Targets: []string{"staging"}}, ErrInvalidSubmission, "mode must be"},
		{"suite mismatch", false, health, Submission{SuiteFile: "writes.yaml", Mode: store.ModeRun, Targets: []string{"staging"}}, ErrInvalidSubmission, "does not match"},
		{"zero entry", false, Entry{}, Submission{Mode: store.ModeRun, Targets: []string{"staging"}}, ErrInvalidSubmission, "not an approved"},
		{"writes requested, server disallows", false, health, Submission{Mode: store.ModeRun, Targets: []string{"staging"}, AllowWrites: true}, ErrWritesNotPermitted, "does not allow writes"},
		{"mutating, neither allows", false, writes, Submission{Mode: store.ModeRun, Targets: []string{"staging"}}, ErrWritesNotPermitted, "does not allow writes"},
		{"mutating, only request allows", false, writes, Submission{Mode: store.ModeRun, Targets: []string{"staging"}, AllowWrites: true}, ErrWritesNotPermitted, "does not allow writes"},
		{"mutating, only server allows", true, writes, Submission{Mode: store.ModeRun, Targets: []string{"staging"}}, ErrWritesNotPermitted, "must set allow_writes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateSubmission(testConfig(tc.allow), tc.entry, tc.sub)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.text) {
				t.Errorf("message %q does not mention %q", err, tc.text)
			}
		})
	}
	if _, err := ValidateSubmission(nil, health, Submission{}); err == nil {
		t.Error("nil config accepted")
	}
}

func TestValidateSubmissionMutatingNeedsBoth(t *testing.T) {
	c := testCatalog(t)
	writes := mustGet(t, c, "writes.yaml")
	n, err := ValidateSubmission(testConfig(true), writes, Submission{
		Mode: store.ModeRun, Targets: []string{"staging"}, AllowWrites: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !n.Mutating || !n.WritesRequested || n.MaxAttempts != 1 {
		t.Errorf("mutating run = %+v", n)
	}
	_, err = ValidateSubmission(testConfig(true), writes, Submission{Mode: store.ModeRun, Targets: []string{"staging"}})
	if err == nil || !strings.Contains(err.Error(), "increment") {
		t.Error("error does not name the mutating case")
	}
}

func TestEchoedValuesAreBounded(t *testing.T) {
	c := testCatalog(t)
	long := strings.Repeat("x", 10000)
	_, err := ValidateSubmission(testConfig(false), mustGet(t, c, "health.yaml"), Submission{Mode: store.ModeRun, Targets: []string{long}})
	if err == nil || len(err.Error()) > 300 {
		t.Errorf("error = %.80v...", err)
	}
}

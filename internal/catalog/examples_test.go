package catalog

import (
	"path/filepath"
	"testing"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/store"
)

// TestServerExamples keeps examples/server consistent with the code: the
// annotated configuration parses and validates, every example suite loads,
// and the documented submissions pass the policy.
func TestServerExamples(t *testing.T) {
	root := filepath.Join("..", "..")
	cfg, err := config.LoadServer(filepath.Join(root, "examples", "server", "backendlab-server.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := LoadDir(filepath.Join(root, filepath.FromSlash(cfg.SuitesDir)), cfg.Limits)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Problems()) != 0 {
		t.Fatalf("example suites do not load: %+v", c.Problems())
	}
	if len(c.List()) != 2 {
		t.Fatalf("example suites: %+v", c.List())
	}

	compare, ok := c.Get("invoice-compare.yaml")
	if !ok || compare.Mutating {
		t.Fatalf("invoice-compare.yaml: %+v", compare)
	}
	if _, err := ValidateSubmission(cfg, compare, Submission{Mode: store.ModeCompare, Targets: []string{"baseline", "candidate"}}); err != nil {
		t.Errorf("documented compare submission: %v", err)
	}
	writes, ok := c.Get("counter-writes.yaml")
	if !ok || !writes.Mutating {
		t.Fatalf("counter-writes.yaml: %+v", writes)
	}
	if _, err := ValidateSubmission(cfg, writes, Submission{Mode: store.ModeRun, Targets: []string{"candidate"}, AllowWrites: true}); err != nil {
		t.Errorf("documented writes submission: %v", err)
	}
	if cfg.Auth.SecureCookiesEnabled() {
		t.Error("the local example disables secure cookies for plain HTTP; the doc says so")
	}
}

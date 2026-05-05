package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/happydev123/backendlab/internal/limits"
	"github.com/happydev123/backendlab/internal/store"
	"github.com/happydev123/backendlab/internal/suite"
)

func storedRun(file, content string, mode store.Mode, targets []string, writes, mutating bool) *store.Run {
	return &store.Run{
		PublicID:        "run_aaaaaaaaaaaaaaaa",
		SuiteFile:       file,
		SuiteSHA256:     suite.Digest([]byte(content)),
		SuiteSnapshot:   []byte(content),
		Mode:            mode,
		Targets:         targets,
		WritesRequested: writes,
		Mutating:        mutating,
		Status:          store.StatusInterrupted,
	}
}

func TestCheckResubmission(t *testing.T) {
	c := testCatalog(t)
	run := storedRun("health.yaml", readOnlySuite, store.ModeRun, []string{"staging"}, false, false)
	if err := CheckResubmission(testConfig(false), c, run); err != nil {
		t.Fatalf("unchanged read-only run: %v", err)
	}

	writes := storedRun("writes.yaml", mutatingSuite, store.ModeRun, []string{"staging"}, true, true)
	if err := CheckResubmission(testConfig(true), c, writes); err != nil {
		t.Fatalf("mutating run with writes allowed: %v", err)
	}
	// The server no longer allows writes: same policy as a new submission.
	if err := CheckResubmission(testConfig(false), c, writes); !errors.Is(err, ErrWritesNotPermitted) {
		t.Errorf("writes revoked: err = %v", err)
	}

	gone := storedRun("health.yaml", readOnlySuite, store.ModeRun, []string{"removed-target"}, false, false)
	if err := CheckResubmission(testConfig(false), c, gone); !errors.Is(err, ErrInvalidSubmission) {
		t.Errorf("target removed from config: err = %v", err)
	}
}

func TestCheckResubmissionRejectsChangedSuite(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "health.yaml", readOnlySuite)
	before := storedRun("health.yaml", readOnlySuite, store.ModeRun, []string{"staging"}, false, false)

	edited := readOnlySuite + "  - id: extra\n    method: GET\n    path: /extra\n"
	if err := os.WriteFile(filepath.Join(dir, "health.yaml"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadDir(dir, limits.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckResubmission(testConfig(false), c, before); !errors.Is(err, ErrSuiteChanged) {
		t.Errorf("edited suite: err = %v", err)
	}

	removed := storedRun("deleted.yaml", readOnlySuite, store.ModeRun, []string{"staging"}, false, false)
	if err := CheckResubmission(testConfig(false), c, removed); !errors.Is(err, ErrSuiteChanged) {
		t.Errorf("removed suite: err = %v", err)
	}
	if err := CheckResubmission(nil, c, before); err == nil {
		t.Error("nil config accepted")
	}
}

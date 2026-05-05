package catalog

import (
	"errors"
	"fmt"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/store"
)

// ErrSuiteChanged is returned by CheckResubmission when the suite file a
// run was submitted from is no longer approved in the same form.
var ErrSuiteChanged = errors.New("suite changed since submission")

// CheckResubmission decides whether run may be resubmitted under the
// current server policy. store.Resubmit copies the stored snapshot,
// targets and write request without re-checking anything, so callers must
// call this first.
//
// The rule is strict: the run's suite file must still be in the catalog
// with the same SHA-256 as the stored snapshot. A run whose suite was
// edited or removed is rejected with ErrSuiteChanged rather than
// re-validated against the new file, because resubmission re-executes the
// stored snapshot and the operator approved the file as it is now. To run
// the current file, submit a new run. When the digest matches, the
// snapshot equals the catalog entry and the same checks as for a new
// submission apply (ValidateSubmission): the targets must still be
// configured and, for mutating suites, the server must still allow writes
// and the original run must have requested them.
func CheckResubmission(cfg *config.Server, c *Catalog, run *store.Run) error {
	if cfg == nil || c == nil || run == nil {
		return errors.New("catalog: configuration, catalog and run are required")
	}
	e, ok := c.Get(run.SuiteFile)
	if !ok {
		return fmt.Errorf("%w: suite file %s is no longer in the approved suites directory; submit a new run",
			ErrSuiteChanged, run.SuiteFile)
	}
	if e.SHA256 != run.SuiteSHA256 {
		return fmt.Errorf("%w: suite file %s was modified after run %s was submitted; submit a new run to use the current file",
			ErrSuiteChanged, run.SuiteFile, run.PublicID)
	}
	_, err := ValidateSubmission(cfg, e, Submission{
		SuiteFile:   run.SuiteFile,
		Mode:        run.Mode,
		Targets:     run.Targets,
		AllowWrites: run.WritesRequested,
	})
	return err
}

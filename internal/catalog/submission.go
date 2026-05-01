package catalog

import (
	"errors"
	"fmt"
	"strings"

	"github.com/happydev123/backendlab/internal/config"
	"github.com/happydev123/backendlab/internal/jsonutil"
	"github.com/happydev123/backendlab/internal/store"
)

var (
	// ErrInvalidSubmission marks a malformed submission: unknown mode,
	// wrong number of targets, a target that is not configured, or a suite
	// that does not match the catalog entry.
	ErrInvalidSubmission = errors.New("invalid submission")
	// ErrWritesNotPermitted marks a submission refused by the write
	// policy. Mutating suites need both the server setting allow_writes
	// and an explicit request; neither alone is enough.
	ErrWritesNotPermitted = errors.New("writes not permitted")
)

// maxEchoedName bounds user-supplied names quoted in error messages.
const maxEchoedName = 64

// Submission is a request to run an approved suite. It names targets
// from the server configuration only; it has no field for URLs,
// environment variables or suite content.
type Submission struct {
	SuiteFile string
	Mode      store.Mode
	// Targets is one target for ModeRun, or baseline then candidate for
	// ModeCompare.
	Targets []string
	// AllowWrites is the explicit per-request write permission.
	AllowWrites bool
}

// ValidateSubmission applies the server policy to sub for the approved
// suite e and returns the run to enqueue. The snapshot is the catalog's
// copy of the suite bytes. Errors wrap ErrInvalidSubmission or
// ErrWritesNotPermitted; their messages are safe to show to the operator.
//
// Mutating runs get MaxAttempts 1; read-only runs get
// worker.max_read_only_attempts.
func ValidateSubmission(cfg *config.Server, e Entry, sub Submission) (store.NewRun, error) {
	if cfg == nil {
		return store.NewRun{}, errors.New("catalog: server configuration is required")
	}
	if len(e.snapshot) == 0 || e.File == "" {
		return store.NewRun{}, invalid("suite is not an approved catalog entry")
	}
	if sub.SuiteFile != "" && sub.SuiteFile != e.File {
		return store.NewRun{}, invalid("suite %q does not match catalog entry %q", echo(sub.SuiteFile), e.File)
	}
	if err := checkTargets(cfg, sub.Mode, sub.Targets); err != nil {
		return store.NewRun{}, err
	}
	if err := checkWrites(cfg, e, sub.AllowWrites); err != nil {
		return store.NewRun{}, err
	}

	attempts := 1
	if !e.Mutating {
		attempts = cfg.Worker.MaxReadOnlyAttempts
		if attempts < 1 {
			attempts = 1
		}
		if attempts > store.MaxAttemptsLimit {
			attempts = store.MaxAttemptsLimit
		}
	}
	n, err := store.NewRun{
		SuiteName:       e.Name,
		SuiteFile:       e.File,
		SuiteSnapshot:   e.Snapshot(),
		Mode:            sub.Mode,
		Targets:         append([]string(nil), sub.Targets...),
		WritesRequested: sub.AllowWrites,
		Mutating:        e.Mutating,
		MaxAttempts:     attempts,
	}.Validate()
	if err != nil {
		return store.NewRun{}, invalid("%v", err)
	}
	return n, nil
}

func checkTargets(cfg *config.Server, mode store.Mode, targets []string) error {
	switch mode {
	case store.ModeRun:
		if len(targets) != 1 {
			return invalid("run mode needs exactly one target (got %d)", len(targets))
		}
	case store.ModeCompare:
		if len(targets) != 2 {
			return invalid("compare mode needs exactly two targets, baseline then candidate (got %d)", len(targets))
		}
		if targets[0] == targets[1] {
			return invalid("compare mode needs two different targets (got %q twice)", echo(targets[0]))
		}
	default:
		return invalid("mode must be %q or %q (got %q)", store.ModeRun, store.ModeCompare, echo(string(mode)))
	}
	for _, t := range targets {
		if _, ok := cfg.Targets[t]; !ok {
			return invalid("unknown target %q; targets are defined in the server configuration", echo(t))
		}
	}
	return nil
}

func checkWrites(cfg *config.Server, e Entry, requested bool) error {
	if requested && !cfg.AllowWrites {
		return fmt.Errorf("%w: the server configuration does not allow writes (allow_writes is false)", ErrWritesNotPermitted)
	}
	if !e.Mutating {
		return nil
	}
	cases := strings.Join(e.MutatingCases, ", ")
	if !cfg.AllowWrites {
		return fmt.Errorf("%w: suite %s contains mutating cases (%s) and the server configuration does not allow writes",
			ErrWritesNotPermitted, e.File, cases)
	}
	if !requested {
		return fmt.Errorf("%w: suite %s contains mutating cases (%s); the request must set allow_writes to run it",
			ErrWritesNotPermitted, e.File, cases)
	}
	return nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSubmission, fmt.Sprintf(format, args...))
}

// echo bounds a user-supplied value before it is quoted in an error.
func echo(s string) string { return jsonutil.Truncate(s, maxEchoedName) }
